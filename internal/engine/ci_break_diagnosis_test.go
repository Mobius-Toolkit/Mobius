package engine_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// hardMainRun adds a failed run of the workflow 7 on main at the commit sha. Its second attempt is the failed one, so the
// break is hard at once.
func hardMainRun(fake *testkit.FakeGitHub, sha string) {
	failed := failedMainRun(7)
	failed.HeadSHA = sha
	failed.Attempt = 2
	run := fake.AddWorkflowRun(shop, failed)
	fake.AddWorkflowJob(shop, run, 2, testkit.WorkflowJob{ID: 901, Name: "test", Conclusion: "failure", Steps: []testkit.WorkflowStep{
		{Name: "Set up", Conclusion: "success"},
		{Name: "Run go test", Conclusion: "failure"},
	}})
	fake.AddJobLog(901, "ok  internal/store\n--- FAIL: TestPlan\nFAIL\n")
}

// flakyMainRun adds a failed run of the workflow 7 on main at the commit sha, and passes its re-run. The break is flaky.
func flakyMainRun(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, sha string) {
	t.Helper()
	failed := failedMainRun(7)
	failed.HeadSHA = sha
	run := fake.AddWorkflowRun(shop, failed)
	fake.AddWorkflowJob(shop, run, 1, testkit.WorkflowJob{ID: 901, Name: "test", Conclusion: "failure", Steps: []testkit.WorkflowStep{
		{Name: "Set up", Conclusion: "success"},
		{Name: "Run go test", Conclusion: "failure"},
	}})
	fake.AddJobLog(901, "ok  internal/store\n--- FAIL: TestPlan\nFAIL\n")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	fake.SetWorkflowRunStatus(run, "completed", "success")
}

func passedMainRun(fake *testkit.FakeGitHub) int64 {
	passed := failedMainRun(7)
	passed.Conclusion = "success"
	return fake.AddWorkflowRun(shop, passed)
}

// waitForSharedIssue waits for the issue that the break and the record share, and checks that there is one issue.
func waitForSharedIssue(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, places int) int64 {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, "SELECT count(*) FROM ci_failure_places") == places && rowCount(t, server, "SELECT count(*) FROM ci_diagnoses WHERE ended_at = ''") == 0
	})
	waitForPolls(t, fake)
	numbers := fake.SubIssueNumbers(shop, maintenance)
	if len(numbers) != 1 {
		t.Fatalf("sub-issues = %v", numbers)
	}
	number := numbers[0]
	if n := rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_breaks WHERE issue = %d", number)); n != 1 {
		t.Errorf("breaks with the issue = %d", n)
	}
	if n := rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)); n != 1 {
		t.Errorf("records with the issue = %d", n)
	}
	return number
}

func TestTheSameTestOnTheDefaultBranchAndThenOnAPullRequestGivesOneIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStarts, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	sha := approvalHead(t, server, fake)
	flakyMainRun(t, server, fake, head(t, fake, "main"))
	number := ciBreakIssue(t, fake)
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})

	failActions(fake, sha)

	if got := waitForSharedIssue(t, server, fake, 2); got != number {
		t.Errorf("issue = %d, want %d", got, number)
	}
}

func TestTheSameTestOnAPullRequestAndThenOnTheDefaultBranchGivesOneIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStarts, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	sha := approvalHead(t, server, fake)
	failActions(fake, sha)
	testkit.WaitFor(t, func() bool { return rowCount(t, server, "SELECT count(*) FROM ci_failure_places") == 1 })

	hardMainRun(fake, head(t, fake, "main"))

	waitForSharedIssue(t, server, fake, 2)
}

func TestABreakTakesTheIssueOfARecordThatTwoPullRequestsMade(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)
	failActions(fake, first)
	failActions(fake, second)
	number := ciBreakIssue(t, fake)
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})

	hardMainRun(fake, head(t, fake, "main"))

	if got := waitForSharedIssue(t, server, fake, 3); got != number {
		t.Errorf("issue = %d, want %d", got, number)
	}
}

func TestABreakCreatesItsOwnIssueWhenTheIssueOfTheRecordIsClosed(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)
	failActions(fake, first)
	failActions(fake, second)
	recordIssue := ciBreakIssue(t, fake)
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", recordIssue)) == 1
	})
	fake.CloseIssue(shop, recordIssue)

	hardMainRun(fake, head(t, fake, "main"))

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_breaks WHERE issue > 0 AND issue != %d", recordIssue)) == 1
	})
	waitForPolls(t, fake)
	if n := len(openHardBreaks(t, server)); n != 1 {
		t.Errorf("open hard breaks = %d", n)
	}
	if n := rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_breaks WHERE issue = %d", recordIssue)); n != 0 {
		t.Errorf("breaks with the closed issue = %d", n)
	}
}

func TestAPassedRunEndsTheBreakButKeepsTheIssueOpenThatAPullRequestRecordShares(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStarts, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	sha := approvalHead(t, server, fake)
	failActions(fake, sha)
	testkit.WaitFor(t, func() bool { return rowCount(t, server, "SELECT count(*) FROM ci_failure_places") == 1 })
	hardMainRun(fake, head(t, fake, "main"))
	number := waitForSharedIssue(t, server, fake, 2)
	if n := len(openHardBreaks(t, server)); n != 1 {
		t.Fatalf("open hard breaks = %d", n)
	}

	newer := passedMainRun(fake)

	testkit.WaitFor(t, func() bool {
		breaks := ciBreaks(t, server)
		return len(breaks) == 1 && breaks[0].endedRunID == newer
	})
	waitForPolls(t, fake)
	if n := len(openHardBreaks(t, server)); n != 0 {
		t.Errorf("open hard breaks = %d", n)
	}
	if state, _ := fake.State(shop, number); state != "open" {
		t.Errorf("state = %q", state)
	}
	for _, comment := range fake.Comments(shop, number) {
		if strings.Contains(comment.Body, "Mobius closes this issue") {
			t.Errorf("comment = %q", comment.Body)
		}
	}
}

func TestAPassedRunClosesTheIssueOfABreakWhoseRecordHasNoPullRequestPlace(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStarts, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	approvalHead(t, server, fake)
	hardMainRun(fake, head(t, fake, "main"))
	number := waitForSharedIssue(t, server, fake, 1)

	newer := passedMainRun(fake)

	testkit.WaitFor(t, func() bool {
		breaks := ciBreaks(t, server)
		return len(breaks) == 1 && breaks[0].endedRunID == newer
	})
	if state, reason := fake.State(shop, number); state != "closed" || reason != "completed" {
		t.Errorf("state = %q, reason = %q", state, reason)
	}
}

func TestTheStopHoldsTheDispatchWhileTheDiagnoserReadsAHardBreak(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	addMaintenanceWorkstream(fake, maintenance)
	server := connectDiagnoser(t, fake, leadStarts, "[[prompts]]\nwhen = \"You are a Diagnoser\"\nreply = [\"Waiting.\"]\nhang = true\n")
	server.WaitForFirstPoll(t, shop)
	hardMainRun(fake, head(t, fake, "main"))
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, "SELECT count(*) FROM ci_diagnoses WHERE ci_break != 0 AND ended_at = ''") == 1
	})
	dispatchTask(fake, 45, "Add plan model")

	waitForPolls(t, fake)

	if hasLiveTask(t, server, 45) {
		t.Error("a task started")
	}
	if labels := fake.Labels(shop, 45); !slices.Contains(labels, "mobius:ready") {
		t.Errorf("labels = %v", labels)
	}
}
