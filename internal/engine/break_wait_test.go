package engine_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
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
	server, sha, run, _ := waitingTaskIn(t, fake)
	return server, sha, run
}

// waitingTaskIn is waitingTask with the data directory of the server.
func waitingTaskIn(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string, int64, string) {
	t.Helper()
	server, dataDir := connectTask(t, fake, leadStarts, fixes, longGrace)
	logStateOnFailure(t, server, fake)
	sha := checksHead(t, server, fake)
	run := hardBreak(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == sha })
	return server, sha, run, dataDir
}

// breakWaitEvents gives the texts of the Lead events of the kind break_wait, the oldest first.
func breakWaitEvents(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	rows, err := server.DB.Query("SELECT payload FROM lead_events WHERE kind = 'break_wait' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var texts []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, text)
	}
	return texts
}

func TestATaskThatStartsToWaitForABreakGivesItsLeadOneEventWithTheBreak(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, run := waitingTask(t, fake)

	events := testkit.WaitForValue(t, func() ([]string, bool) {
		texts := breakWaitEvents(t, server)
		return texts, len(texts) == 1
	})
	waitForPolls(t, fake)

	if got := breakWaitEvents(t, server); len(got) != 1 {
		t.Errorf("events = %q", got)
	}
	runURL := fmt.Sprintf("%s/%s/actions/runs/%d", fake.URL, shop, run)
	for _, want := range []string{"#41", "pull request #42", sha, "workflow ci", "run " + runURL, "started 20", " UTC, no task issue"} {
		if !strings.Contains(events[0], want) {
			t.Errorf("the event has no %q: %s", want, events[0])
		}
	}
	var issue int64
	if err := server.DB.QueryRow("SELECT issue FROM lead_events WHERE kind = 'break_wait'").Scan(&issue); err != nil || issue != 41 {
		t.Errorf("issue = %d, error = %v", issue, err)
	}
}

func TestARestartWithTheSameWaitingHeadGivesNoSecondBreakWaitEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _, dataDir := waitingTaskIn(t, fake)
	testkit.WaitFor(t, func() bool { return len(breakWaitEvents(t, server)) == 1 })

	server.Stop()
	restarted := testserver.Start(t, dataDir, fake.URL)
	waitForPolls(t, fake)

	if got := breakWaitHead(t, restarted); got != sha {
		t.Errorf("break_wait_head = %s", got)
	}
	if got := breakWaitEvents(t, restarted); len(got) != 1 {
		t.Errorf("events = %q", got)
	}
}

func TestANewFailedHeadOfATaskWhileTheBreakIsOpenGivesOneNewBreakWaitEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _ := waitingTask(t, fake)
	testkit.WaitFor(t, func() bool { return len(breakWaitEvents(t, server)) == 1 })

	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
	testkit.Git(t, work, "commit", "--allow-empty", "-m", "Change the plan")
	testkit.Git(t, work, "push", "origin", "HEAD:refs/heads/mobius/41")
	next := head(t, fake, "mobius/41")
	fake.AddCheckRun(shop, checkRun("build", next, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == next })
	events := testkit.WaitForValue(t, func() ([]string, bool) {
		texts := breakWaitEvents(t, server)
		return texts, len(texts) == 2
	})
	waitForPolls(t, fake)

	if got := breakWaitEvents(t, server); len(got) != 2 {
		t.Errorf("events = %q", got)
	}
	if !strings.Contains(events[0], sha) || !strings.Contains(events[1], next) || next == sha {
		t.Errorf("events = %q", events)
	}
}

func TestATaskOfAnotherRepositoryGivesItsBreakWaitEventToTheWorkstreamWithFullReferences(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addGardenTask(fake)
	fake.AddLabel(garden, 7, "mobius:working", testkit.AppSlug+"[bot]")
	fake.CommitFileOnBranch(garden, "mobius/7", "water.txt", "daily\n", "Water the plants")
	number := fake.OpenPullRequest(garden, "Water the plants", "mobius/7")
	sha := testkit.Git(t, fake.Remote(garden), "rev-parse", "mobius/7")
	server := startServer(t, fake, t.TempDir(), fmt.Sprintf(`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, state_at, branch, pull_request)
		VALUES ('owner/garden', 'owner/shop', 7, 12, 'checks', '2026-10-04T10:00:00Z', strftime('%%Y-%%m-%%dT%%H:%%M:%%fZ', 'now'), 'mobius/7', %d)`, number))
	server.WaitForFirstPoll(t, shop)
	server.WaitForFirstPoll(t, garden)
	run := fake.AddWorkflowRun(garden, failedMainRun(7))
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	fake.SetWorkflowRunStatus(run, "completed", "failure")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})

	fake.AddCheckRun(garden, checkRun("build", sha, "completed", "failure"))

	events := testkit.WaitForValue(t, func() ([]string, bool) {
		texts := breakWaitEvents(t, server)
		return texts, len(texts) == 1
	})
	var repository string
	var workstream int64
	if err := server.DB.QueryRow("SELECT repository, workstream FROM lead_events WHERE kind = 'break_wait'").Scan(&repository, &workstream); err != nil || repository != shop || workstream != 12 {
		t.Errorf("repository = %s, workstream = %d, error = %v", repository, workstream, err)
	}
	for _, want := range []string{"owner/garden#7", fmt.Sprintf("owner/garden#%d", number), "Open breaks of owner/garden", fmt.Sprintf("%s/owner/garden/actions/runs/%d", fake.URL, run)} {
		if !strings.Contains(events[0], want) {
			t.Errorf("the event has no %q: %s", want, events[0])
		}
	}
	if strings.Contains(strings.ReplaceAll(events[0], "owner/garden#", ""), "#7") {
		t.Errorf("the event names #7 with no repository: %s", events[0])
	}
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
		`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, run_event, rerun_at, result, started_at)
			VALUES ('owner/shop', 7, 'ci', 1, '', 1, 'push', '2026-10-04T10:00:00Z', 'hard', '2026-10-04T10:00:00Z')`,
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

