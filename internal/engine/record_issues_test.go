package engine_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// acceptedIssueCreates counts the creates of an issue that GitHub answered with a success status.
func acceptedIssueCreates(fake *testkit.FakeGitHub) int {
	count := 0
	for _, request := range fake.Requests() {
		if request.Method == http.MethodPost && strings.HasSuffix(request.Path, "/issues") && request.Status < http.StatusMultipleChoices {
			count++
		}
	}
	return count
}

// requireRowThenIssue checks the issue that the poll makes for a record of table with the title. GitHub refuses each
// create, so the poll adds one row of issues with no number, and the record keeps the id of the row. After GitHub
// accepts the create, the fake GitHub has one issue below the maintenance Workstream, the row and the record have its
// number, and no second row exists.
func requireRowThenIssue(t *testing.T, fake *testkit.FakeGitHub, server *testserver.Server, table, title string) {
	t.Helper()
	rowOfRecord := fmt.Sprintf("SELECT count(*) FROM issues JOIN %s r ON r.issue_row = issues.id WHERE issues.title = ? AND issues.number IS NULL AND r.issue = 0", table)
	testkit.WaitFor(t, func() bool { return countRows(t, server, rowOfRecord, title) == 1 })
	waitForPolls(t, fake)
	if rows := countRows(t, server, "SELECT count(*) FROM issues WHERE title = ?", title); rows != 1 {
		t.Errorf("rows = %d", rows)
	}
	if created := acceptedIssueCreates(fake); created != 0 {
		t.Errorf("created issues = %d", created)
	}
	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 0 {
		t.Errorf("sub-issues = %v", numbers)
	}

	fake.FailIssueCreates(0)

	number := ciBreakIssue(t, fake)
	withNumber := fmt.Sprintf("SELECT count(*) FROM issues JOIN %s r ON r.issue_row = issues.id WHERE issues.title = ? AND issues.number = ? AND r.issue = ?", table)
	testkit.WaitFor(t, func() bool { return countRows(t, server, withNumber, title, number, number) == 1 })
	waitForPolls(t, fake)
	if rows := countRows(t, server, "SELECT count(*) FROM issues WHERE title = ?", title); rows != 1 {
		t.Errorf("rows = %d", rows)
	}
	if created := acceptedIssueCreates(fake); created != 1 {
		t.Errorf("created issues = %d", created)
	}
	if numbers := fake.SubIssueNumbers(shop, maintenance); !slices.Equal(numbers, []int64{number}) {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestTheIssueOfAHardBreakIsARowWithNoNumberUntilTheSendCreatesTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	fake.FailIssueCreates(1000)
	server := startServerWithHarness(t, fake, t.TempDir())
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "failure")

	requireRowThenIssue(t, fake, server, "ci_breaks", `Fix the failed workflow "ci" on main`)
}

func TestTheIssueOfACIFailureIsARowWithNoNumberUntilTheSendCreatesTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.FailIssueCreates(1000)

	twoPlacesOfTheSameFailure(t, fake, server)

	requireRowThenIssue(t, fake, server, "ci_failures", "Fix the CI failure of TestPlan")
}

func TestTheIssueOfACheckTimeoutIsARowWithNoNumberUntilTheSendCreatesTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	fake.FailIssueCreates(1000)
	server, _ := connectTask(t, fake, leadStarts, commits, timeoutConfig(1))
	fake.SetCheck(shop, hangingCheck)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	requireRowThenIssue(t, fake, server, "check_timeouts", "Examine the time of the local check of owner/shop")
}

func TestTheIssueOfAnInitFailureIsARowWithNoNumberUntilTheSendCreatesTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	fake.FailIssueCreates(1000)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.SetInit(shop, failingInit)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	requireRowThenIssue(t, fake, server, "init_failures", "Fix the setup script .mobius/init of owner/shop")
}

func TestACreateThatGitHubDidAndAnsweredWith500GivesNoSecondIssueAndNoSecondRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	fake.LoseIssueCreateAnswers(1)
	server := startServerWithHarness(t, fake, t.TempDir())
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "failure")

	number := ciBreakIssue(t, fake)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM ci_breaks WHERE issue = ?", number) == 1
	})
	waitForPolls(t, fake)
	if created := createdIssues(fake); created != 1 {
		t.Errorf("created issues = %d", created)
	}
	if rows := countRows(t, server, "SELECT count(*) FROM issues WHERE number = ? AND id = (SELECT issue_row FROM ci_breaks)", number); rows != 1 {
		t.Errorf("rows of the issue = %d", rows)
	}
	if rows := countRows(t, server, "SELECT count(*) FROM issues WHERE title = ?", `Fix the failed workflow "ci" on main`); rows != 1 {
		t.Errorf("rows = %d", rows)
	}
	if numbers := fake.SubIssueNumbers(shop, maintenance); !slices.Equal(numbers, []int64{number}) {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestARefusedLinkOfTheIssueOfAHardBreakLeavesTheCreatedIssueAndMakesNoSecondIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	fake.RefuseAddSubIssues(shop, maintenance, true)
	server := startServerWithHarness(t, fake, t.TempDir())
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "failure")

	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM ci_breaks WHERE issue != 0") == 1 &&
			countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field = 'parent' AND status = 'refused'") == 1
	})
	waitForPolls(t, fake)
	if created := acceptedIssueCreates(fake); created != 1 {
		t.Errorf("created issues = %d", created)
	}
	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 0 {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestARefusedCreateOfTheIssueOfAnInitFailureClearsTheRowAndTheNextPollCreatesTheIssueAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	fake.RefuseIssueCreates(422)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.SetInit(shop, failingInit)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field = 'create' AND status = 'refused'") >= 2
	})
	if rows := countRows(t, server, "SELECT count(*) FROM init_failures WHERE ended_at = '' AND issue = 0"); rows != 1 {
		t.Errorf("open records with no issue = %d", rows)
	}
	if events := countRows(t, server, "SELECT count(*) FROM lead_events WHERE kind = 'issue_create_refused'"); events != 0 {
		t.Errorf("Lead events = %d", events)
	}

	fake.RefuseIssueCreates(0)

	number := ciBreakIssue(t, fake)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM init_failures WHERE issue = ?", number) == 1
	})
}
