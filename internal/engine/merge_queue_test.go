package engine_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const queueLead = `
[[prompts]]
when = "Approve #45"
call = { tool = "approve_pull_request", arguments = { repository = "owner/shop", n = 45, report = "Checked the diff. Verified the tests." } }

[[prompts]]
when = "Approve #43"
call = { tool = "approve_pull_request", arguments = { repository = "owner/shop", n = 43, report = "Checked the diff. Verified the tests." } }

[[prompts]]
when = "Approve #41"
call = { tool = "approve_pull_request", arguments = { repository = "owner/shop", n = 41, report = "Checked the diff. Verified the tests." } }

[[prompts]]
when = "dispatch of #45"
call = { tool = "start_implementer", arguments = { repository = "owner/shop", n = 45, instructions = "Task 45" } }

[[prompts]]
when = "dispatch of #43"
call = { tool = "start_implementer", arguments = { repository = "owner/shop", n = 43, instructions = "Task 43" } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "start_implementer", arguments = { repository = "owner/shop", n = 41, instructions = "Task 41" } }
`

const queueImplementer = `
[[prompts]]
when = "Merge the base branch and remove the conflicts."
shell = "git merge -q origin/main; echo cents > plan.txt && git add plan.txt && git commit -q --no-edit"

[[prompts]]
shell = "branch=$(git branch --show-current); echo $branch > ${branch##*/}.txt && git add . && git commit -q -m \"Add for $branch\""
`

// queueTasks starts a server with issues in numbers of the Workstream #12, in Autopilot level.
func queueTasks(t *testing.T, fake *testkit.FakeGitHub, level string, numbers []int) (*testserver.Server, string) {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, dataDir := connectTask(t, fake, queueLead, queueImplementer, func(cfg *config.Config) {
		cfg.Roles.Implementer.Max = 3
		cfg.ReviewQuietPeriod = 50 * time.Millisecond
	})
	authorize(t, server)
	if level != "off" {
		setWorkstreamAutopilot(t, server, level)
	}
	for _, number := range numbers {
		if number != 41 {
			fake.AddSubIssueOf(shop, 12, int64(number), fmt.Sprintf("Task %d", number))
		}
	}
	for _, number := range numbers {
		fake.AddLabel(shop, int64(number), "mobius:ready", "owner")
		testkit.WaitFor(t, func() bool { return liveTaskState(t, server, int64(number)) == "approval" })
	}
	return server, dataDir
}

// TestMergeQueueOrderAndOneAtATime verifies that three approved tasks with auto_merge of one repository
// are behind the default branch. Only the first task that entered the queue gets a branch update. The
// second gets its update only after the first merged.
func TestMergeQueueOrderAndOneAtATime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "merge", []int{41, 43, 45})

	pr41 := liveTask(t, server, 41).PullRequest.Int64
	pr43 := liveTask(t, server, 43).PullRequest.Int64

	head43 := head(t, fake, "mobius/43")
	head45 := head(t, fake, "mobius/45")

	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	// Hold the merge of the first task so we can check that PR 44 and PR 46 do not update before it merges.
	reached41, release41 := fake.HoldMerge(shop, pr41)

	sendChat(t, server, leadChat, "Approve #41")
	sendChat(t, server, leadChat, "Approve #43")
	sendChat(t, server, leadChat, "Approve #45")

	// Only the first task gets an update while the other tasks wait.
	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() >= 1 })

	// Wait for the first task to attempt merging.
	<-reached41
	if calls := fake.UpdateBranchCalls(); calls != 1 {
		t.Errorf("update calls before first merge = %d, want 1", calls)
	}
	if head(t, fake, "mobius/43") != head43 {
		t.Errorf("PR 44 updated before PR 42 merged")
	}
	if head(t, fake, "mobius/45") != head45 {
		t.Errorf("PR 46 updated before PR 42 merged")
	}
	release41()

	// The first task merges.
	testkit.WaitFor(t, func() bool { return fake.MergeCalls() == 1 })
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })

	// Hold the merge of the second task to check that PR 46 does not update before it merges.
	reached43, release43 := fake.HoldMerge(shop, pr43)

	// After the first task merged, the second task gets its update.
	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() >= 2 })

	// Wait for the second task to attempt merging.
	<-reached43
	if calls := fake.UpdateBranchCalls(); calls != 2 {
		t.Errorf("update calls before second merge = %d, want 2", calls)
	}
	if head(t, fake, "mobius/45") != head45 {
		t.Errorf("PR 46 updated before PR 44 merged")
	}
	release43()

	testkit.WaitFor(t, func() bool { return fake.MergeCalls() == 2 })
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })

	// Then PR 46 gets its update and merges.
	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() >= 3 })
	testkit.WaitFor(t, func() bool { return fake.MergeCalls() == 3 })
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 45) == "" })

	if calls := fake.UpdateBranchCalls(); calls != 3 {
		t.Errorf("final update calls = %d, want 3", calls)
	}
}

