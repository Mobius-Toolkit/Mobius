package engine_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
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

// leadChat is the Lead chat of the Workstream #12.
var leadChat = engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 12}

type chatLine struct {
	Author, Text string
}

func sendChat(t *testing.T, server *testserver.Server, key engine.ChatKey, text string) {
	t.Helper()
	if err := server.Engine.SendChat(t.Context(), key, text); err != nil {
		t.Fatal(err)
	}
}

func stopChat(t *testing.T, server *testserver.Server, key engine.ChatKey) {
	t.Helper()
	if err := server.Engine.StopChat(t.Context(), key); err != nil {
		t.Fatal(err)
	}
}

func chatView(t *testing.T, server *testserver.Server, key engine.ChatKey) engine.ChatView {
	t.Helper()
	view, err := server.Engine.ChatView(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func chatLines(t *testing.T, server *testserver.Server, key engine.ChatKey) []chatLine {
	t.Helper()
	var lines []chatLine
	for _, message := range chatView(t, server, key).Messages {
		lines = append(lines, chatLine{message.Author, message.Text})
	}
	return lines
}

// waitForChat waits until the chat has a message of author with text.
func waitForChat(t *testing.T, server *testserver.Server, key engine.ChatKey, author, text string) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return slices.Contains(chatLines(t, server, key), chatLine{author, text}) })
}

// chatSessions gives the sessions of role in the chat of key, the oldest first.
func chatSessions(t *testing.T, server *testserver.Server, key engine.ChatKey, role string) []store.Session {
	t.Helper()
	sessions, err := store.New(server.DB).ListSessions(t.Context(), store.ListSessionsParams{Organization: key.Organization, Repository: key.Repository, Workstream: key.Workstream})
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(sessions, func(session store.Session) bool { return session.Role != role })
}

// waitForChatSession waits for the first session of role in the chat of key that is ready.
func waitForChatSession(t *testing.T, server *testserver.Server, key engine.ChatKey, role string, ready func(store.Session) bool) store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() (store.Session, bool) {
		for _, session := range chatSessions(t, server, key, role) {
			if ready(session) {
				return session, true
			}
		}
		return store.Session{}, false
	})
}

// endedChatSession waits for the end of the Lead session index of the Workstream #12.
func endedChatSession(t *testing.T, server *testserver.Server, index int) store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, leadChat, engine.LeadRole)
		if len(sessions) <= index {
			return store.Session{}, false
		}
		return sessions[index], sessions[index].EndedAt.Valid
	})
}

// promptTexts gives the text of each prompt of the session.
func promptTexts(t *testing.T, server *testserver.Server, session int64) []string {
	t.Helper()
	var texts []string
	for _, row := range rows(t, server, session, "prompt") {
		texts = append(texts, row["text"].(string))
	}
	return texts
}

// leadPrompts gives the prompts of the Lead sessions of the Workstream #12, the oldest first.
func leadPrompts(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var texts []string
	for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
		texts = append(texts, promptTexts(t, server, session.ID)...)
	}
	return texts
}

// inOrder fails the test when text does not have each part, or when the first places of the parts are not in the
// order of parts.
func inOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	last := -1
	for _, part := range parts {
		at := strings.Index(text, part)
		if at < 0 || at < last {
			t.Fatalf("%q is not in order in %s", part, text)
		}
		last = at
	}
}

func keepSessionOpen(cfg *config.Config) {
	cfg.LeadIdleTimeout = 30 * time.Second
}

// dispatchTask adds the issue number with title below the Workstream #12, and the Owner adds mobius:ready.
func dispatchTask(fake *testkit.FakeGitHub, number int64, title string) {
	fake.AddIssue(shop, number, title)
	fake.AddSubIssue(shop, 12, number)
	fake.AddLabel(shop, number, "mobius:ready", "owner")
}

// undelivered gives the events of the Workstream #12 that no Lead took.
func undelivered(t *testing.T, server *testserver.Server) []store.LeadEvent {
	t.Helper()
	events, err := store.New(server.DB).ListUndeliveredLeadEvents(t.Context(), store.ListUndeliveredLeadEventsParams{Repository: shop, Workstream: 12})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// eventDelivered tells if the Workstream #12 has a Lead event and the Lead took each event. One query reads both
// counts, so a new event cannot come between them.
func eventDelivered(t *testing.T, server *testserver.Server) bool {
	t.Helper()
	var events, open int
	err := server.DB.QueryRow("SELECT count(*), coalesce(sum(delivered_at IS NULL), 0) FROM lead_events WHERE repository = ? AND workstream = 12", shop).Scan(&events, &open)
	if err != nil {
		t.Fatal(err)
	}
	return events > 0 && open == 0
}

func TestAnOwnerMessageGetsTheLeadReplyInTheChat(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Hello\", \" there\"]\n")

	sendChat(t, server, leadChat, "Plan the loyalty API")

	waitForChat(t, server, leadChat, "Lead", "Hello there")
	want := []chatLine{{"Owner", "Plan the loyalty API"}, {"Lead", "Hello there"}}
	if got := chatLines(t, server, leadChat); !reflect.DeepEqual(got, want) {
		t.Errorf("chat = %+v", got)
	}
	session := endedChatSession(t, server, 0)
	if got := reply(t, server, session.ID); got != "Hello there" {
		t.Errorf("reply = %q", got)
	}
}

