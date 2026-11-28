package engine_test

import (
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const subscriptionReplyText = "Claude Code can't use your Claude subscription, usually because a payment is past due · " +
	"Pay or renew at https://claude.ai/settings/billing (if you just paid, try again shortly), or use an Anthropic API key instead"

const subscriptionUpdate = `updates = ['''{"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "` + subscriptionReplyText + `"}}''']`

// The first prompt of Claude Code replies with the subscription error and ends with the error "Authentication
// required", as the adapter does. The same prompt again and the next prompt do the work.
const subscriptionError = `
[[prompts]]
` + subscriptionUpdate + `
error = { code = -32000, message = "Authentication required" }

[[prompts]]
reply = ["Seen."]

[[prompts]]
reply = ["Noted."]
`

// subscriptionMessage gives the chat message of the pause. account is the account in the message, or "". With resend,
// the message tells that Mobius sends the prompt again, and else that the next prompt waits.
func subscriptionMessage(until time.Time, account string, resend bool) string {
	if account != "" {
		account = " The account is: " + account + "."
	}
	next := "The next prompt waits until "
	if resend {
		next = "Mobius sends the prompt again at "
	}
	return "Claude Code cannot use the Claude subscription." + account + " The Owner must pay or renew at https://claude.ai/settings/billing. " +
		next + until.UTC().Format("2006-01-02 15:04 UTC") + "."
}

const (
	ownerEmail = "owner@example.com"

	// accountNotice is the notification of the adapter for the Claude subscription.
	accountNotice = `{"method": "auth/status_update", "params": {"state": "authenticated", "accounts": [{"label": "Claude Max", "active": true, "email": "` + ownerEmail + `", "_meta": {"claudeCode": {"kind": "account", "plan": "max"}}}]}}`
)

// accountProblem is the first prompt of Claude Code: it gets the notification notice and then the error "Authentication
// required". After the file gate exists, the agent sends the notification of the Claude subscription. The same prompt
// again and the next prompt do the work.
func accountProblem(notice, gate string) string {
	return "[[prompts]]\nnotices = ['''" + notice + "''']\nerror = { code = -32000, message = \"Authentication required\" }\n" +
		"later = { wait_for = '" + gate + "', notices = ['''" + accountNotice + "'''] }\n\n" +
		"[[prompts]]\nreply = [\"Noted.\"]\n"
}

// requirePauseReason fails when the pause of claude-code in the API has not the reason want.
func requirePauseReason(t *testing.T, server *testserver.Server, want string) {
	t.Helper()
	for _, pause := range apiPauses(t, server) {
		if pause.Harness == "claude-code" {
			if pause.Reason != want {
				t.Errorf("reason = %q, want %q", pause.Reason, want)
			}
			return
		}
	}
	t.Error("the API has no pause of claude-code")
}

func TestALoggedOutNotificationInATurnThatRepliesNormallyKeepsTheReplyAndSendsNoPromptAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nnotices = ['''{\"method\": \"auth/status_update\", \"params\": {\"state\": \"unauthenticated\"}}''']\nreply = [\"Done.\"]\n", keepSessionOpen)

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return reflect.DeepEqual(chatTexts(t, server, "Lead"), []string{"Done."}) })
	testkit.WaitFor(t, func() bool { return ownerMessageDelivered(t, server) })
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if got := promptTexts(t, server, sessions[0].ID); len(got) != 1 {
		t.Errorf("prompts = %q", got)
	}
	if !claudeCodePaused(t, server) {
		t.Error("the notification did not pause Claude Code")
	}
	var untilText string
	if err := server.DB.QueryRow("SELECT paused_until FROM harness_pauses WHERE harness = 'claude-code'").Scan(&untilText); err != nil {
		t.Fatal(err)
	}
	want := []string{subscriptionMessage(parseTime(t, untilText), "Not logged in", false)}
	if got := chatTexts(t, server, "Mobius"); !reflect.DeepEqual(got, want) {
		t.Errorf("Mobius chat messages = %q, want %q", got, want)
	}
}

func TestAnAuthStatusUpdateOfAnotherHarnessPausesNoHarness(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nnotices = ['''{\"method\": \"auth/status_update\", \"params\": {\"state\": \"unauthenticated\"}}''']\nreply = [\"Done.\"]\n", func(cfg *config.Config) {
		cfg.Roles.Lead = config.RoleBinding{Harness: config.Antigravity, Model: "gemini-3-pro", Max: 2}
	})

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return reflect.DeepEqual(chatTexts(t, server, "Lead"), []string{"Done."}) })
	var pauses int
	if err := server.DB.QueryRow("SELECT COUNT(*) FROM harness_pauses").Scan(&pauses); err != nil {
		t.Fatal(err)
	}
	if got := chatTexts(t, server, "Mobius"); pauses != 0 || len(got) != 0 {
		t.Errorf("pauses = %d, Mobius chat messages = %q", pauses, got)
	}
}

