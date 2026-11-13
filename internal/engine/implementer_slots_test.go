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

// firstSlots gives the first binding of the Implementer max slots.
func firstSlots(slots int) func(*config.Config) {
	return func(cfg *config.Config) { cfg.Roles.Implementer.Max = slots }
}

// addBinding adds a binding of the Implementer and gives its id.
func addBinding(t *testing.T, server *testserver.Server, harness config.Harness, model, effort string, slots int) int64 {
	t.Helper()
	id, err := server.Engine.AddImplementerBinding(t.Context(), config.RoleBinding{Harness: harness, Model: model, Effort: effort, Max: slots})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// bindingOf gives the harness and the model of the row of the agent.
func bindingOf(t *testing.T, server *testserver.Server, agent *engine.Agent) string {
	t.Helper()
	row := session(t, server, agent.ID())
	return row.Harness + " " + row.Model
}

func requireSession(t *testing.T, server *testserver.Server, agent *engine.Agent, want string) {
	t.Helper()
	if got := bindingOf(t, server, agent); got != want {
		t.Errorf("session %d runs on %q, want %q", agent.ID(), got, want)
	}
}

func TestTwoBindingsWithOneSlotEachRunTwoImplementerSessionsAtTheSameTimeAndAThirdWaitsWithTheReasonOfTheFirstBinding(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, answersDone, firstSlots(1))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)

	first := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, first, "done")
	second := start(t, server, implementerSpec(t, server, fake, 43))
	defer end(t, second, "done")
	third := startLater(t.Context(), server, implementerSpec(t, server, fake, 45))

	waiting := queued(t, server, engine.ImplementerRole)

	requireSession(t, server, first, "devin swe-1.5")
	requireSession(t, server, second, "claude-code sonnet")
	if waiting.QueueReason.String != "no free implementer slot (1/1)" || waiting.Harness != "devin" {
		t.Errorf("waiting session = %+v, want the reason of the first binding", waiting)
	}
	end(t, second, "done")
	agent := awaitWithin(t, third)
	defer end(t, agent, "done")
	requireSession(t, server, agent, "claude-code sonnet")
}

func TestANewSessionTakesTheBindingWithTheFewestRunningSessionsAndTheFirstBindingAtEqualCounts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, answersDone, firstSlots(2))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 2)

	var agents []*engine.Agent
	for _, number := range []int64{41, 43, 45, 47} {
		agent := start(t, server, implementerSpec(t, server, fake, number))
		defer end(t, agent, "done")
		agents = append(agents, agent)
	}

	for i, want := range []string{"devin swe-1.5", "claude-code sonnet", "devin swe-1.5", "claude-code sonnet"} {
		requireSession(t, server, agents[i], want)
	}
}

func TestAPauseOfTheHarnessOfTheFirstBindingMovesTheNextSessionToTheSecondBindingAndAThirdSessionWaitsForThePause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, usageLimit, firstSlots(1))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)
	paused := start(t, server, implementerSpec(t, server, fake, 41))
	go func() { _ = paused.Prompt(t.Context(), "Store plans in cents.", nil) }()
	testkit.WaitFor(t, func() bool { _, found := devinPause(t, server); return found })

	second := start(t, server, implementerSpec(t, server, fake, 43))
	defer end(t, second, "done")

	requireSession(t, server, second, "claude-code sonnet")
	if got := session(t, server, second.ID()); got.QueueReason.Valid {
		t.Errorf("second session = %+v, want no queue reason", got)
	}
	third := startLater(t.Context(), server, implementerSpec(t, server, fake, 45))
	waiting := testkit.WaitForValue(t, func() (store.Session, bool) {
		for _, node := range tree(t, server) {
			if node.Session.Role == engine.ImplementerRole && node.Session.ID != paused.ID() && node.Session.ID != second.ID() && node.Session.QueueReason.Valid {
				return node.Session, true
			}
		}
		return store.Session{}, false
	})
	if !strings.HasPrefix(waiting.QueueReason.String, "paused until ") || waiting.Harness != "devin" {
		t.Errorf("waiting session = %+v, want the pause of the first binding", waiting)
	}
	end(t, paused, "stopped")
	if err := server.Engine.Resume(t.Context(), "devin"); err != nil {
		t.Fatal(err)
	}
	agent := awaitWithin(t, third)
	defer end(t, agent, "done")
	requireSession(t, server, agent, "devin swe-1.5")
}