func TestTheLeadWorksInTheLeadDirectoryWithTheGHOfTheOwner(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "[[prompts]]\nreply = [\"Hello\"]\n")

	sendChat(t, server, leadChat, "Plan the loyalty API")

	waitForChat(t, server, leadChat, "Lead", "Hello")
	harnesses := os.DirFS(filepath.Join(dataDir, "harnesses"))
	pwd, err := fs.ReadFile(harnesses, "pwd")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(dataDir, "leads", "owner", "shop", "12"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(pwd)); got != want {
		t.Errorf("pwd = %s, want %s", got, want)
	}
	env, err := fs.ReadFile(harnesses, "env")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "MOBIUS_GH_TOKEN_URL=") {
		t.Errorf("env = %s", env)
	}
}

func TestAChatSessionGetsTheModelTheEffortAndTheFullAutoMode(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Hello\"]\n")

	sendChat(t, server, leadChat, "Plan the loyalty API")

	session := endedChatSession(t, server, 0)
	updates := rows(t, server, session.ID, "update")
	got := []string{currentOption(t, updates, "mode"), currentOption(t, updates, "model"), currentOption(t, updates, "thought_level")}
	if want := []string{"bypassPermissions", "opus", "high"}; !reflect.DeepEqual(got, want) {
		t.Errorf("options = %q", got)
	}
}

func TestARefusedModelEndsTheChatSessionBeforeTheFirstPrompt(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "")
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", "[options]\nmodel = [\"sonnet\", \"haiku\"]\n")

	sendChat(t, server, leadChat, "Plan the loyalty API")

	session := endedChatSession(t, server, 0)
	if session.EndReason.String != "failed" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	if got := promptTexts(t, server, session.ID); len(got) != 0 {
		t.Errorf("prompts = %q", got)
	}
	errors := rows(t, server, session.ID, "error")
	if len(errors) != 1 || errors[0]["message"] != `claude-agent-acp refuses model "opus", it has: sonnet, haiku` {
		t.Errorf("errors = %v", errors)
	}
	testkit.WaitFor(t, func() bool { return !chatView(t, server, leadChat).Writing })
}

func TestAHarnessThatNeedsALoginFailsTheChatSession(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "")
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", "login_required = true\n"+options)

	sendChat(t, server, leadChat, "Plan the loyalty API")

	session := endedChatSession(t, server, 0)
	errors := rows(t, server, session.ID, "error")
	if session.EndReason.String != "failed" || len(errors) != 1 || !strings.Contains(errors[0]["message"].(string), "Authentication required") {
		t.Errorf("session = %+v, errors = %v", session, errors)
	}
}

