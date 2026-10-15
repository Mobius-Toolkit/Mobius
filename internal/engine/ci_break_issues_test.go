package engine_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const maintenance = 20

// failedRunWithJob adds a failed run of the workflow 7 with the failed job "test" and its log.
func failedRunWithJob(fake *testkit.FakeGitHub) int64 {
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	fake.AddWorkflowJob(shop, run, 1, testkit.WorkflowJob{ID: 900, Name: "lint", Conclusion: "success", Steps: []testkit.WorkflowStep{{Name: "Run lint", Conclusion: "success"}}})
	fake.AddWorkflowJob(shop, run, 1, testkit.WorkflowJob{ID: 901, Name: "test", Conclusion: "failure", Steps: []testkit.WorkflowStep{
		{Name: "Set up", Conclusion: "success"},
		{Name: "Run go test", Conclusion: "failure"},
	}})
	fake.AddJobLog(901, "ok  internal/store\n--- FAIL: TestPlan\nFAIL\n")
	return run
}

func ciBreakIssue(t *testing.T, fake *testkit.FakeGitHub) int64 {
	t.Helper()
	return testkit.WaitForValue(t, func() (int64, bool) {
		numbers := fake.SubIssueNumbers(shop, maintenance)
		if len(numbers) == 0 {
			return 0, false
		}
		return numbers[0], true
	})
}

func TestAHardBreakGivesOneIssueBelowTheMaintenanceWorkstreamWithTheFailureAndNoReadyLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "failure")

	number := ciBreakIssue(t, fake)
	title, body := fake.Issue(shop, number)
	if title != `Fix the failed workflow "ci" on main` {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{
		`The workflow "ci" failed on main.`,
		"Run: " + fake.URL + "/owner/shop/actions/runs/1",
		"Attempt: 1",
		"Result of the re-run: hard.",
		"Failed job: test",
		"Failed step: Run go test",
		"--- FAIL: TestPlan",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not have %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "lint") {
		t.Errorf("body has the passed job:\n%s", body)
	}
	if labels := fake.Labels(shop, number); len(labels) != 0 {
		t.Errorf("labels = %v", labels)
	}
	waitForPolls(t, fake)
	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 1 {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestAFlakyBreakGivesAnIssueWithReadyThatTheUsualFlowDispatchesWithAutopilotOff(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	server, _ := connect(t, fake, "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "success")

	number := ciBreakIssue(t, fake)
	_, body := fake.Issue(shop, number)
	for _, want := range []string{"Result of the re-run: flaky.", "Failed job: test", "Failed step: Run go test", "--- FAIL: TestPlan"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not have %q:\n%s", want, body)
		}
	}
	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("workstream = %d", task.Workstream)
	}
	waitForPolls(t, fake)
	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 1 {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestABreakThatAlreadyHasAnIssueGetsNoSecondIssueAfterARestart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddWorkflowRun(shop, failedMainRun(7))
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddIssue(shop, 30, "Fix the failed workflow")
	dataDir := t.TempDir()
	seed(t, dataDir, `INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, rerun_at, result, issue, started_at)
		VALUES ('owner/shop', 7, 'ci', 1, '', 1, '2026-10-04T10:00:00Z', 'hard', 30, '2026-10-04T10:00:00Z')`)
	startServer(t, fake, dataDir, "")

	waitForPolls(t, fake)

	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 0 {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestAPassedRunClosesTheOpenIssueOfAHardBreakWithACommentAndTheRunLink(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	fake.SetWorkflowRunStatus(run, "completed", "failure")
	number := ciBreakIssue(t, fake)

	passed := failedMainRun(7)
	passed.Conclusion = "success"
	newer := fake.AddWorkflowRun(shop, passed)

	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: newer, rerun: true, result: "hard"})
	if state, reason := fake.State(shop, number); state != "closed" || reason != "completed" {
		t.Errorf("state = %q, reason = %q", state, reason)
	}
	comments := fake.Comments(shop, number)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, fake.URL+"/owner/shop/actions/runs/2") {
		t.Errorf("comments = %v", comments)
	}
}

func TestAPassedRunDoesNotCommentOnTheIssueOfAHardBreakThatIsAlreadyClosed(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	fake.SetWorkflowRunStatus(run, "completed", "failure")
	number := ciBreakIssue(t, fake)
	fake.CloseIssueAs(shop, number, "not_planned")

	passed := failedMainRun(7)
	passed.Conclusion = "success"
	newer := fake.AddWorkflowRun(shop, passed)

	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: newer, rerun: true, result: "hard"})
	if comments := fake.Comments(shop, number); len(comments) != 0 {
		t.Errorf("comments = %v", comments)
	}
	if _, reason := fake.State(shop, number); reason != "not_planned" {
		t.Errorf("reason = %q", reason)
	}
}

