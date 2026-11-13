package engine_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const readyForApprovalMarker = " ready for Lead approval of #41 \"Add plan model\""

// reviewedHeadServer starts the task of #41 with the Lead script lead, and lets Mobius approve a reviewed head.
// ReviewQuietPeriod is long, so a test can change the pull request or the fake GitHub while the task is in checks.
func reviewedHeadServer(t *testing.T, fake *testkit.FakeGitHub, lead string, quietPeriod time.Duration) *testserver.Server {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, lead, commits, func(cfg *config.Config) {
		cfg.ReviewQuietPeriod = quietPeriod
		cfg.LeadIdleTimeout = 30 * time.Second
	})
	server.ApproveReviewedHeads(t)
	return server
}

func reviewedHead(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var reviewed string
	if err := server.DB.QueryRow("SELECT coalesce(max(reviewed_head), '') FROM tasks WHERE issue = 41").Scan(&reviewed); err != nil {
		t.Fatal(err)
	}
	return reviewed
}

func autoApprovedReport(head string) string {
	return fmt.Sprintf("Mobius approved head %.8s: the Reviewer found no open finding on this head, and each check of the head passed.", head)
}

// checkNoLeadTurnForTheHead checks that the Lead got no ready_for_approval event and no prompt of one.
func checkNoLeadTurnForTheHead(t *testing.T, server *testserver.Server) {
	t.Helper()
	for _, event := range leadEvents(t, server) {
		if event.Kind == "ready_for_approval" {
			t.Errorf("ready_for_approval event: %q", event.Payload)
		}
	}
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, readyForApprovalMarker) {
			t.Errorf("the Lead got a prompt for the approval: %q", prompt)
		}
	}
}

// autoApprovedEvent waits for the event auto_approved, and gives it.
func autoApprovedEvent(t *testing.T, server *testserver.Server) leadEvent {
	t.Helper()
	return testkit.WaitForValue(t, func() (leadEvent, bool) {
		events := leadEvents(t, server)
		index := slices.IndexFunc(events, func(event leadEvent) bool { return event.Kind == "auto_approved" })
		if index < 0 {
			return leadEvent{}, false
		}
		return events[index], true
	})
}

func TestMobiusApprovesAHeadThatTheReviewerApprovedWithNoLeadTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, 100*time.Millisecond)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	event := autoApprovedEvent(t, server)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	approved := head(t, fake, "mobius/41")

	want := fmt.Sprintf("Mobius approved #41 \"Add plan model\": pull request #42 https://github.com/owner/shop/pull/42, head %.8s. The Reviewer found no open finding on this head. The Owner got it for review.", approved)
	if !strings.Contains(event.Payload, want) {
		t.Errorf("event = %q, want %q", event.Payload, want)
	}
	if event.Delivered {
		t.Errorf("the auto_approved event started a turn: %q", event.Payload)
	}
	comments := fake.Comments(shop, 42)
	if !slices.ContainsFunc(comments, func(comment testkit.Comment) bool { return comment.Body == autoApprovedReport(approved) }) {
		t.Errorf("no approval comment: %+v", comments)
	}
	if got := approvedReport(t, server); got != autoApprovedReport(approved) {
		t.Errorf("approved_report = %q", got)
	}
	if got := approvedHead(t, server); got != approved {
		t.Errorf("approved_head = %q, want %q", got, approved)
	}
	wantSummary := "Mobius approved the pull request: the Reviewer found no open finding on this head, and each check of the head passed."
	if !slices.ContainsFunc(fake.CheckRuns(shop), func(run testkit.CheckRun) bool {
		return run.Name == "Mobius" && run.HeadSHA == approved && run.Conclusion == "success" &&
			run.Output != nil && run.Output.Summary == wantSummary
	}) {
		t.Errorf("no Mobius check run of %s with the summary %q: %+v", approved, wantSummary, fake.CheckRuns(shop))
	}
	checkNoLeadTurnForTheHead(t, server)
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, "# Event\n\n"+event.Payload) {
			t.Errorf("the auto_approved event started a turn: %q", event.Payload)
		}
	}
}