// TestMergeQueueUpdateAndMerge verifies that the head of the queue gets one update, its new head
// passes CI and gets the approval again, and Mobius merges it with no agent round.
func TestMergeQueueUpdateAndMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "merge", []int{41})
	old := head(t, fake, "mobius/41")

	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })
	newHead := head(t, fake, "mobius/41")
	if newHead == old {
		t.Fatal("the head did not change")
	}
	if calls, updates := fake.MergeCalls(), fake.UpdateBranchCalls(); calls != 1 || updates != 1 {
		t.Errorf("merge calls = %d, update calls = %d", calls, updates)
	}
	if count := implementers(t, server); count != 1 {
		t.Errorf("implementer sessions = %d, want 1", count)
	}
	checkApprovalAgain(t, fake, server, old, newHead, "Mobius merges it after its checks pass.")
}

// TestMergeQueueHeadAlreadyContainsDefaultBranchMergesWithNoUpdate verifies that a head that
// already contains the default branch merges with no update.
func TestMergeQueueHeadAlreadyContainsDefaultBranchMergesWithNoUpdate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "merge", []int{41})

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })
	if updates := fake.UpdateBranchCalls(); updates != 0 {
		t.Errorf("update calls = %d, want 0", updates)
	}
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d, want 1", calls)
	}
}

// TestMergeQueueExitAfterAConflict verifies that the update of the head gives a merge conflict.
// The task leaves the queue and gets a conflict round, and the next task becomes the head.
func TestMergeQueueExitAfterAConflict(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	// Task 41 writes to plan.txt so it will conflict with a commit to plan.txt on main.
	firstConflicting := `
[[prompts]]
when = "Merge the base branch and remove the conflicts."
shell = "git merge -q origin/main; echo cents > plan.txt && git add plan.txt && git commit -q --no-edit"

[[prompts]]
shell = "branch=$(git branch --show-current); if [ \"$branch\" = \"mobius/41\" ]; then echo cents > plan.txt; else echo $branch > ${branch##*/}.txt; fi && git add . && git commit -q -m \"Add for $branch\""
`
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, queueLead, firstConflicting, func(cfg *config.Config) {
		cfg.Roles.Implementer.Max = 3
		cfg.ReviewQuietPeriod = 50 * time.Millisecond
	})
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "approval" })
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "approval" })

	// Commit conflict on main for task 41's plan.txt.
	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")

	sendChat(t, server, leadChat, "Approve #41")
	sendChat(t, server, leadChat, "Approve #43")

	// Task 41 encounters conflict, leaves the queue, and gets a conflict round.
	testkit.WaitFor(t, func() bool { return implementers(t, server) >= 3 })
	// Task 43 becomes the head of the queue, updates its branch, and merges.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })
	if calls := fake.MergeCalls(); calls < 1 {
		t.Errorf("merge calls = %d, want at least 1", calls)
	}
}

// TestMergeQueueExitAfterAFailedCI verifies that a check of the updated head fails. The task leaves
// the queue and gets a fix round, and the next task becomes the head.
func TestMergeQueueExitAfterAFailedCI(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "merge", []int{41, 43})
	oldHead41 := head(t, fake, "mobius/41")

	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	sendChat(t, server, leadChat, "Approve #41")
	sendChat(t, server, leadChat, "Approve #43")

	// Wait for task 41 to get updated branch.
	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != oldHead41 })
	newHead41 := head(t, fake, "mobius/41")

	// Fail a check on task 41's updated head.
	fake.AddCheckRun(shop, checkRun("build", newHead41, "completed", "failure"))

	// Task 41 leaves queue and gets a fix round.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "working" })

	// Task 43 becomes the head, updates, and merges.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })
	if calls := fake.MergeCalls(); calls < 1 {
		t.Errorf("merge calls = %d, want at least 1", calls)
	}
}

