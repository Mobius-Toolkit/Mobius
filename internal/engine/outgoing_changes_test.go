package engine_test

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// appComments gives the bodies of the comments of the Mobius App on the issue number, with the hidden write marker.
func appComments(fake *testkit.FakeGitHub, number int64) []string {
	var bodies []string
	for _, comment := range fake.CommentsWithMarkers(shop, number) {
		if comment.Author == testkit.AppSlug+"[bot]" {
			bodies = append(bodies, comment.Body)
		}
	}
	return bodies
}

// declineOne makes Mobius add a reply on the issue 60 to a comment of the owner.
func declineOne(fake *testkit.FakeGitHub, text string) {
	fake.AddComment(shop, 60, "owner", text)
}

func TestAnOutgoingChangeThatGitHubFailsAtOnceIsSentByALaterPollWithItsMarker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)
	fake.FailAddComment(shop, 60, true)

	declineOne(fake, "Use a smaller font.")
	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 1 })
	if got := appComments(fake, 60); len(got) != 0 {
		t.Fatalf("comments = %q", got)
	}
	fake.FailAddComment(shop, 60, false)

	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 1 })
	waitForPolls(t, fake)
	got := appComments(fake, 60)
	if len(got) != 1 || !strings.HasSuffix(got[0], "\n\n<!-- mobius:write 1 -->") {
		t.Errorf("comments = %q", got)
	}
}

func TestAnOutgoingChangeWhoseAnswerGotLostIsFoundByItsMarkerAndPostedOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)
	fake.LoseCommentAnswers(shop, 60, true)

	declineOne(fake, "Use a smaller font.")

	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 1 })
	waitForPolls(t, fake)
	if got := appComments(fake, 60); len(got) != 1 || !strings.HasSuffix(got[0], "\n\n<!-- mobius:write 1 -->") {
		t.Errorf("comments = %q", got)
	}
}

func TestARefusedOutgoingChangeGetsRefusedAndTheNextChangeOfTheItemGoesOut(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)
	fake.FailAddComment(shop, 60, true)
	declineOne(fake, "Use a smaller font.")
	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 1 })
	declineOne(fake, "Use a bigger font.")
	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 2 })

	fake.RefuseNextComment(shop, 60, http.StatusNotFound)
	fake.FailAddComment(shop, 60, false)

	testkit.WaitFor(t, func() bool {
		return outgoingChanges(t, server, "refused") == 1 && outgoingChanges(t, server, "sent") == 1
	})
	if got := appComments(fake, 60); len(got) != 1 || !strings.HasSuffix(got[0], "\n\n<!-- mobius:write 2 -->") {
		t.Errorf("comments = %q", got)
	}
}

func TestAnOutgoingChangeWhoseCommentsGitHubRefusesToReadGetsRefusedAndTheNextChangeOfTheItemGoesOut(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)
	fake.FailAddComment(shop, 60, true)
	declineOne(fake, "Use a smaller font.")
	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 1 })
	declineOne(fake, "Use a bigger font.")
	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 2 })

	fake.RefuseNextCommentList(shop, 60, http.StatusNotFound)
	fake.FailAddComment(shop, 60, false)

	testkit.WaitFor(t, func() bool {
		return outgoingChanges(t, server, "refused") == 1 && outgoingChanges(t, server, "sent") == 1
	})
	if got := appComments(fake, 60); len(got) != 1 || !strings.HasSuffix(got[0], "\n\n<!-- mobius:write 2 -->") {
		t.Errorf("comments = %q", got)
	}
}

func TestANewOutgoingChangeNeverGetsTheIdOfARowThatTheHousekeeperDeleted(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, seen, func(cfg *config.Config) {
		cfg.LeadIdleTimeout = 5 * time.Second
		cfg.HousekeeperInterval = 100 * time.Millisecond
	})
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)
	declineOne(fake, "Use a smaller font.")
	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 1 })
	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := server.DB.Exec("UPDATE outgoing_changes SET created_at = ?", old); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 0 })
	fake.FailAddComment(shop, 60, true)

	declineOne(fake, "Use a bigger font.")
	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 1 })
	waitForPolls(t, fake)
	fake.FailAddComment(shop, 60, false)

	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 1 })
	got := appComments(fake, 60)
	if len(got) != 2 || !strings.HasSuffix(got[0], "\n\n<!-- mobius:write 1 -->") || !strings.HasSuffix(got[1], "\n\n<!-- mobius:write 2 -->") {
		t.Errorf("comments = %q", got)
	}
}

func TestAReviewCommentEditWaitsForThePendingPostAndThenChangesTheNewComment(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, noFinding+leadStarts, commits, noChange)
	fake.FailAddComment(shop, 42, true)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 2 })
	waitForPolls(t, fake)
	if got := roundComments(fake); len(got) != 0 {
		t.Fatalf("comments = %q", got)
	}
	fake.FailAddComment(shop, 42, false)

	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 0 })
	got := roundComments(fake)
	if len(got) != 1 || !strings.HasPrefix(got[0], "Review ended, round 1 of 10\n") {
		t.Errorf("comments = %q", got)
	}
}