func TestABreakWithoutAMaintenanceWorkstreamGetsAnIssueWhenTheWorkstreamAppears(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	fake.RefuseRerun(http.StatusForbidden)
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
	waitForPolls(t, fake)

	if created := createdIssues(fake); created != 0 {
		t.Fatalf("created issues = %d", created)
	}
	addMaintenanceWorkstream(fake, maintenance)

	ciBreakIssue(t, fake)
	waitForPolls(t, fake)
	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 1 {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestTwoFailedJobsWithLongLogsGiveOneIssueBelowTheLimitOfGitHub(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(shop, failedMainRun(7))
	for i, name := range []string{"check", "e2e"} {
		id := int64(910 + i)
		fake.AddWorkflowJob(shop, run, 1, testkit.WorkflowJob{ID: id, Name: name, Conclusion: "failure", Steps: []testkit.WorkflowStep{
			{Name: "Run " + name, Conclusion: "failure"},
		}})
		var log strings.Builder
		for line := 1; line <= 200; line++ {
			fmt.Fprintf(&log, "%s line %03d %s\n", name, line, strings.Repeat("x", 480))
		}
		fake.AddJobLog(id, log.String())
	}
	addMaintenanceWorkstream(fake, maintenance)
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "failure")

	number := ciBreakIssue(t, fake)
	_, body := fake.Issue(shop, number)
	if len(body) >= 65536 {
		t.Errorf("body length = %d", len(body))
	}
	for _, want := range []string{"Failed job: check", "Failed step: Run check", "check line 200 ", "Failed job: e2e", "Failed step: Run e2e", "e2e line 200 "} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not have %q", want)
		}
	}
}

func TestARefusedJobListCreatesNoIssueUntilTheListWorks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	fake.RefuseWorkflowJobs(http.StatusForbidden)
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	fake.SetWorkflowRunStatus(run, "completed", "failure")
	waitForPolls(t, fake)

	if created := createdIssues(fake); created != 0 {
		t.Fatalf("created issues = %d", created)
	}
	fake.RefuseWorkflowJobs(0)

	number := ciBreakIssue(t, fake)
	if _, body := fake.Issue(shop, number); !strings.Contains(body, "Failed step: Run go test") {
		t.Errorf("body = %q", body)
	}
}

func TestAFailedJobLogReadCreatesNoIssueUntilTheReadWorks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	fake.RefuseJobLogs(http.StatusInternalServerError)
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	fake.SetWorkflowRunStatus(run, "completed", "failure")
	waitForPolls(t, fake)

	if created := createdIssues(fake); created != 0 {
		t.Fatalf("created issues = %d", created)
	}
	fake.RefuseJobLogs(0)

	number := ciBreakIssue(t, fake)
	if _, body := fake.Issue(shop, number); !strings.Contains(body, "--- FAIL: TestPlan") {
		t.Errorf("body = %q", body)
	}
}

func TestAFailedAddBelowTheWorkstreamGivesOneIssueThatTheNextPollAddsBelowTheWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	fake.FailAddSubIssue(shop, maintenance, true)
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	fake.SetWorkflowRunStatus(run, "completed", "failure")
	testkit.WaitFor(t, func() bool { return createdIssues(fake) == 1 })
	waitForPolls(t, fake)

	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 0 {
		t.Fatalf("sub-issues = %v", numbers)
	}
	fake.FailAddSubIssue(shop, maintenance, false)

	ciBreakIssue(t, fake)
	waitForPolls(t, fake)
	if created := createdIssues(fake); created != 1 {
		t.Errorf("created issues = %d", created)
	}
}

func TestARefusedCloseKeepsTheBreakOpenUntilTheCloseWorks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	server := startServer(t, fake, t.TempDir(), "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	fake.SetWorkflowRunStatus(run, "completed", "failure")
	number := ciBreakIssue(t, fake)
	fake.RefuseCloses(http.StatusForbidden)

	passed := failedMainRun(7)
	passed.Conclusion = "success"
	newer := fake.AddWorkflowRun(shop, passed)
	waitForPolls(t, fake)

	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true, result: "hard"})
	if state, _ := fake.State(shop, number); state != "open" {
		t.Fatalf("state = %q", state)
	}
	fake.RefuseCloses(0)

	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: newer, rerun: true, result: "hard"})
	if state, reason := fake.State(shop, number); state != "closed" || reason != "completed" {
		t.Errorf("state = %q, reason = %q", state, reason)
	}
	if comments := fake.Comments(shop, number); len(comments) != 1 {
		t.Errorf("comments = %v", comments)
	}
}

func createdIssues(fake *testkit.FakeGitHub) int {
	count := 0
	for _, request := range fake.Requests() {
		if request.Method == http.MethodPost && strings.HasSuffix(request.Path, "/issues") {
			count++
		}
	}
	return count
}
