package engine_test

import (
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// startEnded starts a server with an ended task for #41. #41 has the labels before the first poll.
func startEnded(t *testing.T, fake *testkit.FakeGitHub, labels ...string) *testserver.Server {
	t.Helper()
	dataDir := t.TempDir()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	for _, label := range labels {
		fake.AddLabel(shop, 41, label, testkit.AppSlug+"[bot]")
	}
	testkit.InstallFakeAgent(t, dataDir, options)
	seed(t, dataDir, `INSERT INTO tasks (id, repository, workstream_repository, issue, workstream, state, dispatched_at, state_at) VALUES (1, 'owner/shop', 'owner/shop', 41, 12, 'ended', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z')`)
	server := startServerWith(t, fake, testserver.Config(t, dataDir), "")
	server.WaitForFirstPoll(t, shop)
	return server
}

func TestAFailedAddOfTheWorkingLabelAfterThePassedCIIsRepairedAtTheNextPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, id := stoppedOnFailedCI(t, fake, noChange)
	fake.FailAddLabels(shop, 41, true)

	fake.SetCheckRunStatus(id, "completed", "success")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" })
	waitForPolls(t, fake)
	if hasLabel(fake, "mobius:working") {
		t.Fatalf("labels = %v", fake.Labels(shop, 41))
	}

	fake.FailAddLabels(shop, 41, false)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	if labels := fake.Labels(shop, 41); !slices.Equal(labels, []string{"mobius:working"}) {
		t.Errorf("labels = %v", labels)
	}
}

func TestAPollRemovesAStaleNeedsHumanLabelFromTheIssueOfATaskInChecks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := seedWaiting(t, fake, "checks")

	fake.AddLabel(shop, 41, "mobius:needs-human", testkit.AppSlug+"[bot]")

	testkit.WaitFor(t, func() bool { return !hasLabel(fake, "mobius:needs-human") })
	if labels := fake.Labels(shop, 41); !slices.Equal(labels, []string{"mobius:working"}) {
		t.Errorf("labels = %v", labels)
	}
	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s", state)
	}
}

func TestAPollRemovesTheStateLabelsOfAnIssueWithAnEndedTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	startEnded(t, fake)

	for _, label := range []string{"mobius:working", "mobius:review", "mobius:question", "mobius:needs-human"} {
		fake.AddLabel(shop, 41, label, testkit.AppSlug+"[bot]")
	}

	testkit.WaitFor(t, func() bool { return len(fake.Labels(shop, 41)) == 0 })
}

func TestAPollKeepsTheNeedsHumanLabelOfAnIssueWithAnOpenLostItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startEnded(t, fake, "mobius:working")
	testkit.WaitFor(t, func() bool { return len(inbox(t, server)) == 1 && hasLabel(fake, "mobius:needs-human") })

	fake.AddLabel(shop, 41, "mobius:question", testkit.AppSlug+"[bot]")

	testkit.WaitFor(t, func() bool { return !hasLabel(fake, "mobius:question") })
	waitForPolls(t, fake)
	if labels := fake.Labels(shop, 41); !slices.Equal(labels, []string{"mobius:needs-human"}) {
		t.Errorf("labels = %v", labels)
	}
	if items := inbox(t, server); len(items) != 1 || items[0].Kind != "lost" {
		t.Errorf("inbox = %+v", items)
	}
}
