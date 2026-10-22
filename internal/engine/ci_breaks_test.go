package engine_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type ciBreak struct {
	runID, endedRunID int64
	rerun             bool
	result            string
}

func ciBreaks(t *testing.T, server *testserver.Server) []ciBreak {
	t.Helper()
	rows, err := server.DB.Query("SELECT run_id, ended_run_id, rerun_at != '', result FROM ci_breaks ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var breaks []ciBreak
	for rows.Next() {
		var b ciBreak
		if err := rows.Scan(&b.runID, &b.endedRunID, &b.rerun, &b.result); err != nil {
			t.Fatal(err)
		}
		breaks = append(breaks, b)
	}
	return breaks
}

func waitForCIBreak(t *testing.T, server *testserver.Server, want ciBreak) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		breaks := ciBreaks(t, server)
		return len(breaks) == 1 && breaks[0] == want
	})
}

func waitForRefusedRerun(t *testing.T, fake *testkit.FakeGitHub, status int) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		for _, request := range fake.Requests() {
			if request.Method == http.MethodPost && strings.HasSuffix(request.Path, "/rerun-failed-jobs") && request.Status == status {
				return true
			}
		}
		return false
	})
}

func failedMainRun(workflow int64) testkit.WorkflowRun {
	return testkit.WorkflowRun{Name: "ci", WorkflowID: workflow, Branch: "main", Status: "completed", Conclusion: "failure"}
}

const (
	timeLimitMessage = "The job has exceeded the maximum execution time of 30m0s"
	priorityCancel   = "Canceling since a higher priority waiting request for release exists"
)

// addCancelledJob adds the job "check / e2e" to the attempt of the run. The job ends cancelled in the step "Install
// browsers" and has the annotations messages. The job has the id of its check run.
func addCancelledJob(fake *testkit.FakeGitHub, run int64, attempt int, messages ...string) {
	job := fake.AddCheckRun(shop, testkit.CheckRun{Name: "check / e2e", Status: "completed", Conclusion: "cancelled"})
	fake.AddWorkflowJob(shop, run, attempt, testkit.WorkflowJob{ID: job, Name: "check / e2e", Conclusion: "cancelled", Steps: []testkit.WorkflowStep{
		{Name: "Set up job", Conclusion: "success"},
		{Name: "Install browsers", Conclusion: "cancelled"},
	}})
	for _, message := range messages {
		fake.AddAnnotation(job, ".github", 1, message)
	}
	fake.AddJobLog(job, "Installing browsers\n")
}

// cancelledMainRun adds a cancelled run of the workflow on main at the commit sha with the cancelled job of
// addCancelledJob.
func cancelledMainRun(fake *testkit.FakeGitHub, workflow int64, sha string, messages ...string) int64 {
	cancelled := failedMainRun(workflow)
	cancelled.Conclusion = "cancelled"
	cancelled.HeadSHA = sha
	run := fake.AddWorkflowRun(shop, cancelled)
	addCancelledJob(fake, run, 1, messages...)
	return run
}

// timedOutMainRun adds a cancelled run of the workflow 7 at the commit sha whose job passed its time limit.
func timedOutMainRun(fake *testkit.FakeGitHub, sha string) int64 {
	return cancelledMainRun(fake, 7, sha, timeLimitMessage, "The operation was canceled.")
}

// timeOutAgain makes the re-run attempt of the run end cancelled because a job passed its time limit.
func timeOutAgain(fake *testkit.FakeGitHub, run int64) {
	addCancelledJob(fake, run, fake.WorkflowRunAttempt(run), timeLimitMessage)
	fake.SetWorkflowRunStatus(run, "completed", "cancelled")
}

func annotationReads(fake *testkit.FakeGitHub) int {
	n := 0
	for _, request := range fake.Requests() {
		if request.Method == http.MethodGet && strings.HasSuffix(request.Path, "/annotations") {
			n++
		}
	}
	return n
}