func TestAUsageLimitPauseHasNoReason(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimitTextOnly, keepSessionOpen)

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
	requirePauseReason(t, server, "")
}

func TestANewClaudeCodeSessionThatReportsTheClaudeSubscriptionDuringThePauseOfTheReplyTextKeepsThePause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	// Only the prompt of the Lead gets the subscription error. A prompt of the Coordinator that Mobius sends gets a reply.
	server, _ := connectScript(t, fake, "session_notices = ['''"+accountNotice+"''']\n"+options+"[[prompts]]\nwhen = \"Plan the API\"\n"+subscriptionUpdate+
		"\nerror = { code = -32000, message = \"Authentication required\" }\n\n[[prompts]]\nreply = [\"Noted.\"]\n", keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the API")
	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })

	sendChat(t, server, coordinatorChat, "Hello")

	waitForChatSession(t, server, coordinatorChat, engine.CoordinatorRole, func(session store.Session) bool {
		return strings.HasPrefix(session.QueueReason.String, "paused until")
	})
	if !claudeCodePaused(t, server) {
		t.Error("the notification of the Claude subscription ended the pause of the reply text")
	}
	if got := promptTexts(t, server, chatSessions(t, server, coordinatorChat, engine.CoordinatorRole)[0].ID); len(got) != 0 {
		t.Errorf("prompts of the Coordinator during the pause = %q", got)
	}
}

// requireAccountPause sends a chat message to the Lead, and checks that the notification notice pauses Claude Code with
// the message that names account, with no email and no ERROR line in logged. Then it checks that the notification of
// the Claude subscription ends the pause, and that the prompt goes again.
func requireAccountPause(t *testing.T, notice, account string) {
	t.Helper()
	logged := &logBuffer{}
	log.SetOutput(logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	gate := filepath.Join(t.TempDir(), "gate")
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, accountProblem(notice, gate), keepSessionOpen)
	before := time.Now().UTC()

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
	requirePauseReason(t, server, "subscription")
	var untilText string
	if err := server.DB.QueryRow("SELECT paused_until FROM harness_pauses WHERE harness = 'claude-code'").Scan(&untilText); err != nil {
		t.Fatal(err)
	}
	until := parseTime(t, untilText)
	if until.Before(before.Add(30*time.Minute)) || until.After(time.Now().UTC().Add(30*time.Minute)) {
		t.Errorf("until = %v, before = %v", until, before)
	}
	// The notification pauses first, and then the error of the turn makes Prompt send the prompt again.
	testkit.WaitFor(t, func() bool { return len(chatTexts(t, server, "Mobius")) == 2 })
	want := subscriptionMessage(until, account, true)
	if got, wantTexts := chatTexts(t, server, "Mobius"), []string{subscriptionMessage(until, account, false), want}; !reflect.DeepEqual(got, wantTexts) {
		t.Errorf("Mobius chat messages = %q, want %q", got, wantTexts)
	}
	if got := chatTexts(t, server, "Lead"); len(got) != 0 {
		t.Errorf("Lead chat messages = %q", got)
	}
	if ownerMessageDelivered(t, server) {
		t.Error("the Owner message has a delivery time while the turn waits for the end of the pause")
	}
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 1 || sessions[0].EndReason.Valid {
		t.Errorf("sessions = %+v", sessions)
	}

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return reflect.DeepEqual(chatTexts(t, server, "Lead"), []string{"Noted."}) })
	if claudeCodePaused(t, server) {
		t.Error("the pause did not end")
	}
	if got := promptTexts(t, server, sessions[0].ID); len(got) != 2 || got[0] != got[1] {
		t.Errorf("prompts = %q", got)
	}
	if strings.Contains(logged.String(), ownerEmail) || strings.Contains(want, ownerEmail) {
		t.Errorf("an email is in the log or in the message: %s", logged.String())
	}
	if strings.Contains(logged.String(), "ERROR") {
		t.Errorf("log = %s", logged.String())
	}
}

// Serial: the test sets the output of the standard logger.
func TestAnAuthStatusUpdateWithStateUnauthenticatedPausesClaudeCodeUntilTheClaudeSubscriptionNotification(t *testing.T) {
	requireAccountPause(t, `{"method": "auth/status_update", "params": {"state": "unauthenticated"}}`, "Not logged in")
}

