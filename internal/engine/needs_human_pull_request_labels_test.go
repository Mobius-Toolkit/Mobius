package engine_test

import (
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const pullRequestNumber = 42

func pullRequestHasNeedsHuman(fake *testkit.FakeGitHub) bool {
	return slices.Contains(fake.Labels(shop, pullRequestNumber), "mobius:needs-human")
}

// staleInNeedsHuman starts a task whose stale pull request with a merge conflict goes to a human, and waits for it.
func staleInNeedsHuman(t *testing.T, fake *testkit.FakeGitHub) {
	t.Helper()
	server, _ := startReady(t, fake, "", mergesCents)
	fake.SetCreatedAt(shop, pullRequestNumber, 0)
	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
}

func TestATaskWithAPullRequestThatGoesToNeedsHumanGetsTheLabelOnTheIssueAndOnThePullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	staleInNeedsHuman(t, fake)

	testkit.WaitFor(t, func() bool { return pullRequestHasNeedsHuman(fake) })
}

func TestTheLabelGoesAwayFromThePullRequestWhenTheTaskLeavesNeedsHumanThroughTheRemoval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	staleInNeedsHuman(t, fake)
	testkit.WaitFor(t, func() bool { return pullRequestHasNeedsHuman(fake) })

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")

	testkit.WaitFor(t, func() bool {
		return !pullRequestHasNeedsHuman(fake) && hasLabel(fake, "mobius:working")
	})
}

func TestATaskWithNoPullRequestThatGoesToNeedsHumanGetsTheLabelOnlyOnTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	handedToHuman(t, fake)

	if writes := fake.LabelWrites(shop, pullRequestNumber); writes != 0 {
		t.Errorf("label writes on #%d = %d", pullRequestNumber, writes)
	}
}
