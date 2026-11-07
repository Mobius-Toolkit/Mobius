package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// movedReply is the script of a fake Harness that answers each prompt.
const movedReply = "[[prompts]]\nreply = [\"Moved.\"]\n"

// installDevin makes the fake Devin play script.
func installDevin(t *testing.T, dataDir, script string) {
	t.Helper()
	testkit.InstallFakeHarness(t, dataDir, "devin", options+script)
}

// setHarness sets the Harness and the model of the Role role. The other fields of the binding stay.
func setHarness(t *testing.T, server *testserver.Server, role string, harness config.Harness, model string) {
	t.Helper()
	settings, err := server.Engine.Roles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var binding config.RoleBinding
	for _, setting := range settings {
		if setting.Role == role {
			binding = setting.Binding
		}
	}
	binding.Harness, binding.Model = harness, model
	if err := server.Engine.SetRole(t.Context(), role, binding); err != nil {
		t.Fatal(err)
	}
}

// waitForPausedSession waits until the chat of key has n sessions of role and the last one waits for a pause. It gives
// the sessions.
func waitForPausedSession(t *testing.T, server *testserver.Server, key engine.ChatKey, role string, n int) []store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := chatSessions(t, server, key, role)
		return sessions, len(sessions) == n && strings.HasPrefix(sessions[n-1].QueueReason.String, "paused until ")
	})
}

// ownerDelivered tells if the first message of the Owner in the chat of key has a delivery time.
func ownerDelivered(t *testing.T, server *testserver.Server, key engine.ChatKey) bool {
	t.Helper()
	for _, message := range chatView(t, server, key).Messages {
		if message.Author == "Owner" {
			return message.DeliveredAt.Valid
		}
	}
	return false
}

func TestALeadThatWaitsForAPauseMovesToTheNewHarnessOfItsRole(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	installDevin(t, dataDir, movedReply)
	sendChat(t, server, leadChat, "Plan the API")
	waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)
	if ownerDelivered(t, server, leadChat) {
		t.Error("the Owner message has a delivery time while the Lead waits for the pause")
	}

	setHarness(t, server, "lead", config.Devin, "swe-1.5")

	testkit.WaitFor(t, func() bool { return ownerDelivered(t, server, leadChat) })
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 2 || sessions[0].EndReason.String != "moved" || sessions[1].Harness != "devin" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if prompts := promptTexts(t, server, sessions[1].ID); len(prompts) != 1 || !strings.Contains(prompts[0], "Plan the API") {
		t.Errorf("prompts of the new session = %q", prompts)
	}
	waitForChat(t, server, leadChat, "Lead", "Moved.")
}

func TestALeadThatWaitsForAPauseGivesItsEventToTheNewSessionOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	installDevin(t, dataDir, movedReply)
	dispatchTask(fake, 41, "Add plan model")
	waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)
	if eventDelivered(t, server) {
		t.Error("the event has a delivery time while the Lead waits for the pause")
	}

	setHarness(t, server, "lead", config.Devin, "swe-1.5")

	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 2 || sessions[0].EndReason.String != "moved" || sessions[1].Harness != "devin" {
		t.Fatalf("sessions = %+v", sessions)
	}
	prompts := promptTexts(t, server, sessions[1].ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], " dispatch of #41 ") {
		t.Errorf("prompts of the new session = %q", prompts)
	}
}

func TestACoordinatorThatWaitsForAPauseMovesToTheNewHarnessOfItsRole(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	installDevin(t, dataDir, movedReply)
	sendChat(t, server, coordinatorChat, "Which Workstreams are open?")
	waitForPausedSession(t, server, coordinatorChat, engine.CoordinatorRole, 1)

	setHarness(t, server, "coordinator", config.Devin, "swe-1.5")

	testkit.WaitFor(t, func() bool { return ownerDelivered(t, server, coordinatorChat) })
	sessions := chatSessions(t, server, coordinatorChat, engine.CoordinatorRole)
	if len(sessions) != 2 || sessions[0].EndReason.String != "moved" || sessions[1].Harness != "devin" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if prompts := promptTexts(t, server, sessions[1].ID); len(prompts) != 1 || !strings.Contains(prompts[0], "Which Workstreams are open?") {
		t.Errorf("prompts of the new session = %q", prompts)
	}
	waitForChat(t, server, coordinatorChat, "Coordinator", "Moved.")
}