func TestTheFirstPromptHasTheContextPartsInOrder(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "")
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddLabel(shop, 41, "mobius:ready", "mallory")
	fake.AddIssue(shop, 42, "Old spike")
	fake.CloseIssue(shop, 42)
	fake.AddIssue(shop, 43, "Plan API")
	fake.AddIssue(shop, 50, "Billing")
	fake.AddLabel(shop, 50, "mobius:workstream", "owner")
	fake.AddIssue(shop, 51, "Invoice totals")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddSubIssue(shop, 12, 42)
	fake.AddSubIssue(shop, 41, 43)
	fake.AddSubIssue(shop, 12, 50)
	fake.AddSubIssue(shop, 50, 51)
	lead := filepath.Join(dataDir, "leads", "owner", "shop", "12")
	if err := os.MkdirAll(lead, 0o750); err != nil {
		t.Fatal(err)
	}
	var memory []string
	for line := 1; line <= 201; line++ {
		memory = append(memory, fmt.Sprintf("note %d", line))
	}
	if err := os.WriteFile(filepath.Join(lead, "MEMORY.md"), []byte(strings.Join(memory, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	for number := 1; number <= 21; number++ {
		author := "Owner"
		if number%2 == 0 {
			author = "Lead"
		}
		_, err := store.New(server.DB).AddChatMessage(t.Context(), store.AddChatMessageParams{
			Organization: "owner", Repository: shop, Workstream: 12, Author: author, Time: time.Now().UTC().Format(time.RFC3339Nano), Text: fmt.Sprintf("message %d", number),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	sendChat(t, server, leadChat, "Plan the next step")

	prompt := promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]
	inOrder(t, prompt,
		"You are the Lead of one Workstream.",
		"# Brief\n\nShip loyalty plans to all shops.\n",
		"# MEMORY.md\n\nnote 1\n",
		"note 200\nMEMORY.md is too long. Make it shorter.\n",
		"# Task list\n\n#41 Add plan model: ready\n#43 Plan API: open\n\n",
		"# Chat history\n\nLead (",
		"):\nmessage 2\n\n",
		"):\nmessage 21\n\n",
		"# Owner message\n\nPlan the next step",
	)
	for _, absent := range []string{"note 201", "message 1\n", "Old spike", "Invoice totals"} {
		if strings.Contains(prompt, absent) {
			t.Errorf("%q in %s", absent, prompt)
		}
	}
	if !strings.HasSuffix(prompt, "Plan the next step") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestANewOwnerMessageWaitsForTheTurnAndAStopEndsTheTurn(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nhang = true\n\n[[prompts]]\nreply = [\"After the stop\"]\n")
	sendChat(t, server, leadChat, "Plan the loyalty API")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return len(promptTexts(t, server, session.ID)) == 1
	})

	sendChat(t, server, leadChat, "Also add a plan price")

	if !chatView(t, server, leadChat).Writing {
		t.Error("the Lead does not write")
	}
	if got := promptTexts(t, server, session.ID); len(got) != 1 {
		t.Errorf("prompts = %q", got)
	}

	stopChat(t, server, leadChat)

	waitForChat(t, server, leadChat, "Lead", "After the stop")
	session = endedChatSession(t, server, 0)
	if session.EndReason.String != "idle" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	if got := promptTexts(t, server, session.ID); got[1] != "Also add a plan price" {
		t.Errorf("prompts = %q", got)
	}
}

func TestAnIdleLeadSavesItsMemoryAndTheNextMessageStartsANewSession(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"First answer\"]\n")
	sendChat(t, server, leadChat, "Plan the loyalty API")

	first := endedChatSession(t, server, 0)

	if first.EndReason.String != "idle" {
		t.Errorf("end reason = %s", first.EndReason.String)
	}
	if got := promptTexts(t, server, first.ID); got[len(got)-1] != "Save in the Workstream memory what the next session needs." {
		t.Errorf("prompts = %q", got)
	}

	sendChat(t, server, leadChat, "Add a plan price")

	prompt := promptTexts(t, server, endedChatSession(t, server, 1).ID)[0]
	_, history, _ := strings.Cut(prompt, "# Chat history")
	for _, part := range []string{"):\nPlan the loyalty API\n", "):\nFirst answer\n"} {
		if !strings.Contains(history, part) {
			t.Errorf("%q not in %s", part, history)
		}
	}
	if !strings.HasSuffix(history, "# Owner message\n\nAdd a plan price") {
		t.Errorf("history = %s", history)
	}
}

func TestALeadReplyIsUnreadUntilTheOwnerSeesIt(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Hello\"]\n")
	changes := listen(t, server)

	sendChat(t, server, leadChat, "Plan the loyalty API")

	waitForChat(t, server, leadChat, "Lead", "Hello")
	unread, err := server.Engine.UnreadChats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := []engine.Unread{{Key: leadChat, Count: 1}}; !reflect.DeepEqual(unread, want) {
		t.Errorf("unread = %+v", unread)
	}
	messages := chatView(t, server, leadChat).Messages

	if err := server.Engine.SeeChat(t.Context(), leadChat, messages[len(messages)-1].ID); err != nil {
		t.Fatal(err)
	}

	if unread, err = server.Engine.UnreadChats(t.Context()); err != nil || len(unread) != 0 {
		t.Errorf("unread = %+v, %v", unread, err)
	}
	waitForChange(t, changes, func(change engine.Change) bool {
		return change.Unread != nil && *change.Unread == engine.Unread{Key: leadChat}
	})
}

// waitForChange waits for a change of the engine that matches. The test fails after one minute.
func waitForChange(t *testing.T, changes <-chan engine.Change, matches func(engine.Change) bool) engine.Change {
	t.Helper()
	deadline := time.After(time.Minute)
	for {
		select {
		case change, ok := <-changes:
			if !ok {
				t.Fatal("the engine closed the listener")
			}
			if matches(change) {
				return change
			}
		case <-deadline:
			t.Fatal("no such change after one minute")
		}
	}
}

func TestAStopBeforeTheFirstTurnStartsNoTurn(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nhang = true\n")

	sendChat(t, server, leadChat, "Plan the loyalty API")
	stopChat(t, server, leadChat)

	waitForPolls(t, fake)
	for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
		if session.EndReason.String != "stopped" || len(promptTexts(t, server, session.ID)) != 0 {
			t.Errorf("session = %+v", session)
		}
	}
	if chatView(t, server, leadChat).Writing {
		t.Error("the Lead writes")
	}
}

