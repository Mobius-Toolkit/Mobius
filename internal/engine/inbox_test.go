package engine_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type inboxItem struct {
	ID                   int64       `json:"id"`
	Kind                 string      `json:"kind"`
	Repository           string      `json:"repository"`
	WorkstreamRepository string      `json:"workstreamRepository"`
	Workstream           int64       `json:"workstream"`
	Issue                int64       `json:"issue"`
	Title                string      `json:"title"`
	Reason               string      `json:"reason"`
	Text                 string      `json:"text"`
	Link                 string      `json:"link"`
	PullRequest          int64       `json:"pullRequest"`
	PullRequestURL       string      `json:"pullRequestUrl"`
	ReportURL            string      `json:"reportUrl"`
	DismissedAt          *string     `json:"dismissedAt"`
	Merging              bool        `json:"merging"`
	MergeRefusal         string      `json:"mergeRefusal"`
	Action               *actionJSON `json:"action"`
	Todo                 *struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Path      string `json:"path"`
		Line      int64  `json:"line"`
		Reason    string `json:"reason"`
		Mandatory bool   `json:"mandatory"`
		Note      string `json:"note"`
	} `json:"todo"`
}

// inbox gives the Inbox through the API, without the access check items. A server whose Role bindings have no fake
// Harness has such an item for each binding.
func inbox(t *testing.T, server *testserver.Server) []inboxItem {
	t.Helper()
	return slices.DeleteFunc(allInbox(t, server), func(item inboxItem) bool { return item.Kind == "access check" })
}

// allInbox gives the Inbox through the API.
func allInbox(t *testing.T, server *testserver.Server) []inboxItem {
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

func TestTheLeadAsksAQuestionAndATrustedReplyDispatchesTheTaskAgainWithNoAutopilot(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "answer of #41"
reply = ["Seen"]

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

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
	if got := fake.Comments(shop, 41); !slices.Equal(got, []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: marked("Cents or dollars?", leadMark)}}) {
		t.Errorf("comments = %v", got)
	}
	if got := fake.Labels(shop, 41); !slices.Equal(got, []string{"mobius:question"}) {
		t.Errorf("labels = %v", got)
	}
	if state := taskState(t, server); state != "asked" {
		t.Errorf("state = %s", state)
	}
	if got := activeTasks(t, server); got != 0 {
		t.Errorf("active tasks = %d", got)
	}
	items := inbox(t, server)
	want := inboxItem{ID: items[0].ID, Kind: "question", Repository: shop, WorkstreamRepository: shop, Workstream: 12, Issue: 41, Title: "Add plan model", Reason: "The Lead asks a question", Text: "Cents or dollars?", Link: "https://github.com/owner/shop/issues/41"}
	if len(items) != 1 || items[0] != want {
		t.Errorf("inbox = %+v", items)
	}

	fake.AddComment(shop, 41, "owner", "Cents.")

	prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model" by @owner:`)
	if !strings.Contains(prompt, ` reply on #41 "Add plan model" by @owner:`+"\n\n> Cents.") || strings.Contains(prompt, " comment on #41") {
		t.Errorf("prompt = %s", prompt)
	}
	testkit.WaitFor(t, func() bool { return slices.Equal(fake.Labels(shop, 41), []string{"mobius:working"}) })
	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}
}

func TestTheLeadAsksOnTheWorkstreamIssueAndATrustedReplyRemovesTheQuestionLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "comment on #12"
reply = ["Seen"]

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 12, text = "Change the Brief?" } }

