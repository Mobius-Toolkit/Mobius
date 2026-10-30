package engine_test

import (
	"fmt"
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
	if labels := fake.Labels(shop, number); slices.Contains(labels, "mobius:ready") || slices.Contains(labels, "mobius:triage") {
		t.Errorf("labels = %v", labels)
	}
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failures WHERE issue = %d", number)) == 1
	})
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