func TestTheLeadChatCreatesAWorkstreamAfterTheApproval(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "# Event\n\n"
list_tools = true

[[prompts]]
when = "# Owner message\n\nMove the API work to a new Workstream."
reply = ["Title: Shop API\n\nBrief: The public API of the shop. The context is in #12."]

[[prompts]]
when = "Yes, create it."
call = { tool = "create_workstream", arguments = { title = "Shop API", brief = "The public API of the shop. The context is in #12." } }
`)
	changes := listen(t, server)

	sendChat(t, server, leadChat, "Move the API work to a new Workstream.")
	waitForChat(t, server, leadChat, "Lead", "Title: Shop API\n\nBrief: The public API of the shop. The context is in #12.")
	sendChat(t, server, leadChat, "Yes, create it.")

	waitForChat(t, server, leadChat, "Lead", "Created the Workstream #13.")
	if title, body := fake.Issue(shop, 13); title != "Shop API" || body != "The public API of the shop. The context is in #12." {
		t.Errorf("issue = %s, %s", title, body)
	}
	if got := fake.Labels(shop, 13); !slices.Equal(got, []string{"mobius:workstream"}) {
		t.Errorf("labels = %v", got)
	}
	waitForWorkstreams(t, changes)
	testkit.WaitFor(t, func() bool { return len(workstreams(t, server)) == 2 })
	// The Lead of the new Workstream gets the creation event, and it has the tools of each Lead.
	ws13 := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 13}
	text := testkit.WaitForValue(t, func() (string, bool) {
		sessions := chatSessions(t, server, ws13, engine.LeadRole)
		if len(sessions) == 0 {
			return "", false
		}
		text := reply(t, server, sessions[0].ID)
		return text, text != ""
	})
	for _, name := range []string{"tell_owner", "create_workstream", "move_task"} {
		if !slices.Contains(toolNames(t, text), name) {
			t.Errorf("tools = %v", toolNames(t, text))
		}
	}
}

func TestTheLeadChatCreatesAWorkstreamAndMovesATaskToIt(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	// The first prompt of a new session has the earlier messages in its history, so the prompt of the newest message comes first.
	server, _ := connect(t, fake, `
[[prompts]]
when = "# Event\n\n"
reply = ["Noted."]

[[prompts]]
when = "Yes, move it."
call = { tool = "move_task", arguments = { n = 13, workstream = 15 } }

[[prompts]]
when = "Yes, create it."
call = { tool = "create_workstream", arguments = { title = "Shop API", brief = "The public API of the shop. The context is in #12." } }
`)
	fake.AddIssue(shop, 13, "Add the API route")
	fake.AddIssue(shop, 14, "Add plan model")
	fake.AddSubIssue(shop, 12, 13)
	fake.AddSubIssue(shop, 12, 14)
	waitForPoll(t, server, fake)

	sendChat(t, server, leadChat, "Yes, create it.")
	waitForChat(t, server, leadChat, "Lead", "Created the Workstream #15.")
	sendChat(t, server, leadChat, "Yes, move it.")

	waitForChat(t, server, leadChat, "Lead", "Moved #13 to the Workstream #15.")
	if got := fake.SubIssueNumbers(shop, 15); !slices.Equal(got, []int64{13}) {
		t.Errorf("sub-issues of #15 = %v", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); !slices.Equal(got, []int64{14}) {
		t.Errorf("sub-issues of #12 = %v", got)
	}
	testkit.WaitFor(t, func() bool {
		return slices.Equal(taskNumbers(t, server, 12), []int64{14}) && slices.Equal(taskNumbers(t, server, 15), []int64{13})
	})
}

func TestTheLeadGetsAnEventAndTheNextOwnerQuestionInTheSameSession(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"Noted.\"]\n\n[[prompts]]\nreply = [\"The Owner dispatched #41.\"]\n", keepSessionOpen)

	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	sendChat(t, server, leadChat, "What happened with #41?")

	waitForChat(t, server, leadChat, "Lead", "The Owner dispatched #41.")
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	prompts := promptTexts(t, server, sessions[0].ID)
	if len(prompts) != 2 || !strings.Contains(prompts[0], "# Event\n\n") || !strings.Contains(prompts[0], ` dispatch of #41 "Add plan model" by @owner:`) || prompts[1] != "What happened with #41?" {
		t.Errorf("prompts = %q", prompts)
	}
	// The reply text of a turn for an event goes only to the Transcript.
	var leadTexts []string
	for _, line := range chatLines(t, server, leadChat) {
		if line.Author == "Lead" {
			leadTexts = append(leadTexts, line.Text)
		}
	}
	if !slices.Equal(leadTexts, []string{"The Owner dispatched #41."}) {
		t.Errorf("Lead messages = %q", leadTexts)
	}
	if got := reply(t, server, sessions[0].ID); !strings.HasPrefix(got, "Noted.") {
		t.Errorf("reply = %q", got)
	}
}

