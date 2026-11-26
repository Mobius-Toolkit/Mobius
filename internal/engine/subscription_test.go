package engine_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
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

func subscriptionMessage(until time.Time) string {
	return "Claude Code cannot use the Claude subscription. The Owner must pay or renew at https://claude.ai/settings/billing. " +
		"Mobius sends the prompt again at " + until.UTC().Format("2006-01-02 15:04 UTC") + "."
}

func TestAClaudeCodeTurnWithTheSubscriptionErrorPausesClaudeCodeAndSendsThePromptAgainAfterThePause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, subscriptionError, keepSessionOpen)
	before := time.Now().UTC()

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
	var untilText string
	if err := server.DB.QueryRow("SELECT paused_until FROM harness_pauses WHERE harness = 'claude-code'").Scan(&untilText); err != nil {
		t.Fatal(err)
	}
	until := parseTime(t, untilText)
	if until.Before(before.Add(30*time.Minute)) || until.After(time.Now().UTC().Add(30*time.Minute)) {
		t.Errorf("until = %v, before = %v", until, before)
	}
	testkit.WaitFor(t, func() bool { return len(chatTexts(t, server, "Mobius")) == 1 })
	if got := chatTexts(t, server, "Mobius"); got[0] != subscriptionMessage(until) {
		t.Errorf("Mobius chat messages = %q, want %q", got, subscriptionMessage(until))
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

	if err := server.Engine.Resume(t.Context(), "claude-code"); err != nil {
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

	if err := server.Engine.Resume(t.Context(), "claude-code"); err != nil {
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
