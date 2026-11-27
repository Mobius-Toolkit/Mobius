package engine_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type ownerChatMessage struct {
	ID          int64          `json:"id"`
	Author      string         `json:"author"`
	BrowserID   string         `json:"browserId"`
	Text        string         `json:"text"`
	DeliveredAt *string        `json:"deliveredAt"`
	DisplayOnly bool           `json:"displayOnly"`
	InboxItems  []apiInboxItem `json:"inboxItems"`
	Selections  []apiSelection `json:"selections"`
	Images      int64          `json:"images"`
}

func ownerChatMessages(t *testing.T, server *testserver.Server, key engine.ChatKey) []ownerChatMessage {
	t.Helper()
	query := url.Values{"organization": {key.Organization}}
	if key.Repository != "" {
		query.Set("repository", key.Repository)
		query.Set("workstream", itoa(key.Workstream))
	}
	chat := apiData[struct {
		Messages []ownerChatMessage `json:"messages"`
	}](t, server, "/api/chat?"+query.Encode())
	return chat.Messages
}

func seedDisplayOnlyMessage(id int64, repository string, workstream int64, text, browserID string) string {
	const format = `INSERT INTO chat_messages (id, organization, repository, workstream, author, time, text, browser_id, display_only)
	VALUES (%d, 'owner', '%s', %d, 'Owner', '2026-10-04T10:00:0%dZ', '%s', %s, 1)`
	if browserID == "" {
		return fmt.Sprintf(format, id, repository, workstream, id, text, "NULL")
	}
	return fmt.Sprintf(format, id, repository, workstream, id, text, "'"+browserID+"'")
}

func TestAnAnswerOfALeadQuestionAddsADisplayOnlyMessageWithTheCardAndGivesTheLeadOneTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	answer(t, server, item.ID, "Cents.")

	messages := ownerChatMessages(t, server, leadChat)
	i := slices.IndexFunc(messages, func(m ownerChatMessage) bool { return m.Author == "Owner" && m.Text == "Cents." })
	if i < 0 {
		t.Fatalf("missing Owner answer message in %+v", messages)
	}
	got := messages[i]
	wantCards := []apiInboxItem{{
		Reference: "#41",
		Title:     item.Title,
		Reason:    item.Reason,
		Link:      "https://github.com/owner/shop/issues/41",
	}}
	if !got.DisplayOnly || got.BrowserID != "" || got.DeliveredAt != nil || !slices.Equal(got.InboxItems, wantCards) {
		t.Errorf("message = %+v, want displayOnly with card %+v", got, wantCards)
	}

	waitForLeadPrompt(t, server, ` answer of #41 "Add plan model" by @owner:`)
	waitForNoItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return !chatView(t, server, leadChat).Writing })
	waitForPolls(t, fake)

	var answerPrompts int
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, "Cents.") {
			answerPrompts++
		}
		if strings.Contains(prompt, "# Owner message") && strings.Contains(prompt, "Cents.") {
			t.Errorf("the Lead got the answer through the chat: %s", prompt)
		}
	}
	if answerPrompts != 1 {
		t.Errorf("Lead prompts with Cents. = %d, want 1", answerPrompts)
	}
}

