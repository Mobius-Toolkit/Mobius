package engine_test

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	State    string `json:"state"`
	Error    string `json:"error"`
	Result   string `json:"result"`
	Message  string `json:"message"`
	OldBrief string `json:"oldBrief"`
}

type actionRow struct {
	ID      int64
	State   string
	Error   string
	Result  string
	Message string
}

const (
	actionWaits = `The action waits for the approval of the Owner. Its tray row is "`
	// approvalSeen answers the Approval messages, so the Coordinator that gets one calls no tool.
	approvalSeen = "[[prompts]]\nwhen = \"# Approval message\"\nreply = [\"Seen\"]\n\n"
	seenScript   = "[[prompts]]\nreply = [\"Seen\"]\n"
)

// actionWaitsText gives the result of a tool that waits for the approval, with the title of its tray row and its ID.
func actionWaitsText(title string, id int64) string {
	return fmt.Sprintf(`%s%s". In the chat, name the action by these words. Its ID is %d, only for withdraw.`, actionWaits, title, id)
}

// replacesAction gives the result part for a replaced pending action, with the title of its tray row.
func replacesAction(title string, id int64) string {
	return fmt.Sprintf(` It replaces the pending action "%s" (ID %d), which does not run.`, title, id)
}

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
		var linked int
		if err := server.DB.QueryRow("SELECT count(*) FROM pending_actions WHERE inbox_item IS NOT NULL").Scan(&linked); err != nil {
			t.Fatal(err)
		}
		return found, len(found) == count && linked == count
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