func openHardBreaks(t *testing.T, server *testserver.Server) []store.CiBreak {
	t.Helper()
	breaks, err := store.New(server.DB).ListOpenHardCIBreaks(t.Context(), shop)
	if err != nil {
		t.Fatal(err)
	}
	return breaks
}

func TestAFailedRunOnTheDefaultBranchGetsOneRerunAlsoAfterMorePolls(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	waitForPolls(t, fake)

	if attempt := fake.WorkflowRunAttempt(run); attempt != 2 {
		t.Errorf("attempt = %d", attempt)
	}
	var name, url string
	if err := server.DB.QueryRow("SELECT workflow_name, run_url FROM ci_breaks").Scan(&name, &url); err != nil {
		t.Fatal(err)
	}
	if name != "ci" || url != fake.URL+"/owner/shop/actions/runs/1" {
		t.Errorf("name = %q, url = %q", name, url)
	}
}

func TestARestartAfterTheRerunCallBeforeItsMarkDoesNotRunTheJobsAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	rerun := failedMainRun(7)
	rerun.Attempt = 2
	rerun.Status, rerun.Conclusion = "in_progress", ""
	run := fake.AddWorkflowRun(shop, rerun)
	dataDir := t.TempDir()
	seed(t, dataDir, `INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, started_at)
		VALUES ('owner/shop', 7, 'ci', 1, '', 1, '2026-10-04T10:00:00Z')`)
	server := startServer(t, fake, dataDir, "")

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	waitForPolls(t, fake)

	if attempt := fake.WorkflowRunAttempt(run); attempt != 2 {
		t.Errorf("attempt = %d", attempt)
	}
}

func TestARestartKeepsTheBreakOfARunThatMobiusRanAgainAndDoesNotRunItAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	dataDir := t.TempDir()
	seed(t, dataDir, `INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, rerun_at, started_at)
		VALUES ('owner/shop', 7, 'ci', 1, '', 1, '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z')`)
	startServer(t, fake, dataDir, "")

	waitForPolls(t, fake)

	if attempt := fake.WorkflowRunAttempt(run); attempt != 1 {
		t.Errorf("attempt = %d", attempt)
	}
}

func TestARefusedRerunDoesNotStopThePollAndGivesAHardBreak(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.RefuseRerun(http.StatusForbidden)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
	waitForPolls(t, fake)

	if attempt := fake.WorkflowRunAttempt(run); attempt != 1 {
		t.Errorf("attempt = %d", attempt)
	}
	if breaks := openHardBreaks(t, server); len(breaks) != 1 {
		t.Errorf("hard breaks = %d", len(breaks))
	}
}

func TestAStartupFailureRunStartsABreakAndTheRefusedRerunGivesAHardBreak(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.RefuseRerun(http.StatusUnprocessableEntity)
	failed := failedMainRun(7)
	failed.Conclusion = "startup_failure"
	run := fake.AddWorkflowRun(shop, failed)
	server := startServer(t, fake, t.TempDir(), "")

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
	if breaks := openHardBreaks(t, server); len(breaks) != 1 {
		t.Errorf("hard breaks = %d", len(breaks))
	}
}

func TestALaterAttemptOfTheFailedRunThatPassesEndsAHardBreakAlsoWhenGitHubRefusedTheRerun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.RefuseRerun(http.StatusForbidden)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})

	fake.RerunWorkflowRun(run, "success")

	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: run, rerun: true, result: "hard"})
	if breaks := openHardBreaks(t, server); len(breaks) != 0 {
		t.Errorf("open hard breaks = %+v", breaks)
	}
}

func TestARefusedListOfRunsDoesNotStopTheDispatch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.RefuseWorkflowRuns(http.StatusForbidden)
	server, _ := connect(t, fake, seen)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	liveTaskOf(t, server, 41)
}

