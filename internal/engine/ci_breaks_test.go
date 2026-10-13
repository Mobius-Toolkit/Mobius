package engine_test

import (
	"net/http"
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

func failedMainRun(workflow int64) testkit.WorkflowRun {
	return testkit.WorkflowRun{Name: "ci", WorkflowID: workflow, Branch: "main", Status: "completed", Conclusion: "failure"}
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

func TestARerunThatGitHubAnswersWithAServerErrorIsSentAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.RefuseRerun(http.StatusBadGateway)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startServer(t, fake, t.TempDir(), "")

	waitForPolls(t, fake)
	if breaks := ciBreaks(t, server); len(breaks) != 1 || breaks[0].rerun || breaks[0].result != "" {
		t.Errorf("breaks = %v", breaks)
	}
	fake.RefuseRerun(0)

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	if attempt := fake.WorkflowRunAttempt(run); attempt != 2 {
		t.Errorf("attempt = %d", attempt)
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