// approveNext waits for the action number n and approves it.
func approveNext(t *testing.T, server *testserver.Server, n int) actionJSON {
	t.Helper()
	return decided(t, server, waitForActions(t, server, n)[n-1].ID, "approve", "")
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

	if got := reply(t, server, session); !strings.HasPrefix(got, actionWaits) {
		t.Errorf("reply = %q", got)
	}
	action := waitForActions(t, server, 1)[0]
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if action.State != "pending" || len(leadEvents(t, server)) != 0 {
		t.Errorf("action = %+v, events = %v", action, leadEvents(t, server))
	}
	items := itemsOf(t, server, "approval")
	if len(items) != 1 || items[0].Title != "Message to the Lead of #12 Integrate loyalty plans" || items[0].Reason != "Waits for your approval" ||
		items[0].Text != "workstream: 12\ntext: Create the task issues." || items[0].Action == nil || items[0].Action.ID != action.ID {
		t.Errorf("items = %+v", items)
	}
	cards := slices.DeleteFunc(chatLines(t, server, coordinatorChat), func(line chatLine) bool { return line.Author != "Action" })
	if want := []chatLine{{"Action", "Message to the Lead of #12 Integrate loyalty plans"}}; !slices.Equal(cards, want) {
		t.Errorf("cards = %v", cards)
	}

	done := decided(t, server, action.ID, "approve", "Go ahead.")

	if done.State != "done" || done.Result != "Sent the message to the Lead of #12 Integrate loyalty plans." || done.Message != "Go ahead." || done.Tool != "message_lead" || done.Role != "coordinator" {
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
	want := "# Approval message\n\nThe Owner approved `message_lead` 1 (Create the task issues.). The result: Sent the message to the Lead of #12 Integrate loyalty plans.\n\nThe message of the Owner: Go ahead."
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
		found.Arguments[0].Name != "workstream" || found.Arguments[0].Value != "12" ||
		found.Arguments[1].Name != "text" || found.Arguments[1].Value != "Create the task issues." {
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
	want := "# Approval message\n\nThe Owner rejected `message_lead` 1 (Create the task issues.).\n\nThe message of the Owner: Ask about the price first."
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
	want := "# Approval message\n\nThe Owner approved `message_lead` 1 (Create the task issues.), and it failed: #20 is not an open Workstream. The action still waits for the approval of the Owner."
	if got := approvalPrompts(t, server); !slices.Equal(got, []string{want}) {
		t.Errorf("approval prompts = %q", got)
	}

	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	waitForBilling(t, server)
	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Error != "" || done.Result != "Sent the message to the Lead of #20 Billing." {
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
	session := run(t, server, coordinatorSpec(t), "Send 1.", "Send 2.")
	rows := waitForActions(t, server, 2)
	first, second := rows[0], rows[1]

	calls := mcpCalls(t, server, session)
	title := "Message to the Lead of #12 Integrate loyalty plans"
	if len(calls) != 2 || calls[1]["result"] != actionWaitsText(title, second.ID)+replacesAction(title, first.ID) {
		t.Errorf("calls = %v", calls)
	}

	if first.State != "replaced" || second.State != "pending" {
		t.Errorf("rows = %+v", rows)
	}
	items := itemsOf(t, server, "approval")
	if len(items) != 1 || items[0].Action == nil || items[0].Action.ID != second.ID || items[0].Text != "workstream: 12\ntext: Second text." {
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

func TestTheResultOfAToolNamesTheTrayRowOfTheActionAndOfTheReplacedAction(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+
		callCreateWorkstream("Create loyalty.", "Loyalty API", "Plan the loyalty API.")+
		callCreateWorkstream("Create billing.", "Billing API", "Plan the billing API.")+
		seenScript)
	session := run(t, server, coordinatorSpec(t), "Create loyalty.", "Create billing.")
	rows := waitForActions(t, server, 2)

	calls := mcpCalls(t, server, session)
	loyalty := `Create the Workstream "Loyalty API"`
	billing := `Create the Workstream "Billing API"`
	if len(calls) != 2 || calls[0]["result"] != actionWaitsText(loyalty, rows[0].ID) {
		t.Errorf("calls = %v", calls)
	}
	if calls[1]["result"] != actionWaitsText(billing, rows[1].ID)+replacesAction(loyalty, rows[0].ID) {
		t.Errorf("result = %q", calls[1]["result"])
	}
	for _, call := range calls {
		if result := call["result"].(string); !strings.Contains(result, "only for withdraw") {
			t.Errorf("result = %q", result)
		}
	}
}

func TestACallWithNoSingleRepositoryInTheOrganizationStoresNoAction(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository("owner/other")
	server, _ := connect(t, fake, callMessageLead("Send.", 12, "Create the task issues.")+seenScript)

	session := run(t, server, coordinatorSpec(t), "Send.")

	calls := mcpCalls(t, server, session)
	if len(calls) != 1 || calls[0]["error"] != "Mobius needs the issue repository of owner. Set organizations.owner.issue_repository in config.toml." {
		t.Errorf("calls = %v", calls)
	}
	if got := actionRows(t, server); len(got) != 0 {
		t.Errorf("rows = %+v", got)
	}
	if items := itemsOf(t, server, "approval"); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}

func TestAnApprovalMessageStoredWhileSealedGoesToTheAgentWhenTheSealIsAborted(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+seenScript)
	if got := <-startDrain(t, server); got != "drained" {
		t.Fatalf("drain end = %s", got)
	}
	if got := server.Engine.Seal(); got != engine.Drained {
		t.Fatalf("seal = %s", got)
	}
	text := "The Owner rejected `message_lead` 1 (Create the task issues.)."
	if err := server.Engine.PostApproval(t.Context(), coordinatorChat, text); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)
	if got := coordinatorPrompts(t, server); len(got) != 0 {
		t.Fatalf("prompts = %q", got)
	}

	server.Engine.AbortDrain()

	testkit.WaitFor(t, func() bool { return len(approvalPrompts(t, server)) == 1 })
	if got := approvalPrompts(t, server); !slices.Equal(got, []string{"# Approval message\n\n" + text}) {
		t.Errorf("approval prompts = %q", got)
	}
}

func callCreateWorkstream(when, title, brief string, labels ...string) string {
	return fmt.Sprintf("[[prompts]]\nwhen = %q\ncall = { tool = \"create_workstream\", arguments = { title = %q, brief = %q, labels = %q } }\n\n", when, title, brief, labels)
}

func createdIssue(t *testing.T, server *testserver.Server, id int64) sql.NullInt64 {
	t.Helper()
	var issue sql.NullInt64
	if err := server.DB.QueryRow("SELECT created_issue FROM pending_actions WHERE id = ?", id).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	return issue
}

// The poll adds mobius:evergreen to a maintenance Workstream at a time that a test does not control.
func workstreamLabels(fake *testkit.FakeGitHub, number int64) []string {
	return slices.DeleteFunc(fake.Labels(shop, number), func(label string) bool { return label == "mobius:evergreen" })
}

func TestTheCoordinatorCallsCreateWorkstreamAndGitHubGetsTheIssueOnlyAfterTheOwnerApproves(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callCreateWorkstream("Create.", "Upkeep", "Keep the code healthy.", "mobius:maintenance")+seenScript)

	session := run(t, server, coordinatorSpec(t), "Create.")

	if got := reply(t, server, session); !strings.HasPrefix(got, actionWaits) {
		t.Errorf("reply = %q", got)
	}
	action := waitForActions(t, server, 1)[0]
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if action.State != "pending" || fake.HasIssue(shop, 13) {
		t.Errorf("action = %+v, issue = %v", action, fake.HasIssue(shop, 13))
	}
	items := itemsOf(t, server, "approval")
	wantText := "title: Upkeep\nlabels: [\"mobius:maintenance\"]\nbrief: Keep the code healthy."
	if len(items) != 1 || items[0].Title != `Create the Workstream "Upkeep"` || items[0].Text != wantText {
		t.Errorf("items = %+v", items)
	}

	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Result != "Created the Workstream #13." {
		t.Errorf("action = %+v", done)
	}
	title, body := fake.Issue(shop, 13)
	if labels := workstreamLabels(fake, 13); title != "Upkeep" || body != "Keep the code healthy." || !slices.Equal(labels, []string{"mobius:workstream", "mobius:maintenance"}) {
		t.Errorf("#13 = %q, %q, %v", title, body, labels)
	}
	if fake.HasIssue(shop, 14) {
		t.Error("the Workstream exists two times")
	}
}

func TestAFailedAddOfTheLabelsLeavesTheActionPendingAndASecondApprovalCreatesNoSecondIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callCreateWorkstream("Create.", "Upkeep", "Keep the code healthy.", "mobius:maintenance")+seenScript)
	fake.FailAddLabels(shop, 13, true)
	run(t, server, coordinatorSpec(t), "Create.")
	action := waitForActions(t, server, 1)[0]

	failed := decided(t, server, action.ID, "approve", "")

	if failed.State != "pending" || !strings.HasPrefix(failed.Error, "GitHub created #13, but the add of the Workstream labels failed.") {
		t.Errorf("action = %+v", failed)
	}
	if issue := createdIssue(t, server, action.ID); issue != (sql.NullInt64{Int64: 13, Valid: true}) {
		t.Errorf("created issue = %+v", issue)
	}
	if items := itemsOf(t, server, "approval"); len(items) != 1 {
		t.Errorf("items = %+v", items)
	}

	fake.FailAddLabels(shop, 13, false)
	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Error != "" || done.Result != "Created the Workstream #13." {
		t.Errorf("action = %+v", done)
	}
	if labels := workstreamLabels(fake, 13); !slices.Equal(labels, []string{"mobius:workstream", "mobius:maintenance"}) {
		t.Errorf("labels of #13 = %v", labels)
	}
	if fake.HasIssue(shop, 14) {
		t.Error("the Workstream exists two times")
	}
	waitForNoItem(t, server, "approval")
}

