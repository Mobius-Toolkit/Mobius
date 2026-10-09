package engine_test

import (
	"slices"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// staleConflictInNeedsHuman starts a task whose stale pull request with a merge conflict goes to a human, and waits
// for it. The caller makes the pull request fresh to let Mobius continue the task.
func staleConflictInNeedsHuman(t *testing.T, fake *testkit.FakeGitHub, implementer string) *testserver.Server {
	t.Helper()
	server, _ := startReady(t, fake, "", implementer)
	fake.SetCreatedAt(shop, pullRequestNumber, 0)
	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && pullRequestHasNeedsHuman(fake) })
	return server
}

// stoppedOnFailedCI starts a task whose only fix round made no commit, so the failed check run of its head hands it to
// a human. It gives the id of the check run.
func stoppedOnFailedCI(t *testing.T, fake *testkit.FakeGitHub, adjust func(*config.Config)) (*testserver.Server, int64) {
	t.Helper()
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, adjust)
	sha := approvalHead(t, server, fake)
	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	return server, id
}

func staleItems(t *testing.T, server *testserver.Server) int {
	t.Helper()
	count := 0
	for _, item := range inbox(t, server) {
		if item.Kind == "stale pull request" {
			count++
		}
	}
	return count
}

func TestATaskInNeedsHumanWithAMergeConflictGetsAConflictRoundAndGoesToApprovalWithNoNeedsHumanLabel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := staleConflictInNeedsHuman(t, fake, mergesCents)
	first := head(t, fake, "mobius/41")
	stopped := liveTask(t, server, 41)

	fake.SetCreatedAt(shop, pullRequestNumber, time.Now().Unix())

	waitForReadyEvents(t, server, 2)
	merged := head(t, fake, "mobius/41")
	isAncestor(t, fake, first, merged)
	isAncestor(t, fake, "main", merged)
	testkit.WaitFor(t, func() bool {
		return taskState(t, server) == "approval" && !hasLabel(fake, "mobius:needs-human") && !pullRequestHasNeedsHuman(fake)
	})
	task := liveTask(t, server, 41)
	if task.FixRounds != stopped.FixRounds || task.ReviewRounds != stopped.ReviewRounds || task.WorkerRestarts != stopped.WorkerRestarts {
		t.Errorf("task = %+v, stopped = %+v", task, stopped)
	}
	if count := implementers(t, server); count != 2 {
		t.Errorf("Implementers = %d", count)
	}
	if count := staleItems(t, server); count != 1 {
		t.Errorf("stale items = %d", count)
	}
}

func TestAStalePullRequestInNeedsHumanGetsNoConflictRoundAndNoNewStaleItem(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := staleConflictInNeedsHuman(t, fake, mergesCents)

	waitForPolls(t, fake)

	if count := implementers(t, server); count != 1 {
		t.Errorf("Implementers = %d", count)
	}
	if count := staleItems(t, server); count != 1 {
		t.Errorf("stale items = %d", count)
	}
}

func TestAFailedAutomaticConflictRoundStartsNoSecondRoundOnTheSameHead(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := staleConflictInNeedsHuman(t, fake, commits+"\n"+conflictWhen+"shell = \"true\"\n")
	first := head(t, fake, "mobius/41")

	fake.SetCreatedAt(shop, pullRequestNumber, time.Now().Unix())

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": the conflict round did not merge the base branch.")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	waitForPolls(t, fake)

	if got := head(t, fake, "mobius/41"); got != first {
		t.Errorf("head = %s", got)
	}
	if count := implementers(t, server); count != 2 {
		t.Errorf("Implementers = %d", count)
	}
	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
}

func TestAPassedCIOnTheHeadOfAStopOnFailedCIMovesTheTaskToApprovalWithNoNeedsHumanLabel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, id := stoppedOnFailedCI(t, fake, noChange)
	sha := head(t, fake, "mobius/41")
	stopped := liveTask(t, server, 41)

	fake.SetCheckRunStatus(id, "completed", "success")

	waitForReadyEvents(t, server, 2)
	testkit.WaitFor(t, func() bool {
		return taskState(t, server) == "approval" && !hasLabel(fake, "mobius:needs-human") && !pullRequestHasNeedsHuman(fake)
	})
	task := liveTask(t, server, 41)
	if task.FixRounds != stopped.FixRounds || task.ReviewRounds != stopped.ReviewRounds || task.WorkerRestarts != stopped.WorkerRestarts {
		t.Errorf("task = %+v, stopped = %+v", task, stopped)
	}
	if got := head(t, fake, "mobius/41"); got != sha {
		t.Errorf("head = %s", got)
	}
	if count := implementers(t, server); count != 2 {
		t.Errorf("Implementers = %d", count)
	}
}

func TestAStopOnTheRoundLimitStaysInNeedsHumanWhenTheCIPasses(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, func(cfg *config.Config) { cfg.MaxFixRounds = 1 })
	sha := approvalHead(t, server, fake)
	if _, err := server.DB.Exec("UPDATE tasks SET fix_rounds = 1 WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}
	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })

	fake.SetCheckRunStatus(id, "completed", "success")
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if !hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if events := readyEvents(t, server); events != 1 {
		t.Errorf("events = %d", events)
	}
}

func TestATaskThatContinuesKeepsMobiusQuestion(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, id := stoppedOnFailedCI(t, fake, noChange)
	fake.AddLabel(shop, 41, "mobius:question", "owner")

	fake.SetCheckRunStatus(id, "completed", "success")

	waitForReadyEvents(t, server, 2)
	testkit.WaitFor(t, func() bool { return !hasLabel(fake, "mobius:needs-human") })
	if labels := fake.Labels(shop, 41); !slices.Contains(labels, "mobius:question") {
		t.Errorf("labels = %v", labels)
	}
}
