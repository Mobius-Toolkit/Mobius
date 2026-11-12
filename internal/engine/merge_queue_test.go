package engine_test

import (
	"database/sql"
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
when = "Approve #47"
call = { tool = "approve_pull_request", arguments = { repository = "owner/shop", n = 47, report = "Checked the diff. Verified the tests." } }

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
when = "dispatch of #47"
call = { tool = "start_implementer", arguments = { repository = "owner/shop", n = 47, instructions = "Task 47" } }

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

// approveAgain gives the Lead script entries that approve the issues in again a second time. The event of an approved
// head that changed names the pull request. queued gives all issues of the test in the order of their numbers: the
// pull requests of the issues have the numbers after the highest issue number, in that order.
func approveAgain(queued []int, again ...int) string {
	var script strings.Builder
	for _, number := range slices.Backward(again) {
		pullRequest := queued[len(queued)-1] + 1 + slices.Index(queued, number)
		fmt.Fprintf(&script, `
[[prompts]]
when = "/pull/%d.\nMobius or the Lead approved the head"
call = { tool = "approve_pull_request", arguments = { repository = "owner/shop", n = %d, report = "Checked the diff again. Verified the tests." } }
`, pullRequest, number)
	}
	return script.String()
}

// readyItem waits for the ready for review Inbox item of the issue, and gives it.
func readyItem(t *testing.T, server *testserver.Server, issue int64) inboxItem {
	t.Helper()
	return testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range itemsOf(t, server, "ready for review") {
			if item.Issue == issue && !item.Merging {
				return item, true
			}
		}
		return inboxItem{}, false
	})
}

// commitQueueFiles commits on main the file that the pull request of each issue in numbers adds, with the same
// content. Main and each pull request then change a common file, and the update of the branch merges with no conflict.
func commitQueueFiles(fake *testkit.FakeGitHub, numbers ...int) {
	for _, number := range numbers {
		fake.CommitFile(shop, fmt.Sprintf("%d.txt", number), fmt.Sprintf("mobius/%d\n", number), fmt.Sprintf("Add the file of #%d", number))
	}
}

// queueTasks starts a server with issues in numbers of the Workstream #12, in Autopilot level.
func queueTasks(t *testing.T, fake *testkit.FakeGitHub, level string, numbers []int) (*testserver.Server, string) {
	t.Helper()
	return queueTasksWith(t, fake, queueLead, level, numbers)
}

// queueTasksWith is queueTasks with lead as the script of the Lead.
func queueTasksWith(t *testing.T, fake *testkit.FakeGitHub, lead, level string, numbers []int) (*testserver.Server, string) {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, dataDir := connectTask(t, fake, lead, queueImplementer, func(cfg *config.Config) {
		cfg.Roles.Implementer.Max = 4
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

// TestMergeQueueOrderAndOneAtATime verifies that three approved tasks with auto_merge of one repository are behind the
// default branch with a common file. Only the first task that entered the queue gets a branch update while it waits.
func TestMergeQueueOrderAndOneAtATime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "merge", []int{41, 43, 45})
	head41 := head(t, fake, "mobius/41")
	head43 := head(t, fake, "mobius/43")
	head45 := head(t, fake, "mobius/45")

	commitQueueFiles(fake, 41, 43, 45)
	// The update of the branch of the first task stays open, so the first task stays the head.
	fake.HoldBranchUpdates()

	sendChat(t, server, leadChat, "Approve #41")
	sendChat(t, server, leadChat, "Approve #43")
	sendChat(t, server, leadChat, "Approve #45")

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() >= 1 })
	for _, number := range []int64{43, 45} {
		testkit.WaitFor(t, func() bool { return liveTask(t, server, number).MergeQueuedAt.Valid })
	}
	waitForPolls(t, fake)

	if calls := fake.UpdateBranchCalls(); calls != 1 {
		t.Errorf("update calls = %d, want 1", calls)
	}
	for ref, want := range map[string]string{"mobius/41": head41, "mobius/43": head43, "mobius/45": head45} {
		if got := head(t, fake, ref); got != want {
			t.Errorf("head of %s = %s, want %s", ref, got, want)
		}
	}
}

// TestMergeQueueUpdateAndMerge verifies that the head of the queue that is behind the default branch with a common file
// gets one update, its new head passes CI and gets the approval of the Lead again, and Mobius merges it with no agent
// round.
func TestMergeQueueUpdateAndMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasksWith(t, fake, approveAgain([]int{41}, 41)+queueLead, "merge", []int{41})
	old := head(t, fake, "mobius/41")

	commitQueueFiles(fake, 41)

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
	if count := approvals(fake); count != 2 {
		t.Errorf("approvals = %d, want 2", count)
	}
}