func TestAnEventDuringATurnForAnOwnerMessageGetsItsOwnTurnAfterThatTurn(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Plan the API\"\nhang = true\n\n[[prompts]]\nreply = [\"Noted.\"]\n", keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the API")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return len(promptTexts(t, server, session.ID)) == 1
	})

	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == 1 })
	if got := promptTexts(t, server, session.ID); len(got) != 1 {
		t.Errorf("prompts = %q", got)
	}
	stopChat(t, server, leadChat)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := promptTexts(t, server, session.ID)
		return prompts, len(prompts) == 2 && eventDelivered(t, server)
	})
	if !strings.HasSuffix(prompts[0], "# Owner message\n\nPlan the API") || !strings.HasPrefix(prompts[1], "# Event\n\n") || !strings.Contains(prompts[1], " dispatch of #41 ") {
		t.Errorf("prompts = %q", prompts)
	}
	if got := chatSessions(t, server, leadChat, engine.LeadRole); len(got) != 1 {
		t.Errorf("sessions = %+v", got)
	}
}

func TestAnEventKeepsItsPlaceBeforeALaterOwnerMessage(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Plan the API\"\nhang = true\n\n[[prompts]]\nreply = [\"Noted.\"]\n\n[[prompts]]\nreply = [\"Done.\"]\n", keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the API")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return len(promptTexts(t, server, session.ID)) == 1
	})
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == 1 })
	sendChat(t, server, leadChat, "Also add a price")
	stopChat(t, server, leadChat)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := promptTexts(t, server, session.ID)
		return prompts, len(prompts) == 3 && eventDelivered(t, server)
	})
	if !strings.Contains(prompts[1], " dispatch of #41 ") || prompts[2] != "Also add a price" {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAnEventAndAnOwnerMessageMakeOneLeadSession(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"Noted.\"]\n\n[[prompts]]\nreply = [\"Hello\"]\n", keepSessionOpen)

	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	sendChat(t, server, leadChat, "Hello")

	waitForChat(t, server, leadChat, "Lead", "Hello")
	if got := tree(t, server); len(got) != 1 || got[0].Session.Role != engine.LeadRole {
		t.Errorf("sessions = %+v", got)
	}
}

// waitForPolls waits for two more polls of owner/shop with no change.
func waitForPolls(t *testing.T, fake *testkit.FakeGitHub) {
	t.Helper()
	before := fake.NotModifiedCount()
	testkit.WaitFor(t, func() bool { return fake.NotModifiedCount() >= before+4 })
}

func TestAStopDoesNotCancelATurnForAnEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nhang = true\n", keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return len(promptTexts(t, server, session.ID)) == 1
	})

	stopChat(t, server, leadChat)
	waitForPolls(t, fake)

	if got := chatSessions(t, server, leadChat, engine.LeadRole)[0]; got.EndedAt.Valid || len(promptTexts(t, server, session.ID)) != 1 {
		t.Errorf("session = %+v", got)
	}
	if got := undelivered(t, server); len(got) != 1 {
		t.Errorf("undelivered = %+v", got)
	}
}

// eventLines gives the number of events in the chat.
func eventLines(t *testing.T, server *testserver.Server) int {
	t.Helper()
	count := 0
	for _, line := range chatLines(t, server, leadChat) {
		if line.Author == "Event" {
			count++
		}
	}
	return count
}

// onlySession gives the prompts of the one Lead session of the Workstream #12.
func onlySession(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	return promptTexts(t, server, sessions[0].ID)
}

// held tells if the Workstream #12 has events undelivered events, and none of them is ready. The read of the ready
// events comes after the read of the undelivered events, so a new event between the two reads gives false.
func held(t *testing.T, server *testserver.Server, events int) bool {
	t.Helper()
	if len(undelivered(t, server)) != events {
		return false
	}
	ready, err := store.New(server.DB).ListReadyLeadEvents(t.Context(), store.ListReadyLeadEventsParams{Repository: shop, Workstream: 12})
	if err != nil {
		t.Fatal(err)
	}
	return len(ready) == 0
}

const holdEvent = "call = { tool = \"hold_event\", arguments = {} }"