[[prompts]]
reply = ["Seen"]
`)

	dispatchTask(fake, 41, "Add plan model")

	calls := testkit.WaitForValue(t, func() ([]map[string]any, bool) {
		calls := leadCalls(t, server)
		return calls, len(calls) > 0
	})
	if calls[0]["result"] != "Asked on #12." {
		t.Errorf("call = %v", calls[0])
	}
	if got := fake.Comments(shop, 12); !slices.Equal(got, []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: marked("Change the Brief?", leadMark)}}) {
		t.Errorf("comments = %v", got)
	}
	if got := fake.Labels(shop, 12); !slices.Equal(got, []string{"mobius:workstream", "mobius:question"}) {
		t.Errorf("labels = %v", got)
	}
	items := inbox(t, server)
	want := inboxItem{ID: items[0].ID, Kind: "question", Repository: shop, WorkstreamRepository: shop, Workstream: 12, Issue: 12, Title: "Integrate loyalty plans", Reason: "The Lead asks a question", Text: "Change the Brief?", Link: "https://github.com/owner/shop/issues/12"}
	if len(items) != 1 || items[0] != want {
		t.Errorf("inbox = %+v", items)
	}

	fake.AddComment(shop, 12, "owner", "Yes.")

	testkit.WaitFor(t, func() bool { return slices.Equal(fake.Labels(shop, 12), []string{"mobius:workstream"}) })
}

func TestAskOnAnIssueWithNoLiveTaskInTheWorkstreamChangesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("ask", `{ repository = "owner/shop", n = 41, text = "Cents or dollars?" }`))
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

func TestAskOnATaskThatIsNotDispatchedKeepsTheTaskState(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("ask", `{ repository = "owner/shop", n = 41, text = "Cents or dollars?" }`))
	addTask(t, server, fake, 41, 12, 45)

	session := run(t, server, leadSpec(t), "1. ")

	if got := reply(t, server, session); got != "Asked on #41." {
		t.Errorf("reply = %q", got)
	}
	if state := taskState(t, server); state != "ready_for_review" {
		t.Errorf("state = %s", state)
	}
}

func TestASecondAskOnAnIssueNamesTheQuestionThatItReplacesAndKeepsOneOpenItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake,
		call("ask", `{ repository = "owner/shop", n = 41, text = "\n  Cents or dollars?\nMore context." }`)+
			call("ask", `{ repository = "owner/shop", n = 41, text = "Which currency?" }`))
	addTask(t, server, fake, 41, 12, 45)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	calls := mcpCalls(t, server, session)
	if len(calls) != 2 || calls[0]["result"] != "Asked on #41." || calls[1]["result"] != `Asked on #41. This question replaced the open question "Cents or dollars?".` {
		t.Errorf("calls = %v", calls)
	}
	items := inbox(t, server)
	if len(items) != 1 || items[0].Text != "Which currency?" {
		t.Errorf("inbox = %+v", items)
	}
}

func TestTellOwnerAddsAChatMessageAndNoInboxItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("tell_owner", `{ text = "#41 needs a decision." }`))

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
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
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
	t.Parallel()
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
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
}

func TestTheReplyTextOfALaterTurnWithNoTellOwnerGoesToTheChat(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("ask", `{ repository = "owner/shop", n = 41, text = "Cents or dollars?" }`))
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

func TestSendAgainAddsTheOwnerMessageWithItsImagesToTheChatAndClosesTheItem(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	flag := filepath.Join(t.TempDir(), "taken")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"if [ -e '"+flag+"' ]; then true; else kill -9 $PPID; sleep 5; fi\"\n")
	sendChatImages(t, server, leadChat, "Plan the loyalty API", []engine.Image{pngImage, jpegImage})
	failed := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range inbox(t, server) {
			if item.Kind == "Lead failed" {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	status, text := send(t, server, http.MethodPost, fmt.Sprintf("/api/inbox/%d/send", failed.ID), "")

	if status != http.StatusNoContent {
		t.Fatalf("send: status %d: %s", status, text)
	}
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("Inbox = %+v", got)
	}
	var owner []store.ChatMessage
	for _, message := range chatView(t, server, leadChat).Messages {
		if message.Author == "Owner" {
			owner = append(owner, message)
		}
	}
	if len(owner) != 2 || owner[1].Text != "Plan the loyalty API" || owner[1].ID == owner[0].ID {
		t.Fatalf("Owner messages = %+v", owner)
	}
	if !owner[1].BrowserID.Valid {
		t.Error("the new message has no browser id")
	}
	for position, want := range []engine.Image{pngImage, jpegImage} {
		image, err := server.Engine.MessageImage(owner[1].ID, position)
		if err != nil || image == nil || !reflect.DeepEqual(*image, want) {
			t.Errorf("image %d = %+v, %v", position, image, err)
		}
	}
	status, _ = send(t, server, http.MethodPost, fmt.Sprintf("/api/inbox/%d/send", failed.ID), "")
	if status != http.StatusConflict {
		t.Errorf("second send: status %d", status)
	}
}
