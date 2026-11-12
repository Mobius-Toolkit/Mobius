package engine_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const pendingScreenshots = "The screenshots are pending. See the [checks of the commit](https://github.com/owner/shop/commit/abc/checks)."

func screenshotsSectionOf(text string) string {
	return "<!-- screenshots -->\n### UI screenshots\n\n" + text + "\n<!-- /screenshots -->"
}

// reviewedHeadWithCI gives a task whose head the Reviewer approved and whose CI passed. The quiet period is long, so the
// task waits in checks until a test adds a completed workflow run.
func reviewedHeadWithCI(t *testing.T) (*testkit.FakeGitHub, *testserver.Server, string) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, time.Hour)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" && reviewedHead(t, server) != "" })
	return fake, server, head(t, fake, "mobius/41")
}

func readyForApprovalEvents(t *testing.T, server *testserver.Server) []leadEvent {
	t.Helper()
	var ready []leadEvent
	for _, event := range leadEvents(t, server) {
		if event.Kind == "ready_for_approval" {
			ready = append(ready, event)
		}
	}
	return ready
}

func checkNoApproval(t *testing.T, fake *testkit.FakeGitHub, server *testserver.Server) {
	t.Helper()
	for _, event := range leadEvents(t, server) {
		if event.Kind == "auto_approved" {
			t.Errorf("auto_approved event: %q", event.Payload)
		}
	}
	for _, comment := range fake.Comments(shop, 42) {
		if strings.HasPrefix(comment.Body, "Mobius approved head ") {
			t.Errorf("approval comment: %q", comment.Body)
		}
	}
	if got := approvedReport(t, server); got != "" {
		t.Errorf("approved_report = %q", got)
	}
}

// screenshotsCheckRunURL gives the link of the check run Screenshots of the commit sha.
func screenshotsCheckRunURL(t *testing.T, fake *testkit.FakeGitHub, sha string) string {
	t.Helper()
	index := slices.IndexFunc(fake.CheckRuns(shop), func(run testkit.CheckRun) bool { return run.Name == "Screenshots" && run.HeadSHA == sha })
	if index < 0 {
		t.Fatalf("no check run Screenshots on %s", sha)
	}
	return fmt.Sprintf("https://github.com/%s/runs/%d", shop, index+1)
}

func TestAReviewedHeadWithARunningScreenshotsCheckRunStaysInChecks(t *testing.T) {
	t.Parallel()
	fake, server, sha := reviewedHeadWithCI(t)
	fake.AddCheckRun(shop, checkRun("Screenshots", sha, "in_progress", ""))
	passCI(fake, sha)

	waitForPolls(t, fake)

	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s, want checks", state)
	}
	checkNoApproval(t, fake, server)
	if ready := readyForApprovalEvents(t, server); len(ready) != 0 {
		t.Errorf("ready_for_approval events: %+v", ready)
	}
}

func TestAReviewedHeadWhoseScreenshotsCheckRunCompletesWithSuccessGetsTheApprovalOfMobius(t *testing.T) {
	t.Parallel()
	fake, server, sha := reviewedHeadWithCI(t)
	fake.AddCheckRun(shop, checkRun("Screenshots", sha, "in_progress", ""))
	passCI(fake, sha)
	waitForPolls(t, fake)

	fake.CompleteCheckRun(shop, sha, "Screenshots", "success", "No screen changed.")

	event := autoApprovedEvent(t, server)
	if !strings.Contains(event.Payload, "The Reviewer found no open finding on this head.") {
		t.Errorf("event = %q", event.Payload)
	}
	if got := approvedReport(t, server); got != autoApprovedReport(sha) {
		t.Errorf("approved_report = %q", got)
	}
}

func TestAReviewedHeadWhoseScreenshotsCheckRunCompletesWithNeutralGivesTheLeadReadyForApprovalAndNoApproval(t *testing.T) {
	t.Parallel()
	fake, server, sha := reviewedHeadWithCI(t)
	fake.AddCheckRun(shop, checkRun("Screenshots", sha, "in_progress", ""))
	passCI(fake, sha)
	waitForPolls(t, fake)

	fake.CompleteCheckRun(shop, sha, "Screenshots", "neutral", "Screens changed")

	testkit.WaitFor(t, func() bool { return len(readyForApprovalEvents(t, server)) > 0 })
	waitForPolls(t, fake)

	ready := readyForApprovalEvents(t, server)
	if len(ready) != 1 {
		t.Fatalf("ready_for_approval events: %+v", ready)
	}
	want := "\nScreenshots: Screens changed " + screenshotsCheckRunURL(t, fake, sha) + "\nA person must look at these checks before the approval."
	if !strings.Contains(ready[0].Payload, want) {
		t.Errorf("event = %q, want %q", ready[0].Payload, want)
	}
	if state := taskState(t, server); state != "approval" {
		t.Errorf("state = %s, want approval", state)
	}
	checkNoApproval(t, fake, server)
	for _, review := range fake.SubmittedReviews(shop, 42) {
		if review.Event == "APPROVE" {
			t.Errorf("review: %+v", review)
		}
	}
}

