package engine_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// leadStartsWithFortyFour is a Lead that starts an Implementer for the dispatch of #41, #43 and #44.
const leadStartsWithFortyFour = "[[prompts]]\nwhen = \"dispatch of #44\"\ncall = { tool = \"start_implementer\", arguments = { repository = \"owner/shop\", n = 44, instructions = \"Round prices down.\" } }\n\n" + leadStartsTwo

// approvalHeadOf starts the task of the issue number and gives the head of its branch in the state approval.
func approvalHeadOf(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, number int) string {
	t.Helper()
	fake.AddLabel(shop, int64(number), "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, int64(number)) == "approval" })
	return head(t, fake, fmt.Sprintf("mobius/%d", number))
}

func TestTheSameTestInTwoPullRequestsGivesOneIssueBelowTheMaintenanceWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)

	firstJob := failActions(fake, first)
	secondJob := failActions(fake, second)

	number := ciBreakIssue(t, fake)
	title, body := fake.Issue(shop, number)
	if title != "Fix the CI failure of TestPlan" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{
		"Reason: The plan test expects cents.",
		"Test: TestPlan",
		"Location: plan_test.go:12",
		"Head: " + first,
		"Head: " + second,
		fmt.Sprintf("Failed job: https://github.com/owner/shop/runs/%d", firstJob),
		fmt.Sprintf("Failed job: https://github.com/owner/shop/runs/%d", secondJob),
		"\n````\n--- FAIL: TestPlan\n````\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not have %q:\n%s", want, body)
		}
	}
	if labels := fake.Labels(shop, number); slices.Contains(labels, "mobius:triage") {
		t.Errorf("labels = %v", labels)
	}
	if !fake.LabelAddedBy(shop, number, "mobius:ready", testkit.AppSlug+"[bot]") {
		t.Errorf("the App did not add mobius:ready to the issue")
	}
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})
}

// createdCIFailureIssue gives the number of the issue of the record that GitHub created but did not add below the
// Workstream.
func createdCIFailureIssue(t *testing.T, server *testserver.Server) int64 {
	t.Helper()
	var issue int64
	if err := server.DB.QueryRow("SELECT issue FROM ci_failures WHERE issue < 0").Scan(&issue); err != nil {
		t.Fatal(err)
	}
	return -issue
}

// twoPlacesOfTheSameFailure stops the tasks of #41 and #43 by the same CI failure, so that the two places give one record.
func twoPlacesOfTheSameFailure(t *testing.T, fake *testkit.FakeGitHub, server *testserver.Server) {
	t.Helper()
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)
	failActions(fake, first)
	failActions(fake, second)
}

func TestANewCIFailureIssueGetsReadyAndTheUsualFlowStartsItsTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)

	twoPlacesOfTheSameFailure(t, fake, server)

	number := ciBreakIssue(t, fake)
	if !fake.LabelAddedBy(shop, number, "mobius:ready", testkit.AppSlug+"[bot]") {
		t.Errorf("the App did not add mobius:ready to the issue")
	}
	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
}

func TestRepairsOffHoldsTheReadyIssueOfACIFailureAndRepairsOnStartsItsTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	turnRepairsOff(t, server)

	twoPlacesOfTheSameFailure(t, fake, server)

	number := ciBreakIssue(t, fake)
	waitForHeldWork(t, server, "repairs", number)
	waitForPolls(t, fake)
	if count := tasksOf(t, server, number); count != 0 {
		t.Errorf("tasks = %d", count)
	}
	if labels := fake.Labels(shop, number); !slices.Contains(labels, "mobius:ready") {
		t.Errorf("labels = %v", labels)
	}

	turnRepairsOn(t, server)

	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
}

func TestACIFailureIssueThatGitHubDidNotAddBelowTheWorkstreamGetsReadyAgainAndItsTaskStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.RefuseAddSubIssues(shop, maintenance, true)

	twoPlacesOfTheSameFailure(t, fake, server)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, "SELECT count(*) FROM ci_failures WHERE issue < 0") == 1
	})
	number := createdCIFailureIssue(t, server)
	waitForPolls(t, fake)
	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 0 {
		t.Errorf("sub-issues = %v", numbers)
	}
	if hasLiveTask(t, server, number) {
		t.Errorf("the issue has a task")
	}

	fake.RefuseAddSubIssues(shop, maintenance, false)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})
	if numbers := fake.SubIssueNumbers(shop, maintenance); !slices.Equal(numbers, []int64{number}) {
		t.Errorf("sub-issues = %v", numbers)
	}
	if !fake.LabelAddedBy(shop, number, "mobius:ready", testkit.AppSlug+"[bot]") {
		t.Errorf("the App did not add mobius:ready to the issue")
	}
	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
}

