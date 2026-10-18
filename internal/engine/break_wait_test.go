package engine_test

import (
	"strings"
	"testing"
	"time"

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

// logStateOnFailure logs the task, the breaks of the CI, the sessions, the errors of the sessions, the pull requests,
// their check runs and the latest requests of the fake GitHub when the test fails. Thus a failed wait shows the step
// that did not finish. Few requests at the end show a poll that blocks.
func logStateOnFailure(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, query := range []string{
			"SELECT 'task', state, state_at, coalesce(worker, ''), worker_restarts, fix_rounds, coalesce(check_head, ''), coalesce(ci_failed_head, ''), coalesce(break_wait_head, ''), coalesce(rerun_head, '') FROM tasks WHERE issue = 41",
			"SELECT 'break', run_id, rerun_at != '', result, ended_at != '', issue FROM ci_breaks",
			"SELECT 'session', id, role, coalesce(end_reason, ''), coalesce(ended_at, '') FROM sessions",
			"SELECT 'error', session, kind, json FROM transcript WHERE kind = 'error'",
		} {
			dbRows, err := server.DB.Query(query)
			if err != nil {
				t.Error(err)
				continue
			}
			columns, _ := dbRows.Columns()
			for dbRows.Next() {
				values := make([]any, len(columns))
				for i := range values {
					values[i] = new(any)
				}
				if err := dbRows.Scan(values...); err != nil {
					t.Error(err)
					break
				}
				line := make([]any, len(values))
				for i, value := range values {
					line[i] = *value.(*any)
				}
				t.Log(line...)
			}
			_ = dbRows.Close()
		}
		for _, pull := range fake.PullRequests(shop) {
			sha := head(t, fake, pull.Head)
			t.Logf("pull request #%d head %s %s", pull.Number, pull.Head, sha)
			for _, run := range fake.CheckRuns(shop) {
				if run.HeadSHA == sha {
					t.Logf("check run %q on %s: %s %q", run.Name, run.HeadSHA, run.Status, run.Conclusion)
				}
			}
		}
		requests := fake.Requests()
		recent := 0
		for _, request := range requests {
			if time.Since(request.Time) <= 10*time.Second {
				recent++
			}
		}
		t.Logf("%d requests in the last 10 seconds", recent)
		for _, request := range requests[max(len(requests)-20, 0):] {
			t.Logf("%s %s %s?%s %d", request.Time.Format("15:04:05.000"), request.Method, request.Path, request.Query, request.Status)
		}
	})
}

// waitingTask dispatches #41 and waits until its CI fails on the head while the default branch has a hard break. It
// gives the head.
func waitingTask(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string, int64) {
	t.Helper()
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	logStateOnFailure(t, server, fake)
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

func TestAFailedCheckRunThatShowsAfterTheFirstReadOfTheHeadInAPollGetsNoFixRoundWhileTheDefaultBranchBreaks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	logStateOnFailure(t, server, fake)
	sha := checksHead(t, server, fake)
	hardBreak(t, server, fake)

	fake.AddCheckRunAfterNextRead(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == sha })
	wantWaiting(t, server, fake)
}

func TestTheWaitGoesOnWithNoConflictRoundUntilGitHubAddsTheMergeCommitOfTheBranchUpdate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _ := waitingTask(t, fake)
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
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

func TestTheWaitEndsWhenGitHubDoesNotAddTheMergeCommitOfTheBranchUpdateInTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _ := waitingTask(t, fake)
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.HoldBranchUpdates()
	passMain(fake)
	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 1 })
	wantWaiting(t, server, fake)

	server.Engine.SetClock(func() time.Time { return time.Now().Add(10 * time.Minute) })

	testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
	waitForPolls(t, fake)
	if calls, got := fake.UpdateBranchCalls(), breakWaitHead(t, server); calls != 1 || got != "" {
		t.Errorf("update calls = %d, break_wait_head = %s", calls, got)
	}
	isAncestor(t, fake, sha, head(t, fake, "mobius/41"))
	if log := testkit.Git(t, fake.Remote(shop), "log", "--format=%s", "mobius/41"); strings.Contains(log, "Add price") {
		t.Errorf("log = %s", log)
	}
}

func TestANewHeadWithAFailedCIAfterTheEndOfTheBreakGetsTheUsualFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _ := waitingTask(t, fake)
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
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

func TestABranchWithEachCommitOfItsBaseWhenTheBreakEndsGetsARefusedUpdateAndTheUsualFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _ := waitingTask(t, fake)
	wantWaiting(t, server, fake)

	passMain(fake)

	if round := roundPrompt(t, server); !strings.Contains(round, "Check run \"build\"") {
		t.Errorf("round = %s", round)
	}
	if calls, got := fake.UpdateBranchCalls(), breakWaitHead(t, server); calls != 1 || got != "" {
		t.Errorf("update calls = %d, break_wait_head = %s", calls, got)
	}
	if got := head(t, fake, "mobius/41"); got != sha {
		t.Errorf("head = %s", got)
	}
	if task := liveTask(t, server, 41); task.CheckHead.String != sha {
		t.Errorf("task = %+v", task)
	}
}

func TestAWaitingHeadWhoseCIPassesWhileTheBreakIsOpenMovesToApprovalWithNoBranchUpdate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	logStateOnFailure(t, server, fake)
	sha := checksHead(t, server, fake)
	hardBreak(t, server, fake)
	failed := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == sha })

	fake.SetCheckRunStatus(failed, "completed", "success")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	if got, calls := breakWaitHead(t, server), fake.UpdateBranchCalls(); got != "" || calls != 0 {
		t.Errorf("break_wait_head = %s, update calls = %d", got, calls)
	}
	if task := liveTask(t, server, 41); task.FixRounds != 0 || implementers(t, server) != 1 {
		t.Errorf("task = %+v, Implementers = %d", task, implementers(t, server))
	}
}

func TestTheTaskOfTheIssueOfAnOpenBreakGetsTheUsualFixRoundForAFailedCI(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	logStateOnFailure(t, server, fake)
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
	logStateOnFailure(t, server, fake)
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