func TestMobiusMergesAHeadThatTheReviewerApprovedWithNoLeadTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, 100*time.Millisecond)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "merge" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	event := autoApprovedEvent(t, server)
	testkit.WaitFor(t, func() bool { return fake.MergeCalls() == 1 && taskState(t, server) == "" })

	if !strings.HasSuffix(event.Payload, "Mobius merges it after its checks pass.") {
		t.Errorf("event = %q", event.Payload)
	}
	checkNoLeadTurnForTheHead(t, server)
}

// TestAHeadAfterTheReviewedHeadGivesTheLeadReadyForApproval checks that a head that no review round approved gets the
// Lead turn. The Lead updates the branch while the task waits in checks, so the new head has no review round.
func TestAHeadAfterTheReviewedHeadGivesTheLeadReadyForApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadUpdates+leadStarts, 10*time.Second)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" && reviewedHead(t, server) != "" })
	reviewed := head(t, fake, "mobius/41")
	if got := reviewedHead(t, server); got != reviewed {
		t.Fatalf("reviewed_head = %q, want %q", got, reviewed)
	}
	fake.CommitFile(shop, "other.txt", "content\n", "Commit on main")

	sendChat(t, server, leadChat, "Update #41")

	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != reviewed })
	prompt := waitForLeadPrompt(t, server, readyForApprovalMarker)
	if !strings.Contains(prompt, fmt.Sprintf("CI of head %.8s:", head(t, fake, "mobius/41"))) {
		t.Errorf("prompt = %q", prompt)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	for _, event := range leadEvents(t, server) {
		if event.Kind == "auto_approved" {
			t.Errorf("auto_approved event for a head with no review round: %q", event.Payload)
		}
	}
	for _, comment := range fake.Comments(shop, 42) {
		if strings.HasPrefix(comment.Body, "Mobius approved head ") {
			t.Errorf("approval comment: %q", comment.Body)
		}
	}
}

// TestARefusedCommentOfTheApprovalOfAReviewedHeadDoesNotStopTheApproval checks that a 422 answer of GitHub marks the
// outgoing change of the approval comment refused, and that Mobius still approves the head and stores its report. The
// Lead gets no ready_for_approval event.
func TestARefusedCommentOfTheApprovalOfAReviewedHeadDoesNotStopTheApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, 5*time.Second)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" && reviewedHead(t, server) != "" })
	fake.RefuseAddComment(shop, 42, http.StatusUnprocessableEntity)

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind == "auto_approved" })
	})
	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "refused") == 1 })
	waitForPolls(t, fake)
	if got := fake.RefusedCommentCalls(); got != 1 {
		t.Errorf("refused approval comments = %d, want 1", got)
	}
	for _, event := range leadEvents(t, server) {
		if event.Kind == "ready_for_approval" {
			t.Errorf("ready_for_approval event after a refused comment: %q", event.Payload)
		}
	}
	if got := approvedReport(t, server); got == "" {
		t.Error("the task stores no approved report")
	}
}