func TestAPendingCIFailureIssueWithNoReadyGetsBelowTheWorkstreamWithNoLabelAndNoTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.RefuseAddSubIssues(shop, maintenance, true)

	twoPlacesOfTheSameFailure(t, fake, server)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, "SELECT count(*) FROM ci_failures WHERE issue < 0") == 1
	})
	number := createdCIFailureIssue(t, server)
	fake.RemoveLabel(shop, number, "mobius:ready", "owner")
	waitForPolls(t, fake)

	fake.RefuseAddSubIssues(shop, maintenance, false)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})
	if numbers := fake.SubIssueNumbers(shop, maintenance); !slices.Equal(numbers, []int64{number}) {
		t.Errorf("sub-issues = %v", numbers)
	}
	if labels := fake.Labels(shop, number); slices.Contains(labels, "mobius:ready") {
		t.Errorf("labels = %v", labels)
	}
	waitForPolls(t, fake)
	if hasLiveTask(t, server, number) {
		t.Errorf("the issue has a task")
	}
}

func TestAPendingCIFailureIssueWithATaskGetsBelowTheWorkstreamWithNoLabelAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.RefuseAddSubIssues(shop, maintenance, true)

	twoPlacesOfTheSameFailure(t, fake, server)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, "SELECT count(*) FROM ci_failures WHERE issue < 0") == 1
	})
	number := createdCIFailureIssue(t, server)
	title, _ := fake.Issue(shop, number)
	fake.RemoveLabel(shop, number, "mobius:ready", "owner")
	fake.AddSubIssue(shop, maintenance, number)
	fake.AddLabel(shop, number, "mobius:ready", "owner")
	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
	waitForPolls(t, fake)
	writes := fake.LabelWrites(shop, number)

	fake.RefuseAddSubIssues(shop, maintenance, false)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})
	waitForPolls(t, fake)
	if labels := fake.Labels(shop, number); slices.Contains(labels, "mobius:ready") {
		t.Errorf("labels = %v", labels)
	}
	if got := fake.LabelWrites(shop, number); got != writes {
		t.Errorf("label writes = %d, want %d", got, writes)
	}
	if text := fmt.Sprintf("No effect: %q has a live task", title); slices.Contains(feedTexts(t, server), text) {
		t.Errorf("the issue got mobius:ready again: %s", text)
	}
}

func TestARefusedLabelWriteOfACIFailureIssueDoesNotStopThePoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, maintenance, 45, "Add plan model")
	fake.RefuseAddSubIssues(shop, maintenance, true)

	twoPlacesOfTheSameFailure(t, fake, server)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, "SELECT count(*) FROM ci_failures WHERE issue < 0") == 1
	})
	number := createdCIFailureIssue(t, server)
	fake.KeepLabelFromBot(shop, number, "mobius:ready")
	fake.RefuseAddSubIssues(shop, maintenance, false)
	testkit.WaitFor(t, func() bool {
		return slices.Contains(fake.SubIssueNumbers(shop, maintenance), number) &&
			rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", -number)) == 1
	})

	fake.AddLabel(shop, 45, "mobius:ready", "owner")
	fake.SetBody(shop, number, "The same CI failure occurs in 2 places.")

	if task := liveTaskOf(t, server, 45); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}

	fake.KeepLabelFromBot(shop, number, "")
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})
	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
}

func TestARefusedAddOfTheReadyLabelOfACIFailureIssueWritesItAgainAndStartsTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.RefuseAddSubIssues(shop, maintenance, true)

	twoPlacesOfTheSameFailure(t, fake, server)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, "SELECT count(*) FROM ci_failures WHERE issue < 0") == 1
	})
	number := createdCIFailureIssue(t, server)
	fake.RefuseAddLabelOnce(shop, number, http.StatusForbidden)
	fake.RefuseAddSubIssues(shop, maintenance, false)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})
	if numbers := fake.SubIssueNumbers(shop, maintenance); !slices.Equal(numbers, []int64{number}) {
		t.Errorf("sub-issues = %v", numbers)
	}
	if labels := fake.Labels(shop, number); !slices.Contains(labels, "mobius:ready") {
		t.Errorf("labels = %v", labels)
	}
	if writes := fake.LabelWrites(shop, number); writes < 3 {
		t.Errorf("label writes = %d", writes)
	}
	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
}

