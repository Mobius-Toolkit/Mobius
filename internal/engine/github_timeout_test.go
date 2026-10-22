package engine_test

import (
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// shortGitHubTimeout makes the time limit of a GitHub call short for the test.
func shortGitHubTimeout(t *testing.T) {
	t.Helper()
	before := github.Timeout
	github.Timeout = time.Second
	t.Cleanup(func() { github.Timeout = before })
}

func TestAGitHubCallThatDoesNotAnswerFailsTheWorkerAndTheWorkerStartsAgain(t *testing.T) {
	shortGitHubTimeout(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.HangNext("POST /repos/{owner}/{repo}/pulls")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	if task := liveTask(t, server, 41); task.WorkerRestarts != 1 {
		t.Errorf("Worker restarts = %d", task.WorkerRestarts)
	}
}

func TestThePollContinuesAfterAGitHubCallThatDoesNotAnswer(t *testing.T) {
	shortGitHubTimeout(t)
	fake := testkit.NewFakeGitHub(t)
	connectTask(t, fake, leadStarts, commits, noChange)
	hung := fake.HangNext("GET /repos/{owner}/{repo}/issues")
	testkit.WaitFor(t, func() bool {
		select {
		case <-hung:
			return true
		default:
			return false
		}
	})
	began := time.Now()

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	if waited := time.Since(began); waited < github.Timeout {
		t.Errorf("the pull request came after %v", waited)
	}
}

// Serial: the test changes the wait after a rate limit.
func TestAGitHubRateLimitOfAWorkerCostsNoRestart(t *testing.T) {
	before := *engine.RateLimitGrace
	*engine.RateLimitGrace = 100 * time.Millisecond
	t.Cleanup(func() { *engine.RateLimitGrace = before })
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	reset := time.Now().Add(2 * time.Second)
	fake.RateLimitNext("POST /repos/{owner}/{repo}/pulls", reset)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	if len(fake.PullRequests(shop)) != 1 {
		t.Errorf("pull requests = %d", len(fake.PullRequests(shop)))
	}
	if time.Now().Before(reset.Truncate(time.Second)) {
		t.Error("the Worker started again before the reset")
	}
	if task := liveTask(t, server, 41); task.WorkerRestarts != 0 {
		t.Errorf("Worker restarts = %d", task.WorkerRestarts)
	}
}