func TestTheCoordinatorCallsTellCuratorAndTheCuratorGetsTheRequestOnlyAfterTheOwnerApproves(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, requestScript)

	sendChat(t, server, coordinatorChat, "Remember the format rule.")

	action := waitForActions(t, server, 1)[0]
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if sessions := curatorSessions(t, server); action.State != "pending" || len(sessions) != 0 || requestCount(t, server) != 0 {
		t.Errorf("action = %+v, Curators = %+v", action, sessions)
	}
	items := itemsOf(t, server, "approval")
	wantText := "repository: owner/shop\ntext: Add the lesson: run make fmt before each commit."
	if len(items) != 1 || items[0].Title != "Request to the Curator of owner/shop" || items[0].Text != wantText {
		t.Errorf("items = %+v", items)
	}

	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Result != "Sent the request to the Curator. The result arrives later." {
		t.Errorf("action = %+v", done)
	}
	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		sessions := curatorSessions(t, server)
		if len(sessions) != 1 {
			return nil, false
		}
		prompts := promptTexts(t, server, sessions[0].ID)
		return prompts, len(prompts) == 1
	})
	if !strings.Contains(prompts[0], "## Request 1\n\nAdd the lesson: run make fmt before each commit.\n\n") {
		t.Errorf("prompts = %q", prompts)
	}
}

