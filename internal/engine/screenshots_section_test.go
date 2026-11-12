package engine_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	pendingScreenshots = "The screenshots are pending. See the [checks of the commit](https://github.com/owner/shop/commit/abc/checks)."
	changedSentence    = "The screenshots section of the pull request shows changed screens, or the screenshots failed. Compare each changed screen with the issue before the approval."
)

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

// passCIWithSection sets the screenshots section and waits for the polls that read it, and then passes the CI. A poll
// reads the pull request before its check runs, so a CI that passes before the poll read the section can approve the
// head with no section.
func passCIWithSection(t *testing.T, fake *testkit.FakeGitHub, sha, text string) {
	t.Helper()
	fake.SetBody(shop, 42, screenshotsSectionOf(text))
	waitForPolls(t, fake)
	passCI(fake, sha)
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

func TestAHeadWithPendingScreenshotsStaysInChecksUntilTheSectionSaysNoScreenChanged(t *testing.T) {
	t.Parallel()
	fake, server, sha := reviewedHeadWithCI(t)
	passCIWithSection(t, fake, sha, pendingScreenshots)

	waitForPolls(t, fake)

	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s, want checks", state)
	}
	checkNoApproval(t, fake, server)
	if ready := readyForApprovalEvents(t, server); len(ready) != 0 {
		t.Errorf("ready_for_approval events: %+v", ready)
	}

	fake.SetBody(shop, 42, screenshotsSectionOf("No screen changed."))

	event := autoApprovedEvent(t, server)
	if !strings.Contains(event.Payload, "The Reviewer found no open finding on this head.") {
		t.Errorf("event = %q", event.Payload)
	}
	if got := approvedReport(t, server); got != autoApprovedReport(sha) {
		t.Errorf("approved_report = %q", got)
	}
}

func checkChangedScreenshots(t *testing.T, text string) {
	t.Helper()
	fake, server, sha := reviewedHeadWithCI(t)
	passCIWithSection(t, fake, sha, text)

	testkit.WaitFor(t, func() bool { return len(readyForApprovalEvents(t, server)) > 0 })
	waitForPolls(t, fake)

	if ready := readyForApprovalEvents(t, server); len(ready) != 1 {
		t.Fatalf("ready_for_approval events: %+v", ready)
	}
	if payload := readyForApprovalEvents(t, server)[0].Payload; !strings.Contains(payload, "\n"+changedSentence) {
		t.Errorf("event = %q, want the sentence %q", payload, changedSentence)
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

func TestAHeadWithChangedScreensGivesTheLeadReadyForApprovalAndNoApproval(t *testing.T) {
	t.Parallel()
	checkChangedScreenshots(t, "| Screen | State | main | This pull request |\n|---|---|---|---|\n| chat | desktop | ![](a.png) | ![](b.png) |")
}

func TestAHeadWithFailedScreenshotsGivesTheLeadReadyForApprovalAndNoApproval(t *testing.T) {
	t.Parallel()
	checkChangedScreenshots(t, "The screenshots failed. See the [run](https://github.com/owner/shop/actions/runs/1).")
}

func TestAHeadWithPendingScreenshotsForMoreThanTwoHoursGivesTheLeadALongCIEventForTheScreenshots(t *testing.T) {
	t.Parallel()
	fake, server, sha := reviewedHeadWithCI(t)
	passCIWithSection(t, fake, sha, pendingScreenshots)
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
	if want := "head " + sha + ". These runs did not complete: screenshots."; !strings.Contains(event.Payload, want) {
		t.Errorf("event = %q, want %q", event.Payload, want)
	}
	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s, want checks", state)
	}
	checkNoApproval(t, fake, server)
}

func TestATaskInChecksReadsThePullRequestAgainAtEachPollAndLeavesChecksWhenTheBodyChangesWithNoIssueListChange(t *testing.T) {
	t.Parallel()
	fake, server, sha := reviewedHeadWithCI(t)
	passCIWithSection(t, fake, sha, pendingScreenshots)
	waitForPolls(t, fake)
	if state := taskState(t, server); state != "checks" {
		t.Fatalf("state = %s, want checks", state)
	}

	fake.SetPullRequestBody(shop, 42, screenshotsSectionOf("No screen changed."))

	autoApprovedEvent(t, server)
	if got := approvedReport(t, server); got != autoApprovedReport(sha) {
		t.Errorf("approved_report = %q", got)
	}
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

func TestTheHeadOfTheMergeQueueLeavesChecksAfterAMergeOfTheDefaultBranchWhenTheBodyChangesWithNoIssueListChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := queueTasks(t, fake, "merge", []int{41})
	pull := liveTask(t, server, 41).PullRequest.Int64
	fake.SetBody(shop, pull, screenshotsSectionOf(pendingScreenshots))
	waitForPolls(t, fake)
	old := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	update := mergeMainAtMerge(fake)

	sendChat(t, server, leadChat, "Approve #41")
	update()

	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "checks" })
	if head(t, fake, "mobius/41") == old {
		t.Fatal("the head did not change")
	}
	waitForPolls(t, fake)
	if state := liveTaskState(t, server, 41); state != "checks" {
		t.Fatalf("state = %s, want checks", state)
	}

	fake.SetPullRequestBody(shop, pull, screenshotsSectionOf("No screen changed."))

	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 41) == "" })
	if calls := fake.MergeCalls(); calls != 2 {
		t.Errorf("merge calls = %d, want 2 with the request with the old head", calls)
	}
}