func TestAnAnswerOfACoordinatorQuestionAddsOneMessageWithTheCardAndGivesItOnce(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Cents and Pro, please."
reply = ["Got the answer"]

[[prompts]]
when = "Ask the Owner"
call = { tool = "ask", arguments = { text = "Two choices.", questions = [
  { text = "Which unit?", options = [{ title = "Cents", details = "Whole numbers." }, { title = "Dollars" }] },
  { text = "Which plans?", multiple = true, options = [{ title = "Free" }, { title = "Pro" }] },
] } }

[[prompts]]
reply = ["Asked"]
`)
	sendChat(t, server, coordinatorChat, "Ask the Owner")
	item := testkit.WaitForValue(t, func() (coordinatorItem, bool) {
		items := coordinatorItems(t, server)
		if len(items) != 1 {
			return coordinatorItem{}, false
		}
		return items[0], true
	})

	answer(t, server, item.ID, "Cents and Pro, please.")

	waitForChat(t, server, coordinatorChat, "Coordinator", "Got the answer")
	testkit.WaitFor(t, func() bool { return !chatView(t, server, coordinatorChat).Writing })

	var found []ownerChatMessage
	for _, message := range ownerChatMessages(t, server, coordinatorChat) {
		if message.Author == "Owner" && message.Text == "Cents and Pro, please." {
			found = append(found, message)
		}
	}
	wantCards := []apiInboxItem{{
		Reference: "",
		Title:     "Which unit?",
		Reason:    "The Coordinator asks a question",
		Link:      "",
	}}
	if len(found) != 1 || found[0].DisplayOnly || found[0].DeliveredAt == nil || !slices.Equal(found[0].InboxItems, wantCards) {
		t.Errorf("Owner messages = %+v, want 1 delivered non-displayOnly message with card %+v", found, wantCards)
	}
	var prompts int
	for _, prompt := range coordinatorPrompts(t, server) {
		if strings.Contains(prompt, "Cents and Pro, please.") {
			prompts++
		}
	}
	if prompts != 1 {
		t.Errorf("Coordinator prompts with answer = %d, want 1", prompts)
	}
}

func TestDoneOfATodoWithANoteAddsADisplayOnlyMessageBeforeTheRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, mandatoryNeed)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY. The Owner got it for review. The pull request stays a draft until the Owner closes the open todos.")
	todo := waitForItem(t, server, "todo")

	status, body := doneTodo(t, server, todo.ID, "Set in prod.")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	messages := ownerChatMessages(t, server, leadChat)
	ownerIdx := slices.IndexFunc(messages, func(m ownerChatMessage) bool {
		return m.Author == "Owner" && m.Text == "Set in prod."
	})
	recordIdx := slices.IndexFunc(messages, func(m ownerChatMessage) bool {
		return m.Author == "Record" && m.Text == `You marked the todo "PLAN_API_KEY" of PR #42 as done.`
	})
	if ownerIdx < 0 || recordIdx < 0 || ownerIdx >= recordIdx {
		t.Fatalf("ownerIdx = %d, recordIdx = %d in %+v", ownerIdx, recordIdx, messages)
	}
	got := messages[ownerIdx]
	wantCards := []apiInboxItem{{
		Reference: "PR #42",
		Title:     "PLAN_API_KEY",
		Reason:    todo.Reason,
		Link:      "https://github.com/owner/shop/pull/42",
	}}
	if !got.DisplayOnly || got.BrowserID != "" || got.DeliveredAt != nil || !slices.Equal(got.InboxItems, wantCards) {
		t.Errorf("message = %+v, want displayOnly with card %+v", got, wantCards)
	}

	waitForPolls(t, fake)
	testkit.WaitFor(t, func() bool { return !chatView(t, server, leadChat).Writing })
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, "Set in prod.") {
			t.Errorf("the Lead got the todo note: %s", prompt)
		}
	}
}

func TestDoneOfATodoWithNoNoteAddsOnlyTheRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, mandatoryNeed)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY. The Owner got it for review. The pull request stays a draft until the Owner closes the open todos.")
	todo := waitForItem(t, server, "todo")

	status, body := doneTodo(t, server, todo.ID, "   ")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	messages := ownerChatMessages(t, server, leadChat)
	if !slices.ContainsFunc(messages, func(m ownerChatMessage) bool {
		return m.Author == "Record" && m.Text == `You marked the todo "PLAN_API_KEY" of PR #42 as done.`
	}) {
		t.Errorf("missing todo record in %+v", messages)
	}
	for _, message := range messages {
		if message.DisplayOnly || (message.Author == "Owner" && message.Text != "Approve #41") {
			t.Errorf("unexpected Owner message = %+v", message)
		}
	}
}

