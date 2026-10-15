package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func breakWaitHead(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var head string
	if err := server.DB.QueryRow("SELECT coalesce(max(break_wait_head), '') FROM tasks WHERE repository = ? AND issue = 41 AND state <> 'ended'", shop).Scan(&head); err != nil {
		t.Fatal(err)
	}
	return head
}

// hardBreak makes a hard break of the CI of the default branch, and gives the id of its failed run.
func hardBreak(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) int64 {
	t.Helper()
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	fake.SetWorkflowRunStatus(run, "completed", "failure")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
	return run
}

// passMain adds a passed run of the workflow of the break to the default branch.
func passMain(fake *testkit.FakeGitHub) int64 {
	passed := failedMainRun(7)
	passed.Conclusion = "success"
	return fake.AddWorkflowRun(shop, passed)
}

// waitingTask dispatches #41 and waits until its CI fails on the head while the default branch has a hard break. It
// gives the head.
func waitingTask(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string, int64) {
	t.Helper()
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	sha := checksHead(t, server, fake)
	run := hardBreak(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == sha })
	return server, sha, run
}

// wantWaiting checks that the task in checks has no fix round, no Implementer round and no stop.
func wantWaiting(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) {
	t.Helper()
	waitForPolls(t, fake)
	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s", state)
	}
	if task := liveTask(t, server, 41); task.FixRounds != 0 || task.CheckHead.Valid {
		t.Errorf("task = %+v", task)
	}
	if count := implementers(t, server); count != 1 {
		t.Errorf("Implementers = %d", count)
	}
	if hasLabel(fake, "mobius:needs-human") || len(mobiusCheckRuns(fake)) != 1 {
		t.Errorf("labels = %v, Mobius check runs = %+v", fake.Labels(shop, 41), mobiusCheckRuns(fake))
	}
}

func TestATaskWhoseCIFailsWhileTheDefaultBranchBreaksWaitsAndTheEndOfTheBreakUpdatesItsBranchOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, run := waitingTask(t, fake)
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.SetBehind(shop, 42)

	wantWaiting(t, server, fake)
	if calls := fake.UpdateBranchCalls(); calls != 0 {
		t.Errorf("update calls = %d", calls)
	}

	ended := passMain(fake)

	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: ended, rerun: true, result: "hard"})
	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == "" })
	merged := head(t, fake, "mobius/41")
	isAncestor(t, fake, sha, merged)
	isAncestor(t, fake, "main", merged)
	if merged == sha {
		t.Errorf("head = %s", merged)
	}
	fake.AddCheckRun(shop, checkRun("build", merged, "completed", "success"))

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	waitForPolls(t, fake)
	if calls := fake.UpdateBranchCalls(); calls != 1 {
		t.Errorf("update calls = %d", calls)
	}
	if task := liveTask(t, server, 41); task.FixRounds != 0 || implementers(t, server) != 1 {
		t.Errorf("task = %+v, Implementers = %d", task, implementers(t, server))
	}
}

func TestTheWaitGoesOnWithNoConflictRoundUntilGitHubAddsTheMergeCommitOfTheBranchUpdate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _ := waitingTask(t, fake)
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.SetBehind(shop, 42)
	fake.HoldBranchUpdates()

	passMain(fake)

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 1 })
	wantWaiting(t, server, fake)
	if calls, got := fake.UpdateBranchCalls(), breakWaitHead(t, server); calls != 1 || got != sha {
		t.Errorf("update calls = %d, break_wait_head = %s", calls, got)
	}

	fake.ReleaseBranchUpdates()

	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == "" })
	merged := head(t, fake, "mobius/41")
	isAncestor(t, fake, "main", merged)
	fake.AddCheckRun(shop, checkRun("build", merged, "completed", "success"))
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	if calls := fake.UpdateBranchCalls(); calls != 1 {
		t.Errorf("update calls = %d", calls)
	}
	if task := liveTask(t, server, 41); task.FixRounds != 0 || implementers(t, server) != 1 {
		t.Errorf("task = %+v, Implementers = %d", task, implementers(t, server))
	}
}

func TestANewHeadWithAFailedCIAfterTheEndOfTheBreakGetsTheUsualFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _ := waitingTask(t, fake)
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.SetBehind(shop, 42)
	passMain(fake)
	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == "" })
	merged := head(t, fake, "mobius/41")
	if merged == sha {
		t.Fatalf("head = %s", merged)
	}

	fake.AddCheckRun(shop, checkRun("lint", merged, "completed", "failure"))

	if round := roundPrompt(t, server); !strings.Contains(round, "Check run \"lint\"") {
		t.Errorf("round = %s", round)
	}
	if calls := fake.UpdateBranchCalls(); calls != 1 {
		t.Errorf("update calls = %d", calls)
	}
}

func TestAHeadThatIsNotBehindItsBaseWhenTheBreakEndsGetsNoUpdateAndTheUsualFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _ := waitingTask(t, fake)
	wantWaiting(t, server, fake)

	passMain(fake)

	if round := roundPrompt(t, server); !strings.Contains(round, "Check run \"build\"") {
		t.Errorf("round = %s", round)
	}
	if calls, got := fake.UpdateBranchCalls(), breakWaitHead(t, server); calls != 0 || got != "" {
		t.Errorf("update calls = %d, break_wait_head = %s", calls, got)
	}
	if task := liveTask(t, server, 41); task.CheckHead.String != sha {
		t.Errorf("task = %+v", task)
	}
}

func TestTheTaskOfTheIssueOfAnOpenBreakGetsTheUsualFixRoundForAFailedCI(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	sha := checksHead(t, server, fake)
	hardBreak(t, server, fake)
	if _, err := server.DB.Exec("UPDATE ci_breaks SET issue = 41"); err != nil {
		t.Fatal(err)
	}

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	if round := roundPrompt(t, server); !strings.Contains(round, "Check run \"build\"") {
		t.Errorf("round = %s", round)
	}
	if got := breakWaitHead(t, server); got != "" {
		t.Errorf("break_wait_head = %s", got)
	}
}

func TestARestartKeepsTheWaitOfATaskAndTheEndOfTheBreakUpdatesItsBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha := seedWaiting(t, fake, "checks")
	for _, statement := range []string{
		`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, rerun_at, result, started_at)
			VALUES ('owner/shop', 7, 'ci', 1, '', 1, '2026-10-04T10:00:00Z', 'hard', '2026-10-04T10:00:00Z')`,
		"UPDATE tasks SET break_wait_head = '" + sha + "'",
	} {
		if _, err := server.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	fake.AddWorkflowRun(shop, failedMainRun(7))
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.SetBehind(shop, 42)

	waitForPolls(t, fake)

	if state := taskState(t, server); state != "checks" || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("state = %s, labels = %v", state, fake.Labels(shop, 41))
	}
	if calls := fake.UpdateBranchCalls(); calls != 0 {
		t.Errorf("update calls = %d", calls)
	}

	passMain(fake)

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 1 })
	isAncestor(t, fake, "main", head(t, fake, "mobius/41"))
	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == "" })
}
