package engine

import (
	"testing"
	"time"

	gh "github.com/google/go-github/v92/github"
)

const app = "mobius-app[bot]"

func TestTheReadyActorIsTheActorOfTheLastReadyLabel(t *testing.T) {
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
	if actor, ok := readyActor([]*gh.IssueEvent{labelEvent("labeled", "mobius:ready", app)}, app); actor != app || !ok {
		t.Errorf("actor = %s, %t", actor, ok)
	}
}

func TestAnIssueWithNoReadyLabelEventHasNoReadyActor(t *testing.T) {
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
	for _, c := range []*gh.IssueComment{comment("Owner", "", 0), comment("owner", "other-app", 0)} {
		if !trustingEngine().commentIsEvent("mobius-app", c) {
			t.Errorf("comment of %s via %s is no event", c.GetUser().GetLogin(), c.GetPerformedViaGithubApp().GetSlug())
		}
	}
}

func TestACommentOfTheLeadABotOrAStrangerIsNoEvent(t *testing.T) {
	for _, c := range []*gh.IssueComment{comment("owner", "mobius-app", 0), comment("coderabbitai[bot]", "", 0), comment(app, "", 0), comment("mallory", "", 0)} {
		if trustingEngine().commentIsEvent("mobius-app", c) {
			t.Errorf("comment of %s via %s is an event", c.GetUser().GetLogin(), c.GetPerformedViaGithubApp().GetSlug())
		}
	}
}

func TestAReplyAfterTheLastCommentOfTheMobiusAppAnswersIt(t *testing.T) {
	comments := []*gh.IssueComment{comment("owner", "", 1), comment(app, "", 2), comment("owner", "", 3)}

	replies, answered := trustingEngine().replies("mobius-app", comments, time.Time{})

	if len(replies) != 2 || !answered {
		t.Errorf("replies = %d, answered = %t", len(replies), answered)
	}
}

func TestAReplyBeforeTheLastCommentOfTheMobiusAppDoesNotAnswerIt(t *testing.T) {
	comments := []*gh.IssueComment{comment("owner", "", 1), comment(app, "", 2), comment("mallory", "", 3)}

	replies, answered := trustingEngine().replies("mobius-app", comments, time.Time{})

	if len(replies) != 1 || answered {
		t.Errorf("replies = %d, answered = %t", len(replies), answered)
	}
}

func TestOnlyACommentAfterTheCursorIsAReply(t *testing.T) {
	comments := []*gh.IssueComment{comment("owner", "", 1), comment("owner", "", 3)}

	replies, answered := trustingEngine().replies("mobius-app", comments, time.Unix(2, 0))

	if len(replies) != 1 || replies[0].GetCreatedAt().Unix() != 3 || !answered {
		t.Errorf("replies = %v, answered = %t", replies, answered)
	}
}

func TestANewCommentOfATrustedUserCounts(t *testing.T) {
	e := trustingEngine()
	if !e.newUserComment("Owner", time.Unix(3, 0), time.Unix(2, 0)) || !e.newUserComment("owner", time.Unix(1, 0), time.Time{}) {
		t.Error("the comment does not count")
	}
}

func TestAnOldCommentOrACommentOfABotOrAStrangerDoesNotCount(t *testing.T) {
	e := trustingEngine()
	for _, c := range []struct {
		login   string
		seconds int64
	}{{"owner", 2}, {"coderabbitai[bot]", 3}, {app, 3}, {"mallory", 3}} {
		if e.newUserComment(c.login, time.Unix(c.seconds, 0), time.Unix(2, 0)) {
			t.Errorf("the comment of %s counts", c.login)
		}
	}
}

func TestTheEventTextHasTheTimeTheKindTheIssueAndTheQuotedText(t *testing.T) {
	issue := &gh.Issue{Number: new(42), Title: new("Plan API")}

	got := eventText(time.Date(2026, 9, 27, 14, 2, 0, 0, time.UTC), "comment on", issue, "owner", "Use cents.\nRound down.")

	if want := "2026-09-27 14:02 UTC comment on #42 \"Plan API\" by @owner:\n\n> Use cents.\n> Round down."; got != want {
		t.Errorf("text = %q", got)
	}
}
