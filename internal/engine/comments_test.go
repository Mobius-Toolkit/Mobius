package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestACommentInTheSecondOfTheCursorGivesOneEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := startWithStoppedTask(t, fake)
	fake.AddComment(shop, 41, "owner", "First.")
	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 1 })

	fake.AddCommentInLastSecond(shop, 41, "owner", "Second.")

	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 2 })
	waitForPolls(t, fake)
	if got := eventLines(t, server); got != 2 {
		t.Errorf("events = %d", got)
	}
}

func TestAHandledCommentGivesNoSecondEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := startWithStoppedTask(t, fake)
	fake.AddComment(shop, 41, "owner", "First.")
	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 1 })

	fake.AddLabel(shop, 41, "priority", "owner")
	waitForPolls(t, fake)

	if got := eventLines(t, server); got != 1 {
		t.Errorf("events = %d", got)
	}
}

func TestAnEditOfAnOldCommentGivesNoEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := startWithStoppedTask(t, fake)
	id := fake.AddComment(shop, 41, "owner", "First.")
	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 1 })
	fake.AddComment(shop, 41, "owner", "Second.")
	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 2 })

	fake.EditComment(shop, id, "First, with a fix.")
	waitForPolls(t, fake)

	if got := eventLines(t, server); got != 2 {
		t.Errorf("events = %d", got)
	}
}

func TestACommentOnAnIssueOutsideThePageGivesOneEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := startWithStoppedTask(t, fake)
	fake.AddIssue(shop, 60, "Other")
	waitForPolls(t, fake)

	fake.AddCommentAfterNextList(shop, 41, "owner", "Late.")
	fake.AddIssue(shop, 61, "Another")

	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 1 })
	waitForPolls(t, fake)
	if got := eventLines(t, server); got != 1 {
		t.Errorf("events = %d", got)
	}
}

func TestAPollReadsTheCommentsOfManyIssuesAndPullRequestsWithTwoCalls(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := startWithStoppedTask(t, fake)
	fake.AddIssue(shop, 43, "Plan API")
	fake.AddSubIssue(shop, 12, 43)
	fake.PushCommit(shop, "mobius/43", "Plan API")
	fake.OpenPullRequest(shop, "Plan API", "mobius/43")
	if _, err := server.DB.Exec(`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, pull_request, branch, fix_rounds)
		VALUES ('owner/shop', 43, 12, 'stopped', '2026-10-04T10:00:00Z', 44, 'mobius/43', 3)`); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)
	// The poll reads the comments before the events of #12, so the comments below come in the next poll.
	reached, release := fake.HoldIssueEvents(shop, 12)
	fake.AddLabel(shop, 12, "priority", "owner")
	<-reached
	single, repositories := fake.CommentReads()

	fake.AddComment(shop, 41, "owner", "Round down.")
	fake.AddComment(shop, 42, "owner", "Use cents.")
	fake.AddReviewComment(shop, 42, 0, "owner", "Rename plan to tier.")
	fake.AddComment(shop, 43, "owner", "Split it.")
	fake.AddComment(shop, 44, "owner", "Use dollars.")
	fake.AddReviewComment(shop, 44, 0, "owner", "Rename tier to plan.")
	release()

	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 4 })
	waitForPolls(t, fake)
	afterSingle, afterRepositories := fake.CommentReads()
	// The poll after the one that read the comments reads the last changed issue again, because its page differs from the saved page.
	if afterSingle != single || afterRepositories != repositories+4 {
		t.Errorf("reads of one issue = %d, reads of the repository = %d", afterSingle-single, afterRepositories-repositories)
	}
}

func TestAnOldCommentGivesNoEventAfterTheCommentCursorsAreMissing(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := startWithStoppedTask(t, fake)
	fake.AddComment(shop, 41, "owner", "First.")
	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 1 })
	if _, err := server.DB.Exec(`DELETE FROM sync_cursors WHERE endpoint IN ('issue_comments', 'review_comments')`); err != nil {
		t.Fatal(err)
	}

	fake.AddLabel(shop, 41, "priority", "owner")
	waitForPolls(t, fake)

	if got := eventLines(t, server); got != 1 {
		t.Errorf("events = %d", got)
	}
}
