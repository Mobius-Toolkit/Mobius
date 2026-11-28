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

// waitForAllRecords waits until the Record messages of the chat of key are exactly want.
func waitForAllRecords(t *testing.T, server *testserver.Server, key engine.ChatKey, want ...string) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return reflect.DeepEqual(records(t, server, key), want) })
}

// waitForRecords is waitForAllRecords for the Records that no label of a person starts. A task that a test starts with
// the label mobius:ready of the owner adds the Record "@owner started #N.", and waitForRecords does not count it.
func waitForRecords(t *testing.T, server *testserver.Server, key engine.ChatKey, want ...string) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(slices.DeleteFunc(records(t, server, key), func(text string) bool { return strings.HasPrefix(text, "@owner started ") }), want)
	})
}

func TestTheMergeOfTheOwnerAddsOneRecordToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := readyToMerge(t, fake, "")

	if status, body := merge(t, server, item.ID); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForAllRecords(t, server, leadChat, "@owner started #41.", "You started the merge of PR #42.")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if got := records(t, server, leadChat); len(got) != 2 {
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

func TestTheStartOfAnIssueInNoWorkstreamAddsOneRecordToTheCoordinatorChat(t *testing.T) {
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

	waitForRecords(t, server, coordinatorChat, "You started owner/shop#50.")
	if got := records(t, server, leadChat); len(got) != 0 {
		t.Errorf("records of the Lead chat = %q", got)
	}
}

func TestTheReadyLabelOfAPersonAddsOneRecordToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForAllRecords(t, server, leadChat, "@owner started #41.")
	waitForPolls(t, fake)
	if got := records(t, server, leadChat); len(got) != 1 {
		t.Errorf("records = %q", got)
	}
}

func TestTheReadyLabelOfAnIssueInNoWorkstreamAddsOneRecordToTheCoordinatorChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 50, "Fix the footer")
	server := startCopiedWithLead(t, fake)

	fake.AddLabel(shop, 50, "mobius:ready", "owner")

	waitForAllRecords(t, server, coordinatorChat, "@owner started owner/shop#50.")
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

func TestTheStopOfAReadyIssueAddsOneRecordToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 40, "Add the price table")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 40)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return stateOfTask(t, server) == "start waits" })
	authorize(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/stop", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForRecords(t, server, leadChat, "You stopped #41.")
}

func TestTheStopOfARunningTaskAddsOneRecordToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\nwhen = \"Plans have a price.\"\nhang = true\n", noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "working" })
	authorize(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/stop", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForRecords(t, server, leadChat, "You stopped #41.")
}

func TestTheRefusedStopOfTheOwnerAddsNoRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)
	authorize(t, server)

	status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/stop", "")

	if status != http.StatusConflict {
		t.Errorf("status = %d: %s", status, body)
	}
	waitForPolls(t, fake)
	if got := records(t, server, leadChat); len(got) != 0 {
		t.Errorf("records = %q", got)
	}
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

func TestTheAnswerOfAQuestionOfTheCoordinatorAddsOneRecordToTheCoordinatorChat(t *testing.T) {
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
	waitForRecords(t, server, coordinatorChat, `You answered "Which unit?".`)
	sendChat(t, server, coordinatorChat, "Ask the Owner again")
	testkit.WaitFor(t, func() bool { return len(coordinatorItems(t, server)) == 1 })
	if status, body := send(t, server, http.MethodPost, "/api/inbox/"+itoa(coordinatorItems(t, server)[0].ID)+"/dismiss", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	waitForRecords(t, server, coordinatorChat, `You answered "Which unit?".`, `You dismissed "Which unit?".`)
}

func TestTheAnswerOfALeadQuestionAddsOneRecordToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	answer(t, server, item.ID, "Cents.")

	waitForNoItem(t, server, "question")
	waitForAllRecords(t, server, leadChat, "@owner started #41.", `You answered "`+item.Title+`".`)
}