func TestATaskSettingGivesTheSessionTheSettingAlsoWhenAnotherBindingIsFreeAndTakesASlotOfTheFirstBinding(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, answersDone, firstSlots(1))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)
	addCopiedTask(t, server, fake, 41)
	addCopiedTask(t, server, fake, 43)
	setBinding(t, server, "implementer", 41, config.ClaudeCode, "opus", "low")

	withSetting := start(t, server, taskSpec(t, shop, 41))
	defer end(t, withSetting, "done")
	next := start(t, server, taskSpec(t, shop, 43))
	defer end(t, next, "done")

	requireSession(t, server, withSetting, "claude-code opus")
	requireSession(t, server, next, "claude-code sonnet")
}

func TestAnAddedBindingGivesAWaitingSessionASlot(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, answersDone, firstSlots(1))
	running := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, running, "done")
	waiting := startLater(t.Context(), server, implementerSpec(t, server, fake, 43))
	queued(t, server, engine.ImplementerRole)

	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)

	agent := awaitWithin(t, waiting)
	defer end(t, agent, "done")
	requireSession(t, server, agent, "claude-code sonnet")
}

func TestTheAgentsPageGivesTheSumOfTheMaxOfTheImplementerBindings(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, answersDone, firstSlots(3))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 2)

	for _, group := range overview(t, server).Groups {
		if group.Name == "Implementer" && group.Max != 5 {
			t.Errorf("Implementer max = %d, want 5", group.Max)
		}
	}
}

// keptOnTheSecondBinding runs the first round of the task #41 on the second binding while a session holds the only
// slot of the first binding. It waits for the kept session, and gives the session of the first binding.
func keptOnTheSecondBinding(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) (*engine.Agent, int64) {
	t.Helper()
	id := addBinding(t, server, config.Devin, "opus", "low", 1)
	holder := start(t, server, taskSpec(t, shop, 43))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	waitForKept(t, server, 41)
	return holder, id
}

func TestAKeptSessionContinuesOnItsBindingInTheNextRoundAlsoWhenTheFirstBindingHasAsManySessions(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := keptServer(t, fake, commits, firstSlots(1))
	holder, _ := keptOnTheSecondBinding(t, server, fake)
	end(t, holder, "done")
	before := harnessStarts(t, dataDir)

	startFixRound(t, server)

	sessions := endedImplementers(t, server, 3)
	first, round := sessions[1], sessions[2]
	requireBinding(t, round, "devin", "opus", "low")
	if round.Continued != 1 || round.AcpSessionID.String != first.AcpSessionID.String {
		t.Errorf("rounds = %+v, %+v, want the second round to continue the first", first, round)
	}
	if got := harnessStarts(t, dataDir); got != before {
		t.Errorf("starts = %d, want %d", got, before)
	}
}

func TestARemovalOfTheBindingOfAKeptSessionClosesTheKeptSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, firstSlots(1))
	holder, id := keptOnTheSecondBinding(t, server, fake)
	defer end(t, holder, "done")
	task := liveTask(t, server, 41)
	kept := server.Engine.KeptSession(task.ID)

	if err := server.Engine.RemoveImplementerBinding(t.Context(), id); err != nil {
		t.Fatal(err)
	}

	waitForClosedKept(t, server, task.ID, kept)
}