func TestAFailedWorkflowDispatchRunStartsNoBreakAndTaskWithFailedCIGetsFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dispatchRun := failedMainRun(7)
	dispatchRun.Event = "workflow_dispatch"
	fake.AddWorkflowRun(shop, dispatchRun)

	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	logStateOnFailure(t, server, fake)
	sha := checksHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	if round := roundPrompt(t, server); !strings.Contains(round, "Check run \"build\"") {
		t.Errorf("round = %s", round)
	}
	if got := breakWaitHead(t, server); got != "" {
		t.Errorf("break_wait_head = %s, want empty", got)
	}
}

func TestAnOpenHardBreakEndsWhenWorkflowIsNotFoundAndTaskLeavesWait(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _, _ := waitingTask(t, fake)

	fake.SetWorkflowNotFound(shop, 7)

	testkit.WaitFor(t, func() bool {
		open, err := store.New(server.DB).ListOpenCIBreaks(t.Context(), shop)
		return err == nil && len(open) == 0
	})
	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == "" })
	if round := roundPrompt(t, server); !strings.Contains(round, "Check run \"build\"") {
		t.Errorf("round = %s", round)
	}
}

func TestACurrentBreakHoldsTaskAndReadsNoWorkflowWhileNewerRunIsInPage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, _ := waitingTask(t, fake)

	waitForWorkflowRead(t, fake, 7)
	fake.AddWorkflowRun(shop, failedMainRun(7))
	waitForPolls(t, fake)
	reads := workflowReads(fake, 7)

	waitForPolls(t, fake)
	wantWaiting(t, server, fake)
	if got := breakWaitHead(t, server); got != sha {
		t.Errorf("break_wait_head = %s, want %s", got, sha)
	}
	if got := workflowReads(fake, 7); got != reads {
		t.Errorf("workflow reads = %d, want %d", got, reads)
	}
}

func waitForWorkflowRead(t *testing.T, fake *testkit.FakeGitHub, workflowID int64) {
	t.Helper()
	path := fmt.Sprintf("/repos/owner/shop/actions/workflows/%d", workflowID)
	testkit.WaitFor(t, func() bool {
		for _, request := range fake.Requests() {
			if request.Method == http.MethodGet && request.Path == path {
				return true
			}
		}
		return false
	})
}

func workflowReads(fake *testkit.FakeGitHub, workflowID int64) int {
	path := fmt.Sprintf("/repos/owner/shop/actions/workflows/%d", workflowID)
	n := 0
	for _, request := range fake.Requests() {
		if request.Method == http.MethodGet && request.Path == path {
			n++
		}
	}
	return n
}

func TestATaskThatWaitsForABreakOfAnOldWorkflowDispatchRunOutsideThePageUpdatesItsBranchAtTheEnd(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha, run := waitingTask(t, fake)
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.SetBehind(shop, 42)
	pushRunOutOfThePage(fake)
	fake.SetWorkflowRunEvent(run, "workflow_dispatch")
	wantWaiting(t, server, fake)

	forgetRunEvents(t, server)

	testkit.WaitFor(t, func() bool { return breakWaitHead(t, server) == "" })
	merged := head(t, fake, "mobius/41")
	isAncestor(t, fake, sha, merged)
	isAncestor(t, fake, "main", merged)
	if merged == sha {
		t.Errorf("head = %s", merged)
	}
	if calls := fake.UpdateBranchCalls(); calls != 1 {
		t.Errorf("update calls = %d", calls)
	}
	if reads := runReads(fake, run); reads != 1 {
		t.Errorf("run reads = %d", reads)
	}
}
