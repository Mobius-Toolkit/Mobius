package engine_test

import (
	"net/http"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// conflictAfterPress presses "Merge" on the item while the mergeable state of the pull request is unknown, so that the
// poll does not merge. Then it makes a merge conflict on the base branch. The poll starts a conflict round and
// dismisses the item.
func conflictAfterPress(t *testing.T, fake *testkit.FakeGitHub, server *testserver.Server, item inboxItem) {
	t.Helper()
	fake.SetMergeableUnknown(shop, 42, true)
	if status, body := merge(t, server, item.ID); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")
	fake.SetMergeableUnknown(shop, 42, false)
	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "ready for review")) == 0 })
}

func TestTheItemOfATaskThatLeftReadyForReviewAfterThePressIsDismissedAndTheNewItemIsMerging(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := readyToMergeWith(t, fake, "", mergesCents, keepSessionOpen)
	reached, release := fake.HoldMerge(shop, 42)

	conflictAfterPress(t, fake, server, item)

	waitForReadyEvents(t, server, 2)
	sendChat(t, server, leadChat, "Approve #41")
	next := waitForItem(t, server, "ready for review")
	if next.ID == item.ID || !next.Merging || next.MergeRefusal != "" {
		t.Errorf("item = %+v, first item = %d", next, item.ID)
	}
	<-reached
	release()

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	testkit.WaitFor(t, func() bool { return len(inbox(t, server)) == 0 })
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d", calls)
	}
	if count := approvals(fake); count != 1 {
		t.Errorf("approvals = %d", count)
	}
}

func TestTheItemOfATaskThatLeftReadyForReviewWithNoPressIsDismissedAndTheNewItemIsNotMerging(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := readyToMergeWith(t, fake, "", mergesCents, keepSessionOpen)

	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")

	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "ready for review")) == 0 })
	waitForReadyEvents(t, server, 2)
	sendChat(t, server, leadChat, "Approve #41")
	next := waitForItem(t, server, "ready for review")
	if next.ID == item.ID || next.Merging || next.MergeRefusal != "" {
		t.Errorf("item = %+v, first item = %d", next, item.ID)
	}
	if items := itemsOf(t, server, "ready for review"); len(items) != 1 {
		t.Errorf("items = %+v", items)
	}
}

func TestAJudgeThatRunsFromReadyForReviewAndStartsNoRoundKeepsTheItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, `call = { tool = "submit_verdicts", arguments = { items = [
    { item = 3, actions = [{ verdict = "reject", text = "The API needs this name." }] },
] } }
`, "", noChange)
	sendChat(t, server, leadChat, "Approve #41")
	item := waitForItem(t, server, "ready for review")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })

	thread := fake.AddReviewComment(shop, 42, 0, bot, "Rename plan to tier.")
	if thread != 3 {
		t.Fatalf("thread = %d", thread)
	}

	resolved(t, fake, thread)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	waitForPolls(t, fake)
	if items := itemsOf(t, server, "ready for review"); len(items) != 1 || items[0].ID != item.ID {
		t.Errorf("items = %+v, first item = %d", items, item.ID)
	}
	if len(judgePrompts(t, server)) != 1 {
		t.Errorf("Judge prompts = %q", judgePrompts(t, server))
	}
}

func TestAStopOfATaskInReadyForReviewDismissesTheItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := readyToMerge(t, fake, "")
	fake.SetCreatedAt(shop, 42, 0)

	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")

	waitForItem(t, server, "stopped")
	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if items := itemsOf(t, server, "ready for review"); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}

func TestAJudgeThatRunsFromReadyForReviewAndStartsAFixRoundDismissesTheItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, `call = { tool = "submit_verdicts", arguments = { items = [
    { item = 3, actions = [{ verdict = "fix", text = "Rename the field." }] },
] } }
`, fixesEach, noChange)
	sendChat(t, server, leadChat, "Approve #41")
	item := waitForItem(t, server, "ready for review")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })

	fake.AddReviewComment(shop, 42, 0, bot, "Rename plan to tier.")

	testkit.WaitFor(t, func() bool {
		for _, open := range itemsOf(t, server, "ready for review") {
			if open.ID == item.ID {
				return false
			}
		}
		return true
	})
	if len(judgePrompts(t, server)) != 1 {
		t.Errorf("Judge prompts = %q", judgePrompts(t, server))
	}
}