func callEditBrief(when, brief, reason string) string {
	return fmt.Sprintf("[[prompts]]\nwhen = %q\ncall = { tool = \"edit_brief\", arguments = { brief = %q, reason = %q } }\n\n", when, brief, reason)
}

func briefOf(t *testing.T, server *testserver.Server, number int64) string {
	t.Helper()
	for _, found := range workstreams(t, server) {
		if found.Number == number {
			return found.Brief
		}
	}
	t.Fatalf("no Workstream #%d", number)
	return ""
}

func TestTheLeadCallsEditBriefAndTheBriefChangesOnlyAfterTheOwnerApproves(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callEditBrief("Change it.", "Ship loyalty plans and gift cards.", "Gift cards are in scope.")+seenScript)
	_, oldBrief := fake.Issue(shop, 12)

	session := run(t, server, leadSpec(t), "Change it.")

	if got := reply(t, server, session); !strings.HasPrefix(got, actionWaits) {
		t.Errorf("reply = %q", got)
	}
	action := waitForActions(t, server, 1)[0]
	waitForPolls(t, fake)
	if _, body := fake.Issue(shop, 12); body != oldBrief || action.State != "pending" {
		t.Errorf("body = %q, action = %+v", body, action)
	}
	items := itemsOf(t, server, "approval")
	if len(items) != 1 || items[0].Title != "Change the Brief of #12 Integrate loyalty plans" || items[0].Repository != shop || items[0].Workstream != 12 ||
		items[0].Text != "reason: Gift cards are in scope.\nbrief: Ship loyalty plans and gift cards." {
		t.Errorf("items = %+v", items)
	}

	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Result != "Changed the Brief of #12 Integrate loyalty plans." {
		t.Errorf("action = %+v", done)
	}
	if _, body := fake.Issue(shop, 12); body != "Ship loyalty plans and gift cards." {
		t.Errorf("body = %q", body)
	}
	if got := briefOf(t, server, 12); got != "Ship loyalty plans and gift cards." {
		t.Errorf("copied Brief = %q", got)
	}
}

func TestTheBriefOfAnEditBriefActionIsTheBriefOnGitHubNowAndTheDoneActionKeepsTheBriefItOverwrote(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, approvalSeen+callEditBrief("Update the Brief.", "Ship loyalty plans and gift cards.", "Customers asked for gift cards.")+seenScript, withUIURL)
	fake.SetBody(shop, 12, "Ship loyalty plans.\n\n"+chatLink+"\n")
	run(t, server, leadSpec(t), "Update the Brief.")
	action := waitForActions(t, server, 1)[0]
	if got, err := server.Engine.ActionBrief(t.Context(), action.ID); err != nil || got != "Ship loyalty plans." {
		t.Errorf("brief = %q, %v", got, err)
	}

	fake.SetBody(shop, 12, "Ship loyalty plans and coupons.\n\n"+chatLink+"\n")
	if got, err := server.Engine.ActionBrief(t.Context(), action.ID); err != nil || got != "Ship loyalty plans and coupons." {
		t.Errorf("changed brief = %q, %v", got, err)
	}
	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.OldBrief != "Ship loyalty plans and coupons." {
		t.Errorf("action = %+v", done)
	}
	if _, body := fake.Issue(shop, 12); body != "Ship loyalty plans and gift cards.\n\n"+chatLink {
		t.Errorf("body = %q", body)
	}
}

