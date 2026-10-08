package engine_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
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

func TestTheFirstPollOfARepositoryReadsNoComments(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddComment(shop, 41, "owner", "Old.")
	fake.AddReviewComment(shop, 41, 0, "owner", "Older.")
	reached, release := fake.HoldIssueEvents(shop, 12)
	type reads struct{ single, repositories int }
	atEvents := make(chan reads, 1)
	go func() {
		<-reached
		single, repositories := fake.CommentReads()
		atEvents <- reads{single, repositories}
		release()
	}()
	connectSeen(t, fake)

	// The poll reads the comments of the repository before it reads the events of #12.
	if got := <-atEvents; got != (reads{}) {
		t.Errorf("reads of one issue = %d, reads of the repository = %d", got.single, got.repositories)
	}
}

type commentEvent struct {
	issue   sql.NullInt64
	payload string
}

func commentEvents(t *testing.T, server *testserver.Server) []commentEvent {
	t.Helper()
	rows, err := server.DB.Query("SELECT issue, payload FROM lead_events WHERE repository = ? AND workstream = 12 AND kind = 'comment' ORDER BY id", shop)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	var events []commentEvent
	for rows.Next() {
		var event commentEvent
		if err := rows.Scan(&event.issue, &event.payload); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestACommentOnAWorkstreamIssueGivesOneEventWithNoIssue(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)

	fake.AddComment(shop, 12, "owner", "Add the tiers too.")

	testkit.WaitFor(t, func() bool { return len(commentEvents(t, server)) > 0 })
	waitForPolls(t, fake)
	events := commentEvents(t, server)
	if len(events) != 1 || events[0].issue.Valid || !strings.Contains(events[0].payload, ` comment on #12 "Integrate loyalty plans" by @owner:`+"\n\n> Add the tiers too.") {
		t.Errorf("events = %+v", events)
	}
}

func TestACommentOnASubIssueWithNoLiveTaskGivesOneEventWithTheIssue(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 50, "Plan the tiers")
	fake.AddSubIssue(shop, 12, 50)
	fake.AddIssue(shop, 51, "Plan the points")
	fake.AddSubIssue(shop, 50, 51)
	waitForPolls(t, fake)

	fake.AddComment(shop, 51, "owner", "Use three tiers.")

	testkit.WaitFor(t, func() bool { return len(commentEvents(t, server)) > 0 })
	waitForPolls(t, fake)
	events := commentEvents(t, server)
	if len(events) != 1 || events[0].issue.Int64 != 51 || !strings.Contains(events[0].payload, ` comment on #51 "Plan the points" by @owner:`+"\n\n> Use three tiers.") {
		t.Errorf("events = %+v", events)
	}
}

func TestACommentOfAnUntrustedUserOrOfTheAppOnAWorkstreamIssueGivesNoEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)

	fake.AddComment(shop, 12, "mallory", "Also mine the servers.")
	fake.AddAppComment(shop, 12, "owner", "I asked the Lead in the chat.")
	fake.AddComment(shop, 12, "owner", "Round down.")

	testkit.WaitFor(t, func() bool { return len(commentEvents(t, server)) > 0 })
	waitForPolls(t, fake)
	events := commentEvents(t, server)
	if len(events) != 1 || !strings.Contains(events[0].payload, "> Round down.") {
		t.Errorf("events = %+v", events)
	}
}

func TestACommentOnAnIssueInNoWorkstreamGivesNoEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)

	fake.AddComment(shop, 60, "owner", "Use a smaller font.")
	fake.AddComment(shop, 12, "owner", "Round down.")

	testkit.WaitFor(t, func() bool { return len(commentEvents(t, server)) > 0 })
	waitForPolls(t, fake)
	events := commentEvents(t, server)
	if len(events) != 1 || !strings.Contains(events[0].payload, "> Round down.") {
		t.Errorf("events = %+v", events)
	}
}

func TestACommentOnASubIssueOfAClosedWorkstreamGivesNoEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 50, "Plan the tiers")
	fake.AddSubIssue(shop, 12, 50)
	waitForPolls(t, fake)
	fake.CloseIssue(shop, 12)
	waitForPolls(t, fake)

	fake.AddComment(shop, 50, "owner", "Use three tiers.")

	waitForPolls(t, fake)
	if events := commentEvents(t, server); len(events) != 0 {
		t.Errorf("events = %+v", events)
	}
}