// TestMergeQueueBehindHeadWithNoCommonFileMergesWithNoUpdate verifies that a head that is behind the default branch
// merges with its approval, with no update of the branch, when the default branch changes no file of the pull request.
func TestMergeQueueBehindHeadWithNoCommonFileMergesWithNoUpdate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "merge", []int{41})
	old := head(t, fake, "mobius/41")

	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })
	if got := head(t, fake, "mobius/41"); got != old {
		t.Errorf("head = %s, want %s", got, old)
	}
	if calls, updates := fake.MergeCalls(), fake.UpdateBranchCalls(); calls != 1 || updates != 0 {
		t.Errorf("merge calls = %d, update calls = %d, want 1 and 0", calls, updates)
	}
	if count := approvals(fake); count != 1 {
		t.Errorf("approvals = %d, want 1", count)
	}
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
	server, _ := queueTasksWith(t, fake, approveAgain([]int{41, 43}, 43)+queueLead, "merge", []int{41, 43})
	oldHead41 := head(t, fake, "mobius/41")

	commitQueueFiles(fake, 41, 43)

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
	server, dataDir := queueTasksWith(t, fake, approveAgain([]int{41, 43}, 41, 43)+queueLead, "merge", []int{41, 43})

	// Hold branch updates so neither finishes updating before restart.
	fake.HoldBranchUpdates()
	commitQueueFiles(fake, 41, 43)

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
// "Merge" enters the queue, waits for its turn, and merges after the task before it.
func TestMergeQueueManualApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "off", []int{41, 43})

	sendChat(t, server, leadChat, "Approve #41")
	item41 := readyItem(t, server, 41)
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "ready_for_review" })

	sendChat(t, server, leadChat, "Approve #43")
	item43 := readyItem(t, server, 43)
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "ready_for_review" })

	pr41 := liveTask(t, server, 41).PullRequest.Int64
	pr43 := liveTask(t, server, 43).PullRequest.Int64
	reached41, release41 := fake.HoldMerge(shop, pr41)
	reached43, release43 := fake.HoldMerge(shop, pr43)

	// Owner approves task 41 with "Merge". It enters the queue first, and its merge waits.
	if status, body := merge(t, server, item41.ID); status != http.StatusOK {
		t.Fatalf("merge status = %d: %s", status, body)
	}
	<-reached41

	// Owner approves task 43 with "Merge". It enters the queue second, behind task 41.
	if status, body := merge(t, server, item43.ID); status != http.StatusOK {
		t.Fatalf("merge status = %d: %s", status, body)
	}
	if calls := fake.MergeCalls(); calls != 0 {
		t.Errorf("merge calls before task 41 merged = %d, want 0", calls)
	}
	release41()

	// Task 41 merges, then task 43 merges.
	<-reached43
	if state := liveTaskState(t, server, 41); state != "" {
		t.Errorf("task 41 state = %q when task 43 merges, want none", state)
	}
	release43()
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })
	if calls, updates := fake.MergeCalls(), fake.UpdateBranchCalls(); calls != 2 || updates != 0 {
		t.Errorf("merge calls = %d, update calls = %d, want 2 and 0", calls, updates)
	}
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
	server, _ := connectTask(t, fake, approveAgain([]int{41, 43}, 43)+queueLead, fixImplementer, func(cfg *config.Config) {
		cfg.Roles.Implementer.Max = 3
		cfg.ReviewQuietPeriod = 50 * time.Millisecond
		// A new Lead session gets the chat history in its first prompt, and the script matches "Approve #43" first.
		cfg.LeadIdleTimeout = 30 * time.Second
	})
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "approval" })
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "approval" })

	oldHead41 := head(t, fake, "mobius/41")
	commitQueueFiles(fake, 41, 43)

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
	server, _ := queueTasksWith(t, fake, approveAgain([]int{41, 43}, 43)+queueLead, "merge", []int{41, 43})

	// Bring task 41 to needs_human through the engine via a merge conflict on a stale pull request.
	pr41 := liveTask(t, server, 41).PullRequest.Int64
	fake.SetCreatedAt(shop, pr41, 0)
	fake.CommitFile(shop, "41.txt", "conflict\n", "Conflict on 41")

	testkit.WaitFor(t, func() bool {
		return liveTaskState(t, server, 41) == "needs_human"
	})

	// Resolve the conflict so the pull request is mergeable again.
	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
	testkit.Git(t, work, "fetch", "origin", "main")
	testkit.Git(t, work, "merge", "-X", "ours", "origin/main")
	testkit.Git(t, work, "push", "origin", "HEAD:refs/heads/mobius/41")
	oldHead41 := head(t, fake, "mobius/41")

	// Move default branch after task 41 reached needs_human and resolved the conflict. The new commits change a file
	// of each pull request, with the content of the pull request.
	commitQueueFiles(fake, 41, 43)
	passMobius(fake, oldHead41)

	// Owner marks PR 41 ready for review and approves it while it is in needs_human.
	fake.SetDraft(shop, pr41, false)
	fake.AddReview(shop, pr41, "owner", "APPROVED", "")

	// Task 41 enters the merge queue while in needs_human.
	testkit.WaitFor(t, func() bool {
		task := liveTask(t, server, 41)
		return task.State == "needs_human" && task.MergeQueuedAt.Valid
	})

	// Lead approves task 43 so it enters the merge queue behind task 41.
	sendChat(t, server, leadChat, "Approve #43")
	testkit.WaitFor(t, func() bool {
		return liveTask(t, server, 43).MergeQueuedAt.Valid
	})

	// Task 41 is head of queue and gets branch update.
	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != oldHead41 })

	// Its new head has no Mobius check run and it is in needs_human, so it leaves the queue.
	// Task 43 becomes the queue head, updates, and merges!
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d, want 1", calls)
	}

	// Task 41 did not merge and is in needs_human.
	if state := liveTaskState(t, server, 41); state != "needs_human" {
		t.Errorf("task 41 state = %s, want needs_human", state)
	}
	if mainLog := testkit.Git(t, fake.Remote(shop), "log", "--format=%s", "main"); strings.Contains(mainLog, "mobius/41") || strings.Contains(mainLog, "Task 41") {
		t.Error("PR 41 merged")
	}

	// Task 41 is no longer in the queue.
	if q := liveTask(t, server, 41).MergeQueuedAt; q.Valid {
		t.Errorf("task 41 merge_queued_at = %q, want empty", q.String)
	}
}

