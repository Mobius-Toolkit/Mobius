package engine_test

import (
	"fmt"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// recordIssue is the number that the fake GitHub gives to the issue of the record of two pull requests of #41 and #43.
const recordIssue = 46

// leadStartsFix is a Lead that starts an Implementer for the dispatch of #41, #43 and #46.
const leadStartsFix = "[[prompts]]\nwhen = \"dispatch of #46\"\ncall = { tool = \"start_implementer\", arguments = { n = 46, instructions = \"Fix TestPlan.\" } }\n\n" + leadStartsTwo

// leadStartsThird is a Lead that starts an Implementer for the dispatch of #50, #41 and #43.
const leadStartsThird = "[[prompts]]\nwhen = \"dispatch of #50\"\ncall = { tool = \"start_implementer\", arguments = { n = 50, instructions = \"Round prices down.\" } }\n\n" + leadStartsTwo

// fixesOnce is an Implementer that commits fix.txt in the first fix round and makes no commit in a later one.
const fixesOnce = "[[prompts]]\nwhen = \"Action: fix\"\nshell = \"test -f fix.txt || { echo fix > fix.txt && git add fix.txt && git commit -q -m 'Fix the check'; }\"\n\n" + commits

// stoppedByTheSameFailure fails TestPlan in the CI of #41 and #43 and waits until the CI stops both tasks and the record
// has its issue. It gives the heads.
func stoppedByTheSameFailure(t *testing.T, fake *testkit.FakeGitHub, lead string) (*testserver.Server, string, string) {
	t.Helper()
	server := connectDiagnoserFixing(t, fake, lead, fixesNothing, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)
	failActions(fake, first)
	failActions(fake, second)
	if number := ciBreakIssue(t, fake); number != recordIssue {
		t.Fatalf("issue = %d", number)
	}
	testkit.WaitFor(t, func() bool {
		return liveTaskState(t, server, 41) == "needs_human" && liveTaskState(t, server, 43) == "needs_human"
	})
	return server, first, second
}

// fixInApproval makes the task of the issue of the record wait for the Lead. It gives the number of its pull request.
func fixInApproval(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) int64 {
	t.Helper()
	fake.AddLabel(shop, recordIssue, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, recordIssue) == "approval" })
	var pull int64
	if err := server.DB.QueryRow("SELECT pull_request FROM tasks WHERE issue = ?", recordIssue).Scan(&pull); err != nil {
		t.Fatal(err)
	}
	return pull
}

// mergedFix merges the pull request of the task of the issue of the record after a new commit of the default branch.
func mergedFix(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) {
	t.Helper()
	pull := fixInApproval(t, server, fake)
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.MergePullRequest(shop, pull)
}

func endedRecords(t *testing.T, server *testserver.Server) int {
	t.Helper()
	return rowCount(t, server, "SELECT count(*) FROM ci_failures WHERE ended_at <> ''")
}

// pushNewerHead adds a commit on top of branch in the fake GitHub, as a person does, and gives the new head.
func pushNewerHead(t *testing.T, fake *testkit.FakeGitHub, branch string) string {
	t.Helper()
	remote := fake.Remote(shop)
	newer := testkit.Git(t, remote, "commit-tree", branch+"^{tree}", "-p", branch, "-m", "Newer")
	testkit.Git(t, remote, "update-ref", "refs/heads/"+branch, newer)
	return newer
}

// passCI adds a passed check run on the head sha.
func passCI(fake *testkit.FakeGitHub, sha string) {
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "success"))
}

func TestTheMergedFixOfARecordUpdatesTheBranchOfEachPullRequestAndTheStoppedTasksContinue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, first, second := stoppedByTheSameFailure(t, fake, leadStartsFix)

	mergedFix(t, server, fake)

	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != first && head(t, fake, "mobius/43") != second })
	isAncestor(t, fake, "main", "mobius/41")
	isAncestor(t, fake, "main", "mobius/43")
	passCI(fake, head(t, fake, "mobius/41"))
	passCI(fake, head(t, fake, "mobius/43"))
	testkit.WaitFor(t, func() bool {
		return liveTaskState(t, server, 41) != "needs_human" && liveTaskState(t, server, 43) != "needs_human"
	})
	waitForPolls(t, fake)
	if calls, ended := fake.UpdateBranchCalls(), endedRecords(t, server); calls != 2 || ended != 1 {
		t.Errorf("update calls = %d, ended records = %d", calls, ended)
	}
}

