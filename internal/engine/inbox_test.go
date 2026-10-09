package engine_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type inboxItem struct {
	ID          int64   `json:"id"`
	Kind        string  `json:"kind"`
	Repository  string  `json:"repository"`
	Workstream  int64   `json:"workstream"`
	Issue       int64   `json:"issue"`
	Text        string  `json:"text"`
	Link        string  `json:"link"`
	DismissedAt *string `json:"dismissedAt"`
}

// inbox gives the Inbox through the API.
func inbox(t *testing.T, server *testserver.Server) []inboxItem {
	t.Helper()
	status, text := send(t, server, http.MethodGet, "/api/inbox", "")
	var body struct {
		Data []inboxItem `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil {
		t.Fatalf("inbox: status %d, %v: %s", status, err, text)
	}
	return body.Data
}

// leadCalls gives the Mobius tool calls of the Lead sessions of the Workstream #12.
func leadCalls(t *testing.T, server *testserver.Server) []map[string]any {
	t.Helper()
	var calls []map[string]any
	for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
		calls = append(calls, mcpCalls(t, server, session.ID)...)
	}
	return calls
}

func TestTheLeadAsksAQuestionAndATrustedReplyGoesToTheLeadAsTheNextEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "comment on #41"
reply = ["Seen"]

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)

	dispatchTask(fake, 41, "Add plan model")

	calls := testkit.WaitForValue(t, func() ([]map[string]any, bool) {
		calls := leadCalls(t, server)
		return calls, len(calls) > 0
	})
	if calls[0]["result"] != "Asked on #41." {
		t.Errorf("call = %v", calls[0])
	}
	if got := fake.Comments(shop, 41); !slices.Equal(got, []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: "Cents or dollars?"}}) {
		t.Errorf("comments = %v", got)
	}
	if got := fake.Labels(shop, 41); !slices.Equal(got, []string{"mobius:working", "mobius:question"}) {
		t.Errorf("labels = %v", got)
	}
	items := inbox(t, server)
	want := inboxItem{ID: items[0].ID, Kind: "question", Repository: shop, Workstream: 12, Issue: 41, Text: "Cents or dollars?", Link: "https://github.com/owner/shop/issues/41"}
	if len(items) != 1 || items[0] != want {
		t.Errorf("inbox = %+v", items)
	}

	fake.AddComment(shop, 41, "owner", "Cents.")

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool {
			return strings.Contains(prompt, ` comment on #41 "Add plan model" by @owner:`+"\n\n> Cents.")
		})
	})
	testkit.WaitFor(t, func() bool { return slices.Equal(fake.Labels(shop, 41), []string{"mobius:working"}) })
	if got := inbox(t, server); len(got) != 1 {
		t.Errorf("inbox = %+v", got)
	}
}

func TestAskOnAnIssueWithNoLiveTaskInTheWorkstreamChangesNothing(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("ask", `{ n = 41, text = "Cents or dollars?" }`))
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)

	dispatchTask(fake, 40, "Plan API")

	calls := testkit.WaitForValue(t, func() ([]map[string]any, bool) {
		calls := leadCalls(t, server)
		return calls, len(calls) > 0
	})
	if calls[0]["error"] != "#41 has no live task in this Workstream." {
		t.Errorf("call = %v", calls[0])
	}
	if got := fake.Comments(shop, 41); len(got) != 0 {
		t.Errorf("comments = %v", got)
	}
	if got := fake.Labels(shop, 41); len(got) != 0 {
		t.Errorf("labels = %v", got)
	}
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
}

func TestTheLeadHoldsADispatchedTaskAndTheTaskWaitsForTheOwnerWithNoWorkerSlot(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "dispatch of #41"
call = { tool = "hold_task", arguments = { n = 41, reason = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)

	dispatchTask(fake, 41, "Add plan model")

	calls := testkit.WaitForValue(t, func() ([]map[string]any, bool) {
		calls := leadCalls(t, server)
		return calls, len(calls) > 0
	})
	if calls[0]["result"] != "Held #41." {
		t.Errorf("call = %v", calls[0])
	}
	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if got := fake.Labels(shop, 41); !slices.Equal(got, []string{"mobius:needs-human"}) {
		t.Errorf("labels = %v", got)
	}
	if got := fake.Comments(shop, 41); !slices.Equal(got, []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: "Cents or dollars?"}}) {
		t.Errorf("comments = %v", got)
	}
	items := inbox(t, server)
	want := inboxItem{ID: items[0].ID, Kind: "question", Repository: shop, Workstream: 12, Issue: 41, Text: "Cents or dollars?", Link: "https://github.com/owner/shop/issues/41"}
	if len(items) != 1 || items[0] != want {
		t.Errorf("inbox = %+v", items)
	}
	if got := activeTasks(t, server); got != 0 {
		t.Errorf("active tasks = %d", got)
	}
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool {
			return strings.Contains(prompt, ` stop of #41 "Add plan model": Cents or dollars?`)
		})
	})
}