// TestMergeQueueRestartAfterBranchUpdate verifies that a task with auto_merge whose branch was updated by
// updateForMerge survives a server restart, returns to checks using durable data, gets the approval of the Lead
// again, and merges.
func TestMergeQueueRestartAfterBranchUpdate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := queueTasksWith(t, fake, approveAgain([]int{41}, 41)+queueLead, "merge", []int{41})

	commitQueueFiles(fake, 41)

	// Hold the branch update answer of task 41 and stop the server right after the head of mobius/41 changes,
	// and before Mobius reads the new head.
	reached, release := fake.HoldBranchUpdateAnswer()

	sendChat(t, server, leadChat, "Approve #41")

	<-reached
	server.Stop()
	release()

	// Restart server with same dataDir.
	cfg := testserver.Config(t, dataDir)
	cfg.Roles.Implementer.Max = 3
	cfg.ReviewQuietPeriod = 300 * time.Millisecond
	server = testserver.StartWith(t, cfg, fake.URL)

	// Task 41 goes to checks after the restart.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "checks" })

	// Task 41 gets the approval again and merges.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })

	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d, want 1", calls)
	}
}

// TestMergeQueueMergeSwitchOffWhileUpdatedHeadWaitsForApprovalAgainLeavesQueueAndNextTaskMerges
// verifies that when the Merge switch is turned off while the updated head waits for approval again,
// the task leaves the queue and the next task (approved with "Merge") merges.
func TestMergeQueueMergeSwitchOffWhileUpdatedHeadWaitsForApprovalAgainLeavesQueueAndNextTaskMerges(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasksWith(t, fake, approveAgain([]int{41, 43}, 41)+queueLead, "merge", []int{41, 43})

	// Move default branch to make task 41 and task 43 behind. Task 41 shares a file with the default branch.
	commitQueueFiles(fake, 41)
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	// Hold branch update answer of task 41 to add an in-progress CI check run before checks read.
	reached, release := fake.HoldBranchUpdateAnswer()

	// Lead approves task 41, which enters the merge queue with auto_merge.
	sendChat(t, server, leadChat, "Approve #41")

	<-reached
	newHead41 := head(t, fake, "mobius/41")
	ci41 := fake.AddCheckRun(shop, checkRun("test", newHead41, "in_progress", ""))
	release()

	// Task 41 returns to checks.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "checks" })

	// The Merge switch goes off while the updated head waits for the approval again.
	turnMerge(t, server, false)

	// Lead approves task 43. Since Merge switch is off, it pauses without auto-merge approval.
	sendChat(t, server, leadChat, "Approve #43")
	item43 := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range itemsOf(t, server, "ready for review") {
			if item.Issue == 43 {
				return item, true
			}
		}
		return inboxItem{}, false
	})

	// Owner approves task 43 with "Merge".
	if status, body := merge(t, server, item43.ID); status != http.StatusOK {
		t.Fatalf("merge status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		return liveTask(t, server, 43).MergeQueuedAt.Valid
	})

	// CI of task 41 completes and passes.
	fake.SetCheckRunStatus(ci41, "completed", "success")

	// Task 41 leaves the queue because its auto-merge paused when the switch was turned off.
	testkit.WaitFor(t, func() bool {
		return !liveTask(t, server, 41).MergeQueuedAt.Valid
	})

	// Task 43 (approved with "Merge") becomes the queue head. It shares no file with the default branch, so it merges
	// with no update.
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "" })

	// Task 41 did not merge and is in ready_for_review.
	if state := liveTaskState(t, server, 41); state != "ready_for_review" {
		t.Errorf("task 41 state = %s, want ready_for_review", state)
	}
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d, want 1", calls)
	}
	if mainLog := testkit.Git(t, fake.Remote(shop), "log", "--format=%s", "main"); strings.Contains(mainLog, "mobius/41") || strings.Contains(mainLog, "Task 41") {
		t.Error("PR 41 merged")
	}
}

