package engine_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// longUsageLimit is a Devin whose first prompt hits a usage limit of two hours.
const longUsageLimit = `
[[prompts]]
error = { code = -32011, message = "Rate limited", data = { retryAfterSeconds = 7200 } }

[[prompts]]
reply = ["Done."]
`

// requireNoReturn fails when the prompt of an agent that must wait for a pause has ended.
func requireNoReturn(t *testing.T, fake *testkit.FakeGitHub, prompted <-chan error) {
	t.Helper()
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	select {
	case err := <-prompted:
		t.Errorf("Prompt returned %v, but the session must stay in its wait", err)
	default:
	}
}

// requireMoved waits for the end of a prompt and requires the error of a move.
func requireMoved(t *testing.T, prompted <-chan error) {
	t.Helper()
	err := testkit.WaitForValue(t, func() (error, bool) {
		select {
		case err := <-prompted:
			return err, true
		default:
			return nil, false
		}
	})
	if err == nil || !strings.Contains(err.Error(), "the Harness of the Role changed") {
		t.Errorf("Prompt returned %v, want the move", err)
	}
}

func TestAnImplementerThatHitsTheUsageLimitMovesAndTheNextSessionOfTheTaskRunsOnTheFreeBinding(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, implementerCommits+noFinding+leadStarts, implementerLimit, noChange)
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)

	sessions := endedImplementers(t, server, 2)
	if sessions[0].EndReason.String != "moved" || sessions[0].Harness != "devin" || sessions[1].EndReason.String != "done" || sessions[1].Harness != "claude-code" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if got := testkit.Git(t, fake.Remote(shop), "show", "mobius/41:plan.txt"); got != "cents" {
		t.Errorf("plan.txt = %q", got)
	}
	if got := taskCounters(t, server); got[0] != 0 {
		t.Errorf("restarts, fix rounds and review rounds = %v", got)
	}
}

// waitForBindingItem waits until the Inbox has, or has not, the item of the failed access check of the second binding.
func waitForBindingItem(t *testing.T, server *testserver.Server, open bool) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		found := slices.ContainsFunc(accessItems(t, server), func(item inboxItem) bool {
			return item.Title == "Access check failed: claude-code sonnet high"
		})
		return found == open
	})
}

// waitsWhileTheOtherBindingHasAnAccessHold gives a server with an Implementer session on Devin that waits for a pause
// of Devin. The access check of the second binding, on Claude Code, failed. It gives the data directory, the session and the result
// of the prompt of the session.
func waitsWhileTheOtherBindingHasAnAccessHold(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string, *engine.Agent, <-chan error) {
	t.Helper()
	server, dataDir := connectWith(t, fake, longUsageLimit, firstSlots(1))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)
	server.Stop()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+noKey)
	cfg := testserver.Config(t, dataDir)
	firstSlots(1)(cfg)
	restarted := testserver.StartWith(t, cfg, fake.URL)
	restarted.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, restarted.Engine.AccessChecked)
	agent := start(t, restarted, implementerSpec(t, restarted, fake, 41))
	requireSession(t, restarted, agent, "devin swe-1.5")
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Store plans in cents.", nil) }()
	testkit.WaitFor(t, func() bool {
		return strings.HasPrefix(session(t, restarted, agent.ID()).QueueReason.String, "paused until ")
	})
	waitForBindingItem(t, restarted, true)
	requireNoReturn(t, fake, prompted)
	return restarted, dataDir, agent, prompted
}

// passAccessOfTheOtherBinding makes the access check of Claude Code pass, and waits until the Inbox item of the failed
// check is gone.
func passAccessOfTheOtherBinding(t *testing.T, server *testserver.Server, dataDir string) {
	t.Helper()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options)
	server.Engine.SetClock(func() time.Time { return time.Now().Add(time.Hour) })
	waitForBindingItem(t, server, false)
}

func TestAnImplementerThatWaitsForAPauseWhileTheOtherBindingHasAnAccessHoldMovesWhenTheAccessHoldEnds(t *testing.T) {
	t.Parallel()
	server, dataDir, agent, prompted := waitsWhileTheOtherBindingHasAnAccessHold(t, testkit.NewFakeGitHub(t))
	defer end(t, agent, "done")

	passAccessOfTheOtherBinding(t, server, dataDir)

	requireMoved(t, prompted)
}

func TestAnImplementerThatWaitsForAPauseInASealedDrainDoesNotMoveWhenTheOtherBindingBecomesFreeUntilTheSealIsAborted(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir, agent, prompted := waitsWhileTheOtherBindingHasAnAccessHold(t, fake)
	defer end(t, agent, "done")
	if got := <-startDrain(t, server); got != "drained" {
		t.Fatalf("drain end = %s", got)
	}
	if got := server.Engine.Seal(); got != engine.Drained {
		t.Fatalf("seal = %s", got)
	}

	passAccessOfTheOtherBinding(t, server, dataDir)

	requireNoReturn(t, fake, prompted)
	server.Engine.AbortDrain()
	requireMoved(t, prompted)
}

func TestAnImplementerThatWaitsForAPauseWhenEachBindingHasAPauseShowsThePauseOfItsOwnBinding(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, usageLimit, firstSlots(1))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+claudeCodeLimit)
	first := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, first, "done")
	second := start(t, server, implementerSpec(t, server, fake, 43))
	defer end(t, second, "done")
	requireSession(t, server, first, "devin swe-1.5")
	requireSession(t, server, second, "claude-code sonnet")
	movedPrompt := make(chan error, 1)
	go func() { movedPrompt <- second.Prompt(t.Context(), "Store plans in cents.", nil) }()
	requireMoved(t, movedPrompt)

	waitingPrompt := make(chan error, 1)
	go func() { waitingPrompt <- first.Prompt(t.Context(), "Store plans in cents.", nil) }()

	found := testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })
	reason := "paused until " + parseTime(t, found.until).UTC().Format("2006-01-02 15:04 UTC")
	testkit.WaitFor(t, func() bool { return session(t, server, first.ID()).QueueReason.String == reason })
	requireNoReturn(t, fake, waitingPrompt)
	if got := session(t, server, first.ID()).QueueReason.String; got != reason {
		t.Errorf("queue reason = %q, want %q", got, reason)
	}
}

func TestAnImplementerWithASettingOfItsTaskWaitsForItsPauseAlsoWhenAnotherBindingIsFree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, usageLimit, firstSlots(1))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)
	addCopiedTask(t, server, fake, 41)
	setBinding(t, server, engine.ImplementerRole, 41, config.Devin, "opus", "low")
	agent := start(t, server, taskSpec(t, shop, 41))
	defer end(t, agent, "done")
	requireSession(t, server, agent, "devin opus")
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Store plans in cents.", nil) }()
	testkit.WaitFor(t, func() bool {
		return strings.HasPrefix(session(t, server, agent.ID()).QueueReason.String, "paused until ")
	})

	requireNoReturn(t, fake, prompted)
}