func TestTheTasksContinueWhenGitHubAddsTheMergeCommitOfTheUpdateAfterALaterPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, first, second := stoppedByTheSameFailure(t, fake, leadStartsFix)
	pull := fixInApproval(t, server, fake)
	fake.UnlistPullRequests(true)
	fake.HoldBranchUpdates()

	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.MergePullRequest(shop, pull)
	fake.CloseIssue(shop, recordIssue)

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 2 })
	waitForPolls(t, fake)
	if head(t, fake, "mobius/41") != first || liveTaskState(t, server, 41) != "needs_human" {
		t.Fatal("the merge commit came before the release")
	}

	fake.ReleaseBranchUpdates()

	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != first && head(t, fake, "mobius/43") != second })
	passCI(fake, head(t, fake, "mobius/41"))
	passCI(fake, head(t, fake, "mobius/43"))
	testkit.WaitFor(t, func() bool {
		return liveTaskState(t, server, 41) != "needs_human" && liveTaskState(t, server, 43) != "needs_human"
	})
	if calls := fake.UpdateBranchCalls(); calls != 2 {
		t.Errorf("update calls = %d", calls)
	}
}

func TestAPullRequestOfARecordWithANewerHeadGetsNoUpdate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, first, _ := stoppedByTheSameFailure(t, fake, leadStartsFix)
	newer := pushNewerHead(t, fake, "mobius/43")

	mergedFix(t, server, fake)

	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != first })
	testkit.WaitFor(t, func() bool { return endedRecords(t, server) == 1 })
	waitForPolls(t, fake)
	if calls, got := fake.UpdateBranchCalls(), head(t, fake, "mobius/43"); calls != 1 || got != newer {
		t.Errorf("update calls = %d, head = %s, want %s", calls, got, newer)
	}
}

func TestACloseOfTheIssueOfARecordWithNoMergedPullRequestUpdatesNoBranchAndALaterFailureGivesANewRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _, _ := stoppedByTheSameFailure(t, fake, leadStartsThird)
	fake.AddSubIssueOf(shop, 12, 50, "Task 50")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	fake.CloseIssue(shop, recordIssue)

	testkit.WaitFor(t, func() bool { return endedRecords(t, server) == 1 })
	waitForPolls(t, fake)
	if calls := fake.UpdateBranchCalls(); calls != 0 {
		t.Errorf("update calls = %d", calls)
	}
	if recent := rowCount(t, server, "SELECT count(*) FROM ci_failure_places WHERE branch_updated_at > '0001-01-02'"); recent != 0 {
		t.Errorf("places that await a merge commit = %d", recent)
	}

	third := approvalHeadOf(t, server, fake, 50)
	failActions(fake, third)

	testkit.WaitFor(t, func() bool { return rowCount(t, server, "SELECT count(*) FROM ci_failures") == 2 })
	if places := rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failure_places WHERE head = '%s'", third)); places != 1 {
		t.Errorf("places of the third head = %d", places)
	}
}

func TestAPullRequestThatFailsTheSameTestOnANewerHeadGetsTheUpdateOfItsNewestHead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoserFixing(t, fake, leadStartsFix, fixesOnce, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)
	failActions(fake, first)
	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != first })
	newer := head(t, fake, "mobius/41")
	failActions(fake, second)
	if number := ciBreakIssue(t, fake); number != recordIssue {
		t.Fatalf("issue = %d", number)
	}

	failActions(fake, newer)

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failure_places WHERE head = '%s'", newer)) == 1 &&
			liveTaskState(t, server, 41) == "needs_human"
	})
	if places := rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM ci_failure_places WHERE head = '%s'", first)); places != 0 {
		t.Fatalf("places of the first head = %d", places)
	}

	mergedFix(t, server, fake)

	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != newer })
	isAncestor(t, fake, "main", "mobius/41")
	testkit.WaitFor(t, func() bool { return endedRecords(t, server) == 1 })
	waitForPolls(t, fake)
	if calls := fake.UpdateBranchCalls(); calls != 1 {
		t.Errorf("update calls = %d", calls)
	}
}