// TestMergeQueuePlacesOnBoardTasksAndInboxItems verifies that three tasks in the merge queue have places 1, 2, 3
// of 3 on board tasks and ready_for_review Inbox items, an unqueued task has null and 0, and after the first
// task leaves the queue the remaining tasks have places 1 and 2 of 2.
func TestMergeQueuePlacesOnBoardTasksAndInboxItems(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasksWith(t, fake, approveAgain([]int{41, 43, 45, 47}, 41)+queueLead, "off", []int{41, 43, 45, 47})

	pr43 := liveTask(t, server, 43).PullRequest.Int64

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

	sendChat(t, server, leadChat, "Approve #45")
	item45 := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range itemsOf(t, server, "ready for review") {
			if item.Issue == 45 {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 45) == "ready_for_review" })

	sendChat(t, server, leadChat, "Approve #47")
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 47) == "ready_for_review" })
	testkit.WaitFor(t, func() bool {
		for _, item := range itemsOf(t, server, "ready for review") {
			if item.Issue == 47 {
				return true
			}
		}
		return false
	})

	// Task 41 shares a file with the default branch and its update of the branch stays open, so tasks remain in the
	// queue while we inspect API data.
	commitQueueFiles(fake, 41)
	fake.HoldBranchUpdates()

	// Owner approves tasks 41, 43, 45 with "Merge".
	if status, body := merge(t, server, item41.ID); status != http.StatusOK {
		t.Fatalf("merge 41 status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		return liveTask(t, server, 41).MergeQueuedAt.Valid
	})

	if status, body := merge(t, server, item43.ID); status != http.StatusOK {
		t.Fatalf("merge 43 status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		return liveTask(t, server, 43).MergeQueuedAt.Valid
	})

	if status, body := merge(t, server, item45.ID); status != http.StatusOK {
		t.Fatalf("merge 45 status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		return liveTask(t, server, 45).MergeQueuedAt.Valid
	})

	type apiBoardTask struct {
		Number          int64  `json:"number"`
		MergeQueuePlace *int64 `json:"mergeQueuePlace"`
		MergeQueueSize  int64  `json:"mergeQueueSize"`
	}

	// Verify places 1, 2, 3 of 3 on board tasks, and null/0 for task 47.
	testkit.WaitFor(t, func() bool {
		board := apiData[[]apiBoardTask](t, server, "/api/tasks")
		var p41, p43, p45, p47 *int64
		var s41, s43, s45, s47 int64
		for _, b := range board {
			switch b.Number {
			case 41:
				p41, s41 = b.MergeQueuePlace, b.MergeQueueSize
			case 43:
				p43, s43 = b.MergeQueuePlace, b.MergeQueueSize
			case 45:
				p45, s45 = b.MergeQueuePlace, b.MergeQueueSize
			case 47:
				p47, s47 = b.MergeQueuePlace, b.MergeQueueSize
			}
		}
		return p41 != nil && *p41 == 1 && s41 == 3 &&
			p43 != nil && *p43 == 2 && s43 == 3 &&
			p45 != nil && *p45 == 3 && s45 == 3 &&
			p47 == nil && s47 == 0
	})

	// Verify places 1, 2, 3 of 3 on ready_for_review Inbox items, and null/0 for task 47.
	testkit.WaitFor(t, func() bool {
		inboxItems := apiData[[]inboxItem](t, server, "/api/inbox")
		var p41, p43, p45, p47 *int64
		var s41, s43, s45, s47 int64
		for _, item := range inboxItems {
			if item.Kind != "ready for review" {
				continue
			}
			switch item.Issue {
			case 41:
				p41, s41 = item.MergeQueuePlace, item.MergeQueueSize
			case 43:
				p43, s43 = item.MergeQueuePlace, item.MergeQueueSize
			case 45:
				p45, s45 = item.MergeQueuePlace, item.MergeQueueSize
			case 47:
				p47, s47 = item.MergeQueuePlace, item.MergeQueueSize
			}
		}
		return p41 != nil && *p41 == 1 && s41 == 3 &&
			p43 != nil && *p43 == 2 && s43 == 3 &&
			p45 != nil && *p45 == 3 && s45 == 3 &&
			p47 == nil && s47 == 0
	})

	reached43, release43 := fake.HoldMerge(shop, pr43)
	defer release43()

	fake.ReleaseBranchUpdates()

	// The Lead approves the new head of task 41 again, and task 41 leaves the queue.
	testkit.WaitFor(t, func() bool { return !liveTask(t, server, 41).MergeQueuedAt.Valid })

	<-reached43

	// After task 41 left the queue, remaining tasks 43 and 45 have 1 and 2 of 2. Task 47 has null and 0.
	testkit.WaitFor(t, func() bool {
		board := apiData[[]apiBoardTask](t, server, "/api/tasks")
		var p43, p45, p47 *int64
		var s43, s45, s47 int64
		for _, b := range board {
			switch b.Number {
			case 43:
				p43, s43 = b.MergeQueuePlace, b.MergeQueueSize
			case 45:
				p45, s45 = b.MergeQueuePlace, b.MergeQueueSize
			case 47:
				p47, s47 = b.MergeQueuePlace, b.MergeQueueSize
			}
		}
		return p43 != nil && *p43 == 1 && s43 == 2 &&
			p45 != nil && *p45 == 2 && s45 == 2 &&
			p47 == nil && s47 == 0
	})

	testkit.WaitFor(t, func() bool {
		inboxItems := apiData[[]inboxItem](t, server, "/api/inbox")
		var p43, p45, p47 *int64
		var s43, s45, s47 int64
		for _, item := range inboxItems {
			if item.Kind != "ready for review" {
				continue
			}
			switch item.Issue {
			case 43:
				p43, s43 = item.MergeQueuePlace, item.MergeQueueSize
			case 45:
				p45, s45 = item.MergeQueuePlace, item.MergeQueueSize
			case 47:
				p47, s47 = item.MergeQueuePlace, item.MergeQueueSize
			}
		}
		return p43 != nil && *p43 == 1 && s43 == 2 &&
			p45 != nil && *p45 == 2 && s45 == 2 &&
			p47 == nil && s47 == 0
	})
}

