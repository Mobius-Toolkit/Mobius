package engine_test

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// records gives the texts of the Record messages of the chat of key.
func records(t *testing.T, server *testserver.Server, key engine.ChatKey) []string {
	t.Helper()
	var texts []string
	for _, line := range chatLines(t, server, key) {
		if line.Author == "Record" {
			texts = append(texts, line.Text)
		}
	}
	return texts
}

func waitForRecords(t *testing.T, server *testserver.Server, key engine.ChatKey, want ...string) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return reflect.DeepEqual(records(t, server, key), want) })
}

func TestTheMergeOfTheOwnerAddsOneRecordToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := readyToMerge(t, fake, "")

	if status, body := merge(t, server, item.ID); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForRecords(t, server, leadChat, "You started the merge of PR #42.")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if got := records(t, server, leadChat); len(got) != 1 {
		t.Errorf("records = %q", got)
	}
}

func TestTheStartOfTheOwnerAddsOneRecordToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	addWorkstream(fake)
	fake.AddLabel(shop, 41, "mobius:triage", testkit.AppSlug+"[bot]")
	server := startCopiedWithLead(t, fake)
	authorize(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/start", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForRecords(t, server, leadChat, "You started #41.")
}

func TestTheStartOfAnIssueOfAnotherRepositoryNamesTheRepositoryInTheRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := connectGarden(t, fake, seen, "")
	fake.AddLabel(garden, 7, "mobius:triage", testkit.AppSlug+"[bot]")
	authorize(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/garden/issues/7/start", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForRecords(t, server, leadChat, "You started owner/garden#7.")
}

func TestTheStartOfAnIssueInNoWorkstreamAddsNoRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fake.AddIssue(shop, 50, "Fix the footer")
	fake.AddLabel(shop, 50, "mobius:triage", testkit.AppSlug+"[bot]")
	server := startCopiedWithLead(t, fake)
	authorize(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/50/start", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForPolls(t, fake)
	if got := records(t, server, leadChat); len(got) != 0 {
		t.Errorf("records = %q", got)
	}
}

func TestTheResumeOfTheOwnerAddsOneRecordToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	addWorkstream(fake)
	fake.AddLabel(shop, 41, "mobius:needs-human", testkit.AppSlug+"[bot]")
	server := startCopiedWithLead(t, fake)
	authorize(t, server)

	resume(t, server)

	waitForRecords(t, server, leadChat, "You resumed #41.")
}

func TestTheDismissOfTheOwnerAddsOneRecordToTheChatOfTheItem(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Ask the Owner"
call = { tool = "ask", arguments = { text = "Pick a unit.", questions = [{ text = "Which unit?", options = [{ title = "Cents" }, { title = "Dollars" }] }] } }

[[prompts]]
reply = ["Asked"]
`)
	sendChat(t, server, coordinatorChat, "Ask the Owner")
	testkit.WaitFor(t, func() bool { return len(coordinatorItems(t, server)) == 1 })
	item := coordinatorItems(t, server)[0]

	if status, body := send(t, server, http.MethodPost, "/api/inbox/"+itoa(item.ID)+"/dismiss", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForRecords(t, server, coordinatorChat, `You dismissed "Which unit?".`)
	if got := records(t, server, leadChat); len(got) != 0 {
		t.Errorf("records of the Lead chat = %q", got)
	}
}

func dismissFailedMessage(t *testing.T, server *testserver.Server, key engine.ChatKey, text string) {
	t.Helper()
	sendChat(t, server, key, text)
	failed := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range inbox(t, server) {
			if item.Kind == "Lead failed" {
				return item, true
			}
		}
		return inboxItem{}, false
	})

	if status, body := send(t, server, http.MethodPost, "/api/inbox/"+itoa(failed.ID)+"/dismiss", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
}

func TestTheDismissOfAFailedMessageOfTheCoordinatorNamesTheStartOfTheMessage(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), dies)

	dismissFailedMessage(t, server, coordinatorChat, "  Please explain why the queue keeps growing after every plan\nSecond line.")

	waitForRecords(t, server, coordinatorChat, `You dismissed the failed message "Please explain why the queue keeps growi…".`)
	if got := records(t, server, leadChat); len(got) != 0 {
		t.Errorf("records of the Lead chat = %q", got)
	}
}

func TestTheDismissOfAFailedMessageOfTheLeadNamesTheStartOfTheMessage(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), dies)

	dismissFailedMessage(t, server, leadChat, "Plan the loyalty points API for the shop checkout\nSecond line.")

	waitForRecords(t, server, leadChat, `You dismissed the failed message "Plan the loyalty points API for the shop…".`)
	if got := records(t, server, coordinatorChat); len(got) != 0 {
		t.Errorf("records of the Coordinator chat = %q", got)
	}
}

func TestTheAnswerOfAQuestionOfTheCoordinatorAddsNoRecord(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Cents please."
reply = ["Got the answer"]

[[prompts]]
when = "Ask the Owner"
call = { tool = "ask", arguments = { text = "Pick a unit.", questions = [{ text = "Which unit?", options = [{ title = "Cents" }, { title = "Dollars" }] }] } }

[[prompts]]
reply = ["Asked"]
`)
	sendChat(t, server, coordinatorChat, "Ask the Owner")
	testkit.WaitFor(t, func() bool { return len(coordinatorItems(t, server)) == 1 })

	answer(t, server, coordinatorItems(t, server)[0].ID, "Cents please.")

	waitForChat(t, server, coordinatorChat, "Coordinator", "Got the answer")
	sendChat(t, server, coordinatorChat, "Ask the Owner again")
	testkit.WaitFor(t, func() bool { return len(coordinatorItems(t, server)) == 1 })
	if status, body := send(t, server, http.MethodPost, "/api/inbox/"+itoa(coordinatorItems(t, server)[0].ID)+"/dismiss", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	waitForRecords(t, server, coordinatorChat, `You dismissed "Which unit?".`)
}