func TestAHeldEventReturnsAfterEachTurnForAnOwnerMessageWithTheLaterEventsOfItsTask(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, fmt.Sprintf(`
[[prompts]]
%s

[[prompts]]
reply = ["Noted."]

[[prompts]]
reply = ["Owner one."]

[[prompts]]
%s

[[prompts]]
reply = ["Owner two."]

[[prompts]]
reply = ["Noted."]

[[prompts]]
reply = ["Noted."]
`, holdEvent, holdEvent), keepSessionOpen)

	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return held(t, server, 1) })
	dispatchTask(fake, 42, "Add plan route")
	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 2 && held(t, server, 1) })
	fake.AddComment(shop, 41, "owner", "Round down.")
	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 3 })
	waitForPolls(t, fake)
	if got := onlySession(t, server); len(got) != 2 || !held(t, server, 2) {
		t.Fatalf("prompts = %q", got)
	}

	sendChat(t, server, leadChat, "First decision")
	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := onlySession(t, server)
		return prompts, len(prompts) == 4 && held(t, server, 2)
	})
	if !strings.Contains(prompts[1], " dispatch of #42 ") || prompts[2] != "First decision" || !strings.HasPrefix(prompts[3], "# Event\n\n") || !strings.Contains(prompts[3], " dispatch of #41 ") {
		t.Errorf("prompts = %q", prompts)
	}

	sendChat(t, server, leadChat, "Second decision")
	prompts = testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := onlySession(t, server)
		return prompts, len(prompts) == 7 && eventDelivered(t, server)
	})
	if prompts[4] != "Second decision" || !strings.Contains(prompts[5], " dispatch of #41 ") || !strings.Contains(prompts[6], " comment on #41 ") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAFreedEventDoesNotMoveALaterEventOfAnotherTaskBeforeAQueuedOwnerMessage(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, fmt.Sprintf(`
[[prompts]]
%s

[[prompts]]
reply = ["Noted."]

[[prompts]]
when = "First decision"
hang = true

[[prompts]]
reply = ["Noted."]

[[prompts]]
reply = ["Noted."]

[[prompts]]
reply = ["Noted."]

[[prompts]]
reply = ["Noted."]
`, holdEvent), keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return held(t, server, 1) })
	sendChat(t, server, leadChat, "First decision")
	testkit.WaitFor(t, func() bool { return len(onlySession(t, server)) == 2 })
	sendChat(t, server, leadChat, "Second decision")
	dispatchTask(fake, 42, "Add plan route")
	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 2 })

	stopChat(t, server, leadChat)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := onlySession(t, server)
		return prompts, len(prompts) == 5 && eventDelivered(t, server)
	})
	if prompts[1] != "First decision" || !strings.Contains(prompts[2], " dispatch of #41 ") || prompts[3] != "Second decision" || !strings.Contains(prompts[4], " dispatch of #42 ") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAHeldEventStaysHeldAfterARestartUntilATurnForAnOwnerMessageEnds(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	seed(t, dataDir,
		`INSERT INTO lead_events (repository, workstream, issue, kind, payload, time, held) VALUES ('owner/shop', 12, 41, 'comment', 'A held comment.', '2026-10-04T10:00:00Z', 1)`)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nreply = [\"Owner one.\"]\n\n[[prompts]]\nreply = [\"Noted.\"]\n")
	cfg := testserver.Config(t, dataDir)
	keepSessionOpen(cfg)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)

	waitForPolls(t, fake)
	if got := chatSessions(t, server, leadChat, engine.LeadRole); len(got) != 0 || !held(t, server, 1) {
		t.Fatalf("sessions = %+v", got)
	}
	sendChat(t, server, leadChat, "Decision")

	testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == 0 })
	prompts := onlySession(t, server)
	if len(prompts) != 2 || !strings.HasSuffix(prompts[0], "# Owner message\n\nDecision") || prompts[1] != "# Event\n\nA held comment." {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestHoldEventInATurnForAnOwnerMessageGivesAnError(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\n"+holdEvent+"\n")

	sendChat(t, server, leadChat, "Hold")

	waitForChat(t, server, leadChat, "Lead", "error: hold_event works only in a turn for an event.")
}

func TestASecondLeadChatWaitsForTheLeadLimitAndShowsTheMessageOfTheOwner(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 50, "Loyalty points")
	fake.AddLabel(shop, 50, "mobius:workstream", "owner")
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Plan the loyalty API\"\nhang = true\n[[prompts]]\nreply = [\"Done.\"]\n", func(cfg *config.Config) {
		cfg.Roles.Lead.Max = 1
	})
	points := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 50}
	sendChat(t, server, leadChat, "Plan the loyalty API")
	testkit.WaitFor(t, func() bool { return len(leadPrompts(t, server)) == 1 })

	sendChat(t, server, points, "Plan the loyalty points")

	queued := waitForChatSession(t, server, points, engine.LeadRole, func(session store.Session) bool { return session.QueueReason.Valid })
	if queued.QueueReason.String != "no free lead slot (1/1)" {
		t.Errorf("queue reason = %s", queued.QueueReason.String)
	}
	if got := chatLines(t, server, points); !slices.Contains(got, chatLine{"Owner", "Plan the loyalty points"}) {
		t.Errorf("chat = %+v", got)
	}

	stopChat(t, server, leadChat)

	first := endedChatSession(t, server, 0)
	if first.EndReason.String != "idle" {
		t.Errorf("end reason = %s", first.EndReason.String)
	}
	testkit.WaitFor(t, func() bool { return len(promptTexts(t, server, queued.ID)) > 0 })
}