// heldQueueHead approves the tasks of numbers with a merge level, so that the task of #41 is the head of the merge
// queue. The default branch moves, so the head updates its branch. The CI of the new head does not complete, so the head
// goes back to checks. It gives the server, the new head of #41, the id of its CI run and the time that the held clock
// of the tests starts from.
func heldQueueHead(t *testing.T, fake *testkit.FakeGitHub, numbers []int) (*testserver.Server, string, int64, time.Time) {
	t.Helper()
	server, _ := queueTasksWith(t, fake, approveAgain(numbers, numbers...)+queueLead, "merge", numbers)
	commitQueueFiles(fake, numbers...)
	reached, release := fake.HoldBranchUpdateAnswer()
	for _, number := range numbers {
		sendChat(t, server, leadChat, fmt.Sprintf("Approve #%d", number))
	}
	<-reached
	newHead := head(t, fake, "mobius/41")
	ci := fake.AddCheckRun(shop, checkRun("test", newHead, "in_progress", ""))
	release()
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "checks" })
	for _, number := range numbers {
		testkit.WaitFor(t, func() bool { return liveTask(t, server, int64(number)).MergeQueuedAt.Valid })
	}
	return server, newHead, ci, time.Now()
}

func queueHeldEvents(t *testing.T, server *testserver.Server) []leadEvent {
	t.Helper()
	return slices.DeleteFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind != "merge_queue_held" })
}

