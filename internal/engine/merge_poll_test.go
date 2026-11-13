package engine_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// pullOfIssue gives the pull request number of each issue of queueTasks, for the issues 41 and 43.
var pullOfIssue = map[int]int64{41: 44, 43: 45}

func TestMobiusApprovesMergesAndEndsAReviewedHeadInOnePoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, 100*time.Millisecond)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "merge" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return fake.PollOf("close", 41) != 0 && taskState(t, server) == "" })

	approved, merged, ended := fake.PollOf("approve", 42), fake.PollOf("merge", 42), fake.PollOf("close", 41)
	if approved == 0 || approved != merged || merged != ended {
		t.Errorf("polls of the approval, the merge and the end = %d, %d, %d, want one poll", approved, merged, ended)
	}
}

// queueBehindAnUpdate starts the tasks of the issues 41 and 43 in Autopilot level merge, and the Lead approves first,
// and then second. The branch of first gets an update that stays open, so second gets its place in the queue after
// first. A common file of the default branch and the pull request gives an update, and commonFiles gives these files.
// At the end of the update, first has no approval of the Mobius check for its new head, goes through the CI, gets its
// approval again from Mobius, and merges. It gives the server after the merge and the end of first.
func queueBehindAnUpdate(t *testing.T, fake *testkit.FakeGitHub, first, second int, commonFiles ...int) *testserver.Server {
	t.Helper()
	// The prompt of the second message holds the first message, so the entry of the second approval comes first.
	lead := approveAgain([]int{41, 43}, first) + approveEntry(second) + approveEntry(first) + queueLead
	server, _ := queueTasksWith(t, fake, lead, "merge", []int{41, 43})
	step := "the start of the queue"
	t.Cleanup(func() {
		if t.Failed() && step != "" {
			t.Logf("the wait for %s failed:\n%s", step, queueStates(t, server, fake))
		}
	})
	commitQueueFiles(fake, commonFiles...)
	fake.HoldBranchUpdates()

	sendChat(t, server, leadChat, fmt.Sprintf("Approve #%d", first))
	step = fmt.Sprintf("the queue place of #%d", first)
	testkit.WaitFor(t, func() bool { return liveTask(t, server, int64(first)).MergeQueuedAt.Valid })
	sendChat(t, server, leadChat, fmt.Sprintf("Approve #%d", second))
	step = fmt.Sprintf("the queue place of #%d", second)
	testkit.WaitFor(t, func() bool { return liveTask(t, server, int64(second)).MergeQueuedAt.Valid })
	step = "the first branch update"
	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() >= 1 })
	fake.ReleaseBranchUpdates()

	step = fmt.Sprintf("the end of #%d", first)
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, int64(first)) == "" })
	step = ""
	return server
}

// queueStates gives a line for each of the issues 41 and 43 with the state of its live task, its queue time, its
// approved and refused review, and the head of its branch.
func queueStates(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) string {
	t.Helper()
	var states strings.Builder
	for _, issue := range []int{41, 43} {
		var state, queuedAt, approved, refused string
		err := server.DB.QueryRow("SELECT state, coalesce(merge_queued_at, ''), coalesce(approved_review, ''), coalesce(refused_review, '') FROM tasks WHERE issue = ? AND state <> 'ended'", issue).
			Scan(&state, &queuedAt, &approved, &refused)
		if err != nil {
			state = err.Error()
		}
		fmt.Fprintf(&states, "#%d: state %q, MergeQueuedAt %q, approved review %q, refused review %q, head %s\n",
			issue, state, queuedAt, approved, refused, head(t, fake, fmt.Sprintf("mobius/%d", issue)))
	}
	return states.String()
}

// checkMergedAndEnded checks that the issue of first closed in the same poll as the merge of its pull request. It gives
// the number of that poll.
func checkMergedAndEnded(t *testing.T, fake *testkit.FakeGitHub, first int) int {
	t.Helper()
	merged, ended := fake.PollOf("merge", pullOfIssue[first]), fake.PollOf("close", int64(first))
	if merged == 0 || ended != merged {
		t.Errorf("polls of the merge and the end of #%d = %d, %d, want one poll", first, merged, ended)
	}
	return merged
}

// approveEntry gives the Lead script entry that approves the pull request of the issue.
func approveEntry(issue int) string {
	return fmt.Sprintf(`
[[prompts]]
when = "Approve #%d"
call = { tool = "approve_pull_request", arguments = { repository = "owner/shop", n = %d, report = "Checked the diff. Verified the tests." } }
`, issue, issue)
}

// TestTheNextHeadWithALowerIDMergesInThePollOfTheMerge verifies the loop of checkTasks: the second task has the lower
// id, so the poll checked it before the merge of the first task. The poll checks it again after the merge.
func TestTheNextHeadWithALowerIDMergesInThePollOfTheMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := queueBehindAnUpdate(t, fake, 43, 41, 43)

	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })

	merged := checkMergedAndEnded(t, fake, 43)
	if got := fake.PollOf("merge", pullOfIssue[41]); got != merged {
		t.Errorf("poll of the merge of #41 = %d, want %d, the poll of the merge of #43", got, merged)
	}
}

func TestTheNextHeadWithALowerIDGetsItsBranchUpdateInThePollOfTheMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := queueBehindAnUpdate(t, fake, 43, 41, 41, 43)

	testkit.WaitFor(t, func() bool { return fake.PollOf("update-branch", pullOfIssue[41]) != 0 })

	merged := checkMergedAndEnded(t, fake, 43)
	if got := fake.PollOf("update-branch", pullOfIssue[41]); got != merged {
		t.Errorf("poll of the update of #41 = %d, want %d, the poll of the merge of #43", got, merged)
	}
	if got := liveTaskState(t, server, 41); got == "" {
		t.Error("#41 ended before its second approval")
	}
}

func TestTheNextHeadWithAHigherIDGetsItsBranchUpdateInThePollOfTheMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	queueBehindAnUpdate(t, fake, 41, 43, 41, 43)

	testkit.WaitFor(t, func() bool { return fake.PollOf("update-branch", pullOfIssue[43]) != 0 })

	merged := checkMergedAndEnded(t, fake, 41)
	if got := fake.PollOf("update-branch", pullOfIssue[43]); got != merged {
		t.Errorf("poll of the update of #43 = %d, want %d, the poll of the merge of #41", got, merged)
	}
}