func TestAChangeOfOnlyTheModelOrTheEffortOfTheLeadKeepsTheWaitingSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the API")
	waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)

	setHarness(t, server, "lead", config.ClaudeCode, "haiku")
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	waiting := waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)[0]
	if waiting.EndedAt.Valid {
		t.Errorf("session = %+v", waiting)
	}
}

func TestAChangeOfAnotherRoleKeepsTheWaitingLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the API")
	waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)

	setHarness(t, server, "judge", config.Devin, "swe-1.5")
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	waiting := waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)[0]
	if waiting.EndedAt.Valid {
		t.Errorf("session = %+v", waiting)
	}
}

func TestAResetOfTheLeadThatChangesItsHarnessMovesTheWaitingLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, movedReply, keepSessionOpen)
	installDevin(t, dataDir, usageLimit)
	setHarness(t, server, "lead", config.Devin, "swe-1.5")
	sendChat(t, server, leadChat, "Plan the API")
	waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)

	if err := server.Engine.ResetRole(t.Context(), "lead"); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return ownerDelivered(t, server, leadChat) })
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 2 || sessions[0].EndReason.String != "moved" || sessions[1].Harness != "claude-code" {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestEachMoveOfAChatAddsNothingToItsCrashCount(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	installDevin(t, dataDir, usageLimit)
	sendChat(t, server, leadChat, "Plan the API")
	waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)

	// One more move than the chat has crashes.
	for n := 2; n <= 5; n++ {
		if n%2 == 0 {
			setHarness(t, server, "lead", config.Devin, "swe-1.5")
		} else {
			setHarness(t, server, "lead", config.ClaudeCode, "opus")
		}
		waitForPausedSession(t, server, leadChat, engine.LeadRole, n)
	}

	if err := server.Engine.Resume(t.Context(), "claude-code"); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return ownerDelivered(t, server, leadChat) })
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	for _, session := range sessions[:4] {
		if session.EndReason.String != "moved" {
			t.Errorf("session = %+v", session)
		}
	}
}

func TestAMoveOfTheSaveTurnLosesTheSaveAndClosesTheChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	script := `
[[prompts]]
when = "Save in the Workstream memory"
error = { code = -32603, message = "Rate limited", data = { errorKind = "rate_limit" } }

[[prompts]]
reply = ["Noted."]
`
	server, dataDir := connectWith(t, fake, script, func(*config.Config) {})
	installDevin(t, dataDir, movedReply)
	sendChat(t, server, leadChat, "Plan the API")
	waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)

	setHarness(t, server, "lead", config.Devin, "swe-1.5")

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := chatSessions(t, server, leadChat, engine.LeadRole)
		return sessions, len(sessions) == 1 && sessions[0].EndedAt.Valid
	})
	if sessions[0].EndReason.String != "moved" {
		t.Errorf("session = %+v", sessions[0])
	}
	waitForPolls(t, fake)
	if sessions := chatSessions(t, server, leadChat, engine.LeadRole); len(sessions) != 1 {
		t.Errorf("sessions = %+v", sessions)
	}
}

func TestAWaitingImplementerStaysInItsWaitAfterAChangeOfItsHarness(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageLimit)
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Store plans in cents.", nil) }()
	testkit.WaitFor(t, func() bool {
		return strings.HasPrefix(session(t, server, agent.ID()).QueueReason.String, "paused until ")
	})

	moveImplementerToClaudeCode(t, server)
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	select {
	case err := <-prompted:
		t.Errorf("Prompt returned %v, but the Implementer must stay in its wait", err)
	default:
	}
}