// TestAnApprovalAgainAfterAnApprovalOfMobiusUsesTheReportOfMobius checks that the report that Mobius stored for the
// reviewed head goes into the comment of the approval again, after a clean merge of main changed the head.
func TestAnApprovalAgainAfterAnApprovalOfMobiusUsesTheReportOfMobius(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, 3*time.Second)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "merge" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" && reviewedHead(t, server) != "" })
	reviewed := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	update := mergeMainAtMerge(fake)

	update()
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind == "approved_again" })
	})

	updated := head(t, fake, "mobius/41")
	if updated == reviewed {
		t.Fatalf("the head did not change: %s", updated)
	}
	want := fmt.Sprintf("Mobius approved the head %.8s again. Only clean merges of main came after the head %.8s, and the changed lines of the pull request are the same. The report of the approval of %.8s:\n\n%s", updated, reviewed, reviewed, autoApprovedReport(reviewed))
	comments := fake.Comments(shop, 42)
	if !slices.ContainsFunc(comments, func(comment testkit.Comment) bool { return comment.Body == want }) {
		t.Errorf("no comment %q in %+v", want, comments)
	}
	wantSummary := "Mobius approved the head again: only clean merges of the default branch came after the approved head."
	if !slices.ContainsFunc(fake.CheckRuns(shop), func(run testkit.CheckRun) bool {
		return run.Name == "Mobius" && run.HeadSHA == updated && run.Conclusion == "success" &&
			run.Output != nil && run.Output.Summary == wantSummary
	}) {
		t.Errorf("no Mobius check run of %s with the summary %q: %+v", updated, wantSummary, fake.CheckRuns(shop))
	}
	if got := approvedReport(t, server); got != autoApprovedReport(reviewed) {
		t.Errorf("approved_report = %q", got)
	}
	if got := approvedReportHead(t, server); got != reviewed {
		t.Errorf("approved_report_head = %q, want %q", got, reviewed)
	}
	checkNoLeadTurnForTheHead(t, server)
}

// TestAFailedLabelWriteOfAnApprovalOfMobiusGivesOneComment checks that a 5xx answer after the comment of an approval
// of Mobius returns, and that the next poll does the missing writes with no second comment.
func TestAFailedLabelWriteOfAnApprovalOfMobiusGivesOneComment(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, 5*time.Second)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" && reviewedHead(t, server) != "" })
	approved := head(t, fake, "mobius/41")
	fake.FailAddLabels(shop, 41, true)

	approvalComments := func() int {
		return len(slices.DeleteFunc(fake.Comments(shop, 42), func(comment testkit.Comment) bool {
			return comment.Body != autoApprovedReport(approved)
		}))
	}
	testkit.WaitFor(t, func() bool { return approvalComments() == 1 && taskState(t, server) == "checks" })
	fake.FailAddLabels(shop, 41, false)

	autoApprovedEvent(t, server)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	waitForPolls(t, fake)
	if got := approvalComments(); got != 1 {
		t.Errorf("approval comments = %d, want 1", got)
	}
	checkNoLeadTurnForTheHead(t, server)
}

// TestALaterHeadAfterAnApprovalOfMobiusNamesMobiusInTheEvent checks that the event ready_for_approval for a head with
// a hand resolution says that Mobius or the Lead approved the earlier head.
func TestALaterHeadAfterAnApprovalOfMobiusNamesMobiusInTheEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, 3*time.Second)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "merge" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" && reviewedHead(t, server) != "" })
	approved := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	reached, release := fake.HoldMerge(shop, 42)

	<-reached
	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
	testkit.Git(t, work, "merge", "--no-ff", "--no-commit", "origin/main")
	if err := os.WriteFile(filepath.Join(work, "plan.txt"), []byte("cents\nHAND\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, work, "add", "plan.txt")
	testkit.Git(t, work, "commit", "-m", "Merge main by hand")
	testkit.Git(t, work, "push", "origin", "mobius/41")
	release()

	prompt := waitForLeadPrompt(t, server, fmt.Sprintf("Mobius or the Lead approved the head %.8s before.", approved))
	event := prompt[strings.LastIndex(prompt, "# Event\n\n"):]
	if !strings.Contains(event, readyForApprovalMarker) {
		t.Errorf("event = %q", event)
	}
	if strings.Contains(event, "The Lead approved the head") {
		t.Errorf("event = %q", event)
	}
}

// TestAFollowUpOfTheReviewGivesTheLeadReadyForApprovalAndNoMerge checks that a clean review round that sends a
// follow-up to the Lead leaves the approval to the Lead, so the pull request does not merge before the Lead decides.
func TestAFollowUpOfTheReviewGivesTheLeadReadyForApprovalAndNoMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	gate := filepath.Join(t.TempDir(), "gate")
	reviewer := fmt.Sprintf("[[prompts]]\nwhen = \"You are the Reviewer\"\nshell = \"while [ ! -e '%s' ]; do sleep 0.05; done\"\n\n", gate)
	server := reviewedHeadServer(t, fake, reviewer+leadStarts, 100*time.Millisecond)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "merge" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		return len(fake.PullRequests(shop)) == 1 &&
			slices.ContainsFunc(fake.Comments(shop, 42), func(comment testkit.Comment) bool { return strings.HasPrefix(comment.Body, "Review started") })
	})
	thread := fake.AddReviewComment(shop, 42, 0, testkit.AppSlug+"[bot]", "Name the unit.\n\n<!-- mobius:follow-up -->")

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	waitForLeadPrompt(t, server, readyForApprovalMarker)
	waitForLeadPrompt(t, server, " follow-up on pull request #42 of #41 \"Add plan model\", item "+itoa(thread))
	waitForPolls(t, fake)
	if got := reviewedHead(t, server); got != "" {
		t.Errorf("reviewed_head = %q", got)
	}
	if calls := fake.MergeCalls(); calls != 0 {
		t.Errorf("merge calls = %d", calls)
	}
	for _, event := range leadEvents(t, server) {
		if event.Kind == "auto_approved" {
			t.Errorf("auto_approved event after a follow-up: %q", event.Payload)
		}
	}
}