// TestMergeQueueRestartKeepsQueueOrderAndHead verifies that a server restart keeps the queue order and the head.
func TestMergeQueueRestartKeepsQueueOrderAndHead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := queueTasks(t, fake, "merge", []int{41, 43})

	// Hold branch updates so neither finishes updating before restart.
	fake.HoldBranchUpdates()
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	sendChat(t, server, leadChat, "Approve #41")
	// Wait for #41 to enter queue.
	testkit.WaitFor(t, func() bool {
		var q string
		_ = server.DB.QueryRow("SELECT coalesce(max(merge_queued_at), '') FROM tasks WHERE issue = 41").Scan(&q)
		return q != ""
	})

	sendChat(t, server, leadChat, "Approve #43")
	// Wait for #43 to enter queue.
	testkit.WaitFor(t, func() bool {
		var q string
		_ = server.DB.QueryRow("SELECT coalesce(max(merge_queued_at), '') FROM tasks WHERE issue = 43").Scan(&q)
		return q != ""
	})

	// Verify initial order in DB.
	var firstIssue int64
	if err := server.DB.QueryRow("SELECT issue FROM tasks WHERE repository = ? AND state <> 'ended' AND merge_queued_at IS NOT NULL ORDER BY julianday(merge_queued_at), id LIMIT 1", shop).Scan(&firstIssue); err != nil {
		t.Fatal(err)
	}
	if firstIssue != 41 {
		t.Fatalf("first issue before restart = %d, want 41", firstIssue)
	}

	// Stop old server first before restarting.
	server.Stop()

	// Restart server with same dataDir.
	cfg := testserver.Config(t, dataDir)
	cfg.Roles.Implementer.Max = 3
	cfg.ReviewQuietPeriod = 50 * time.Millisecond
	server = testserver.StartWith(t, cfg, fake.URL)
	server.WaitForFirstPoll(t, shop)

	// Verify head of queue in DB is still 41.
	if err := server.DB.QueryRow("SELECT issue FROM tasks WHERE repository = ? AND state <> 'ended' AND merge_queued_at IS NOT NULL ORDER BY julianday(merge_queued_at), id LIMIT 1", shop).Scan(&firstIssue); err != nil {
		t.Fatal(err)
	}
	if firstIssue != 41 {
		t.Fatalf("first issue after restart = %d, want 41", firstIssue)
	}

	fake.ReleaseBranchUpdates()

	// 41 merges first, then 43 merges.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })
	if calls := fake.MergeCalls(); calls != 2 {
		t.Errorf("merge calls = %d, want 2", calls)
	}
}

