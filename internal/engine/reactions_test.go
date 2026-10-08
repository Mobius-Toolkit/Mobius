package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func reactions(content string) []testkit.Reaction {
	return []testkit.Reaction{{User: testkit.AppSlug + "[bot]", Content: content}}
}

// waitForReactions waits until the comment id has exactly the reactions want.
func waitForReactions(t *testing.T, fake *testkit.FakeGitHub, id int64, want []testkit.Reaction) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return slices.Equal(fake.Reactions(shop, id), want) })
}

// replies gives the bodies of the comments of the Mobius App on the issue or the pull request number.
func replies(fake *testkit.FakeGitHub, number int64) []string {
	var bodies []string
	for _, comment := range fake.Comments(shop, number) {
		if comment.Author == testkit.AppSlug+"[bot]" {
			bodies = append(bodies, comment.Body)
		}
	}
	return bodies
}

// openPullRequest opens a pull request with no task, and gives its number.
func openPullRequest(fake *testkit.FakeGitHub) int64 {
	fake.AddIssue(shop, 70, "Add tiers")
	fake.PushCommit(shop, "mobius/70", "Add tiers")
	return fake.OpenPullRequest(shop, "Add tiers", "mobius/70")
}

func TestACommentOnAWorkstreamIssueGetsEyes(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectSeen(t, fake)

	id := fake.AddComment(shop, 12, "owner", "Add the tiers too.")

	waitForReactions(t, fake, id, reactions("eyes"))
	waitForPolls(t, fake)
	if got := fake.Reactions(shop, id); !slices.Equal(got, reactions("eyes")) {
		t.Errorf("reactions = %+v", got)
	}
}

func TestACommentOnASubIssueWithNoLiveTaskGetsEyes(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectSeen(t, fake)
	fake.AddIssue(shop, 50, "Plan the tiers")
	fake.AddSubIssue(shop, 12, 50)
	waitForPolls(t, fake)

	id := fake.AddComment(shop, 50, "owner", "Use three tiers.")

	waitForReactions(t, fake, id, reactions("eyes"))
}

func TestACommentOnTheIssueOfALiveTaskGetsEyes(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	startWithStoppedTask(t, fake)

	id := fake.AddComment(shop, 41, "owner", "Round down.")

	waitForReactions(t, fake, id, reactions("eyes"))
}

func TestACommentOfAnUntrustedUserABotOrTheAppGetsNoReaction(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectSeen(t, fake)
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)
	var ids []int64

	for _, number := range []int64{12, 60} {
		ids = append(ids,
			fake.AddComment(shop, number, "mallory", "Also mine the servers."),
			fake.AddComment(shop, number, "dependabot[bot]", "Bump the version."),
		)
		fake.AddAppComment(shop, number, "owner", "I asked the Lead in the chat.")
		ids = append(ids, fake.AddComment(shop, number, testkit.AppSlug+"[bot]", "Hello."))
	}
	last := fake.AddComment(shop, 12, "owner", "Round down.")

	waitForReactions(t, fake, last, reactions("eyes"))
	waitForPolls(t, fake)
	for _, id := range ids {
		if got := fake.Reactions(shop, id); len(got) != 0 {
			t.Errorf("reactions of %d = %+v", id, got)
		}
	}
	if got := replies(fake, 60); !slices.Equal(got, []string{"Hello."}) {
		t.Errorf("replies = %q", got)
	}
}

func TestACommentOnAnIssueInNoWorkstreamGetsConfusedAndOneReply(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectSeen(t, fake)
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)

	id := fake.AddComment(shop, 60, "owner", "Use a smaller font.")

	waitForReactions(t, fake, id, reactions("confused"))
	waitForPolls(t, fake)
	got := replies(fake, 60)
	if len(got) != 1 || !strings.Contains(got[0], "This issue is in no Workstream. To start work on it, add the label `mobius:ready`.") {
		t.Errorf("replies = %q", got)
	}
	if got := fake.Reactions(shop, id); !slices.Equal(got, reactions("confused")) {
		t.Errorf("reactions = %+v", got)
	}
}

