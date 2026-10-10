package engine_test

import (
	"testing"
	"time"

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
	testkit.Slow(t)
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
	testkit.Slow(t)
	shortGitHubTimeout(t)
	fake := testkit.NewFakeGitHub(t)
	connectTask(t, fake, leadStarts, commits, noChange)
	fake.HangNext("GET /repos/{owner}/{repo}/issues")
	began := time.Now()

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	if waited := time.Since(began); waited < github.Timeout {
		t.Errorf("the pull request came after %v", waited)
	}
}
