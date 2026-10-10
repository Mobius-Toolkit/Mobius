package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// prepareAutopilot adds the Workstream #12, with mobius:autopilot of the Owner when autopilot is true.
func prepareAutopilot(fake *testkit.FakeGitHub, autopilot bool) {
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	if autopilot {
		fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	}
}

// startAutopilot starts a server with a Lead that answers with "Seen", the config after adjust and the SQL before.
func startAutopilot(t *testing.T, fake *testkit.FakeGitHub, adjust func(*config.Config), before string) *testserver.Server {
	t.Helper()
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+seen)
	cfg := testserver.Config(t, dataDir)
	adjust(cfg)
	return startServerWith(t, fake, cfg, before)
}

func addTaskIssue(fake *testkit.FakeGitHub, number int64, title string) {
	fake.AddIssue(shop, number, title)
	fake.AddSubIssue(shop, 12, number)
}

func activeTasks(t *testing.T, server *testserver.Server) int64 {
	t.Helper()
	count, err := store.New(server.DB).CountActiveTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestWithAutopilotAFreeWorkerAndNoBlockerATaskStarts(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")

	server := startAutopilot(t, fake, func(*config.Config) {}, "")

	if task := liveTaskOf(t, server, 41); task.Workstream != 12 {
		t.Errorf("task = %+v", task)
	}
	testkit.WaitFor(t, func() bool {
		for _, a := range activities(t, server) {
			if a.Text == `Dispatched "Add plan model"` {
				return a.Actor == testkit.AppSlug+"[bot]"
			}
		}
		return false
	})
	if labels := fake.Labels(shop, 41); len(labels) != 1 || labels[0] != "mobius:working" {
		t.Errorf("labels = %v", labels)
	}
}

func TestWithNoAutopilotNoTaskStarts(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	addTaskIssue(fake, 41, "Add plan model")
	server := startAutopilot(t, fake, func(*config.Config) {}, "")
	server.WaitForFirstPoll(t, shop)

	waitForPolls(t, fake)

	if hasLiveTask(t, server, 41) || activeTasks(t, server) != 0 {
		t.Error("a task started")
	}
}

func TestWithAsManyActiveTasksAsMaxAgentsNoTaskStarts(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	addTaskIssue(fake, 42, "Add plan price")
	server := startAutopilot(t, fake, func(cfg *config.Config) { cfg.MaxAgents = 1 }, "")

	liveTaskOf(t, server, 41)
	waitForPolls(t, fake)

	if hasLiveTask(t, server, 42) || activeTasks(t, server) != 1 {
		t.Error("a second task started")
	}
}

func TestAnOpenBlockerHoldsTheTaskUntilItCloses(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddBlockedBy(shop, 41, 88)
	server := startAutopilot(t, fake, func(*config.Config) {}, "")
	server.WaitForFirstPoll(t, shop)

	waitForPolls(t, fake)

	if hasLiveTask(t, server, 41) {
		t.Fatal("the blocked task started")
	}

	fake.CloseIssue(shop, 88)

	liveTaskOf(t, server, 41)
}

func TestTasksStartInTheOrderOfTheSubIssues(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddIssue(shop, 42, "Add plan price")
	fake.AddIssue(shop, 43, "Add plan name")
	for _, number := range []int64{43, 41, 42} {
		fake.AddSubIssue(shop, 12, number)
	}
	server := startAutopilot(t, fake, func(*config.Config) {}, "")

	first, second, third := liveTaskOf(t, server, 43), liveTaskOf(t, server, 41), liveTaskOf(t, server, 42)

	if first.ID >= second.ID || second.ID >= third.ID {
		t.Errorf("task ids = %d, %d, %d", first.ID, second.ID, third.ID)
	}
}

func TestAnEndedTaskDoesNotStartAgain(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	server := startAutopilot(t, fake, func(*config.Config) {}, `INSERT INTO tasks (repository, issue, workstream, state, dispatched_at) VALUES ('owner/shop', 41, 12, 'ended', '2026-10-04T10:00:00Z')`)
	server.WaitForFirstPoll(t, shop)

	waitForPolls(t, fake)

	if hasLiveTask(t, server, 41) || activeTasks(t, server) != 0 {
		t.Error("the ended task started again")
	}
}

func TestADeclinedTaskDoesNotStartAgain(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nwhen = \"dispatch of #41\"\ncall = { tool = \"decline\", arguments = { n = 41, reason = \"Split it.\" } }\n")
	server := startServerWith(t, fake, testserver.Config(t, dataDir), "")
	testkit.WaitFor(t, func() bool { return len(fake.Comments(shop, 41)) == 1 && !hasLiveTask(t, server, 41) })

	waitForPolls(t, fake)

	if len(fake.Comments(shop, 41)) != 1 || hasLiveTask(t, server, 41) || activeTasks(t, server) != 0 {
		t.Errorf("comments = %+v, live = %v", fake.Comments(shop, 41), hasLiveTask(t, server, 41))
	}
}
