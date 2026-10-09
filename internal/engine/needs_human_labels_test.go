package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestAPollGivesATaskInNeedsHumanTheLabelsAfterAFailedHandToHuman(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	goFile := filepath.Join(t.TempDir(), "go")
	server := startHandToHuman(t, fake, fmt.Sprintf("[[prompts]]\nshell = \"while [ ! -e '%s' ]; do sleep 0.1; done; kill -9 $PPID; sleep 5\"\n", goFile))
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "working" })
	fake.FailAddLabels(shop, 41, true)
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	waitForPolls(t, fake)
	if hasLabel(fake, "mobius:needs-human") {
		t.Fatalf("labels = %v", fake.Labels(shop, 41))
	}
	comments, items := len(fake.Comments(shop, 41)), len(inbox(t, server))

	fake.FailAddLabels(shop, 41, false)

	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") })
	if labels := fake.Labels(shop, 41); slices.Contains(labels, "mobius:working") || slices.Contains(labels, "mobius:review") {
		t.Errorf("labels = %v", labels)
	}
	if len(fake.Comments(shop, 41)) != comments || len(inbox(t, server)) != items {
		t.Errorf("comments = %+v, inbox = %+v", fake.Comments(shop, 41), inbox(t, server))
	}
}

func TestAPollRemovesTheWorkingAndReviewLabelsOfATaskInNeedsHuman(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := seedWaiting(t, fake, "needs_human")
	fake.AddLabel(shop, 41, "mobius:review", testkit.AppSlug+"[bot]")

	testkit.WaitFor(t, func() bool {
		return hasLabel(fake, "mobius:needs-human") && !hasLabel(fake, "mobius:working") && !hasLabel(fake, "mobius:review")
	})
	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
}

func TestAPollWritesNoLabelOfATaskInNeedsHumanWithTheRightLabels(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := handedToHuman(t, fake, dies)
	waitForPolls(t, fake)
	writes := fake.LabelWrites(shop, 41)

	waitForPolls(t, fake)

	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if now := fake.LabelWrites(shop, 41); now != writes {
		t.Errorf("label writes = %d, want %d", now, writes)
	}
}

func TestAPollKeepsTheNeedsHumanLabelOffATaskInNeedsHumanWithTheReadyLabel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	// The prompt of a new session has the earlier events in its history, so the rule of the newest event comes first.
	lead := "[[prompts]]\nwhen = \"resume of #41\"\nreply = [\"I check the task.\"]\n\n" + leadStarts
	server, _ := connectTask(t, fake, lead, dies, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	implementers := len(roleSessions(t, server, engine.ImplementerRole))

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" && !hasLabel(fake, "mobius:ready") })
	waitForPolls(t, fake)
	if hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if pullRequests := fake.PullRequests(shop); len(pullRequests) != 0 {
		t.Errorf("pull requests = %+v", pullRequests)
	}
	if got := len(roleSessions(t, server, engine.ImplementerRole)); got != implementers {
		t.Errorf("Implementers = %d, want %d", got, implementers)
	}
}