func TestARerunThatGitHubAnswersWithAServerErrorIsSentAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.RefuseRerun(http.StatusBadGateway)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")

	waitForRefusedRerun(t, fake, http.StatusBadGateway)
	if breaks := ciBreaks(t, server); len(breaks) != 1 || breaks[0].rerun || breaks[0].result != "" {
		t.Errorf("breaks = %v", breaks)
	}
	fake.RefuseRerun(0)

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	if attempt := fake.WorkflowRunAttempt(run); attempt != 2 {
		t.Errorf("attempt = %d", attempt)
	}
}

func TestANewerPassedRunEndsTheBreakBeforeTheRerunIsSent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.RefuseRerun(http.StatusBadGateway)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")
	waitForRefusedRerun(t, fake, http.StatusBadGateway)
	if breaks := ciBreaks(t, server); len(breaks) != 1 || breaks[0].rerun {
		t.Fatalf("breaks = %v", breaks)
	}

	passed := failedMainRun(7)
	passed.Conclusion = "success"
	newer := fake.AddWorkflowRun(shop, passed)
	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: newer})
	fake.RefuseRerun(0)
	waitForPolls(t, fake)

	if attempt := fake.WorkflowRunAttempt(run); attempt != 1 {
		t.Errorf("attempt = %d", attempt)
	}
	if breaks := ciBreaks(t, server); len(breaks) != 1 || breaks[0] != (ciBreak{runID: run, endedRunID: newer}) {
		t.Errorf("breaks = %v", breaks)
	}
}

func TestARerunThatPassesGivesAFlakyBreakThatIsEnded(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "success")

	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: run, rerun: true, result: "flaky"})
	waitForPolls(t, fake)
	if breaks := openHardBreaks(t, server); len(breaks) != 0 {
		t.Errorf("open hard breaks = %+v", breaks)
	}
	if len(ciBreaks(t, server)) != 1 || fake.WorkflowRunAttempt(run) != 2 {
		t.Errorf("breaks = %+v, attempt = %d", ciBreaks(t, server), fake.WorkflowRunAttempt(run))
	}
}

func TestARerunThatFailsGivesAnOpenHardBreakAndANewerPassedRunEndsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "failure")

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
	if breaks := openHardBreaks(t, server); len(breaks) != 1 || breaks[0].RunID != run || breaks[0].WorkflowName != "ci" {
		t.Errorf("open hard breaks = %+v", breaks)
	}

	fake.AddWorkflowRun(shop, failedMainRun(7))
	waitForPolls(t, fake)
	if breaks := ciBreaks(t, server); len(breaks) != 1 || breaks[0].endedRunID != 0 {
		t.Errorf("breaks = %+v", breaks)
	}

	passed := failedMainRun(7)
	passed.Conclusion = "success"
	newer := fake.AddWorkflowRun(shop, passed)
	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: newer, rerun: true, result: "hard"})
	if breaks := openHardBreaks(t, server); len(breaks) != 0 {
		t.Errorf("open hard breaks = %+v", breaks)
	}
}

func TestARerunThatIsCancelledGivesNoResultAndNoIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "cancelled")

	waitForPolls(t, fake)
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	if breaks := openHardBreaks(t, server); len(breaks) != 0 {
		t.Errorf("open hard breaks = %+v", breaks)
	}
	if created := createdIssues(fake); created != 0 {
		t.Errorf("created issues = %d", created)
	}
}

func TestARerunThatIsCancelledAndANewerPassedRunEndTheBreakWithNoResult(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "cancelled")
	waitForPolls(t, fake)
	passed := failedMainRun(7)
	passed.Conclusion = "success"
	newer := fake.AddWorkflowRun(shop, passed)

	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: newer, rerun: true})
	if created := createdIssues(fake); created != 0 {
		t.Errorf("created issues = %d", created)
	}
}

func TestARerunThatIsCancelledAndANewerFailedRunGiveAnOpenHardBreak(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "cancelled")
	fake.AddWorkflowRun(shop, failedMainRun(7))

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
	if breaks := openHardBreaks(t, server); len(breaks) != 1 || breaks[0].RunID != run {
		t.Errorf("open hard breaks = %+v", breaks)
	}
}

