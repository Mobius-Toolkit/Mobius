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

// TestARefusedApprovalOfAReviewedHeadGivesTheLeadReadyForApproval checks that a 422 answer of GitHub ends the approval
// of Mobius and gives the Lead the event that starts the manual approval.
func TestARefusedApprovalOfAReviewedHeadGivesTheLeadReadyForApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := reviewedHeadServer(t, fake, leadStarts, 5*time.Second)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" && reviewedHead(t, server) != "" })
	fake.RefuseAddComment(shop, 42, http.StatusUnprocessableEntity)

	waitForLeadPrompt(t, server, readyForApprovalMarker)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	waitForPolls(t, fake)

	ready := 0
	for _, event := range leadEvents(t, server) {
		switch event.Kind {
		case "auto_approved":
			t.Errorf("auto_approved event after a refused comment: %q", event.Payload)
		case "ready_for_approval":
			ready++
		}
	}
	if ready != 1 {
		t.Errorf("ready_for_approval events = %d, want 1", ready)
	}
	if got := approvedReport(t, server); got != "" {
		t.Errorf("approved_report = %q", got)
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

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind == "approved_again" })
	})

	updated := head(t, fake, "mobius/41")
	if updated == reviewed {
		t.Fatalf("the head did not change: %s", updated)
	}
	want := fmt.Sprintf("Mobius approved the head %.8s again. Only clean merges of main came after the head %.8s, and they change no file of the pull request. The report of the Lead for %.8s:\n\n%s", updated, reviewed, reviewed, autoApprovedReport(reviewed))
	comments := fake.Comments(shop, 42)
	if !slices.ContainsFunc(comments, func(comment testkit.Comment) bool { return comment.Body == want }) {
		t.Errorf("no comment %q in %+v", want, comments)
	}
	if got := approvedReport(t, server); got != autoApprovedReport(reviewed) {
		t.Errorf("approved_report = %q", got)
	}
	if got := approvedReportHead(t, server); got != reviewed {
		t.Errorf("approved_report_head = %q, want %q", got, reviewed)
	}
	checkNoLeadTurnForTheHead(t, server)
}
