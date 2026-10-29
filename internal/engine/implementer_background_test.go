package engine_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const implementerWaitPrompt = "Before the local check"

const backgroundDone = "call = { tool = \"background_state\", arguments = { state = \"done\" } }\n"

// Serial: the test changes backgroundCheckInterval.
func TestAnImplementerWithABackgroundAgentGetsTheWaitPromptBeforeTheLocalCheckAndPushesTheResult(t *testing.T) {
	shortBackgroundCheck(t, 50*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	implementer := "[[prompts]]\nupdates = ['" + asyncAgentStart + "']\nreply = [\"A helper still runs.\"]\n\n" +
		"[[prompts]]\nwhen = \"" + implementerWaitPrompt + "\"\n" + commitCents + backgroundDone
	server, _ := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.SetCheck(shop, "grep -q cents plan.txt")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	runs := approvalCheckRuns(t, server, fake)
	if len(runs) != 1 || runs[0].HeadSHA != head(t, fake, "mobius/41") {
		t.Errorf("check runs = %+v", runs)
	}
	if plan := testkit.Git(t, fake.Remote(shop), "show", "mobius/41:plan.txt"); plan != "cents" {
		t.Errorf("plan.txt = %s", plan)
	}
	session := endedImplementers(t, server, 1)[0]
	if session.EndReason.String != "done" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 2 || !strings.Contains(prompts[1], implementerWaitPrompt) {
		t.Fatalf("prompts = %q", prompts)
	}
	if checks := rows(t, server, session.ID, "check"); len(checks) != 2 {
		t.Errorf("checks = %+v", checks)
	}
}

func TestAnImplementerWithNoBackgroundTaskGetsNoWaitPrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)
	session := endedImplementers(t, server, 1)[0]
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 || slices.ContainsFunc(prompts, func(prompt string) bool { return strings.Contains(prompt, implementerWaitPrompt) }) {
		t.Errorf("prompts = %q", prompts)
	}
}

// Serial: the test changes backgroundCheckInterval.
func TestACannotDoInTheWaitTurnEndsTheSessionWithTheReasonAndPushesNothing(t *testing.T) {
	shortBackgroundCheck(t, 50*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"cannot_do on #41\"\nreply = [\"ok\"]\n\n" + leadStarts
	implementer := "[[prompts]]\nupdates = ['" + asyncAgentStart + "']\nreply = [\"A helper still runs.\"]\n\n" +
		"[[prompts]]\nwhen = \"" + implementerWaitPrompt + "\"\n" + cannotDoCall
	server, _ := connectTask(t, fake, lead, implementer, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " cannot_do on #41 \"Add plan model\" by the Implementer:\n\n> The plan table does not exist.")
	session := endedImplementers(t, server, 1)[0]
	if session.EndReason.String != "cannot_do" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 2 {
		t.Errorf("prompts = %q", prompts)
	}
	if len(fake.PullRequests(shop)) != 0 || len(fake.CheckRuns(shop)) != 0 {
		t.Errorf("pull requests = %+v, check runs = %+v", fake.PullRequests(shop), fake.CheckRuns(shop))
	}
}