func TestTheBriefOfAnActionOfAnotherToolOrAnUnknownActionGivesARefusal(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	run(t, server, coordinatorSpec(t), "Send.")
	action := waitForActions(t, server, 1)[0]

	_, err := server.Engine.ActionBrief(t.Context(), action.ID)

	if !engine.Refused(err) {
		t.Errorf("err = %v", err)
	}
	if _, err := server.Engine.ActionBrief(t.Context(), action.ID+1); !errors.Is(err, engine.ErrNoAction) {
		t.Errorf("err = %v", err)
	}
}

func TestEditBriefRefusesAnEmptyBriefAtTheCall(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, callEditBrief("Change it.", " ", "Gift cards are in scope.")+seenScript)

	session := run(t, server, leadSpec(t), "Change it.")

	if got, want := reply(t, server, session), "error: brief and reason must not be empty."; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
	if rows := actionRows(t, server); len(rows) != 0 {
		t.Errorf("actions = %+v", rows)
	}
}

func TestTheLeadCallsMoveTaskAndTheIssueMovesOnlyAfterTheOwnerApproves(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	server, _ := connect(t, fake, approvalSeen+callMoveTask("Move it.", 41, 20)+seenScript)
	fake.AddSubIssue(shop, 12, 41)

	session := run(t, server, leadSpec(t), "Move it.")

	if got := reply(t, server, session); !strings.HasPrefix(got, actionWaits) {
		t.Errorf("reply = %q", got)
	}
	action := waitForActions(t, server, 1)[0]
	waitForPolls(t, fake)
	if got := fake.SubIssueNumbers(shop, 12); !slices.Equal(got, []int64{41}) {
		t.Errorf("sub-issues of #12 = %v", got)
	}
	items := itemsOf(t, server, "approval")
	if len(items) != 1 || items[0].Title != "Move owner/shop#41 to the Workstream #20 Billing" || items[0].Repository != shop || items[0].Workstream != 12 {
		t.Errorf("items = %+v", items)
	}

	done := decided(t, server, action.ID, "approve", "")

	if done.Result != "Moved owner/shop#41 to the Workstream #20 Billing." {
		t.Errorf("action = %+v", done)
	}
	if got := fake.SubIssueNumbers(shop, 20); !slices.Equal(got, []int64{41}) {
		t.Errorf("sub-issues of #20 = %v", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); len(got) != 0 {
		t.Errorf("sub-issues of #12 = %v", got)
	}
}

func TestTheOwnerRejectsMessageLeadOfTheLeadWithAMessageAndTheLeadGetsTheMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Tell it.", 20, "Create the task issues.")+seenScript)

	sendChat(t, server, leadChat, "Tell it.")
	action := waitForActions(t, server, 1)[0]
	rejected := decided(t, server, action.ID, "reject", "Ask about the price first.")

	if rejected.State != "rejected" || rejected.Message != "Ask about the price first." {
		t.Errorf("action = %+v", rejected)
	}
	want := "# Approval message\n\nThe Owner rejected `message_lead` 1 (Create the task issues.).\n\nThe message of the Owner: Ask about the price first."
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool { return strings.Contains(prompt, want) })
	})
	waitForPolls(t, fake)
	if events := leadEvents(t, server); len(events) != 0 {
		t.Errorf("events = %v", events)
	}
}

func waitForBilling(t *testing.T, server *testserver.Server) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		var count int
		if err := server.DB.QueryRow("SELECT count(*) FROM copied_workstreams WHERE repository = ? AND number = 20", shop).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count == 1
	})
}