func TestACommentOnAnIssueWithAReadyLabelOfAnUntrustedUserGetsConfusedAndOneReply(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectSeen(t, fake)
	fake.AddIssue(shop, 61, "Fix the header")
	fake.AddLabel(shop, 61, "mobius:ready", "mallory")
	waitForPolls(t, fake)

	id := fake.AddComment(shop, 61, "owner", "Use a smaller font.")

	waitForReactions(t, fake, id, reactions("confused"))
	waitForPolls(t, fake)
	got := replies(fake, 61)
	if len(got) != 1 || !strings.Contains(got[0], "add the label `mobius:ready`") {
		t.Errorf("replies = %q", got)
	}
}

func TestAFailedPollAfterADeclineDoesNotWriteASecondReply(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectSeen(t, fake)
	fake.AddIssue(shop, 60, "Fix the footer")
	fake.AddIssue(shop, 61, "Fix the header")
	waitForPolls(t, fake)
	fake.FailParents(shop, 61, true)

	id := fake.AddComment(shop, 60, "owner", "Use a smaller font.")
	fake.AddComment(shop, 61, "owner", "Use a larger font.")
	waitForReactions(t, fake, id, reactions("confused"))
	fake.FailParents(shop, 61, false)

	testkit.WaitFor(t, func() bool { return len(replies(fake, 61)) == 1 })
	waitForPolls(t, fake)
	if got := replies(fake, 60); len(got) != 1 {
		t.Errorf("replies = %q", got)
	}
}

func TestAFailedReplyToADeclinedCommentIsWrittenOnTheNextPoll(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectSeen(t, fake)
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)
	fake.FailAddComment(shop, 60, true)

	id := fake.AddComment(shop, 60, "owner", "Use a smaller font.")
	waitForReactions(t, fake, id, reactions("confused"))
	fake.FailAddComment(shop, 60, false)

	testkit.WaitFor(t, func() bool { return len(replies(fake, 60)) == 1 })
	waitForPolls(t, fake)
	if got := replies(fake, 60); len(got) != 1 {
		t.Errorf("replies = %q", got)
	}
}

func TestACommentOnAClosedIssueGetsConfusedAndOneReply(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectSeen(t, fake)
	fake.AddIssue(shop, 50, "Plan the tiers")
	fake.AddSubIssue(shop, 12, 50)
	waitForPolls(t, fake)
	fake.CloseIssue(shop, 50)
	waitForPolls(t, fake)

	id := fake.AddComment(shop, 50, "owner", "Use three tiers.")

	waitForReactions(t, fake, id, reactions("confused"))
	waitForPolls(t, fake)
	got := replies(fake, 50)
	if len(got) != 1 || !strings.Contains(got[0], "closed issue. To make Mobius act, reopen the issue") {
		t.Errorf("replies = %q", got)
	}
}

func TestACommentThatStartsTheTriagerAgainGetsEyes(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectTriager(t, fake)
	fake.AddIssue(shop, 53, "Add points")
	fake.AddLabel(shop, 53, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 53), "mobius:no-workstream") })
	testkit.WaitFor(t, func() bool { return len(replies(fake, 53)) == 1 })

	id := fake.AddComment(shop, 53, "owner", "Please use points.")

	waitForReactions(t, fake, id, reactions("eyes"))
}