func TestTheHousekeeperDeletesAFinishedOutgoingChangeOlderThan24HoursAndKeepsAPendingOne(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, seen, func(cfg *config.Config) {
		cfg.LeadIdleTimeout = 5 * time.Second
		cfg.HousekeeperInterval = 100 * time.Millisecond
	})
	fake.AddIssue(shop, 60, "Fix the footer")
	waitForPolls(t, fake)
	declineOne(fake, "Use a smaller font.")
	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 1 })
	fake.FailAddComment(shop, 60, true)
	declineOne(fake, "Use a bigger font.")
	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 1 })

	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := server.DB.Exec("UPDATE outgoing_changes SET created_at = ?", old); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 0 })
	if got := commentWrites(t, server); got != 1 {
		t.Errorf("pending outgoing changes = %d", got)
	}
}

func TestACommentOfAnOutgoingChangeThatComesAfterTheAnswerOfAPersonIsNoQuestion(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddLabel(shop, 41, "mobius:needs-human", testkit.AppSlug+"[bot]")
	fake.AddLabel(shop, 41, "mobius:question", testkit.AppSlug+"[bot]")
	fake.AddComment(shop, 41, testkit.AppSlug+"[bot]", "Cents or dollars?")
	if _, err := server.DB.Exec(`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at) VALUES ('owner/shop', 'owner/shop', 41, 12, 'needs_human', '2026-10-04T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	fake.AddCommentAfterList(shop, 41, testkit.AppSlug+"[bot]", "The task waits for CI.\n\n<!-- mobius:write 9 -->")

	fake.AddComment(shop, 41, "owner", "Cents.")

	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:question") })
	testkit.WaitFor(t, func() bool { return liveTaskOf(t, server, 41).State == "dispatched" })
}

func TestAPendingEditOfAnEarlierReviewRunStillChangesItsCommentWhenANewRunStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := seedReviewerAs(t, fake, options+hangs, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 100 })
	if number := fake.OpenPullRequest(shop, "Add plan model", "mobius/41"); number != 42 {
		t.Fatalf("pull request = %d", number)
	}
	testkit.WaitFor(t, func() bool { return len(roundComments(fake)) == 1 })
	first := len(roleSessions(t, server, engine.ReviewerRole))
	fake.FailUpdateComment(shop, 42, true)

	server.Engine.StopWorker(1)

	// The edit of the first run and the post of the second run wait for the update that GitHub fails.
	testkit.WaitFor(t, func() bool {
		return len(roleSessions(t, server, engine.ReviewerRole)) == first+1 && commentWrites(t, server) == 2
	})
	fake.FailUpdateComment(shop, 42, false)

	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 0 })
	comments := roundComments(fake)
	if len(comments) != 2 || !strings.HasPrefix(comments[0], "Review stopped, round 1 of 10") || comments[1] != "Review started, round 1 of 10" {
		t.Errorf("comments = %q", comments)
	}
	if got := outgoingChanges(t, server, "refused"); got != 0 {
		t.Errorf("refused outgoing changes = %d", got)
	}
}

func TestAnAbandonedReviewRunGetsItsStopEditWhileAnEditOfAnEarlierRunIsPending(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := seedReviewerAs(t, fake, options+hangs, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 100 })
	if number := fake.OpenPullRequest(shop, "Add plan model", "mobius/41"); number != 42 {
		t.Fatalf("pull request = %d", number)
	}
	testkit.WaitFor(t, func() bool { return len(roundComments(fake)) == 1 })
	first := len(roleSessions(t, server, engine.ReviewerRole))
	fake.FailUpdateComment(shop, 42, true)
	server.Engine.StopWorker(1)
	// The edit of the first run and the post of the second run wait for the update that GitHub fails.
	testkit.WaitFor(t, func() bool {
		return len(roleSessions(t, server, engine.ReviewerRole)) == first+1 && commentWrites(t, server) == 2
	})

	server.Engine.StopWorker(1)

	testkit.WaitFor(t, func() bool {
		return len(roleSessions(t, server, engine.ReviewerRole)) == first+2 && commentWrites(t, server) == 4
	})
	fake.FailUpdateComment(shop, 42, false)

	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 0 })
	comments := roundComments(fake)
	started, stopped := "Review started, round 1 of 10", "Review stopped, round 1 of 10"
	if len(comments) != 3 || !strings.HasPrefix(comments[0], stopped) || !strings.HasPrefix(comments[1], stopped) || comments[2] != started {
		t.Errorf("comments = %q", comments)
	}
}

func TestAReplyThatGitHubCreatedBeforeAFiveHundredIsNotPostedAgainAndItsThreadGetsResolved(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddPullRequest(shop, 45, "Add plan model")
	root := fake.AddReviewComment(shop, 45, 0, "owner", "Use cents.")
	fake.LoseReviewReplyAnswers(true)
	server, _ := connect(t, fake, call("reply_thread", "{ repository = \"owner/shop\", thread = "+itoa(root)+`, text = "Fixed in abc123." }`))
	addTask(t, server, fake, 41, 12, 45)

	session := run(t, server, leadSpec(t), "1. ")

	if got, want := reply(t, server, session), "Mobius posts the reply to "+itoa(root)+" later."; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
	if thread := fake.ReviewThread(shop, 45, root); len(thread.Comments) != 2 {
		t.Errorf("thread = %+v", thread)
	}
	fake.LoseReviewReplyAnswers(false)

	testkit.WaitFor(t, func() bool {
		return fake.ReviewThread(shop, 45, root).Resolved && outgoingChanges(t, server, "sent") == 1
	})
	waitForPolls(t, fake)
	thread := fake.ReviewThreadWithMarkers(shop, 45, root)
	if len(thread.Comments) != 2 || !strings.HasSuffix(thread.Comments[1].Body, "\n\n<!-- mobius:write 1 -->") {
		t.Errorf("thread = %+v", thread)
	}
}

func TestCommentPullRequestGivesALaterTextWhenTheSendAtOnceFailsAndALaterPollPostsTheComment(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddPullRequest(shop, 45, "Add plan model")
	fake.FailAddComment(shop, 45, true)
	server, _ := connect(t, fake, call("comment_pull_request", `{ repository = "owner/shop", n = 45, text = "Close this stale pull request." }`))
	addTask(t, server, fake, 41, 12, 45)

	session := run(t, server, leadSpec(t), "1. ")

	if got := reply(t, server, session); got != "Mobius posts the comment on #45 later." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.Comments(shop, 45); len(got) != 0 {
		t.Errorf("comments = %v", got)
	}
	fake.FailAddComment(shop, 45, false)

	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 1 })
	want := []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: marked("Close this stale pull request.", leadMark)}}
	if got := fake.Comments(shop, 45); !reflect.DeepEqual(got, want) {
		t.Errorf("comments = %v, want %v", got, want)
	}
}

func TestAReplyWhoseResolveGitHubRefusesDoesNotHoldTheLaterCommentOfThePullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddPullRequest(shop, 45, "Add plan model")
	root := fake.AddReviewComment(shop, 45, 0, "owner", "Use cents.")
	fake.FailResolveThreads(true)
	server, _ := connect(t, fake, call("reply_thread", "{ repository = \"owner/shop\", thread = "+itoa(root)+`, text = "Fixed in abc123." }`)+
		call("comment_pull_request", `{ repository = "owner/shop", n = 45, text = "Close this stale pull request." }`))
	addTask(t, server, fake, 41, 12, 45)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	if got, want := reply(t, server, session), "Replied to "+itoa(root)+".Commented on #45."; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
	if thread := fake.ReviewThread(shop, 45, root); thread.Resolved || len(thread.Comments) != 2 {
		t.Errorf("thread = %+v", thread)
	}
	want := []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: marked("Close this stale pull request.", leadMark)}}
	if got := fake.Comments(shop, 45); !reflect.DeepEqual(got, want) {
		t.Errorf("comments = %v, want %v", got, want)
	}
	if got := outgoingChanges(t, server, "sent"); got != 2 {
		t.Errorf("sent changes = %d, want 2", got)
	}
}

func TestAReplyWhoseResolveGetsARateLimitErrorIsSentAgainAndALaterPollResolvesTheThread(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddPullRequest(shop, 45, "Add plan model")
	root := fake.AddReviewComment(shop, 45, 0, "owner", "Use cents.")
	fake.RateLimitResolveThreads(true)
	server, _ := connect(t, fake, call("reply_thread", "{ repository = \"owner/shop\", thread = "+itoa(root)+`, text = "Fixed in abc123." }`))
	addTask(t, server, fake, 41, 12, 45)

	session := run(t, server, leadSpec(t), "1. ")

	if got, want := reply(t, server, session), "Mobius posts the reply to "+itoa(root)+" later."; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
	waitForPolls(t, fake)
	if thread := fake.ReviewThread(shop, 45, root); thread.Resolved || len(thread.Comments) != 2 {
		t.Errorf("thread = %+v", thread)
	}
	fake.RateLimitResolveThreads(false)

	testkit.WaitFor(t, func() bool {
		return fake.ReviewThread(shop, 45, root).Resolved && outgoingChanges(t, server, "sent") == 1
	})
	if thread := fake.ReviewThread(shop, 45, root); len(thread.Comments) != 2 {
		t.Errorf("thread = %+v", thread)
	}
}
