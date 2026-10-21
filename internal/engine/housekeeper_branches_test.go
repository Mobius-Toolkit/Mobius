package engine_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func fastHousekeeper(cfg *config.Config) {
	cfg.HousekeeperInterval = 100 * time.Millisecond
}

func hasBranch(t *testing.T, fake *testkit.FakeGitHub, branch string) bool {
	t.Helper()
	return testkit.Git(t, fake.Remote(shop), "branch", "--list", branch) != ""
}

// waitForBranchChecked waits until the ended task of the issue has a branch that the Housekeeper has no more work on.
func waitForBranchChecked(t *testing.T, server *testserver.Server, issue int) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, fmt.Sprintf("SELECT count(*) FROM tasks WHERE issue = %d AND state = 'ended' AND branch_checked = 1", issue)) == 1
	})
}

// pullRequestOf gives the number of the pull request of the branch.
func pullRequestOf(t *testing.T, fake *testkit.FakeGitHub, branch string) int64 {
	t.Helper()
	for _, pull := range fake.PullRequests(shop) {
		if pull.Head == branch {
			return pull.Number
		}
	}
	t.Fatalf("no pull request of %s", branch)
	return 0
}

func TestTheHousekeeperDeletesTheBranchOfAMergedPullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, fastHousekeeper)
	waitForApproval(t, server, fake)
	waitForPolls(t, fake)
	if !hasBranch(t, fake, "mobius/41") {
		t.Fatal("the branch of an open pull request is gone")
	}

	fake.MergePullRequest(shop, 42)

	testkit.WaitFor(t, func() bool { return !hasBranch(t, fake, "mobius/41") })
	waitForBranchChecked(t, server, 41)
	if !hasBranch(t, fake, "main") {
		t.Error("the default branch is gone")
	}
}

func TestTheHousekeeperKeepsTheBranchOfAPullRequestThatClosedWithNoMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, fastHousekeeper)
	waitForApproval(t, server, fake)

	fake.CloseIssue(shop, 42)

	waitForBranchChecked(t, server, 41)
	if !hasBranch(t, fake, "mobius/41") {
		t.Error("the branch is gone")
	}
}

func TestTheHousekeeperKeepsTheBranchOfAMergedPullRequestThatGotACommitAfterTheMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, fastHousekeeper)
	waitForApproval(t, server, fake)
	waitForPolls(t, fake)
	headRead := "GET /repos/{owner}/{repo}/git/ref/heads/{branch...}"
	release := fake.HoldNextOf(headRead, "/repos/owner/shop/git/ref/heads/mobius/41")

	fake.MergePullRequest(shop, 42)
	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
	testkit.Git(t, work, "commit", "--allow-empty", "-m", "Add a commit after the merge")
	testkit.Git(t, work, "push", "origin", "HEAD:refs/heads/mobius/41")
	release()

	waitForBranchChecked(t, server, 41)
	if !hasBranch(t, fake, "mobius/41") {
		t.Error("the branch is gone")
	}
}

func TestARefusedDeletionKeepsTheBranchAndTheHousekeeperDeletesTheBranchOfTheNextRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStartsTwo, commits, fastHousekeeper)
	fake.AddIssue(shop, 43, "Add plan price")
	fake.AddSubIssue(shop, 12, 43)
	approvalHeadOf(t, server, fake, 41)
	approvalHeadOf(t, server, fake, 43)
	fake.RefuseBranchDeletion(shop, "mobius/41", "Cannot delete this branch")

	fake.MergePullRequest(shop, pullRequestOf(t, fake, "mobius/41"))
	fake.MergePullRequest(shop, pullRequestOf(t, fake, "mobius/43"))

	testkit.WaitFor(t, func() bool { return !hasBranch(t, fake, "mobius/43") })
	waitForBranchChecked(t, server, 41)
	waitForBranchChecked(t, server, 43)
	if !hasBranch(t, fake, "mobius/41") {
		t.Error("the branch is gone")
	}
}