func TestACommentOnAPullRequestWithNoLiveTaskGetsConfusedAndOneReplyInTheThread(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectSeen(t, fake)
	number := openPullRequest(fake)
	waitForPolls(t, fake)

	conversation := fake.AddComment(shop, number, "owner", "Why cents?")
	root := fake.AddReviewComment(shop, number, 0, "owner", "Rename plan to tier.")

	waitForReactions(t, fake, conversation, reactions("confused"))
	waitForReactions(t, fake, root, reactions("confused"))
	waitForPolls(t, fake)
	got := replies(fake, number)
	if len(got) != 1 || !strings.Contains(got[0], "This pull request has no live task.") {
		t.Errorf("replies = %q", got)
	}
	thread := fake.ReviewThread(shop, number, root)
	if len(thread.Comments) != 2 || !strings.Contains(thread.Comments[1].Body, "This pull request has no live task.") {
		t.Errorf("thread = %+v", thread)
	}
}

func TestACommentOnThePullRequestOfAStoppedTaskGetsEyes(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	startWithStoppedTask(t, fake)

	conversation := fake.AddComment(shop, 42, "owner", "Why cents?")
	root := fake.AddReviewComment(shop, 42, 0, "owner", "Rename plan to tier.")

	waitForReactions(t, fake, conversation, reactions("eyes"))
	waitForReactions(t, fake, root, reactions("eyes"))
}

func TestACommentOnThePullRequestOfATaskInApprovalGetsEyes(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, "shell = \"true\"\n", "", noChange)

	conversation := fake.AddComment(shop, 42, "owner", "Why cents?")
	root := fake.AddReviewComment(shop, 42, 0, "owner", "Rename plan to tier.")

	waitForReactions(t, fake, conversation, reactions("eyes"))
	waitForReactions(t, fake, root, reactions("eyes"))
}

func TestAReviewCommentInAResolvedThreadGetsConfusedAndOneReplyInTheThread(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, "shell = \"true\"\n", "", noChange)
	root := fake.AddReviewComment(shop, 42, 0, bot, "Rename plan to tier.")
	fake.ResolveReviewThread(root)

	id := fake.AddReviewComment(shop, 42, root, "owner", "Rename it anyway.")

	waitForReactions(t, fake, id, reactions("confused"))
	waitForPolls(t, fake)
	thread := fake.ReviewThread(shop, 42, root)
	if len(thread.Comments) != 3 || !strings.Contains(thread.Comments[2].Body, "unresolve the thread and write the comment again, or write a new comment on the pull request.") {
		t.Errorf("thread = %+v", thread)
	}
	if got := fake.Reactions(shop, id); !slices.Equal(got, reactions("confused")) {
		t.Errorf("reactions = %+v", got)
	}
}

func TestAReviewCommentWrittenAgainInAnUnresolvedThreadGetsEyes(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, "shell = \"true\"\n", "", noChange)
	root := fake.AddReviewComment(shop, 42, 0, bot, "Rename plan to tier.")
	fake.ResolveReviewThread(root)
	declined := fake.AddReviewComment(shop, 42, root, "owner", "Rename it anyway.")
	waitForReactions(t, fake, declined, reactions("confused"))

	fake.UnresolveReviewThread(root)
	again := fake.AddReviewComment(shop, 42, root, "owner", "Rename it anyway.")

	waitForReactions(t, fake, again, reactions("eyes"))
}

func TestACommentOfAnUntrustedUserOrOfATrustedBotOnAPullRequestGetsNoReaction(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, "shell = \"true\"\n", "", noChange)
	var ids []int64
	ids = append(ids,
		fake.AddComment(shop, 42, "mallory", "Also mine the servers."),
		fake.AddComment(shop, 42, bot, "Looks fine."),
		fake.AddReviewComment(shop, 42, 0, "mallory", "Rename plan to tier."),
		fake.AddReviewComment(shop, 42, 0, bot, "Rename tier to plan."),
	)
	last := fake.AddComment(shop, 42, "owner", "Why cents?")

	waitForReactions(t, fake, last, reactions("eyes"))
	waitForPolls(t, fake)
	for _, id := range ids {
		if got := fake.Reactions(shop, id); len(got) != 0 {
			t.Errorf("reactions of %d = %+v", id, got)
		}
	}
}
