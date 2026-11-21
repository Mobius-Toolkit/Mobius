package engine_test

import (
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// stateLabelWrites counts the own writes of the send of the state labels for the issue or pull request number, for the
// label and the change, labeled or unlabeled.
func stateLabelWrites(t *testing.T, server *testserver.Server, number int64, label, change string) int {
	t.Helper()
	return countRows(t, server, `SELECT count(*) FROM own_writes WHERE field = 'state_labels' AND repository = ? AND number = ?
		AND json_extract(metadata, '$.label') = ? AND json_extract(metadata, '$.change') = ?`, shop, number, label, change)
}

// waitForSentChanges waits until the send has written each outgoing change. A person who removes a label before that
// point races with a later send that writes the label again.
func waitForSentChanges(t *testing.T, server *testserver.Server) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE status = 'pending'") == 0
	})
}

// moveToNeedsHuman moves the live task of #41 to needs_human with SQL, and gives #41 the label that the send of the
// state labels writes after a move of the engine.
func moveToNeedsHuman(t *testing.T, fake *testkit.FakeGitHub, server *testserver.Server) {
	t.Helper()
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'needs_human' WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}
	fake.AddLabel(shop, 41, "mobius:needs-human", testkit.AppSlug+"[bot]")
}

func TestADispatchGivesTheIssueTheWorkingLabelThroughTheSendAsAnOwnWrite(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	dispatchTask(fake, 41, "Add plan model")

	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") })
	if got := fake.LabelActor(shop, 41, "mobius:working"); got != app {
		t.Errorf("actor of mobius:working = %s", got)
	}
	if got := stateLabelWrites(t, server, 41, "mobius:working", "labeled"); got != 1 {
		t.Errorf("own writes of mobius:working = %d, want 1", got)
	}
}

func TestAStopToNeedsHumanGivesTheIssueAndThePullRequestTheLabelAndTheEndOfTheTaskRemovesIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := startReady(t, fake, "", mergesCents)
	fake.SetCreatedAt(shop, pullRequestNumber, 0)
	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")

	testkit.WaitFor(t, func() bool {
		return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") && pullRequestHasNeedsHuman(fake)
	})

	if hasLabel(fake, "mobius:working") || hasLabel(fake, "mobius:review") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	for _, number := range []int64{41, pullRequestNumber} {
		if got := stateLabelWrites(t, server, number, "mobius:needs-human", "labeled"); got != 1 {
			t.Errorf("own writes of the add on #%d = %d, want 1", number, got)
		}
	}

	fake.MergePullRequest(shop, pullRequestNumber)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	testkit.WaitFor(t, func() bool { return !hasLabel(fake, "mobius:needs-human") && !pullRequestHasNeedsHuman(fake) })
	if labels := fake.Labels(shop, 41); slices.Contains(labels, "mobius:working") || slices.Contains(labels, "mobius:review") {
		t.Errorf("labels = %v", labels)
	}
	for _, number := range []int64{41, pullRequestNumber} {
		if got := stateLabelWrites(t, server, number, "mobius:needs-human", "unlabeled"); got != 1 {
			t.Errorf("own writes of the removal on #%d = %d, want 1", number, got)
		}
	}
}

func TestAskGivesTheIssueTheQuestionLabelAndCloseQuestionRemovesItAsOwnWrites(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "answer of #41"
reply = ["Seen"]

[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 41), "mobius:question") })
	if got := stateLabelWrites(t, server, 41, "mobius:question", "labeled"); got != 1 {
		t.Errorf("own writes of the add = %d, want 1", got)
	}

	sendChat(t, server, leadChat, "Dollars were chosen")

	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:question") })
	if got := stateLabelWrites(t, server, 41, "mobius:question", "unlabeled"); got != 1 {
		t.Errorf("own writes of the removal = %d, want 1", got)
	}
	waitForNoItem(t, server, "question")
}

func TestALostTaskGetsTheNeedsHumanLabelThroughTheSend(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddLabel(shop, 41, "mobius:working", app)

	server := startCopiedWithLead(t, fake)

	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") && !hasLabel(fake, "mobius:working") })
	waitForItem(t, server, "lost")
	if got := stateLabelWrites(t, server, 41, "mobius:needs-human", "labeled"); got != 1 {
		t.Errorf("own writes of the add = %d, want 1", got)
	}
	if got := stateLabelWrites(t, server, 41, "mobius:working", "unlabeled"); got != 1 {
		t.Errorf("own writes of the removal = %d, want 1", got)
	}
}