func TestAStopEndsAWaitingLeadChat(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 50, "Loyalty points")
	fake.AddLabel(shop, 50, "mobius:workstream", "owner")
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Plan the loyalty API\"\nhang = true\n[[prompts]]\nreply = [\"Done.\"]\n", func(cfg *config.Config) {
		cfg.Roles.Lead.Max = 1
	})
	points := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 50}
	sendChat(t, server, leadChat, "Plan the loyalty API")
	testkit.WaitFor(t, func() bool { return len(leadPrompts(t, server)) == 1 })
	sendChat(t, server, points, "Plan the loyalty points")
	queued := waitForChatSession(t, server, points, engine.LeadRole, func(session store.Session) bool { return session.QueueReason.Valid })

	stopChat(t, server, points)

	stopped := waitForChatSession(t, server, points, engine.LeadRole, func(session store.Session) bool { return session.EndedAt.Valid })
	if stopped.ID != queued.ID || stopped.EndReason.String != "stopped" {
		t.Errorf("session = %+v", stopped)
	}
	testkit.WaitFor(t, func() bool { return !chatView(t, server, points).Writing })
	stopChat(t, server, leadChat)
	endedChatSession(t, server, 0)
}

func TestAStopKeepsTheLaterMessageOfAWaitingLeadChat(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 50, "Loyalty points")
	fake.AddLabel(shop, 50, "mobius:workstream", "owner")
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Plan the loyalty API\"\nhang = true\n[[prompts]]\nreply = [\"Done.\"]\n", func(cfg *config.Config) {
		cfg.Roles.Lead.Max = 1
	})
	points := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 50}
	sendChat(t, server, leadChat, "Plan the loyalty API")
	testkit.WaitFor(t, func() bool { return len(leadPrompts(t, server)) == 1 })
	sendChat(t, server, points, "Message A")
	queued := waitForChatSession(t, server, points, engine.LeadRole, func(session store.Session) bool { return session.QueueReason.Valid })
	sendChat(t, server, points, "Message B")

	stopChat(t, server, points)
	stopChat(t, server, leadChat)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := promptTexts(t, server, queued.ID)
		return prompts, len(prompts) > 0
	})
	// The chat history in the prompt still shows message A, but the turn answers message B.
	if !strings.HasSuffix(prompts[0], "Message B") {
		t.Errorf("prompts = %q", prompts)
	}
	for _, prompt := range prompts {
		if strings.HasSuffix(prompt, "Message A") {
			t.Errorf("prompts = %q", prompts)
		}
	}
}

func TestAStopKeepsTheEventOfAWaitingLeadChat(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 50, "Loyalty points")
	fake.AddLabel(shop, 50, "mobius:workstream", "owner")
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Plan the loyalty API\"\nhang = true\n[[prompts]]\nreply = [\"Done.\"]\n", func(cfg *config.Config) {
		cfg.Roles.Lead.Max = 1
	})
	points := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 50}
	sendChat(t, server, leadChat, "Plan the loyalty API")
	testkit.WaitFor(t, func() bool { return len(leadPrompts(t, server)) == 1 })
	fake.AddIssue(shop, 51, "Add points model")
	fake.AddSubIssue(shop, 50, 51)
	fake.AddLabel(shop, 51, "mobius:ready", "owner")
	queued := waitForChatSession(t, server, points, engine.LeadRole, func(session store.Session) bool { return session.QueueReason.Valid })

	stopChat(t, server, points)
	stopChat(t, server, leadChat)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := promptTexts(t, server, queued.ID)
		return prompts, len(prompts) > 0
	})
	if !strings.Contains(prompts[0], "# Event\n\n") || !strings.Contains(prompts[0], " dispatch of #51 ") {
		t.Errorf("prompts = %q", prompts)
	}
}

// taskNumbers gives the numbers of the tasks of the Workstream number in the Tasks tab.
func taskNumbers(t *testing.T, server *testserver.Server, workstream int64) []int64 {
	t.Helper()
	lines, err := server.Engine.Tasks(t.Context(), shop, workstream)
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int64
	for _, line := range lines {
		numbers = append(numbers, line.Number)
	}
	return numbers
}