func TestHoldTaskRefusesATaskThatIsNotDispatched(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("hold_task", `{ n = 41, reason = "Cents or dollars?" }`))
	addTask(t, server, fake, 41, 12, 45)

	session := run(t, server, leadSpec(t), "1. ")

	if got := reply(t, server, session); got != "error: The task of #41 is ready_for_review. Only a dispatched task can be held." {
		t.Errorf("reply = %q", got)
	}
	if state := taskState(t, server); state != "ready_for_review" {
		t.Errorf("state = %s", state)
	}
	if got := fake.Comments(shop, 41); len(got) != 0 {
		t.Errorf("comments = %v", got)
	}
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
}

func TestTellOwnerAddsAChatMessageAndAnInboxItem(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("tell_owner", `{ text = "#41 needs a decision." }`))
	changes := listen(t, server)

	dispatchTask(fake, 41, "Add plan model")

	waitForChat(t, server, leadChat, "tell_owner", "#41 needs a decision.")
	var lines []chatLine
	for _, line := range chatLines(t, server, leadChat) {
		if line.Author != "Event" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Errorf("chat = %+v", lines)
	}
	unread, err := server.Engine.UnreadChats(t.Context())
	if err != nil || !slices.Equal(unread, []engine.Unread{{Key: leadChat, Count: 1}}) {
		t.Errorf("unread = %+v, %v", unread, err)
	}
	items := testkit.WaitForValue(t, func() ([]inboxItem, bool) {
		items := inbox(t, server)
		return items, len(items) == 1
	})
	want := inboxItem{ID: items[0].ID, Kind: "Lead", Repository: shop, Workstream: 12, Issue: 12, Text: "#41 needs a decision.", Link: "https://github.com/owner/shop/issues/12"}
	if items[0] != want {
		t.Errorf("item = %+v", items[0])
	}
	waitForChange(t, changes, func(change engine.Change) bool { return change.Inbox != nil && change.Inbox.ID == items[0].ID })
}

const tellOwnerThenReply = `
[[prompts]]
when = "Second"
reply = ["Second reply."]

[[prompts]]
when = "First"
call = { tool = "tell_owner", arguments = { text = "#41 needs a decision." } }
reply = ["Same text again."]
`

func TestTheReplyTextAfterTellOwnerDoesNotGoToTheChat(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, tellOwnerThenReply)

	sendChat(t, server, leadChat, "First")

	session := endedChatSession(t, server, 0)
	if got := reply(t, server, session.ID); !strings.Contains(got, "Same text again.") {
		t.Errorf("reply = %q", got)
	}
	want := []chatLine{{"Owner", "First"}, {"tell_owner", "#41 needs a decision."}}
	if got := chatLines(t, server, leadChat); !reflect.DeepEqual(got, want) {
		t.Errorf("chat = %+v", got)
	}
	if got := inbox(t, server); len(got) != 1 {
		t.Errorf("inbox = %+v", got)
	}
}

func TestTheReplyTextOfALaterTurnWithNoTellOwnerGoesToTheChat(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, tellOwnerThenReply, func(cfg *config.Config) { cfg.LeadIdleTimeout = 30 * time.Second })
	sendChat(t, server, leadChat, "First")
	waitForChat(t, server, leadChat, "tell_owner", "#41 needs a decision.")

	sendChat(t, server, leadChat, "Second")

	waitForChat(t, server, leadChat, "Lead", "Second reply.")
	if got := chatSessions(t, server, leadChat, engine.LeadRole); len(got) != 1 {
		t.Errorf("sessions = %+v", got)
	}
	want := []chatLine{{"Owner", "First"}, {"tell_owner", "#41 needs a decision."}, {"Owner", "Second"}, {"Lead", "Second reply."}}
	if got := chatLines(t, server, leadChat); !reflect.DeepEqual(got, want) {
		t.Errorf("chat = %+v", got)
	}
}

func TestDismissRemovesTheItemFromTheInbox(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("tell_owner", `{ text = "#41 needs a decision." }`))
	dispatchTask(fake, 41, "Add plan model")
	item := testkit.WaitForValue(t, func() (inboxItem, bool) {
		items := inbox(t, server)
		if len(items) == 0 {
			return inboxItem{}, false
		}
		return items[0], true
	})
	changes := listen(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/inbox/"+itoa(item.ID)+"/dismiss", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
	dismissed := waitForChange(t, changes, func(change engine.Change) bool { return change.Inbox != nil && change.Inbox.DismissedAt.Valid })
	if dismissed.Inbox.ID != item.ID {
		t.Errorf("dismissed = %+v", dismissed.Inbox)
	}
}