func TestTheSendAgainOfAFailedMessageAddsOneRecordToTheChatOfTheItem(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), dies)
	sendChat(t, server, leadChat, "Plan the loyalty points API for the shop checkout\nSecond line.")
	failed := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range inbox(t, server) {
			if item.Kind == "Lead failed" {
				return item, true
			}
		}
		return inboxItem{}, false
	})

	if status, body := send(t, server, http.MethodPost, "/api/inbox/"+itoa(failed.ID)+"/send", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForRecords(t, server, leadChat, `You sent the failed message "Plan the loyalty points API for the shop…" again.`)
	if got := records(t, server, coordinatorChat); len(got) != 0 {
		t.Errorf("records of the Coordinator chat = %q", got)
	}
}

func TestTheApprovalOfAnActionAddsOneRecordToTheChatOfTheSender(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	session := run(t, server, coordinatorSpec(t), "Send.")
	reply(t, server, session)
	item := waitForItem(t, server, "approval")

	approveNext(t, server, 1)

	waitForNoItem(t, server, "approval")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	waitForRecords(t, server, coordinatorChat, `You approved "`+item.Title+`".`)
	if got := records(t, server, leadChat); len(got) != 0 {
		t.Errorf("records of the Lead chat = %q", got)
	}
}

func TestTheRefusedApprovalOfAnActionAddsNoRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	session := run(t, server, coordinatorSpec(t), "Send.")
	reply(t, server, session)
	item := waitForItem(t, server, "approval")
	action := waitForActions(t, server, 1)[0]
	decided(t, server, action.ID, "reject", "")
	waitForRecords(t, server, coordinatorChat, `You rejected "`+item.Title+`".`)

	if status, _ := decide(t, server, action.ID, "approve", ""); status != http.StatusConflict {
		t.Errorf("status = %d, want %d", status, http.StatusConflict)
	}

	if got := records(t, server, coordinatorChat); len(got) != 1 {
		t.Errorf("records = %q", got)
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
		close func(*testkit.FakeGitHub)
		path  string
		want  string
	}{
		"completed": {func(fake *testkit.FakeGitHub) { fake.CloseIssue(shop, 41) }, "complete", "You closed the Workstream."},
		"won't do":  {func(*testkit.FakeGitHub) {}, "close", "You closed the Workstream as won't do."},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			fake.AddUserCode(testkit.AppID, "user-code", "owner")
			addWorkstream(fake)
			server := startCopiedWithLead(t, fake)
			authorize(t, server)
			test.close(fake)

			if status, body := send(t, server, http.MethodPost, "/api/workstreams/owner/shop/12/"+test.path, ""); status != http.StatusNoContent {
				t.Fatalf("status = %d: %s", status, body)
			}

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

func TestAnEngineActionGivesItsRecordAndAddRecordIsTheOnlyWriteOfTheLine(t *testing.T) {
	t.Parallel()
	server := startWithTasks(t, testkit.NewFakeGitHub(t))

	record, err := server.Engine.SetSwitch(t.Context(), "start", false)
	if err != nil {
		t.Fatal(err)
	}

	if want := (engine.Record{Chats: []engine.ChatKey{coordinatorChat}, Text: "You turned Start off."}); !reflect.DeepEqual(record, want) {
		t.Errorf("record = %+v, want %+v", record, want)
	}
	if got := records(t, server, coordinatorChat); len(got) != 0 {
		t.Errorf("records before AddRecord = %q", got)
	}
	server.Engine.AddRecord(t.Context(), engine.Record{Text: "No chat."})
	server.Engine.AddRecord(t.Context(), engine.Record{Chats: []engine.ChatKey{coordinatorChat, leadChat}, Text: "Two chats."})
	if got := records(t, server, coordinatorChat); !reflect.DeepEqual(got, []string{"Two chats."}) {
		t.Errorf("records of the Coordinator = %q", got)
	}
	if got := records(t, server, leadChat); !reflect.DeepEqual(got, []string{"Two chats."}) {
		t.Errorf("records of the Lead = %q", got)
	}
}
