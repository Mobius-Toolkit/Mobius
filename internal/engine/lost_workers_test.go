package engine_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// seedWorkerTask adds the task id for the issue number to the store of a server that did not start yet, as a server
// that stopped during the turn of its Implementer leaves it. The tasks wait in the queue in the order of their ids.
func seedWorkerTask(t *testing.T, fake *testkit.FakeGitHub, dataDir string, id, number int64, state string) {
	t.Helper()
	fake.AddIssue(shop, number, "Task")
	fake.AddSubIssue(shop, 12, number)
	fake.AddLabel(shop, number, "mobius:working", testkit.AppSlug+"[bot]")
	seed(t, dataDir, fmt.Sprintf(`INSERT INTO tasks (id, repository, issue, workstream, state, dispatched_at, queued_at, worker, worker_input)
		VALUES (%d, 'owner/shop', %d, 12, '%s', '2026-10-04T10:00:00Z', '2026-10-04T10:00:0%dZ', 'implementer', 'Store plans in cents.')`, id, number, state, id))
}

// connectLost starts a server whose Implementers hang, with the tasks that seed adds. The first poll starts the Worker
// of each task again, with no count.
func connectLost(t *testing.T, fake *testkit.FakeGitHub, adjust func(*config.Config), seedTasks func(dataDir string)) *testserver.Server {
	t.Helper()
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) {
		adjust(cfg)
		testkit.InstallFakeHarness(t, cfg.DataDir, "devin", options+hangs)
		seedTasks(cfg.DataDir)
	})
	return server
}

func taskRestarts(t *testing.T, server *testserver.Server, number int64) int64 {
	t.Helper()
	var restarts int64
	if err := server.DB.QueryRow("SELECT worker_restarts FROM tasks WHERE repository = ? AND issue = ?", shop, number).Scan(&restarts); err != nil {
		t.Fatal(err)
	}
	return restarts
}

func TestAWorkingTaskThatLostItsWorkerGetsANewWorkerAndTheRestartCounts(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectLost(t, fake, noChange, func(dataDir string) { seedWorkerTask(t, fake, dataDir, 1, 41, "working") })
	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.ImplementerRole)) == 1 })
	if restarts := taskRestarts(t, server, 41); restarts != 0 {
		t.Fatalf("worker restarts after the restart of the server = %d", restarts)
	}

	server.Engine.StopWorker(1)

	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.ImplementerRole)) == 2 })
	if restarts := taskRestarts(t, server, 41); restarts != 1 {
		t.Errorf("worker restarts = %d", restarts)
	}
	if state := taskState(t, server); state != "working" && state != "queued" {
		t.Errorf("state = %s", state)
	}
}

func TestAQueuedTaskThatLostItsWorkerGetsANewWorkerAndATaskInTheQueueDoesNotRestart(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectLost(t, fake, func(cfg *config.Config) { cfg.MaxAgents = 1 }, func(dataDir string) {
		seedWorkerTask(t, fake, dataDir, 1, 41, "working")
		seedWorkerTask(t, fake, dataDir, 2, 43, "queued")
	})
	waiting := queued(t, server, engine.ImplementerRole)
	if waiting.QueueReason.String != "no free agent slot (1/1)" {
		t.Fatalf("queue reason = %q", waiting.QueueReason.String)
	}

	waitForPolls(t, fake)

	if restarts := []int64{taskRestarts(t, server, 41), taskRestarts(t, server, 43)}; !reflect.DeepEqual(restarts, []int64{0, 0}) {
		t.Errorf("worker restarts = %v", restarts)
	}
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 2 {
		t.Errorf("Implementers = %+v", sessions)
	}

	server.Engine.StopWorker(2)

	testkit.WaitFor(t, func() bool { return taskRestarts(t, server, 43) == 1 })
	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.ImplementerRole)) == 3 })
	queued(t, server, engine.ImplementerRole)
	if restarts := taskRestarts(t, server, 41); restarts != 0 {
		t.Errorf("worker restarts of #41 = %d", restarts)
	}
	if state := taskState(t, server); state != "working" {
		t.Errorf("state of #41 = %s", state)
	}
}

func TestATaskThatLosesItsWorkerAfterMaxWorkerRestartsGoesToAHuman(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectLost(t, fake, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 }, func(dataDir string) {
		seedWorkerTask(t, fake, dataDir, 1, 41, "working")
	})
	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.ImplementerRole)) == 1 })
	server.Engine.StopWorker(1)
	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.ImplementerRole)) == 2 })

	server.Engine.StopWorker(1)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	if restarts := taskRestarts(t, server, 41); restarts != 1 {
		t.Errorf("worker restarts = %d", restarts)
	}
	if hasLabel(fake, "mobius:working") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	waitForPolls(t, fake)
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 2 {
		t.Errorf("Implementers = %+v", sessions)
	}
}

func TestAFixRoundThatWaitsForGitHubGetsNoSecondWorkerFromThePoll(t *testing.T) {
	shortGitHubTimeout(t)
	fake := testkit.NewFakeGitHub(t)
	implementer := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo more >> plan.txt && git commit -q -am 'Remove the lines'\"\n\n" + commits
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, implementer, func(cfg *config.Config) { cfg.MaxFixRounds = 2 })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	readyForReview(t, server, fake)
	// The Lead tool moves the task to working, and then waits for GitHub with no goroutine of a Worker.
	fake.HangNext("GET /repos/{owner}/{repo}/issues/{number}/comments")

	sendChat(t, server, leadChat, "Send the findings to #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "working" })
	waitForPolls(t, fake)
	if restarts := workerRestarts(t, server); restarts != 0 {
		t.Errorf("worker restarts = %d", restarts)
	}
}