func TestDoneOfAnOwnerActionWithNextImplementerAddsMessageWithoutNoteInRecordAndGivesOneDispatchEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadMarks+leadSees, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	sendChat(t, server, leadChat, "Mark implementer on #41")
	item := waitForItem(t, server, "owner action")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "owner_action" })

	status, body := doneTodo(t, server, item.ID, "Logged in.")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	messages := ownerChatMessages(t, server, leadChat)
	ownerIdx := slices.IndexFunc(messages, func(m ownerChatMessage) bool {
		return m.Author == "Owner" && m.Text == "Logged in."
	})
	wantRecord := "You marked the action \"" + loginAction + "\" of #41 as done."
	recordIdx := slices.IndexFunc(messages, func(m ownerChatMessage) bool {
		return m.Author == "Record" && m.Text == wantRecord
	})
	if ownerIdx < 0 || recordIdx < 0 || ownerIdx >= recordIdx {
		t.Fatalf("ownerIdx = %d, recordIdx = %d in %+v", ownerIdx, recordIdx, messages)
	}
	for _, message := range messages {
		if message.Author == "Record" && strings.Contains(message.Text, "Note:") {
			t.Errorf("record has Note part: %+v", message)
		}
	}
	got := messages[ownerIdx]
	wantCards := []apiInboxItem{{
		Reference: "#41",
		Title:     loginAction,
		Reason:    workstreamTitle,
		Link:      "https://github.com/owner/shop/issues/41",
	}}
	if !got.DisplayOnly || got.BrowserID != "" || got.DeliveredAt != nil || !slices.Equal(got.InboxItems, wantCards) {
		t.Errorf("message = %+v, want displayOnly with card %+v", got, wantCards)
	}

	testkit.WaitFor(t, func() bool { return !chatView(t, server, leadChat).Writing })
	waitForPolls(t, fake)

	var dispatches []string
	for _, event := range leadEvents(t, server) {
		if event.Kind == "dispatch" && strings.Contains(event.Payload, "action of the Owner done of") {
			dispatches = append(dispatches, event.Payload)
		}
	}
	if len(dispatches) != 1 || !strings.Contains(dispatches[0], "\nNote of the Owner: Logged in.") {
		t.Errorf("dispatches = %q", dispatches)
	}
	var notePrompts int
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, "Logged in.") {
			notePrompts++
		}
		if strings.Contains(prompt, "# Owner message") && strings.Contains(prompt, "Logged in.") {
			t.Errorf("the Lead got the note through the chat: %s", prompt)
		}
	}
	if notePrompts != 1 {
		t.Errorf("Lead prompts with Logged in. = %d, want 1", notePrompts)
	}
}

func TestDoneOfAnOwnerActionWithNextImplementerAndNoNoteAddsOnlyTheRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadMarks+leadSees, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	sendChat(t, server, leadChat, "Mark implementer on #41")
	item := waitForItem(t, server, "owner action")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "owner_action" })

	status, body := doneTodo(t, server, item.ID, "  ")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	messages := ownerChatMessages(t, server, leadChat)
	wantRecord := "You marked the action \"" + loginAction + "\" of #41 as done."
	if !slices.ContainsFunc(messages, func(m ownerChatMessage) bool {
		return m.Author == "Record" && m.Text == wantRecord
	}) {
		t.Errorf("missing owner action record in %+v", messages)
	}
	for _, message := range messages {
		if message.DisplayOnly || (message.Author == "Owner" && message.Text != "Mark implementer on #41") {
			t.Errorf("unexpected Owner message = %+v", message)
		}
	}
}

func TestARestartDoesNotGiveADisplayOnlyOwnerMessageToAnAgent(t *testing.T) {
	t.Parallel()
	server := startWithLeadMessages(t,
		seedMessage(1, shop, 12, "Owner", "Waiting message.", "a1", "", ""),
		seedDisplayOnlyMessage(2, shop, 12, "Inbox answer with id.", "a2"),
		seedDisplayOnlyMessage(3, shop, 12, "Inbox answer with no id.", ""),
	)

	testkit.WaitFor(t, func() bool { return strings.Contains(joinedLeadPrompts(t, server), "Waiting message.") })
	endedChatSession(t, server, 0)

	prompts := joinedLeadPrompts(t, server)
	for _, text := range []string{"Inbox answer with id.", "Inbox answer with no id."} {
		if strings.Contains(prompts, text) {
			t.Errorf("the agent got display-only message %q after restart: %s", text, prompts)
		}
	}

	sendChat(t, server, leadChat, "Second turn.")
	second := endedChatSession(t, server, 1)
	historyPrompt := promptTexts(t, server, second.ID)[0]
	_, history, _ := strings.Cut(historyPrompt, "# Chat history")
	if !strings.Contains(history, "Waiting message.") {
		t.Errorf("history missing normal Owner message: %s", history)
	}
	for _, text := range []string{"Inbox answer with id.", "Inbox answer with no id."} {
		if strings.Contains(history, text) {
			t.Errorf("history contains display-only message %q: %s", text, history)
		}
	}
}