// TestMergeQueueRemovesAHeldHeadAfterTwoHours verifies that the head of the queue that stays in checks for more than
// two hours leaves the queue with one Lead event, and the next task becomes the head and updates its branch.
func TestMergeQueueRemovesAHeldHeadAfterTwoHours(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, newHead, _, start := heldQueueHead(t, fake, []int{41, 43})
	pr41 := liveTask(t, server, 41).PullRequest.Int64
	updates := fake.UpdateBranchCalls()

	at := start.Add(2*time.Hour + time.Minute)
	server.Engine.SetClock(func() time.Time { return at })

	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := queueHeldEvents(t, server)
		return events, len(events) == 1
	})
	want := fmt.Sprintf("%s #41 \"Add plan model\" held the head of the merge queue in checks for more than 2h0m0s: pull request #%d https://github.com/owner/shop/pull/%d, head %s. Mobius removed it from the queue, so the next task can merge. 1 other tasks wait in the queue. The task enters the queue again at the end after each check of its head passes and its head has the Mobius check.",
		at.UTC().Format("2006-01-02 15:04 UTC"), pr41, pr41, newHead)
	if events[0].Payload != want {
		t.Errorf("event = %s, want %s", events[0].Payload, want)
	}
	if liveTask(t, server, 41).MergeQueuedAt.Valid {
		t.Error("task 41 stays in the queue")
	}
	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() > updates })
	waitForPolls(t, fake)
	if events := queueHeldEvents(t, server); len(events) != 1 {
		t.Errorf("events = %+v, want 1", events)
	}
	if state := liveTaskState(t, server, 41); state != "checks" {
		t.Errorf("task 41 state = %s, want checks", state)
	}
}

// TestMergeQueueKeepsAHeadThatWaitsLessThanTwoHours verifies that the head of the queue stays in the queue before two
// hours pass.
func TestMergeQueueKeepsAHeadThatWaitsLessThanTwoHours(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _, _, start := heldQueueHead(t, fake, []int{41, 43})

	server.Engine.SetClock(func() time.Time { return start.Add(time.Hour + 59*time.Minute) })
	waitForPolls(t, fake)

	if !liveTask(t, server, 41).MergeQueuedAt.Valid {
		t.Error("task 41 left the queue")
	}
	if events := queueHeldEvents(t, server); len(events) != 0 {
		t.Errorf("events = %+v", events)
	}
}

// TestMergeQueueKeepsAHeldHeadThatHasNoOtherTaskInTheQueue verifies that a head that waits for more than two hours
// stays in the queue when no other task waits in the queue.
func TestMergeQueueKeepsAHeldHeadThatHasNoOtherTaskInTheQueue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _, _, start := heldQueueHead(t, fake, []int{41})

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })
	waitForPolls(t, fake)

	if !liveTask(t, server, 41).MergeQueuedAt.Valid {
		t.Error("task 41 left the queue")
	}
	if events := queueHeldEvents(t, server); len(events) != 0 {
		t.Errorf("events = %+v", events)
	}
}

// TestMergeQueueReEntryOfARemovedHeadGoesAfterTheNextTask verifies that a head that Mobius removed from the queue
// enters the queue again behind the next task after its CI passes and its head has the Mobius check.
func TestMergeQueueReEntryOfARemovedHeadGoesAfterTheNextTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _, ci41, start := heldQueueHead(t, fake, []int{41, 43})
	// The update of the branch of #43 stays open, so #43 stays the head in ready_for_review.
	fake.HoldBranchUpdates()

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })
	testkit.WaitFor(t, func() bool { return !liveTask(t, server, 41).MergeQueuedAt.Valid })
	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() >= 2 })

	fake.SetCheckRunStatus(ci41, "completed", "success")

	testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).MergeQueuedAt.Valid })
	issues := query(t, server, func(rows *sql.Rows, issue *int64) error { return rows.Scan(issue) },
		"SELECT issue FROM tasks WHERE repository = ? AND state <> 'ended' AND merge_queued_at IS NOT NULL ORDER BY julianday(merge_queued_at), id", shop)
	if !slices.Equal(issues, []int64{43, 41}) {
		t.Errorf("queue = %v, want [43 41]", issues)
	}
}