func TestAFailedRunThatAHumanRanAgainGivesAHardBreakWithNoNewRerun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	failed := failedMainRun(7)
	failed.Attempt = 2
	run := fake.AddWorkflowRun(shop, failed)
	server := startServer(t, fake, t.TempDir(), "")

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
	waitForPolls(t, fake)

	if attempt := fake.WorkflowRunAttempt(run); attempt != 2 {
		t.Errorf("attempt = %d", attempt)
	}
}

func TestATimedOutRunOnTheDefaultBranchStartsABreakAndGetsOneRerun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := timedOutMainRun(fake, "")
	server := startServer(t, fake, t.TempDir(), "")

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	waitForPolls(t, fake)

	if attempt := fake.WorkflowRunAttempt(run); attempt != 2 {
		t.Errorf("attempt = %d", attempt)
	}
	if breaks := ciBreaks(t, server); len(breaks) != 1 {
		t.Errorf("breaks = %+v", breaks)
	}
}

func TestARerunThatTimesOutAgainGivesAnOpenHardBreak(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := timedOutMainRun(fake, "")
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	timeOutAgain(fake, run)

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
	if breaks := openHardBreaks(t, server); len(breaks) != 1 || breaks[0].RunID != run {
		t.Errorf("open hard breaks = %+v", breaks)
	}
}

func TestARerunThatIsCancelledWithoutATimeLimitStillGivesNoResult(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := timedOutMainRun(fake, "")
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	addCancelledJob(fake, run, fake.WorkflowRunAttempt(run), priorityCancel)
	reads := annotationReads(fake)
	fake.SetWorkflowRunStatus(run, "completed", "cancelled")

	testkit.WaitFor(t, func() bool { return annotationReads(fake) > reads })
	waitForPolls(t, fake)
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
}

func TestATimedOutRunNewerThanTheBreakGivesAnOpenHardBreak(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "cancelled")
	timedOutMainRun(fake, "")

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
}

func TestACancelledRunWithNoAnnotationOrOnlyThePriorityCancelStartsNoBreakAndGetsNoRerun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	runs := []int64{cancelledMainRun(fake, 7, ""), cancelledMainRun(fake, 8, "", priorityCancel, "The operation was canceled.")}
	server := startServer(t, fake, t.TempDir(), "")

	testkit.WaitFor(t, func() bool { return annotationReads(fake) >= 2 })
	waitForPolls(t, fake)

	for _, run := range runs {
		if attempt := fake.WorkflowRunAttempt(run); attempt != 1 {
			t.Errorf("attempt of run %d = %d", run, attempt)
		}
	}
	if breaks := ciBreaks(t, server); len(breaks) != 0 {
		t.Errorf("breaks = %+v", breaks)
	}
}

func TestACancelledRunAFailedRunOfAForkOrAnotherBranchAndARunInProgressChangeNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	passed := failedMainRun(7)
	passed.Conclusion = "success"
	cancelled := failedMainRun(7)
	cancelled.Conclusion = "cancelled"
	skipped := failedMainRun(8)
	skipped.Conclusion = "skipped"
	fork := failedMainRun(9)
	fork.HeadRepository = "stranger/shop"
	feature := failedMainRun(10)
	feature.Branch = "feature"
	running := failedMainRun(11)
	running.Status, running.Conclusion = "in_progress", ""
	var runs []int64
	for _, run := range []testkit.WorkflowRun{passed, cancelled, skipped, fork, feature, running} {
		runs = append(runs, fake.AddWorkflowRun(shop, run))
	}
	server := startServer(t, fake, t.TempDir(), "")

	waitForPolls(t, fake)

	for _, run := range runs {
		if attempt := fake.WorkflowRunAttempt(run); attempt != 1 {
			t.Errorf("attempt of run %d = %d", run, attempt)
		}
	}
	if breaks := ciBreaks(t, server); len(breaks) != 0 {
		t.Errorf("breaks = %+v", breaks)
	}
}
