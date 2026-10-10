package engine

import (
	"testing"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
)

func trustingEngine() *Engine {
	return New(nil, nil, &config.Config{TrustedUsers: []string{"owner"}, TrustedBots: []string{"coderabbitai[bot]"}}, Agents{})
}

func labelEvent(event, label, actor string) *gh.IssueEvent {
	return &gh.IssueEvent{Event: &event, Actor: &gh.User{Login: &actor}, Label: &gh.Label{Name: label}}
}

func TestAutopilotOfATrustedUserCounts(t *testing.T) {
	t.Parallel()
	events := []*gh.IssueEvent{labelEvent("labeled", "mobius:autopilot", "Owner"), labelEvent("labeled", "bug", "mallory")}

	if !trustingEngine().addedByTrustedUser(events) {
		t.Error("autopilot is off")
	}
}

func TestAutopilotOfABotTheMobiusAppOrAStrangerDoesNotCount(t *testing.T) {
	t.Parallel()
	for _, actor := range []string{"coderabbitai[bot]", "mobius-app[bot]", "mallory"} {
		events := []*gh.IssueEvent{
			labelEvent("labeled", "mobius:autopilot", "owner"),
			labelEvent("unlabeled", "mobius:autopilot", actor),
			labelEvent("labeled", "mobius:autopilot", actor),
		}

		if trustingEngine().addedByTrustedUser(events) {
			t.Errorf("autopilot of %s is on", actor)
		}
	}
}