func TestAThirdFailureOfTheSameTestGivesNoSecondIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsWithFortyFour, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	fake.AddSubIssueOf(shop, 12, 44, "Task 44")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)
	third := approvalHeadOf(t, server, fake, 44)
	failActions(fake, first)
	failActions(fake, second)
	ciBreakIssue(t, fake)

	failActions(fake, third)

	testkit.WaitFor(t, func() bool { return rowCount(t, server, "SELECT count(*) FROM ci_failure_places") == 3 })
	waitForPolls(t, fake)
	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 1 {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestTheFixRoundOfAFailureWithAnOpenRecordIssueGetsTheIssueAndTheStep(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsWithFortyFour, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	fake.AddSubIssueOf(shop, 12, 44, "Task 44")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)
	third := approvalHeadOf(t, server, fake, 44)
	failActions(fake, first)
	failActions(fake, second)
	number := ciBreakIssue(t, fake)
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})

	failActions(fake, third)

	want := fmt.Sprintf("The change of the pull request did not cause this failure.\n  The open issue #%d holds this failure. Do not fix it in this task.\n"+
		"\nFor each failure that the change of the pull request did not cause:\n", number)
	round := testkit.WaitForValue(t, func() (string, bool) {
		for _, session := range roleSessions(t, server, engine.ImplementerRole) {
			for _, prompt := range promptTexts(t, server, session.ID) {
				if strings.Contains(prompt, want) {
					return prompt, true
				}
			}
		}
		return "", false
	})
	step := "3. When the reason of the failure names an open issue, do not fix the test, and make no commit for it.\n"
	if !strings.Contains(round, step) {
		t.Errorf("%q is not in %s", step, round)
	}
	if n := strings.Count(round, "The open issue"); n != 1 {
		t.Errorf("issue sentences = %d in %s", n, round)
	}
}

func TestTheFixRoundsOfAFailureThatGotItsIssueAfterTheDiagnosisGetTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lingers := reportsPlan + "linger = \"3s\"\n"
	server := connectDiagnoserFixing(t, fake, leadStartsTwo, fixesNothing, lingers)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)
	failActions(fake, first)
	testkit.WaitFor(t, func() bool {
		return liveTaskState(t, server, 41) == "needs_human" && slices.Contains(fake.Labels(shop, 41), "mobius:needs-human")
	})

	failActions(fake, second)

	number := ciBreakIssue(t, fake)
	sentence := fmt.Sprintf("  The open issue #%d holds this failure. Do not fix it in this task.\n", number)
	roundsWithIssue := func() int {
		rounds := 0
		for _, session := range roleSessions(t, server, engine.ImplementerRole) {
			for _, prompt := range promptTexts(t, server, session.ID) {
				if strings.Contains(prompt, sentence) {
					rounds++
					if n := strings.Count(prompt, "The open issue"); n != 1 {
						t.Errorf("issue sentences = %d in %s", n, prompt)
					}
				}
			}
		}
		return rounds
	}
	testkit.WaitFor(t, func() bool { return roundsWithIssue() == 1 })

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")

	testkit.WaitFor(t, func() bool { return roundsWithIssue() == 2 })
}

func TestAFailureInOnePullRequestGivesNoIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStarts, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	sha := approvalHead(t, server, fake)

	failActions(fake, sha)

	testkit.WaitFor(t, func() bool { return rowCount(t, server, "SELECT count(*) FROM ci_failure_places") == 1 })
	waitForPolls(t, fake)
	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 0 {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestALongReasonTestAndLocationGiveAnIssueThatGitHubAccepts(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 70000)
	reportsLong := "[[prompts]]\nwhen = \"You are a Diagnoser\"\ncall = { tool = \"report_failure\", arguments = { reason = \"" + long + "\", test = \"" + long + "\", location = \"" + long + "\", lines = \"--- FAIL: TestPlan\", caused_by_pull_request = false } }\n"
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsLong)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)

	failActions(fake, first)
	failActions(fake, second)

	title, body := fake.Issue(shop, ciBreakIssue(t, fake))
	if len(title) >= 256 {
		t.Errorf("title has %d characters", len(title))
	}
	if len(body) >= 60000 {
		t.Errorf("body has %d bytes", len(body))
	}
	if !strings.Contains(body, "\n````\n--- FAIL: TestPlan\n````\n") {
		t.Errorf("body does not have the failed lines:\n%s", body[:500])
	}
}