// TestMergeQueueManualApproval verifies that a task with no auto_merge that the Owner approved with
// "Merge" enters the queue, waits for its turn, gets the update, and merges after the approval again with no new "Merge".
func TestMergeQueueManualApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "off", []int{41, 43})
	old43 := head(t, fake, "mobius/43")

	sendChat(t, server, leadChat, "Approve #41")
	item41 := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range itemsOf(t, server, "ready for review") {
			if item.Issue == 41 {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "ready_for_review" })

	sendChat(t, server, leadChat, "Approve #43")
	item43 := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range itemsOf(t, server, "ready for review") {
			if item.Issue == 43 {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "ready_for_review" })

	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	// Owner approves task 41 with "Merge". It enters the queue first.
	if status, body := merge(t, server, item41.ID); status != http.StatusOK {
		t.Fatalf("merge status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		var q string
		_ = server.DB.QueryRow("SELECT coalesce(max(merge_queued_at), '') FROM tasks WHERE issue = 41").Scan(&q)
		return q != ""
	})

	// Owner approves task 43 with "Merge". It enters the queue second, behind task 41.
	if status, body := merge(t, server, item43.ID); status != http.StatusOK {
		t.Fatalf("merge status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		var q string
		_ = server.DB.QueryRow("SELECT coalesce(max(merge_queued_at), '') FROM tasks WHERE issue = 43").Scan(&q)
		return q != ""
	})

	// Hold first task's merge so we can check that task 43 gets no update until task 41 merged.
	pr41 := liveTask(t, server, 41).PullRequest.Int64
	pr43 := liveTask(t, server, 43).PullRequest.Int64
	reached41, release41 := fake.HoldMerge(shop, pr41)

	<-reached41
	if calls := fake.UpdateBranchCalls(); calls != 1 {
		t.Errorf("update calls before task 41 merge = %d, want 1", calls)
	}
	if head(t, fake, "mobius/43") != old43 {
		t.Fatal("task 43 updated before its turn")
	}
	release41()

	// Task 41 merges.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })

	// Task 43 waits for its turn, gets update, and merges after approval again with no new "Merge".
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })
	if calls, updates := fake.MergeCalls(), fake.UpdateBranchCalls(); calls != 2 || updates != 2 {
		t.Errorf("merge calls = %d, update calls = %d", calls, updates)
	}
	newHead43 := head(t, fake, "mobius/43")
	if newHead43 == old43 {
		t.Fatal("the head of task 43 did not change")
	}

	comments := fake.Comments(shop, pr43)
	index := slices.IndexFunc(comments, func(comment testkit.Comment) bool {
		return strings.HasPrefix(comment.Body, "Mobius approved the head ")
	})
	if index < 0 {
		t.Fatalf("no comment of the approval again: %+v", comments)
	}
	want := fmt.Sprintf("Mobius approved the head %.8s again. Only clean merges of main came after the head %.8s, and they change no file of the pull request. The report of the Lead for %.8s:\n\nChecked the diff. Verified the tests.", newHead43, old43, old43)
	if got := comments[index].Body; got != want {
		t.Errorf("comment = %q, want %q", got, want)
	}
	if !slices.ContainsFunc(fake.CheckRuns(shop), func(run testkit.CheckRun) bool {
		return run.Name == "Mobius" && run.HeadSHA == newHead43 && run.Conclusion == "success" &&
			run.Output != nil && run.Output.Summary == "Mobius approved the head again: only clean merges of the default branch came after the approval of the Lead."
	}) {
		t.Errorf("no Mobius check run of the approval again with success on %s: %+v", newHead43, fake.CheckRuns(shop))
	}
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadEvents(t, server), func(event leadEvent) bool {
			return event.Kind == "approved_again" && strings.Contains(event.Payload, newHead43[:8])
		})
	})
}

// TestMergeQueueReEntryGoesToEnd verifies that a task that left the queue and becomes ready again goes to the end.
func TestMergeQueueReEntryGoesToEnd(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	// Implementer fixes the failed build on the next prompt.
	fixImplementer := `
[[prompts]]
when = "Check run \"build\""
shell = "branch=$(git branch --show-current); echo fixed > fixed_${branch##*/}.txt && git add . && git commit -q -m \"Fix for $branch\""

[[prompts]]
shell = "branch=$(git branch --show-current); echo $branch > ${branch##*/}.txt && git add . && git commit -q -m \"Add for $branch\""
`
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, queueLead, fixImplementer, func(cfg *config.Config) {
		cfg.Roles.Implementer.Max = 3
		cfg.ReviewQuietPeriod = 50 * time.Millisecond
	})
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "approval" })
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "approval" })

	oldHead41 := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	sendChat(t, server, leadChat, "Approve #41")
	sendChat(t, server, leadChat, "Approve #43")

	// Wait for task 41 to get updated branch.
	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != oldHead41 })
	newHead41 := head(t, fake, "mobius/41")

	// Hold branch updates so task 43 does not finish updating/merging before task 41 re-enters.
	fake.HoldBranchUpdates()

	// Fail a check on task 41's updated head so it exits the queue.
	fake.AddCheckRun(shop, checkRun("build", newHead41, "completed", "failure"))

	// Task 41 exits queue and starts fix round. Task 43 becomes the head!
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "working" })

	// Wait for Task 41 to finish the fix round and reach approval.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "approval" })

	// Lead approves task 41 again. Task 41 re-enters the queue at the end (behind task 43).
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "ready_for_review" })

	// Verify that Task 43 is the head of the queue, and Task 41 is at the end.
	testkit.WaitFor(t, func() bool {
		var headIssue int64
		err := server.DB.QueryRow("SELECT issue FROM tasks WHERE repository = ? AND state <> 'ended' AND merge_queued_at IS NOT NULL ORDER BY julianday(merge_queued_at), id LIMIT 1", shop).Scan(&headIssue)
		var count int
		_ = server.DB.QueryRow("SELECT count(*) FROM tasks WHERE repository = ? AND state <> 'ended' AND merge_queued_at IS NOT NULL", shop).Scan(&count)
		return err == nil && headIssue == 43 && count == 2
	})

	fake.ReleaseBranchUpdates()

	// Task 43 merges first, then Task 41 merges.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })
	if calls := fake.MergeCalls(); calls != 2 {
		t.Errorf("final merge calls = %d, want 2", calls)
	}
}