func TestARoundWithAKeptSessionWaitsForASlotOfTheKeptBindingAlsoWhenAnotherBindingIsFree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := keptServer(t, fake, commits, firstSlots(1))
	holder, _ := keptOnTheSecondBinding(t, server, fake)
	filler := start(t, server, implementerSpec(t, server, fake, 45))
	requireSession(t, server, filler, "devin opus")
	end(t, holder, "done")
	before := harnessStarts(t, dataDir)

	startFixRound(t, server)

	waiting := queued(t, server, engine.ImplementerRole)
	if waiting.QueueReason.String != "no free implementer slot (1/1)" {
		t.Errorf("waiting session = %+v, want the reason of the full binding", waiting)
	}
	end(t, filler, "done")
	rounds := slices.DeleteFunc(endedImplementers(t, server, 4), func(session store.Session) bool { return session.Issue.Int64 != 41 })
	if len(rounds) != 2 {
		t.Fatalf("rounds of #41 = %+v, want 2", rounds)
	}
	first, round := rounds[0], rounds[1]
	requireBinding(t, round, "devin", "opus", "low")
	if round.Continued != 1 || round.AcpSessionID.String != first.AcpSessionID.String {
		t.Errorf("rounds = %+v, %+v, want the second round to continue the first", first, round)
	}
	if got := harnessStarts(t, dataDir); got != before {
		t.Errorf("starts = %d, want %d", got, before)
	}
}

func TestARemovalOfTheFirstBindingKeepsTheCountOfTheRunningSessionsOfTheMovedBinding(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, answersDone, firstSlots(1))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)
	removed := start(t, server, implementerSpec(t, server, fake, 41))
	moved := start(t, server, implementerSpec(t, server, fake, 43))
	defer end(t, moved, "done")
	end(t, removed, "done")

	if err := server.Engine.RemoveImplementerBinding(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	waiting := startLater(t.Context(), server, implementerSpec(t, server, fake, 45))

	reason := queued(t, server, engine.ImplementerRole)
	if reason.QueueReason.String != "no free implementer slot (1/1)" {
		t.Errorf("waiting session = %+v, want the reason of the full binding", reason)
	}
	end(t, moved, "done")
	agent := awaitWithin(t, waiting)
	defer end(t, agent, "done")
	requireSession(t, server, agent, "claude-code sonnet")
}

func TestARoundWithAKeptSessionThatWaitsForItsFullBindingLeavesTheFreeBindingToALaterSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, firstSlots(1))
	holder, _ := keptOnTheSecondBinding(t, server, fake)
	filler := start(t, server, implementerSpec(t, server, fake, 45))
	requireSession(t, server, filler, "devin opus")
	end(t, holder, "done")
	startFixRound(t, server)
	queued(t, server, engine.ImplementerRole)

	later := start(t, server, implementerSpec(t, server, fake, 47))
	defer end(t, later, "done")

	requireSession(t, server, later, "devin swe-1.5")
	end(t, filler, "done")
}

func TestAConflictRoundOfATaskWithAKeptSessionOnAFullBindingTakesTheFreeBinding(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	merges := "[[prompts]]\nwhen = \"Merge the base branch and remove the conflicts.\"\n" +
		"shell = \"git merge -q origin/main; echo cents > plan.txt && git add plan.txt && git commit -q --no-edit\"\n\n"
	server, _ := keptServer(t, fake, merges+commits, firstSlots(1))
	holder, _ := keptOnTheSecondBinding(t, server, fake)
	filler := start(t, server, implementerSpec(t, server, fake, 45))
	defer end(t, filler, "done")
	requireSession(t, server, filler, "devin opus")
	end(t, holder, "done")

	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")

	rounds := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		rounds := slices.DeleteFunc(roleSessions(t, server, engine.ImplementerRole), func(session store.Session) bool { return session.Issue.Int64 != 41 })
		return rounds, len(rounds) == 2 && rounds[1].EndedAt.Valid
	})
	requireBinding(t, rounds[1], "devin", "swe-1.5", "high")
	if rounds[1].Continued != 0 {
		t.Errorf("conflict round = %+v, want a new process", rounds[1])
	}
}
