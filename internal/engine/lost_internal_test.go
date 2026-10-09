package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

func TestAnIdleWorkerStartsOnlyWhenNoWorkerOfTheKeyRuns(t *testing.T) {
	e := New(nil, nil, &config.Config{}, Agents{})
	release := make(chan struct{})
	returned := make(chan struct{})
	first := e.startWorker(1, func(context.Context) { <-release; close(returned) })

	second := e.startIdleWorker(1, func(context.Context, int) {})
	other := e.startIdleWorker(2, func(context.Context, int) {})

	if !first || second || !other {
		t.Errorf("started = %v, %v, %v", first, second, other)
	}
	e.stop(1)
	if !e.hasWorker(1) {
		t.Error("a stopped Worker that did not return has no goroutine")
	}
	close(release)
	<-returned
	e.running.Wait()
	if e.hasWorker(1) || !e.startIdleWorker(1, func(context.Context, int) {}) {
		t.Error("the key has a Worker after the return")
	}
	e.running.Wait()
}

func lostTaskEngine(t *testing.T) (*Engine, store.Task) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	task, err := store.New(db).AddTask(t.Context(), store.AddTaskParams{Repository: "owner/shop", Issue: 41, Workstream: 12, DispatchedAt: "2026-10-04T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return New(db, nil, &config.Config{}, Agents{}), task
}

func setLostState(t *testing.T, e *Engine, task store.Task, state string) {
	t.Helper()
	_, err := e.db.Exec("UPDATE tasks SET state = ?, worker = 'implementer', worker_input = 'Store plans in cents.' WHERE id = ?", state, task.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestATaskThatAStepHoldsGetsNoSecondWorker(t *testing.T) {
	e, task := lostTaskEngine(t)
	repository := github.Repository{FullName: "owner/shop"}
	setLostState(t, e, task, "queued")
	release := e.holdWorker(task.ID)

	if err := e.restartLost(t.Context(), repository, false); err != nil {
		t.Fatal(err)
	}
	held := e.stops[task.ID].starts
	if !e.startWorker(task.ID, func(ctx context.Context) { <-ctx.Done() }) {
		t.Fatal("the Worker did not start")
	}
	release()
	if err := e.restartLost(t.Context(), repository, false); err != nil {
		t.Fatal(err)
	}
	starts := e.stops[task.ID].starts
	e.stopWorkers()

	if held != 0 || starts != 1 {
		t.Errorf("starts = %d, %d", held, starts)
	}
}
