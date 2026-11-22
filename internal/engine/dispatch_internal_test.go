package engine

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const app = "mobius-app[bot]"

var appRepository = github.Repository{FullName: "owner/shop", AppSlug: "mobius-app"}

func trustingEngine() *Engine {
	return New(nil, nil, &config.Config{TrustedUsers: []string{"owner"}, TrustedBots: []string{"coderabbitai[bot]"}}, Agents{})
}

func labelEvent(event, label, actor string) *gh.IssueEvent {
	return &gh.IssueEvent{Event: &event, Actor: &gh.User{Login: &actor}, Label: &gh.Label{Name: label}}
}

func storingEngine(t *testing.T) *Engine {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, nil, &config.Config{TrustedUsers: []string{"owner"}, TrustedBots: []string{"coderabbitai[bot]"}}, Agents{})
}

func comment(author string, viaApp string, seconds int64) *gh.IssueComment {
	c := &gh.IssueComment{User: &gh.User{Login: &author}, Body: new("Use cents."), CreatedAt: &gh.Timestamp{Time: time.Unix(seconds, 0)}}
	if viaApp != "" {
		c.PerformedViaGithubApp = &gh.App{Slug: &viaApp}
	}
	return c
}

func TestACommentOfATrustedUserIsAnEvent(t *testing.T) {
	t.Parallel()
	e := storingEngine(t)
	for _, c := range []*gh.IssueComment{comment("Owner", "", 0), comment("owner", "other-app", 0)} {
		if isEvent, err := e.commentIsEvent(t.Context(), appRepository, c); err != nil || !isEvent {
			t.Errorf("comment of %s via %s is no event", c.GetUser().GetLogin(), c.GetPerformedViaGithubApp().GetSlug())
		}
	}
}

func TestACommentOfTheLeadABotOrAStrangerIsNoEvent(t *testing.T) {
	t.Parallel()
	e := storingEngine(t)
	for _, c := range []*gh.IssueComment{comment("owner", "mobius-app", 0), comment("coderabbitai[bot]", "", 0), comment(app, "", 0), comment("mallory", "", 0)} {
		if isEvent, err := e.commentIsEvent(t.Context(), appRepository, c); err != nil || isEvent {
			t.Errorf("comment of %s via %s is an event", c.GetUser().GetLogin(), c.GetPerformedViaGithubApp().GetSlug())
		}
	}
}

func TestACommentOfATrustedUserThroughTheMobiusAppIsAnEventOnlyWhenItIsAnOwnerAnswer(t *testing.T) {
	t.Parallel()
	e := storingEngine(t)
	askedAt(t, e, 1)
	item, err := e.queries.GetNewestInboxItem(t.Context(), store.GetNewestInboxItemParams{Repository: "owner/shop", Issue: 41, Kind: questionKind})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{3, 7} {
		if err := e.queries.SetAnswerComment(t.Context(), store.SetAnswerCommentParams{Comment: id, ID: item.ID}); err != nil {
			t.Fatal(err)
		}
	}
	onIssue := func(c *gh.IssueComment, id, issue int64) *gh.IssueComment {
		c.ID = &id
		c.IssueURL = new("https://api.github.com/repos/owner/shop/issues/" + strconv.FormatInt(issue, 10))
		return c
	}
	answer := onIssue(comment("owner", "mobius-app", 0), 7, 41)
	firstAnswer := onIssue(comment("owner", "mobius-app", 0), 3, 41)
	other := onIssue(comment("owner", "mobius-app", 0), 8, 41)
	stranger := onIssue(comment("mallory", "mobius-app", 0), 7, 41)
	onAnotherIssue := onIssue(comment("owner", "mobius-app", 0), 7, 42)

	for _, c := range []struct {
		comment *gh.IssueComment
		want    bool
	}{{answer, true}, {firstAnswer, true}, {other, false}, {stranger, false}, {onAnotherIssue, false}} {
		if isEvent, err := e.commentIsEvent(t.Context(), appRepository, c.comment); err != nil || isEvent != c.want {
			t.Errorf("comment %d of %s on %s: event = %t, err = %v", c.comment.GetID(), c.comment.GetUser().GetLogin(), c.comment.GetIssueURL(), isEvent, err)
		}
	}
	elsewhere := github.Repository{FullName: "owner/other", AppSlug: "mobius-app"}
	if isEvent, err := e.commentIsEvent(t.Context(), elsewhere, answer); err != nil || isEvent {
		t.Errorf("the answer is an event of another repository: %t, %v", isEvent, err)
	}
}

