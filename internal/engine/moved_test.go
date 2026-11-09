package engine_test

import (
	"slices"
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

func TestAChangeOfOnlyTheModelOfTheImplementerKeepsTheWaitingImplementer(t *testing.T) {
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

	setHarness(t, server, engine.ImplementerRole, config.Devin, "gemini-3-pro")
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	select {
	case err := <-prompted:
		t.Errorf("Prompt returned %v, but the Implementer must stay in its wait", err)
	default:
	}
}

const (
	devinLimit = "error = { code = -32011, message = \"Rate limited\", data = { retryAfterSeconds = 3600 } }\n\n"
	// implementerLimit is an Implementer on Devin whose prompt hits the usage limit.
	implementerLimit = "[[prompts]]\nwhen = \"You are the Implementer of one task.\"\n" + devinLimit
	// implementerCommits is an Implementer that commits plan.txt with cents in its first round.
	implementerCommits = "[[prompts]]\nwhen = \"You are the Implementer of one task.\"\n" + commitCents + "\n"
	// reviewerLimit is a Reviewer on Devin whose prompt hits the usage limit.
	reviewerLimit = "[[prompts]]\nwhen = \"You are the Reviewer\"\n" + devinLimit
	// leadFix is an Implementer that commits a change in the fix round of the Lead.
	leadFix = "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo $$ >> plan.txt && git commit -q -am 'Remove the lines'\"\n\n"
)

// taskCounters gives the restarts, the fix rounds and the review rounds of the live task of #41.
func taskCounters(t *testing.T, server *testserver.Server) [3]int64 {
	t.Helper()
	task := liveTask(t, server, 41)
	return [3]int64{task.WorkerRestarts, task.FixRounds, task.ReviewRounds}
}

func TestAnImplementerOfAFirstRoundThatWaitsForAPauseMovesToTheNewHarnessAndPushes(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, implementerCommits+noFinding+leadStarts, implementerLimit, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForPausedSession(t, server, leadChat, engine.ImplementerRole, 1)

	moveImplementerToClaudeCode(t, server)

	approvalCheckRuns(t, server, fake)
	sessions := endedImplementers(t, server, 2)
	if sessions[0].EndReason.String != "moved" || sessions[0].Harness != "devin" || sessions[1].EndReason.String != "done" || sessions[1].Harness != "claude-code" {
		t.Fatalf("sessions = %+v", sessions)
	}
	first, second := promptTexts(t, server, sessions[0].ID), promptTexts(t, server, sessions[1].ID)
	if len(first) != 1 || len(second) != 1 || first[0] != second[0] {
		t.Errorf("prompts = %q and %q", first, second)
	}
	if got := testkit.Git(t, fake.Remote(shop), "show", "mobius/41:plan.txt"); got != "cents" {
		t.Errorf("plan.txt = %q", got)
	}
	if got := taskCounters(t, server); got != [3]int64{0, 0, 1} {
		t.Errorf("restarts, fix rounds and review rounds = %v", got)
	}
}

func TestAnImplementerOfAFixRoundWithAFindingOfTheLeadThatWaitsForAPauseMovesToTheNewHarness(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	devin := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\n" + devinLimit + commits
	server, _ := connectTask(t, fake, leadFix+noFinding+leadFindings+leadStarts, devin, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	readyForReview(t, server, fake)
	sendChat(t, server, leadChat, "Send the findings to #41")
	waitForPausedSession(t, server, leadChat, engine.ImplementerRole, 2)
	if got := taskCounters(t, server); got[1] != 1 {
		t.Fatalf("restarts, fix rounds and review rounds = %v", got)
	}

	moveImplementerToClaudeCode(t, server)

	sessions := endedImplementers(t, server, 3)
	if sessions[1].EndReason.String != "moved" || sessions[2].EndReason.String != "done" || sessions[2].Harness != "claude-code" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if prompts := promptTexts(t, server, sessions[2].ID); len(prompts) != 1 || !strings.Contains(prompts[0], "# Open items\n\nFindings of the Lead:\nRemove the lines out of scope.\n") {
		t.Errorf("prompts = %q", prompts)
	}
	if got := taskCounters(t, server); got[0] != 0 || got[1] != 1 {
		t.Errorf("restarts, fix rounds and review rounds = %v", got)
	}
}

func TestAReviewerThatWaitsForAPauseMovesToTheNewHarnessAndReviewsTheSameHead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, noFinding+leadStarts, reviewerLimit+commits, noChange)
	setHarness(t, server, engine.ReviewerRole, config.Devin, "gemini-3-pro")
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForPausedSession(t, server, leadChat, engine.ReviewerRole, 1)
	reviewed := head(t, fake, "mobius/41")

	setHarness(t, server, engine.ReviewerRole, config.ClaudeCode, "opus")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	sessions := endedReviewers(t, server, 2)
	if sessions[0].EndReason.String != "moved" || sessions[0].Harness != "devin" || sessions[1].EndReason.String != "done" || sessions[1].Harness != "claude-code" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if !slices.ContainsFunc(roundComments(fake), func(body string) bool { return strings.Contains(body, "The Reviewer moved to claude-code.") }) {
		t.Errorf("comments = %q", roundComments(fake))
	}
	if head(t, fake, "mobius/41") != reviewed {
		t.Error("the head changed")
	}
	if got := taskCounters(t, server); got != [3]int64{0, 0, 1} {
		t.Errorf("restarts, fix rounds and review rounds = %v", got)
	}
}

func TestAJudgeThatWaitsForAPauseBeforeItsStartMovesToTheNewHarnessAndJudgesTheSameItems(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	pauseDevin := "[[prompts]]\nwhen = \"Pause Devin.\"\n" + devinLimit
	rejects := "call = { tool = \"submit_verdicts\", arguments = { items = [{ item = 2, actions = [{ verdict = \"reject\", text = \"Cents avoid rounding.\" }] }] } }\n"
	server := connectJudgeAfter(t, fake, "", rejects, pauseDevin, noChange)
	setHarness(t, server, engine.JudgeRole, config.Devin, "gemini-3-pro")
	agent := start(t, server, implementerSpec(t, server, fake, 50))
	go func() { _ = agent.Prompt(t.Context(), "Pause Devin.", nil) }()
	testkit.WaitFor(t, func() bool { _, paused := devinPause(t, server); return paused })
	if id := fake.AddComment(shop, 42, bot, "Why cents?"); id != 2 {
		t.Fatalf("comment = %d", id)
	}
	waitForPausedSession(t, server, leadChat, engine.JudgeRole, 1)

	setHarness(t, server, engine.JudgeRole, config.ClaudeCode, "sonnet")

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := roleSessions(t, server, engine.JudgeRole)
		return sessions, len(sessions) == 2 && sessions[1].EndedAt.Valid
	})
	if sessions[0].EndReason.String != "moved" || sessions[0].Harness != "devin" || sessions[1].EndReason.String != "done" || sessions[1].Harness != "claude-code" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if prompts := judgePrompts(t, server); len(prompts) != 1 || !strings.Contains(prompts[0], "Why cents?") {
		t.Errorf("Judge prompts = %q", prompts)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	if hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
}