type apiChatMessage struct {
	ID     int64  `json:"id"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

type apiUnread struct {
	Organization string `json:"organization"`
	Repository   string `json:"repository"`
	Workstream   int64  `json:"workstream"`
	Count        int64  `json:"count"`
}

// nextEventData waits for the next data of events that matches. The test fails after one minute.
func nextEventData[T any](t *testing.T, events <-chan string, matches func(T) bool) T {
	t.Helper()
	deadline := time.After(time.Minute)
	for {
		select {
		case data := <-events:
			var value T
			if err := json.Unmarshal([]byte(data), &value); err != nil {
				t.Fatal(err)
			}
			if matches(value) {
				return value
			}
		case <-deadline:
			t.Fatal("no such event after one minute")
		}
	}
}

func TestTheChatAPISendsSeesAndStopsWithLiveEvents(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Hello\"]\n")
	messages := liveEvents(t, server, "message")
	chats := liveEvents(t, server, "chat")
	unreads := liveEvents(t, server, "unread")
	key := `"organization":"owner","repository":"owner/shop","workstream":12`

	if status, body := send(t, server, http.MethodPost, "/api/chat/messages", `{`+key+`,"text":"Plan the loyalty API"}`); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	nextEventData(t, chats, func(state chatState) bool { return state.Workstream == 12 && state.Writing })
	lead := nextEventData(t, messages, func(message apiChatMessage) bool { return message.Author == "Lead" })
	nextEventData(t, unreads, func(unread apiUnread) bool { return unread.Count == 1 })
	nextEventData(t, chats, func(state chatState) bool { return state.Workstream == 12 && !state.Writing })
	chat := apiData[struct {
		Messages []apiChatMessage `json:"messages"`
		Harness  string           `json:"harness"`
	}](t, server, "/api/chat?organization=owner&repository=owner/shop&workstream=12")
	if len(chat.Messages) != 2 || chat.Messages[0].Author != "Owner" || chat.Messages[1] != lead || chat.Harness != "claude-code" {
		t.Errorf("chat = %+v", chat)
	}
	if got := apiData[[]apiUnread](t, server, "/api/unread"); !slices.Equal(got, []apiUnread{{"owner", shop, 12, 1}}) {
		t.Errorf("unread = %+v", got)
	}

	if status, body := send(t, server, http.MethodPost, "/api/chat/seen", `{`+key+`,"message":`+itoa(lead.ID)+`}`); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if got := apiData[[]apiUnread](t, server, "/api/unread"); len(got) != 0 {
		t.Errorf("unread = %+v", got)
	}
	nextEventData(t, unreads, func(unread apiUnread) bool { return unread.Count == 0 })
	if status, body := send(t, server, http.MethodPost, "/api/chat/stop", `{`+key+`}`); status != http.StatusNoContent {
		t.Errorf("status = %d: %s", status, body)
	}
	if status, body := send(t, server, http.MethodPost, "/api/chat/messages", `{"organization":"nobody","text":"Hello"}`); status != http.StatusConflict {
		t.Errorf("status = %d: %s", status, body)
	}
}

// chatState is the data of a chat event.
type chatState struct {
	Workstream int64 `json:"workstream"`
	Writing    bool  `json:"writing"`
}

func TestTheLeadChatRefusesToMoveATaskWhenTheTaskOrTheTargetDoesNotFit(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	// The first prompt of a new session has the earlier messages in its history, so the prompt of the newest message comes first.
	server, _ := connect(t, fake, `
[[prompts]]
when = "case-live"
call = { tool = "move_task", arguments = { n = 13, workstream = 20 } }

[[prompts]]
when = "case-plain"
call = { tool = "move_task", arguments = { n = 13, workstream = 14 } }

[[prompts]]
when = "case-closed"
call = { tool = "move_task", arguments = { n = 13, workstream = 22 } }

[[prompts]]
when = "case-self"
call = { tool = "move_task", arguments = { n = 13, workstream = 12 } }

[[prompts]]
when = "case-pull"
call = { tool = "move_task", arguments = { n = 15, workstream = 20 } }

[[prompts]]
when = "case-other"
call = { tool = "move_task", arguments = { n = 21, workstream = 20 } }
`)
	fake.AddIssue(shop, 13, "Add the API route")
	fake.AddIssue(shop, 14, "Add plan model")
	fake.AddPullRequest(shop, 15, "Add the API route")
	fake.AddSubIssue(shop, 12, 13)
	fake.AddSubIssue(shop, 12, 14)
	fake.AddSubIssue(shop, 12, 15)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 21, "Invoice totals")
	fake.AddSubIssue(shop, 20, 21)
	fake.AddIssue(shop, 22, "Old Billing")
	fake.AddLabel(shop, 22, "mobius:workstream", "owner")
	fake.CloseIssue(shop, 22)

	for _, c := range []struct{ message, result string }{
		{"case-other", "error: #21 is not in this Workstream."},
		{"case-pull", "error: #15 is not an issue of owner/shop."},
		{"case-self", "error: #12 is this Workstream."},
		{"case-closed", "error: #22 is not an open Workstream."},
		{"case-plain", "error: #14 is not an open Workstream."},
	} {
		sendChat(t, server, leadChat, c.message)
		waitForChat(t, server, leadChat, "Lead", c.result)
	}
	if _, err := server.DB.Exec("INSERT INTO tasks (repository, issue, workstream, state, dispatched_at) VALUES (?, 13, 12, 'dispatched', ?)", shop, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	sendChat(t, server, leadChat, "case-live")

	waitForChat(t, server, leadChat, "Lead", "error: #13 has a live task. Stop the task first.")
	if got := fake.SubIssueNumbers(shop, 12); !reflect.DeepEqual(got, []int64{13, 14, 15}) {
		t.Errorf("sub-issues of #12 = %v", got)
	}
	if got := fake.SubIssueNumbers(shop, 20); !reflect.DeepEqual(got, []int64{21}) {
		t.Errorf("sub-issues of #20 = %v", got)
	}
}