// TestMergeQueueNeedsHumanLeavesQueueWhenHeadHasNoMobiusCheckRun verifies that an approved task in needs_human
// whose branch was updated leaves the queue when its new head has no Mobius check run, allowing the next task to merge.
func TestMergeQueueNeedsHumanLeavesQueueWhenHeadHasNoMobiusCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "merge", []int{41, 43})
	oldHead41 := head(t, fake, "mobius/41")

	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	sendChat(t, server, leadChat, "Approve #41")
	sendChat(t, server, leadChat, "Approve #43")

	// Wait for task 41 to enter queue.
	testkit.WaitFor(t, func() bool {
		var q string
		_ = server.DB.QueryRow("SELECT coalesce(max(merge_queued_at), '') FROM tasks WHERE issue = 41").Scan(&q)
		return q != ""
	})

	// Move task 41 to needs_human.
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'needs_human' WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	// Task 41 is head of queue and gets branch update.
	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != oldHead41 })

	// Its new head has no Mobius check run and it is in needs_human, so it leaves the queue.
	// Task 43 becomes the queue head, updates, and merges!
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })
	if calls := fake.MergeCalls(); calls < 1 {
		t.Errorf("merge calls = %d, want at least 1", calls)
	}

	// Task 41 is no longer in the queue.
	var q41 string
	_ = server.DB.QueryRow("SELECT coalesce(max(merge_queued_at), '') FROM tasks WHERE issue = 41").Scan(&q41)
	if q41 != "" {
		t.Errorf("task 41 merge_queued_at = %q, want empty", q41)
	}
}

// TestMergeQueueManualApprovalRestartAfterBranchUpdate verifies that a manual task whose branch was
// updated by updateForMerge survives a server restart, returns to checks using durable data, and merges.
func TestMergeQueueManualApprovalRestartAfterBranchUpdate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := queueTasks(t, fake, "off", []int{41, 43})

	sendChat(t, server, leadChat, "Approve #41")
	item41 := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range itemsOf(t, server, "ready for review") {
			if item.Issue == 41 {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "ready_for_review" })

	sendChat(t, server, leadChat, "Approve #43")
	item43 := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range itemsOf(t, server, "ready for review") {
			if item.Issue == 43 {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "ready_for_review" })

	oldHead41 := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	// Owner approves task 41 with "Merge".
	if status, body := merge(t, server, item41.ID); status != http.StatusOK {
		t.Fatalf("merge status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		var q string
		_ = server.DB.QueryRow("SELECT coalesce(max(merge_queued_at), '') FROM tasks WHERE issue = 41").Scan(&q)
		return q != ""
	})

	// Owner approves task 43 with "Merge".
	if status, body := merge(t, server, item43.ID); status != http.StatusOK {
		t.Fatalf("merge status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		var q string
		_ = server.DB.QueryRow("SELECT coalesce(max(merge_queued_at), '') FROM tasks WHERE issue = 43").Scan(&q)
		return q != ""
	})

	// Hold merge on task 41 so it doesn't merge before the restart.
	pr41 := liveTask(t, server, 41).PullRequest.Int64
	reached41, release41 := fake.HoldMerge(shop, pr41)

	// Wait for task 41 to get updated branch from updateForMerge and reach merge.
	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != oldHead41 })
	<-reached41

	// Stop old server.
	server.Stop()
	release41()

	// Restart server with same dataDir.
	cfg := testserver.Config(t, dataDir)
	cfg.Roles.Implementer.Max = 3
	cfg.ReviewQuietPeriod = 50 * time.Millisecond
	server = testserver.StartWith(t, cfg, fake.URL)
	server.WaitForFirstPoll(t, shop)

	// Task 41 returns to checks, gets approved again, and merges. Then task 43 merges.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })
	if calls := fake.MergeCalls(); calls != 2 {
		t.Errorf("merge calls = %d, want 2", calls)
	}
}
