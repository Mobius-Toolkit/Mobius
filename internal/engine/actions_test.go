package engine_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// actionJSON is a pending action of the API.
type actionJSON struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Tool      string `json:"tool"`
	Arguments []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"arguments"`
	State   string `json:"state"`
	Error   string `json:"error"`
	Result  string `json:"result"`
	Message string `json:"message"`
}

type actionRow struct {
	ID      int64
	State   string
	Error   string
	Result  string
	Message string
}

const (
	actionWaits = "The action waits for the approval of the Owner."
	// approvalSeen answers the Approval messages, so the Coordinator that gets one calls no tool.
	approvalSeen = "[[prompts]]\nwhen = \"# Approval message\"\nreply = [\"Seen\"]\n\n"
	seenScript   = "[[prompts]]\nreply = [\"Seen\"]\n"
)

func callMessageLead(when string, workstream int, text string) string {
	return fmt.Sprintf("[[prompts]]\nwhen = %q\ncall = { tool = \"message_lead\", arguments = { workstream = %d, text = %q } }\n\n", when, workstream, text)
}

func coordinatorSpec(t *testing.T) engine.Spec {
	t.Helper()
	return engine.Spec{Role: engine.CoordinatorRole, Organization: "owner", Dir: t.TempDir()}
}

func actionRows(t *testing.T, server *testserver.Server) []actionRow {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, a *actionRow) error {
		return rows.Scan(&a.ID, &a.State, &a.Error, &a.Result, &a.Message)
	}, "SELECT id, state, error, result, message FROM pending_actions ORDER BY id")
}

func waitForActions(t *testing.T, server *testserver.Server, count int) []actionRow {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]actionRow, bool) {
		found := actionRows(t, server)
		return found, len(found) == count
	})
}

func decide(t *testing.T, server *testserver.Server, id int64, decision, message string) (int, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		t.Fatal(err)
	}
	return send(t, server, http.MethodPost, "/api/actions/"+itoa(id)+"/"+decision, string(body))
}

func decided(t *testing.T, server *testserver.Server, id int64, decision, message string) actionJSON {
	t.Helper()
	status, text := decide(t, server, id, decision, message)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, text)
	}
	var body struct {
		Data actionJSON `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func approvalPrompts(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var found []string
	for _, prompt := range coordinatorPrompts(t, server) {
		if at := strings.LastIndex(prompt, "# Approval message\n\nThe Owner"); at >= 0 {
			found = append(found, prompt[at:])
		}
	}
	return found
}

func toLead12(t *testing.T, server *testserver.Server) []leadEvent {
	t.Helper()
	return slices.DeleteFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Workstream != 12 })
}

const coordinatorEvent = "Message of the Coordinator, approved by the Owner:\n\nCreate the task issues."

func TestTheCoordinatorCallsMessageLeadAndTheLeadGetsTheTextOnlyAfterTheOwnerApproves(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)

	session := run(t, server, coordinatorSpec(t), "Send.")

	if got := reply(t, server, session); got != actionWaits {
		t.Errorf("reply = %q", got)
	}
	action := waitForActions(t, server, 1)[0]
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if action.State != "pending" || len(leadEvents(t, server)) != 0 {
		t.Errorf("action = %+v, events = %v", action, leadEvents(t, server))
	}
	items := itemsOf(t, server, "approval")
	if len(items) != 1 || items[0].Title != "Message to the Lead of #12" || items[0].Reason != "Waits for your approval" ||
		items[0].Text != "text: Create the task issues.\nworkstream: 12" || items[0].Action == nil || items[0].Action.ID != action.ID {
		t.Errorf("items = %+v", items)
	}
	cards := slices.DeleteFunc(chatLines(t, server, coordinatorChat), func(line chatLine) bool { return line.Author != "Action" })
	if want := []chatLine{{"Action", "Message to the Lead of #12"}}; !slices.Equal(cards, want) {
		t.Errorf("cards = %v", cards)
	}

	done := decided(t, server, action.ID, "approve", "Go ahead.")

	if done.State != "done" || done.Result != "Sent the message to the Lead of #12." || done.Message != "Go ahead." || done.Tool != "message_lead" || done.Role != "coordinator" {
		t.Errorf("action = %+v", done)
	}
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	if events := toLead12(t, server); !slices.Equal(events, []leadEvent{{Workstream: 12, Kind: "coordinator", Payload: coordinatorEvent, Delivered: true}}) {
		t.Errorf("events = %v", events)
	}
	if prompts := leadPrompts(t, server); len(prompts) == 0 || !strings.HasSuffix(prompts[0], "# Event\n\n"+coordinatorEvent) {
		t.Errorf("prompts = %q", prompts)
	}
	waitForNoItem(t, server, "approval")
	testkit.WaitFor(t, func() bool { return len(approvalPrompts(t, server)) == 1 })
	want := "# Approval message\n\nThe Owner approved `message_lead`. The result: Sent the message to the Lead of #12.\n\nThe message of the Owner: Go ahead."
	if got := approvalPrompts(t, server); !slices.Equal(got, []string{want}) {
		t.Errorf("approval prompts = %q", got)
	}
	for _, line := range chatLines(t, server, coordinatorChat) {
		if line.Author == "Approval" {
			t.Errorf("the chat shows %v", line)
		}
	}
}

