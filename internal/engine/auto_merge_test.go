package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const mergeOff = "The merge is off for this Workstream."

// approvable starts a server for #41 whose Autopilot level is level, and waits for the event ready for Lead approval.
// With authorized, the Owner authorized the Mobius App and set the level in the UI. Else the Owner added the label of the
// merge on GitHub.
func approvable(t *testing.T, fake *testkit.FakeGitHub, level string, authorized bool) *testserver.Server {
	t.Helper()
	return approvableWith(t, fake, level, authorized, "")
}

// approvableWith is approvable with the script reviewer for the Reviewer.
func approvableWith(t *testing.T, fake *testkit.FakeGitHub, level string, authorized bool, reviewer string) *testserver.Server {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, reviewer+leadApproves+leadStarts, commits, keepSessionOpen)
	switch {
	case authorized:
		authorize(t, server)
		setWorkstreamAutopilot(t, server, level)
	case level != "off":
		fake.AddLabel(shop, 12, "mobius:auto-merge", "owner")
	}
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == level })
	if !strings.HasPrefix(level, "start") {
		fake.AddLabel(shop, 41, "mobius:ready", "owner")
	}
	waitForReadyEvents(t, fake, server, 1)
	return server
}

func autoMerge(t *testing.T, server *testserver.Server) int64 {
	t.Helper()
	var autoMerge int64
	if err := server.DB.QueryRow("SELECT coalesce(max(auto_merge), 0) FROM tasks WHERE issue = 41").Scan(&autoMerge); err != nil {
		t.Fatal(err)
	}
	return autoMerge
}

func TestWithAMergeLevelTheApprovalOfTheLeadAddsTheReviewOfTheOwnerAndMobiusMerges(t *testing.T) {
	t.Parallel()
	for _, level := range []string{"merge", "start_and_merge"} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := approvable(t, fake, level, true)
			sha := head(t, fake, "mobius/41")

			sendChat(t, server, leadChat, "Approve #41")

			waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. Mobius merges it after its checks pass.")
			testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
			testkit.WaitFor(t, func() bool { return len(inbox(t, server)) == 0 })
			if calls := fake.MergeCalls(); calls != 1 {
				t.Errorf("merge calls = %d", calls)
			}
			reviews := fake.SubmittedReviews(shop, 42)
			if approvals(fake) != 1 || reviews[len(reviews)-1].CommitID != sha {
				t.Errorf("reviews = %+v", reviews)
			}
		})
	}
}

func TestWithoutAMergeLevelTheApprovalOfTheLeadAddsNoReviewAndTheItemKeepsTheMergeButton(t *testing.T) {
	t.Parallel()
	for _, level := range []string{"off", "start"} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := approvable(t, fake, level, true)

			sendChat(t, server, leadChat, "Approve #41")

			waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got it for review.")
			item := waitForItem(t, server, "ready for review")
			waitForPolls(t, fake)
			if item.Merging || item.MergeRefusal != "" || approvals(fake) != 0 || fake.MergeCalls() != 0 || autoMerge(t, server) != 0 {
				t.Errorf("item = %+v, approvals = %d, merge calls = %d", item, approvals(fake), fake.MergeCalls())
			}
		})
	}
}

func TestAHeadThatDoesNotContainTheDefaultBranchGetsAnUpdateAndMergesAfterTheNewApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	old := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 1 })
	testkit.WaitFor(t, func() bool { return readyEvents(t, server) == 2 && taskState(t, server) == "approval" })
	isAncestor(t, fake, "main", head(t, fake, "mobius/41"))
	if calls := fake.MergeCalls(); calls != 0 {
		t.Errorf("merge calls = %d", calls)
	}
	if got := head(t, fake, "mobius/41"); got == old {
		t.Error("the head did not change")
	}

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls, updates, count := fake.MergeCalls(), fake.UpdateBranchCalls(), approvals(fake); calls != 1 || updates != 1 || count != 2 {
		t.Errorf("merge calls = %d, update calls = %d, approvals = %d", calls, updates, count)
	}
	if got := testkit.Git(t, fake.Remote(shop), "show", "main:price.txt"); got != "cents" {
		t.Errorf("price.txt = %q", got)
	}
}

func TestAHeadThatContainsTheDefaultBranchMergesWithNoUpdate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if updates := fake.UpdateBranchCalls(); updates != 0 {
		t.Errorf("update calls = %d", updates)
	}
}

func TestAChangeOfTheLevelToAMergeLevelApprovesTheTaskThatWaitsForTheMergeButton(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "off", true)
	sendChat(t, server, leadChat, "Approve #41")
	waitForItem(t, server, "ready for review")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	waitForPolls(t, fake)
	if calls := fake.MergeCalls(); calls != 0 {
		t.Fatalf("merge calls = %d", calls)
	}

	setWorkstreamAutopilot(t, server, "merge")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls, count := fake.MergeCalls(), approvals(fake); calls != 1 || count != 1 {
		t.Errorf("merge calls = %d, approvals = %d", calls, count)
	}
}

func TestAChangeOfTheLevelToOffStopsTheAutomaticMergeAndTheMergeButtonMerges(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	build := fake.AddCheckRun(shop, checkRun("build", head(t, fake, "mobius/41"), "in_progress", ""))
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].Merging
	})

	setWorkstreamAutopilot(t, server, "off")

	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && !items[0].Merging && items[0].MergeRefusal == mergeOff && autoMerge(t, server) == 0
	})
	fake.SetCheckRunStatus(build, "completed", "success")
	waitForPolls(t, fake)
	if calls, state := fake.MergeCalls(), taskState(t, server); calls != 0 || state != "ready_for_review" {
		t.Fatalf("merge calls = %d, state = %s", calls, state)
	}

	if status, body := merge(t, server, itemsOf(t, server, "ready for review")[0].ID); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls, count := fake.MergeCalls(), approvals(fake); calls != 1 || count != 2 {
		t.Errorf("merge calls = %d, approvals = %d", calls, count)
	}
}

func TestAnOwnerWithNoUserTokenGetsTheApprovalAndTheMergeButtonWithTheReason(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", false)

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(chatLines(t, server, leadChat), func(line chatLine) bool {
			return line.Author == "Lead" && strings.HasPrefix(line.Text, "Approved pull request #42 of #41. The Owner got it for review. Mobius did not start the merge: ")
		})
	})
	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && !items[0].Merging && strings.Contains(items[0].MergeRefusal, "tell the Owner to open")
	})
	waitForPolls(t, fake)
	if state, calls := taskState(t, server), fake.MergeCalls(); state != "ready_for_review" || calls != 0 || approvals(fake) != 0 || autoMerge(t, server) != 0 {
		t.Errorf("state = %s, merge calls = %d, approvals = %d", state, calls, approvals(fake))
	}
}