func TestAScreenshotsSectionInTheBodyHasNoEffectWhenTheHeadHasNoScreenshotsCheckRun(t *testing.T) {
	t.Parallel()
	fake, server, sha := reviewedHeadWithCI(t)
	fake.SetBody(shop, 42, screenshotsSectionOf(pendingScreenshots))
	waitForPolls(t, fake)

	passCI(fake, sha)

	autoApprovedEvent(t, server)
	if got := approvedReport(t, server); got != autoApprovedReport(sha) {
		t.Errorf("approved_report = %q", got)
	}
}

func TestAHeadWithARunningScreenshotsCheckRunForMoreThanTwoHoursGivesTheLeadALongCIEventForTheScreenshots(t *testing.T) {
	t.Parallel()
	fake, server, sha := reviewedHeadWithCI(t)
	fake.AddCheckRun(shop, checkRun("Screenshots", sha, "in_progress", ""))
	passCI(fake, sha)
	waitForPolls(t, fake)
	start := time.Now()

	server.Engine.SetClock(func() time.Time { return start.Add(time.Hour + 59*time.Minute) })
	waitForPolls(t, fake)

	if events := longCIEvents(t, server); events != 0 {
		t.Errorf("events = %d", events)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })

	event := testkit.WaitForValue(t, func() (leadEvent, bool) {
		events := leadEvents(t, server)
		index := slices.IndexFunc(events, func(event leadEvent) bool { return event.Kind == "long_ci_wait" })
		if index < 0 {
			return leadEvent{}, false
		}
		return events[index], true
	})
	if want := "head " + sha + ". These runs did not complete: Screenshots."; !strings.Contains(event.Payload, want) {
		t.Errorf("event = %q, want %q", event.Payload, want)
	}
	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s, want checks", state)
	}
	checkNoApproval(t, fake, server)
}

func TestATaskInChecksReadsThePullRequestAgainAtEachPollAndLeavesChecksWhenTheHeadChangesWithNoIssueListChange(t *testing.T) {
	t.Parallel()
	fake, server, sha := reviewedHeadWithCI(t)
	fake.AddCheckRun(shop, checkRun("Screenshots", sha, "in_progress", ""))
	waitForPolls(t, fake)
	if state := taskState(t, server); state != "checks" {
		t.Fatalf("state = %s, want checks", state)
	}

	fake.PushCommitOnTopOfBranch(shop, "mobius/41", "Change the price")
	fake.CompleteCheckRun(shop, sha, "Screenshots", "success", "No screen changed.")
	passCI(fake, sha)
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s, want checks", state)
	}
	checkNoApproval(t, fake, server)

	pushed := head(t, fake, "mobius/41")
	passCI(fake, pushed)

	testkit.WaitFor(t, func() bool { return len(readyForApprovalEvents(t, server)) > 0 })
	if payload := readyForApprovalEvents(t, server)[0].Payload; !strings.Contains(payload, fmt.Sprintf("CI of head %.8s: build success", pushed)) {
		t.Errorf("event = %q", payload)
	}
	checkNoApproval(t, fake, server)
}

func TestATaskInApprovalReadsThePullRequestOneTimeAndLaterPollsReadNoNewCopy(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "off", []int{41})
	pull := liveTask(t, server, 41).PullRequest.Int64
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	reads := fake.SinglePullRequestReads(shop, pull)

	waitForPolls(t, fake)

	if state := liveTaskState(t, server, 41); state != "approval" {
		t.Errorf("state = %s, want approval", state)
	}
	if got := fake.SinglePullRequestReads(shop, pull); got != reads {
		t.Errorf("pull request reads = %d, want %d", got, reads)
	}
}

func TestTheHeadOfTheMergeQueueLeavesChecksAfterTheBranchUpdateWhenItsScreenshotsCheckRunCompletesWithNoIssueListChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "merge", []int{41})
	fake.AddCheckRunAfterBranchUpdate(shop, checkRun("Screenshots", "", "in_progress", ""))
	old := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 1 && liveTaskState(t, server, 41) == "checks" })
	updated := head(t, fake, "mobius/41")
	if updated == old {
		t.Fatal("the head did not change")
	}
	waitForPolls(t, fake)
	if state := liveTaskState(t, server, 41); state != "checks" {
		t.Fatalf("state = %s, want checks", state)
	}

	fake.CompleteCheckRun(shop, updated, "Screenshots", "success", "No screen changed.")

	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d, want 1", calls)
	}
}