func TestTheChatAndTheInboxGiveTheActionOfACard(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	run(t, server, coordinatorSpec(t), "Send.")
	action := waitForActions(t, server, 1)[0]

	status, text := send(t, server, http.MethodGet, "/api/chat?organization=owner&repository=&workstream=0", "")

	var chat struct {
		Data struct {
			Messages []struct {
				Author string      `json:"author"`
				Action *actionJSON `json:"action"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &chat); status != http.StatusOK || err != nil {
		t.Fatalf("chat: status %d, %v: %s", status, err, text)
	}
	var found *actionJSON
	for _, message := range chat.Data.Messages {
		if message.Author == "Action" {
			found = message.Action
		}
		if message.Author != "Action" && message.Action != nil {
			t.Errorf("message = %+v", message)
		}
	}
	if found == nil || found.ID != action.ID || found.State != "pending" || len(found.Arguments) != 2 ||
		found.Arguments[0].Name != "text" || found.Arguments[0].Value != "Create the task issues." ||
		found.Arguments[1].Name != "workstream" || found.Arguments[1].Value != "12" {
		t.Errorf("action = %+v", found)
	}
}

func TestTheOwnerRejectsAnActionWithAMessageAndTheCoordinatorGetsTheMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	run(t, server, coordinatorSpec(t), "Send.")
	action := waitForActions(t, server, 1)[0]

	rejected := decided(t, server, action.ID, "reject", "Ask about the price first.")

	if rejected.State != "rejected" || rejected.Message != "Ask about the price first." {
		t.Errorf("action = %+v", rejected)
	}
	testkit.WaitFor(t, func() bool { return len(approvalPrompts(t, server)) == 1 })
	want := "# Approval message\n\nThe Owner rejected `message_lead`.\n\nThe message of the Owner: Ask about the price first."
	if got := approvalPrompts(t, server); !slices.Equal(got, []string{want}) {
		t.Errorf("approval prompts = %q", got)
	}
	waitForNoItem(t, server, "approval")
	waitForPolls(t, fake)
	if events := leadEvents(t, server); len(events) != 0 {
		t.Errorf("events = %v", events)
	}
	if status, _ := decide(t, server, action.ID, "approve", ""); status != http.StatusConflict {
		t.Errorf("status = %d", status)
	}
}

func TestARestartKeepsAWaitingActionAndTheOwnerCanApproveItAfterTheRestart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	first, dataDir := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	run(t, first, coordinatorSpec(t), "Send.")
	action := waitForActions(t, first, 1)[0]
	first.Stop()
	testkit.InstallFakeAgent(t, dataDir, options+seenScript)
	server := testserver.Start(t, dataDir, fake.URL)
	waitForPolls(t, fake)

	if items := itemsOf(t, server, "approval"); len(items) != 1 || items[0].Action == nil || items[0].Action.ID != action.ID {
		t.Errorf("items = %+v", items)
	}
	if events := leadEvents(t, server); len(events) != 0 {
		t.Errorf("events = %v", events)
	}

	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" {
		t.Errorf("action = %+v", done)
	}
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	if events := toLead12(t, server); !slices.Equal(events, []leadEvent{{Workstream: 12, Kind: "coordinator", Payload: coordinatorEvent, Delivered: true}}) {
		t.Errorf("events = %v", events)
	}
}

func TestAnApprovalThatFailsLeavesTheActionPendingAndASecondApprovalSendsTheMessageOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 20, "Create the task issues.")+seenScript)
	run(t, server, coordinatorSpec(t), "Send.")
	action := waitForActions(t, server, 1)[0]

	failed := decided(t, server, action.ID, "approve", "")

	if failed.State != "pending" || failed.Error != "#20 is not an open Workstream." {
		t.Errorf("action = %+v", failed)
	}
	if got := actionRows(t, server); len(got) != 1 || got[0].State != "pending" || got[0].Error != failed.Error {
		t.Errorf("rows = %+v", got)
	}
	if items := itemsOf(t, server, "approval"); len(items) != 1 {
		t.Errorf("items = %+v", items)
	}
	testkit.WaitFor(t, func() bool { return len(approvalPrompts(t, server)) == 1 })
	want := "# Approval message\n\nThe Owner approved `message_lead`, and it failed: #20 is not an open Workstream. The action still waits for the approval of the Owner."
	if got := approvalPrompts(t, server); !slices.Equal(got, []string{want}) {
		t.Errorf("approval prompts = %q", got)
	}

	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	testkit.WaitFor(t, func() bool {
		var count int
		if err := server.DB.QueryRow("SELECT count(*) FROM copied_workstreams WHERE repository = ? AND number = 20", shop).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count == 1
	})
	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Error != "" || done.Result != "Sent the message to the Lead of #20." {
		t.Errorf("action = %+v", done)
	}
	waitForNoItem(t, server, "approval")
	text := "Message of the Coordinator, approved by the Owner:\n\nCreate the task issues."
	sent := func() []leadEvent {
		return slices.DeleteFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind != "coordinator" })
	}
	testkit.WaitFor(t, func() bool {
		events := sent()
		return len(events) == 1 && events[0].Delivered
	})
	if events := sent(); !slices.Equal(events, []leadEvent{{Workstream: 20, Kind: "coordinator", Payload: text, Delivered: true}}) {
		t.Errorf("events = %v", events)
	}
	if status, _ := decide(t, server, action.ID, "approve", ""); status != http.StatusConflict {
		t.Errorf("status = %d", status)
	}
}

func TestTwoApprovalsAtTheSameTimeSendTheMessageOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	run(t, server, coordinatorSpec(t), "Send.")
	action := waitForActions(t, server, 1)[0]

	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Go(func() { statuses[i], _ = decide(t, server, action.ID, "approve", "") })
	}
	wg.Wait()

	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusConflict}) {
		t.Errorf("statuses = %v", statuses)
	}
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	if events := toLead12(t, server); len(events) != 1 {
		t.Errorf("events = %v", events)
	}
}

func TestASecondCallOfMessageLeadReplacesTheWaitingActionAndDismissesItsItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send 1.", 12, "First text.")+callMessageLead("Send 2.", 12, "Second text.")+seenScript)
	run(t, server, coordinatorSpec(t), "Send 1.", "Send 2.")
	rows := waitForActions(t, server, 2)
	first, second := rows[0], rows[1]

	if first.State != "replaced" || second.State != "pending" {
		t.Errorf("rows = %+v", rows)
	}
	items := itemsOf(t, server, "approval")
	if len(items) != 1 || items[0].Action == nil || items[0].Action.ID != second.ID || items[0].Text != "text: Second text.\nworkstream: 12" {
		t.Errorf("items = %+v", items)
	}
	if status, _ := decide(t, server, first.ID, "approve", ""); status != http.StatusConflict {
		t.Errorf("status = %d", status)
	}
	if status, _ := decide(t, server, 0, "approve", ""); status != http.StatusNotFound {
		t.Errorf("status = %d", status)
	}

	decided(t, server, second.ID, "approve", "")

	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	want := "Message of the Coordinator, approved by the Owner:\n\nSecond text."
	if events := toLead12(t, server); !slices.Equal(events, []leadEvent{{Workstream: 12, Kind: "coordinator", Payload: want, Delivered: true}}) {
		t.Errorf("events = %v", events)
	}
}

func TestACallWithNoSingleRepositoryInTheOrganizationStoresNoAction(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository("owner/other")
	server, _ := connect(t, fake, callMessageLead("Send.", 12, "Create the task issues.")+seenScript)

	session := run(t, server, coordinatorSpec(t), "Send.")

	calls := mcpCalls(t, server, session)
	if len(calls) != 1 || calls[0]["error"] != "The Coordinator chat needs exactly one repository in owner." {
		t.Errorf("calls = %v", calls)
	}
	if got := actionRows(t, server); len(got) != 0 {
		t.Errorf("rows = %+v", got)
	}
	if items := itemsOf(t, server, "approval"); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}