// Serial: the test sets the output of the standard logger.
func TestAnExtensionAuthStatusUpdateWithKindNonePausesClaudeCodeUntilTheClaudeSubscriptionNotification(t *testing.T) {
	requireAccountPause(t, `{"method": "_auth/status_update", "params": {"authStatus": {"kind": "none", "label": "Not logged in", "detail": "Run claude login", "account": {"email": "`+ownerEmail+`"}}}}`, "Not logged in (Run claude login)")
}

func TestAClaudeCodeTurnWithTheSubscriptionErrorPausesClaudeCodeAndSendsThePromptAgainAfterThePause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, subscriptionError, keepSessionOpen)
	before := time.Now().UTC()

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
	requirePauseReason(t, server, "subscription")
	var untilText string
	if err := server.DB.QueryRow("SELECT paused_until FROM harness_pauses WHERE harness = 'claude-code'").Scan(&untilText); err != nil {
		t.Fatal(err)
	}
	until := parseTime(t, untilText)
	if until.Before(before.Add(30*time.Minute)) || until.After(time.Now().UTC().Add(30*time.Minute)) {
		t.Errorf("until = %v, before = %v", until, before)
	}
	testkit.WaitFor(t, func() bool { return len(chatTexts(t, server, "Mobius")) == 1 })
	if got := chatTexts(t, server, "Mobius"); got[0] != subscriptionMessage(until, "", true) {
		t.Errorf("Mobius chat messages = %q, want %q", got, subscriptionMessage(until, "", true))
	}
	if got := chatTexts(t, server, "Lead"); len(got) != 0 {
		t.Errorf("Lead chat messages = %q", got)
	}
	if ownerMessageDelivered(t, server) {
		t.Error("the Owner message has a delivery time while the turn waits for the end of the pause")
	}
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 1 || sessions[0].EndReason.Valid {
		t.Errorf("sessions = %+v", sessions)
	}
	if got := reply(t, server, sessions[0].ID); !strings.Contains(got, subscriptionReplyText) {
		t.Errorf("the Transcript reply = %q", got)
	}

	if _, err := server.Engine.Resume(t.Context(), "claude-code"); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return reflect.DeepEqual(chatTexts(t, server, "Lead"), []string{"Seen."}) })
	testkit.WaitFor(t, func() bool { return ownerMessageDelivered(t, server) })
	prompts := promptTexts(t, server, sessions[0].ID)
	if len(prompts) != 2 || prompts[0] != prompts[1] {
		t.Errorf("prompts = %q", prompts)
	}
	if got := chatTexts(t, server, "Mobius"); len(got) != 1 {
		t.Errorf("Mobius chat messages = %q", got)
	}
}

func TestALeadTurnWithTheSubscriptionErrorKeepsTheEventAndTheOwnerMessageUndeliveredUntilThePromptAfterThePause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, subscriptionError, keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
	sendChat(t, server, leadChat, "Plan the API")
	waitForPolls(t, fake)

	if got := undelivered(t, server); len(got) == 0 {
		t.Errorf("undelivered events = %+v", got)
	}
	if ownerMessageDelivered(t, server) {
		t.Error("the Owner message has a delivery time while the turn waits for the end of the pause")
	}
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 1 || sessions[0].EndReason.Valid {
		t.Fatalf("sessions = %+v", sessions)
	}

	if _, err := server.Engine.Resume(t.Context(), "claude-code"); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) && ownerMessageDelivered(t, server) })
	prompts := promptTexts(t, server, sessions[0].ID)
	if len(prompts) != 3 || prompts[0] != prompts[1] || !strings.Contains(prompts[1], " dispatch of #41 ") || !strings.HasSuffix(prompts[2], "Plan the API") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestALeadSessionThatFailsAfterTheSubscriptionErrorLeavesTheEventUndelivered(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"dispatch of #41\"\n"+subscriptionUpdate+"\nshell = \"sleep 1; kill -9 $PPID; sleep 5\"\n", keepSessionOpen)

	dispatchTask(fake, 41, "Add plan model")

	testkit.WaitFor(t, func() bool {
		failed := 0
		for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
			if session.EndReason.String == "failed" {
				failed++
			}
		}
		return failed >= 4
	})
	waitForPolls(t, fake)

	if got := undelivered(t, server); len(got) == 0 {
		t.Errorf("undelivered events = %+v", got)
	}
	for _, item := range inbox(t, server) {
		if item.Kind == "Lead failed" {
			t.Errorf("Inbox item = %+v", item)
		}
	}
}