func askedAt(t *testing.T, e *Engine, seconds int64) {
	t.Helper()
	_, err := e.queries.AddInboxItem(t.Context(), store.AddInboxItemParams{
		Kind:                 questionKind,
		Organization:         "owner",
		Repository:           "owner/shop",
		WorkstreamRepository: "owner/shop",
		Workstream:           12,
		Issue:                41,
		Time:                 time.Unix(seconds, 0).UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAReplyAfterTheQuestionAnswersIt(t *testing.T) {
	t.Parallel()
	e := storingEngine(t)
	askedAt(t, e, 2)
	comments := []*gh.IssueComment{comment("owner", "", 1), comment("owner", "", 3), comment("owner", "", 4)}

	replies, answered, err := e.replies(t.Context(), appRepository, 41, comments)

	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 3 || !answered {
		t.Errorf("replies = %d, answered = %t", len(replies), answered)
	}
}

func TestAReplyBeforeTheQuestionDoesNotAnswerIt(t *testing.T) {
	t.Parallel()
	e := storingEngine(t)
	askedAt(t, e, 2)
	comments := []*gh.IssueComment{comment("owner", "", 1), comment("mallory", "", 3)}

	replies, answered, err := e.replies(t.Context(), appRepository, 41, comments)

	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || answered {
		t.Errorf("replies = %d, answered = %t", len(replies), answered)
	}
}

func TestACommentOfTheMobiusAppWithNoWriteMarkerDoesNotMoveTheQuestionTime(t *testing.T) {
	t.Parallel()
	e := storingEngine(t)
	askedAt(t, e, 1)
	comments := []*gh.IssueComment{comment("owner", "", 2), comment(app, "", 3)}

	replies, answered, err := e.replies(t.Context(), appRepository, 41, comments)

	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || !answered {
		t.Errorf("replies = %d, answered = %t", len(replies), answered)
	}
	trusted, at, err := e.answerReplies(t.Context(), appRepository, 41, comments)
	if err != nil {
		t.Fatal(err)
	}
	if len(trusted) != 1 || !at.Equal(time.Unix(1, 0)) {
		t.Errorf("trusted = %d, question time = %s", len(trusted), at)
	}
}

func TestTheQuestionTimeIsTheTimeOfTheInboxItemWhileTheSendWaits(t *testing.T) {
	t.Parallel()
	e := storingEngine(t)
	if at, err := e.questionTime(t.Context(), appRepository, 41, nil); err != nil || !at.IsZero() {
		t.Errorf("question time with no item = %s, %v", at, err)
	}
	askedAt(t, e, 5)

	at, err := e.questionTime(t.Context(), appRepository, 41, []*gh.IssueComment{comment(app, "", 9)})

	if err != nil || !at.Equal(time.Unix(5, 0)) {
		t.Errorf("question time = %s, %v", at, err)
	}
}

func TestTheQuestionTimeIsTheGitHubTimeOfTheQuestionComment(t *testing.T) {
	t.Parallel()
	e := storingEngine(t)
	askedAt(t, e, 5)
	err := e.queries.SetQuestionComment(t.Context(), store.SetQuestionCommentParams{Comment: 7, Repository: "owner/shop", Issue: 41, Since: time.Unix(0, 0).UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	question := comment(app, "", 3)
	question.ID = new(int64(7))

	at, err := e.questionTime(t.Context(), appRepository, 41, []*gh.IssueComment{comment(app, "", 9), question})

	if err != nil || !at.Equal(time.Unix(3, 0)) {
		t.Errorf("question time = %s, %v", at, err)
	}
}

func TestACommentOfATrustedUserCounts(t *testing.T) {
	t.Parallel()
	e := trustingEngine()
	if !e.trustedUser("Owner") || !e.trustedUser("owner") {
		t.Error("the user is not trusted")
	}
}

func TestACommentOfABotOrAStrangerDoesNotCount(t *testing.T) {
	t.Parallel()
	e := trustingEngine()
	for _, login := range []string{"coderabbitai[bot]", app, "mallory"} {
		if e.trustedUser(login) {
			t.Errorf("%s is trusted", login)
		}
	}
}

func TestTheEventTextHasTheTimeTheKindTheIssueAndTheQuotedText(t *testing.T) {
	t.Parallel()
	issue := &gh.Issue{Number: new(42), Title: new("Plan API")}

	got := eventText(time.Date(2026, 9, 27, 14, 2, 0, 0, time.UTC), "comment on", "#42", issue, "owner", "Use cents.\nRound down.")

	if want := "2026-09-27 14:02 UTC comment on #42 \"Plan API\" by @owner:\n\n> Use cents.\n> Round down."; got != want {
		t.Errorf("text = %q", got)
	}
}
