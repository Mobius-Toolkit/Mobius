package engine

import (
	"path/filepath"
	"testing"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const app = "mobius-app[bot]"

var appRepository = github.Repository{FullName: "owner/shop", AppSlug: "mobius-app"}

func storingEngine(t *testing.T) *Engine {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, nil, &config.Config{TrustedUsers: []string{"owner"}, TrustedBots: []string{"coderabbitai[bot]"}}, Agents{})
}

func TestTheReadyActorIsTheActorOfTheLastReadyLabel(t *testing.T) {
	t.Parallel()
	events := []*gh.IssueEvent{
		labelEvent("labeled", "mobius:ready", "mallory"),
		labelEvent("unlabeled", "mobius:ready", "mallory"),
		labelEvent("labeled", "mobius:ready", "owner"),
		labelEvent("labeled", "bug", "mallory"),
		labelEvent("unlabeled", "mobius:ready", "mallory"),
	}

	if actor, ok := readyActor(events, app); actor != "owner" || !ok {
		t.Errorf("actor = %s, %t", actor, ok)
	}
}

func TestAfterATriageTheActorOfTheFirstReadyLabelCounts(t *testing.T) {
	t.Parallel()
	events := []*gh.IssueEvent{
		labelEvent("labeled", "mobius:ready", "owner"),
		labelEvent("labeled", "mobius:no-workstream", app),
		labelEvent("unlabeled", "mobius:ready", app),
		labelEvent("unlabeled", "mobius:no-workstream", app),
		labelEvent("labeled", "mobius:ready", app),
	}

	if actor, ok := readyActor(events, app); actor != "owner" || !ok {
		t.Errorf("actor = %s, %t", actor, ok)
	}
}

func TestAnOldTriageOrATriageOfAPersonDoesNotCount(t *testing.T) {
	t.Parallel()
	old := []*gh.IssueEvent{
		labelEvent("labeled", "mobius:ready", "owner"),
		labelEvent("labeled", "mobius:no-workstream", app),
		labelEvent("unlabeled", "mobius:no-workstream", app),
		labelEvent("labeled", "mobius:ready", app),
		labelEvent("unlabeled", "mobius:ready", app),
		labelEvent("labeled", "mobius:ready", app),
	}
	person := []*gh.IssueEvent{
		labelEvent("labeled", "mobius:ready", "owner"),
		labelEvent("labeled", "mobius:no-workstream", "mallory"),
		labelEvent("unlabeled", "mobius:no-workstream", app),
		labelEvent("labeled", "mobius:ready", app),
	}

	for _, events := range [][]*gh.IssueEvent{old, person} {
		if actor, ok := readyActor(events, app); actor != app || !ok {
			t.Errorf("actor = %s, %t", actor, ok)
		}
	}
}

func TestAReadyLabelOfTheMobiusAppWithNoTriageHasTheMobiusAppAsActor(t *testing.T) {
	t.Parallel()
	if actor, ok := readyActor([]*gh.IssueEvent{labelEvent("labeled", "mobius:ready", app)}, app); actor != app || !ok {
		t.Errorf("actor = %s, %t", actor, ok)
	}
}

func TestAnIssueWithNoReadyLabelEventHasNoReadyActor(t *testing.T) {
	t.Parallel()
	if actor, ok := readyActor([]*gh.IssueEvent{labelEvent("labeled", "bug", "owner")}, app); ok {
		t.Errorf("actor = %s", actor)
	}
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
	answer := comment("owner", "mobius-app", 0)
	answer.ID = new(int64(7))
	if err := e.queries.AddOwnerAnswer(t.Context(), store.AddOwnerAnswerParams{Repository: "owner/shop", CommentID: 7}); err != nil {
		t.Fatal(err)
	}
	other := comment("owner", "mobius-app", 0)
	other.ID = new(int64(8))
	stranger := comment("mallory", "mobius-app", 0)
	stranger.ID = new(int64(7))

	for _, c := range []struct {
		comment *gh.IssueComment
		want    bool
	}{{answer, true}, {other, false}, {stranger, false}} {
		if isEvent, err := e.commentIsEvent(t.Context(), appRepository, c.comment); err != nil || isEvent != c.want {
			t.Errorf("comment %d of %s: event = %t, err = %v", c.comment.GetID(), c.comment.GetUser().GetLogin(), isEvent, err)
		}
	}
	elsewhere := github.Repository{FullName: "owner/other", AppSlug: "mobius-app"}
	if isEvent, err := e.commentIsEvent(t.Context(), elsewhere, answer); err != nil || isEvent {
		t.Errorf("the answer is an event of another repository: %t, %v", isEvent, err)
	}
}

func TestAReplyAfterTheLastCommentOfTheMobiusAppAnswersIt(t *testing.T) {
	t.Parallel()
	comments := []*gh.IssueComment{comment("owner", "", 1), comment(app, "", 2), comment("owner", "", 3)}

	replies, answered, err := storingEngine(t).replies(t.Context(), appRepository, comments)

	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 2 || !answered {
		t.Errorf("replies = %d, answered = %t", len(replies), answered)
	}
}

func TestAReplyBeforeTheLastCommentOfTheMobiusAppDoesNotAnswerIt(t *testing.T) {
	t.Parallel()
	comments := []*gh.IssueComment{comment("owner", "", 1), comment(app, "", 2), comment("mallory", "", 3)}

	replies, answered, err := storingEngine(t).replies(t.Context(), appRepository, comments)

	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || answered {
		t.Errorf("replies = %d, answered = %t", len(replies), answered)
	}
}

func TestAReplyBeforeAStopCommentOfTheMobiusAppAnswersTheQuestion(t *testing.T) {
	t.Parallel()
	stop := comment(app, "", 3)
	stop.Body = new("CI failed." + nextStepMarker + "Resume.")
	lost := comment(app, "", 4)
	lost.Body = new(lostText)
	comments := []*gh.IssueComment{comment(app, "", 1), comment("owner", "", 2), stop, lost}

	replies, answered, err := storingEngine(t).replies(t.Context(), appRepository, comments)

	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || !answered {
		t.Errorf("replies = %d, answered = %t", len(replies), answered)
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