func TestTheAnswerOfALeadQuestionAddsNoRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	answer(t, server, item.ID, "Cents.")

	waitForNoItem(t, server, "question")
	waitForPolls(t, fake)
	if got := records(t, server, leadChat); len(got) != 0 {
		t.Errorf("records = %q", got)
	}
}

func TestTheApprovalOfAnActionAddsNoRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	session := run(t, server, coordinatorSpec(t), "Send.")
	reply(t, server, session)

	approveNext(t, server, 1)

	waitForNoItem(t, server, "approval")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	for _, key := range []engine.ChatKey{leadChat, coordinatorChat} {
		if got := records(t, server, key); len(got) != 0 {
			t.Errorf("records of %+v = %q", key, got)
		}
	}
}

func TestTheAutopilotLevelOfTheOwnerAddsOneRecordWithTheNameOfTheLevel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)
	authorize(t, server)

	setWorkstreamAutopilot(t, server, "start_and_merge")

	waitForRecords(t, server, leadChat, "You set the Autopilot level to Start and merge.")
}

func TestTheEvergreenSwitchOfTheOwnerAddsOneRecordThatIsNotUnreadAndNotInThePrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)
	sendChat(t, server, leadChat, "Plan the loyalty API")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool { return session.EndedAt.Valid })
	waitForChat(t, server, leadChat, "Lead", "Seen")
	unread, err := server.Engine.UnreadChats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	changes := listen(t, server)

	if status, body := send(t, server, http.MethodPut, "/api/workstreams/owner/shop/12/evergreen", `{"on":true}`); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	change := waitForChange(t, changes, func(change engine.Change) bool { return change.Message != nil && change.Message.Author == "Record" })
	if change.Message.Text != "You turned Evergreen on." {
		t.Errorf("message = %+v", change.Message)
	}
	if got := chatLines(t, server, leadChat); !slices.Contains(got, chatLine{"Record", "You turned Evergreen on."}) {
		t.Errorf("chat = %+v", got)
	}
	if got, err := server.Engine.UnreadChats(t.Context()); err != nil || !reflect.DeepEqual(got, unread) {
		t.Errorf("unread = %+v, %v, want %+v", got, err, unread)
	}

	if status, body := send(t, server, http.MethodPut, "/api/workstreams/owner/shop/12/evergreen", `{"on":false}`); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	waitForRecords(t, server, leadChat, "You turned Evergreen on.", "You turned Evergreen off.")

	sendChat(t, server, leadChat, "Go on")
	later := waitForChatSession(t, server, leadChat, engine.LeadRole, func(later store.Session) bool {
		return later.ID != session.ID && len(promptTexts(t, server, later.ID)) > 0
	})
	prompts := promptTexts(t, server, later.ID)
	if len(prompts) == 0 || !strings.Contains(prompts[0], "Plan the loyalty API") || strings.Contains(prompts[0], "You turned Evergreen") {
		t.Errorf("first prompt = %q", prompts)
	}
	for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
		for _, prompt := range promptTexts(t, server, session.ID) {
			if strings.Contains(prompt, "You turned Evergreen") {
				t.Errorf("a prompt has a record: %q", prompt)
			}
		}
	}
}

func TestTheCloseOfAWorkstreamAddsOneRecordToTheLeadChat(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		close func(*testing.T, *testserver.Server, *testkit.FakeGitHub)
		want  string
	}{
		"completed": {func(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) {
			fake.CloseIssue(shop, 41)
			if err := server.Engine.CompleteWorkstream(t.Context(), shop, 12); err != nil {
				t.Fatal(err)
			}
		}, "You closed the Workstream."},
		"won't do": {func(t *testing.T, server *testserver.Server, _ *testkit.FakeGitHub) {
			if err := server.Engine.CloseWorkstreamWontDo(t.Context(), shop, 12); err != nil {
				t.Fatal(err)
			}
		}, "You closed the Workstream as won't do."},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			addWorkstream(fake)
			server := startCopiedWithLead(t, fake)

			test.close(t, server, fake)

			waitForRecords(t, server, leadChat, test.want)
		})
	}
}

func TestEachSwitchChangeOfTheOwnerAddsOneRecordToTheCoordinatorChatThatIsNotUnread(t *testing.T) {
	t.Parallel()
	server := startWithTasks(t, testkit.NewFakeGitHub(t))
	unread, err := server.Engine.UnreadChats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, kind := range []string{"Start", "Merge", "Repairs"} {
		for _, state := range []string{"off", "on"} {
			if status := putSwitch(t, server, strings.ToLower(kind), state == "on"); status != http.StatusNoContent {
				t.Fatalf("%s %s: status %d", kind, state, status)
			}
			want = append(want, "You turned "+kind+" "+state+".")
			waitForRecords(t, server, coordinatorChat, want...)
		}
	}

	if got, err := server.Engine.UnreadChats(t.Context()); err != nil || !reflect.DeepEqual(got, unread) {
		t.Errorf("unread = %+v, %v, want %+v", got, err, unread)
	}
}