// TestAQuestionOnTheTaskIssueGivesTheLeadReadyForApproval checks that a clean review round and a passed CI give the
// event ready_for_approval while the task issue has mobius:question.
func TestAQuestionOnTheTaskIssueGivesTheLeadReadyForApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, 5*time.Second)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" && reviewedHead(t, server) != "" })
	fake.AddLabel(shop, 41, "mobius:question", testkit.AppSlug+"[bot]")

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind == "ready_for_approval" })
	})
	waitForPolls(t, fake)
	for _, event := range leadEvents(t, server) {
		if event.Kind == "auto_approved" {
			t.Errorf("auto_approved event with an open question: %q", event.Payload)
		}
	}
	if got := approvedReport(t, server); got != "" {
		t.Errorf("approved_report = %q", got)
	}
}

// TestAFollowUpOfAFailedReviewerSessionGivesTheLeadReadyForApproval checks that a Reviewer session that replaces a
// failed one, after the failed one sent a follow-up, does not set the reviewed head: the Lead has not decided on the
// follow-up.
func TestAFollowUpOfAFailedReviewerSessionGivesTheLeadReadyForApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	gate := filepath.Join(t.TempDir(), "gate")
	reviewer := fmt.Sprintf("[[prompts]]\nwhen = \"You are the Reviewer\"\nshell = \"while [ ! -e '%s' ]; do sleep 0.05; done\"\n\n", gate)
	server := reviewedHeadServer(t, fake, reviewer+leadStarts, 100*time.Millisecond)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "merge" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		return len(fake.PullRequests(shop)) == 1 &&
			slices.ContainsFunc(fake.Comments(shop, 42), func(comment testkit.Comment) bool { return strings.HasPrefix(comment.Body, "Review started") })
	})
	fake.AddReviewComment(shop, 42, 0, testkit.AppSlug+"[bot]", "Name the unit.\n\n<!-- mobius:follow-up -->")
	fake.FailUpdateCheckRuns(true)

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return fake.FailedCheckUpdates() > 0 })
	fake.FailUpdateCheckRuns(false)

	waitForLeadPrompt(t, server, readyForApprovalMarker)
	waitForPolls(t, fake)
	if got := reviewedHead(t, server); got != "" {
		t.Errorf("reviewed_head = %q", got)
	}
	if calls := fake.MergeCalls(); calls != 0 {
		t.Errorf("merge calls = %d", calls)
	}
	if got := len(roleSessions(t, server, engine.ReviewerRole)); got < 2 {
		t.Errorf("Reviewer sessions = %d, want 2 or more", got)
	}
	for _, event := range leadEvents(t, server) {
		if event.Kind == "auto_approved" {
			t.Errorf("auto_approved event after a follow-up: %q", event.Payload)
		}
	}
}
