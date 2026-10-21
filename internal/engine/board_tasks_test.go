package engine_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// boardTasks waits until the board has count tasks, and gives them.
func boardTasks(t *testing.T, server *testserver.Server, count int) []engine.BoardTask {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]engine.BoardTask, bool) {
		tasks, err := server.Engine.BoardTasks(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return tasks, len(tasks) == count
	})
}

func TestTheBoardHasALiveTaskWithItsTitlesPullRequestAndRounds(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	waitForApproval(t, server, fake)

	tasks := boardTasks(t, server, 1)

	task := tasks[0]
	want := engine.BoardTask{
		Repository: shop, Number: 41, Title: "Add plan model", URL: "https://github.com/owner/shop/issues/41",
		WorkstreamRepository: shop, Workstream: 12, WorkstreamTitle: "Integrate loyalty plans",
		State: "approval", StateAt: task.StateAt, PullRequest: 42, PullRequestURL: "https://github.com/owner/shop/pull/42",
		FixRounds: 0, ReviewRounds: 1,
	}
	if task != want {
		t.Errorf("task = %+v, want %+v", task, want)
	}
	if task.StateAt.IsZero() {
		t.Error("the state time is empty")
	}
}

func TestTheBoardHasATaskThatEndedInTheLastDayAndNotATaskThatEndedEarlier(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	waitForApproval(t, server, fake)

	fake.MergePullRequest(shop, 42)

	ended := testkit.WaitForValue(t, func() (engine.BoardTask, bool) {
		tasks, err := server.Engine.BoardTasks(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return firstOf(tasks), len(tasks) == 1 && tasks[0].State == "ended"
	})
	if ended.Title != "Add plan model" || ended.WorkstreamTitle != "Integrate loyalty plans" || ended.PullRequest != 42 {
		t.Errorf("task = %+v", ended)
	}

	server.Engine.SetClock(func() time.Time { return time.Now().Add(24*time.Hour + time.Minute) })

	tasks, err := server.Engine.BoardTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("tasks = %+v", tasks)
	}
}

func firstOf(tasks []engine.BoardTask) engine.BoardTask {
	if len(tasks) == 0 {
		return engine.BoardTask{}
	}
	return tasks[0]
}

func TestTheBoardGivesATaskInAnotherRepositoryItsOwnRepositoryAndTheRepositoryOfTheWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := foreignTask(t, fake)
	fake.AddLabel(garden, 7, "mobius:ready", "owner")
	gardenTask(t, server)

	task := boardTasks(t, server, 1)[0]

	if task.Repository != garden || task.Number != 7 || task.Title != "Water the plants" || task.WorkstreamRepository != shop || task.Workstream != 12 || task.State != "dispatched" {
		t.Errorf("task = %+v", task)
	}
	if task.URL != "https://github.com/owner/garden/issues/7" {
		t.Errorf("URL = %s", task.URL)
	}
}

func TestTheChangeOfATaskFromChecksToApprovalGivesAWorkstreamsChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, func(cfg *config.Config) { cfg.ReviewQuietPeriod = 2 * time.Second })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" })
	changes, stop := server.Engine.Listen()
	defer stop()

	waitForChange(t, changes, func(change engine.Change) bool {
		return change.Workstreams && taskState(t, server) == "approval"
	})
}

func TestTheQueueOfATaskGivesAWorkstreamsChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := foreignTask(t, fake)
	fake.AddLabel(garden, 7, "mobius:ready", "owner")
	task := gardenTask(t, server)
	waitForPolls(t, fake)
	changes, stop := server.Engine.Listen()
	defer stop()

	queued, err := server.Engine.QueueTask(t.Context(), store.QueueTaskParams{QueuedAt: sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true}, ID: task.ID, FromState: "dispatched"})
	if err != nil || queued != 1 {
		t.Fatalf("queued = %d, error = %v", queued, err)
	}

	for {
		select {
		case change := <-changes:
			if change.Workstreams {
				return
			}
		default:
			t.Fatal("no Workstreams change")
		}
	}
}
