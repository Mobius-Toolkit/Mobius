package engine_test

import (
	"net/http"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// needsHumanWithLabels seeds a task in needs_human with a pull request, and waits until the issue and the pull request
// have mobius:needs-human. The Workstream has mobius:autopilot when autopilot is true.
func needsHumanWithLabels(t *testing.T, fake *testkit.FakeGitHub, autopilot bool) *testserver.Server {
	t.Helper()
	server, _ := seedWaiting(t, fake, "needs_human")
	if autopilot {
		fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	}
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") && pullRequestHasNeedsHuman(fake) })
	waitForPolls(t, fake)
	return server
}

func implementerRounds(t *testing.T, server *testserver.Server) int {
	t.Helper()
	return len(roleSessions(t, server, engine.ImplementerRole))
}

func hasNoEffect(t *testing.T, fake *testkit.FakeGitHub, server *testserver.Server) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") && pullRequestHasNeedsHuman(fake) })
	waitForPolls(t, fake)
	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if rounds := implementerRounds(t, server); rounds != 0 {
		t.Errorf("Implementers = %d", rounds)
	}
}

func TestARemovalOfNeedsHumanFromTheIssueByATrustedUserContinuesTheTaskAndRemovesItFromThePullRequest(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := needsHumanWithLabels(t, fake, false)

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")

	testkit.WaitFor(t, func() bool { return implementerRounds(t, server) == 1 })
	if pullRequestHasNeedsHuman(fake) || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v, pull request labels = %v", fake.Labels(shop, 41), fake.Labels(shop, pullRequestNumber))
	}
}

func TestARemovalOfNeedsHumanFromThePullRequestByATrustedUserContinuesTheTaskAndRemovesItFromTheIssue(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := needsHumanWithLabels(t, fake, false)

	fake.RemoveLabel(shop, pullRequestNumber, "mobius:needs-human", "owner")

	testkit.WaitFor(t, func() bool { return implementerRounds(t, server) == 1 })
	if pullRequestHasNeedsHuman(fake) || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v, pull request labels = %v", fake.Labels(shop, 41), fake.Labels(shop, pullRequestNumber))
	}
}

func TestARemovalOfNeedsHumanByTheAppContinuesTheTaskWhenAutopilotIsOn(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := needsHumanWithLabels(t, fake, true)

	fake.RemoveLabel(shop, 41, "mobius:needs-human", app)

	testkit.WaitFor(t, func() bool { return implementerRounds(t, server) == 1 })
	if pullRequestHasNeedsHuman(fake) || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v, pull request labels = %v", fake.Labels(shop, 41), fake.Labels(shop, pullRequestNumber))
	}
}

func TestARemovalOfNeedsHumanByTheAppHasNoEffectWhenAutopilotIsOff(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := needsHumanWithLabels(t, fake, false)

	fake.RemoveLabel(shop, 41, "mobius:needs-human", app)

	hasNoEffect(t, fake, server)
}

func TestARemovalOfNeedsHumanByAnUntrustedUserHasNoEffect(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := needsHumanWithLabels(t, fake, false)

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "stranger")
	fake.RemoveLabel(shop, pullRequestNumber, "mobius:needs-human", "stranger")

	hasNoEffect(t, fake, server)
}

// moveToNeedsHumanLater makes the move of the task to needs_human newer than each label event that the test writes
// after the call.
func moveToNeedsHumanLater(t *testing.T, server *testserver.Server) {
	t.Helper()
	if _, err := server.DB.Exec("UPDATE tasks SET needs_human_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '+1 hour') WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
}

func TestARemovalOfNeedsHumanFromBeforeTheMoveToNeedsHumanHasNoEffect(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.FailAddLabels(shop, 41, true)
	fake.FailAddLabels(shop, pullRequestNumber, true)
	server, _ := seedWaiting(t, fake, "needs_human")
	moveToNeedsHumanLater(t, server)
	fake.AddLabel(shop, pullRequestNumber, "mobius:needs-human", app)
	fake.RemoveLabel(shop, pullRequestNumber, "mobius:needs-human", app)
	fake.AddLabel(shop, 41, "mobius:needs-human", app)
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	waitForPolls(t, fake)

	fake.FailAddLabels(shop, pullRequestNumber, false)

	hasNoEffect(t, fake, server)
}

func TestARemovalOfNeedsHumanFromBeforeTheMoveHasNoEffectWhenTheTaskHasNoPullRequest(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := needsHumanWithLabels(t, fake, false)
	if _, err := server.DB.Exec("UPDATE tasks SET pull_request = NULL WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	moveToNeedsHumanLater(t, server)
	fake.FailAddLabels(shop, 41, true)
	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")
	waitForPolls(t, fake)

	fake.FailAddLabels(shop, 41, false)

	hasNoEffect(t, fake, server)
}

func TestARemovalOfNeedsHumanFromBeforeTheMoveHasNoEffectWhenTheIssueLacksTheLabelAfterTheMove(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.FailAddLabels(shop, 41, true)
	fake.FailAddLabels(shop, pullRequestNumber, true)
	server, _ := seedWaiting(t, fake, "needs_human")
	moveToNeedsHumanLater(t, server)
	fake.AddLabel(shop, pullRequestNumber, "mobius:needs-human", app)
	fake.AddLabel(shop, 41, "mobius:needs-human", app)
	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")
	waitForPolls(t, fake)

	fake.FailAddLabels(shop, 41, false)

	hasNoEffect(t, fake, server)
}

func TestResumeRemovesNeedsHumanFromTheIssueAndFromThePullRequestAsTheOwnerAndTheNextPollContinuesTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := needsHumanWithLabels(t, fake, false)
	authorize(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/resume", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool { return implementerRounds(t, server) == 1 })
	if pullRequestHasNeedsHuman(fake) || hasLabel(fake, "mobius:needs-human") || hasLabel(fake, "mobius:ready") {
		t.Errorf("labels = %v, pull request labels = %v", fake.Labels(shop, 41), fake.Labels(shop, pullRequestNumber))
	}
	for _, number := range []int64{41, pullRequestNumber} {
		if got := fake.LabelActor(shop, number, "mobius:needs-human"); got != "owner" {
			t.Errorf("actor of mobius:needs-human on #%d = %s", number, got)
		}
	}
}

func TestMarkReadyRemovesNeedsHumanFromTheIssueAndFromThePullRequestOfATaskInNeedsHumanUnderAutopilotAndTheNextPollContinuesTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"Start #41.\"\ncall = { tool = \"mark_ready\", arguments = { n = 41 } }\n\n"
	server, _ := seedWaitingWith(t, fake, "needs_human", lead)
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") && pullRequestHasNeedsHuman(fake) })
	waitForPolls(t, fake)

	sendChat(t, server, leadChat, "Start #41.")

	testkit.WaitFor(t, func() bool { return implementerRounds(t, server) == 1 })
	if pullRequestHasNeedsHuman(fake) || hasLabel(fake, "mobius:needs-human") || hasLabel(fake, "mobius:ready") {
		t.Errorf("labels = %v, pull request labels = %v", fake.Labels(shop, 41), fake.Labels(shop, pullRequestNumber))
	}
}
