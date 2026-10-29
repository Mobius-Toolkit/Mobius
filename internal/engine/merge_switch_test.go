package engine_test

import (
	"net/http"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const mergePaused = "Autopilot paused: no automatic merge."

func turnMerge(t *testing.T, server *testserver.Server, on bool) {
	t.Helper()
	if status := putSwitch(t, server, "merge", on); status != http.StatusNoContent {
		t.Fatalf("status = %d", status)
	}
}

// pausedItem waits for the only ready for review item with the reason mergePaused, and for the held issue #41.
func pausedItem(t *testing.T, server *testserver.Server) inboxItem {
	t.Helper()
	item := testkit.WaitForValue(t, func() (inboxItem, bool) {
		items := itemsOf(t, server, "ready for review")
		if len(items) != 1 {
			return inboxItem{}, false
		}
		return items[0], items[0].MergeRefusal == mergePaused && !items[0].Merging
	})
	testkit.WaitFor(t, func() bool {
		held := getSwitches(t, server).Held
		return len(held) == 1 && held[0].Kind == "merge" && held[0].Repository == shop && held[0].Issue == 41
	})
	return item
}

func TestWhileMergeIsOffTheApprovalOfTheLeadGivesNoApprovalAndTheItemKeepsTheMergeButton(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "start_and_merge", true)
	turnMerge(t, server, false)

	sendChat(t, server, leadChat, "Approve #41")

	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. Mobius merges it after the Owner turns the Merge switch on.")
	pausedItem(t, server)
	waitForPolls(t, fake)
	if state, calls := taskState(t, server), fake.MergeCalls(); state != "ready_for_review" || calls != 0 || approvals(fake) != 0 || autoMerge(t, server) != 0 {
		t.Errorf("state = %s, merge calls = %d, approvals = %d", state, calls, approvals(fake))
	}
}

func TestWhileMergeIsOffTheMergeButtonMerges(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	build := fake.AddCheckRun(shop, checkRun("build", head(t, fake, "mobius/41"), "in_progress", ""))
	turnMerge(t, server, false)
	sendChat(t, server, leadChat, "Approve #41")
	item := pausedItem(t, server)

	if status, body := merge(t, server, item.ID); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	fake.SetCheckRunStatus(build, "completed", "success")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls, count := fake.MergeCalls(), approvals(fake); calls != 1 || count != 1 {
		t.Errorf("merge calls = %d, approvals = %d", calls, count)
	}
}

func TestWhileMergeIsOffAnApprovalOfTheOwnerOnGitHubMerges(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	build := fake.AddCheckRun(shop, checkRun("build", head(t, fake, "mobius/41"), "in_progress", ""))
	turnMerge(t, server, false)
	sendChat(t, server, leadChat, "Approve #41")
	pausedItem(t, server)

	fake.AddReview(shop, 42, "owner", "APPROVED", "")
	fake.SetCheckRunStatus(build, "completed", "success")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls, count := fake.MergeCalls(), approvals(fake); calls != 1 || count != 0 {
		t.Errorf("merge calls = %d, approvals = %d", calls, count)
	}
}

func TestMergeGoingOffStopsTheAutomaticMergeThatWaitsForItsChecksAndMergeGoingOnApprovesAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	build := fake.AddCheckRun(shop, checkRun("build", head(t, fake, "mobius/41"), "in_progress", ""))
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].Merging
	})

	turnMerge(t, server, false)

	pausedItem(t, server)
	fake.SetCheckRunStatus(build, "completed", "success")
	waitForPolls(t, fake)
	if calls, state := fake.MergeCalls(), taskState(t, server); calls != 0 || state != "ready_for_review" || autoMerge(t, server) != 0 {
		t.Fatalf("merge calls = %d, state = %s", calls, state)
	}

	turnMerge(t, server, true)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls, count := fake.MergeCalls(), approvals(fake); calls != 1 || count != 2 {
		t.Errorf("merge calls = %d, approvals = %d", calls, count)
	}
	if held := getSwitches(t, server).Held; len(held) != 0 {
		t.Errorf("held = %+v", held)
	}
}

func TestMergeGoingOnApprovesAgainTheTaskThatTheLeadApprovedWhileMergeWasOff(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "start_and_merge", true)
	turnMerge(t, server, false)
	sendChat(t, server, leadChat, "Approve #41")
	pausedItem(t, server)

	turnMerge(t, server, true)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls, count := fake.MergeCalls(), approvals(fake); calls != 1 || count != 1 {
		t.Errorf("merge calls = %d, approvals = %d", calls, count)
	}
	if held := getSwitches(t, server).Held; len(held) != 0 {
		t.Errorf("held = %+v", held)
	}
}

func TestWhileMergeIsOffAChangeOfTheLevelToAMergeLevelGivesNoApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "off", true)
	sendChat(t, server, leadChat, "Approve #41")
	waitForItem(t, server, "ready for review")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	turnMerge(t, server, false)

	setWorkstreamAutopilot(t, server, "merge")

	pausedItem(t, server)
	waitForPolls(t, fake)
	if calls, count := fake.MergeCalls(), approvals(fake); calls != 0 || count != 0 {
		t.Errorf("merge calls = %d, approvals = %d", calls, count)
	}
}

func TestWhileMergeIsOffAChangeOfTheLevelToALevelWithNoMergeRemovesTheReasonAndMergeGoingOnMergesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	turnMerge(t, server, false)
	sendChat(t, server, leadChat, "Approve #41")
	pausedItem(t, server)

	setWorkstreamAutopilot(t, server, "off")

	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].MergeRefusal == ""
	})
	turnMerge(t, server, true)
	waitForPolls(t, fake)
	if calls, count, state := fake.MergeCalls(), approvals(fake), taskState(t, server); calls != 0 || count != 0 || state != "ready_for_review" {
		t.Errorf("merge calls = %d, approvals = %d, state = %s", calls, count, state)
	}
}
