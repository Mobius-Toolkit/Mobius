package engine_test

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// approvablePlan is approvable with the level "merge" and plan.txt on main, which the Implementer changes.
func approvablePlan(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadApproves+leadStarts, "[[prompts]]\n"+appendCents, noChange)
	fake.CommitFileAt(shop, "plan.txt", fmt.Sprintf(planLines, "top"), "Add the plan", seededAt)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "merge" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	return server
}

// pushCommit commits the file path on the existing branch of the remote of owner/shop, and gives the commit.
func pushCommit(t *testing.T, fake *testkit.FakeGitHub, branch, path, content, message string) string {
	t.Helper()
	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch="+branch, fake.Remote(shop), ".")
	if err := os.WriteFile(filepath.Join(work, path), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, work, "add", path)
	testkit.Git(t, work, "commit", "-m", message)
	testkit.Git(t, work, "push", "origin", branch)
	return testkit.Git(t, work, "rev-parse", "HEAD")
}

// eventAfterApproval waits for the event ready for Lead approval that names the approved head, and gives its text.
func eventAfterApproval(t *testing.T, server *testserver.Server, approved string) string {
	t.Helper()
	prompt := waitForLeadPrompt(t, server, fmt.Sprintf("The Lead approved the head %.8s before.", approved))
	parts := strings.Split(prompt, "# Event\n\n")
	return parts[len(parts)-1]
}

// approvalAgain approves the head of #41 at a merge level, adds a commit to main, and waits until the update of the
// branch moved the task back to checks. It gives the server, the approved head, and the new head. The quiet period is
// long, so a test can change the level while the task is in checks.
func approvalAgain(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string, string) {
	t.Helper()
	return approvalAgainWith(t, fake, nil)
}

// approvalAgainWith is approvalAgain with adjust after the config of the test. LeadIdleTimeout stays long unless adjust
// changes it, so a later Lead event goes to the open session.
func approvalAgainWith(t *testing.T, fake *testkit.FakeGitHub, adjust func(*config.Config)) (*testserver.Server, string, string) {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadApproves+leadStarts, commits, func(cfg *config.Config) {
		cfg.ReviewQuietPeriod = 3 * time.Second
		cfg.LeadIdleTimeout = 30 * time.Second
		if adjust != nil {
			adjust(cfg)
		}
	})
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "merge" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	approved := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" && head(t, fake, "mobius/41") != approved })
	return server, approved, head(t, fake, "mobius/41")
}

// approvedReport gives the approved report of the task of #41.
func approvedReport(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var report string
	if err := server.DB.QueryRow("SELECT coalesce(max(approved_report), '') FROM tasks WHERE issue = 41").Scan(&report); err != nil {
		t.Fatal(err)
	}
	return report
}

// approvedReportHead gives the head of the approved report of the task of #41.
func approvedReportHead(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var head string
	if err := server.DB.QueryRow("SELECT coalesce(max(approved_report_head), '') FROM tasks WHERE issue = 41").Scan(&head); err != nil {
		t.Fatal(err)
	}
	return head
}

// approvalAgainComments gives the number of comments of the approval again on the pull request of #41.
func approvalAgainComments(fake *testkit.FakeGitHub) int {
	return len(slices.DeleteFunc(fake.Comments(shop, 42), func(comment testkit.Comment) bool {
		return !strings.HasPrefix(comment.Body, "Mobius approved the head ")
	}))
}

// checkApprovalAgain checks the approval again of head after approved: the comment, the check run, the event, and
// that the Lead got no ready_for_approval for head.
func checkApprovalAgain(t *testing.T, fake *testkit.FakeGitHub, server *testserver.Server, approved, head, result string) {
	t.Helper()
	comments := fake.Comments(shop, 42)
	index := slices.IndexFunc(comments, func(comment testkit.Comment) bool {
		return strings.HasPrefix(comment.Body, "Mobius approved the head ")
	})
	if index < 0 {
		t.Fatalf("no comment of the approval again: %+v", comments)
	}
	want := fmt.Sprintf("Mobius approved the head %.8s again. Only clean merges of main came after the head %.8s, and they change no file of the pull request. The report of the Lead for %.8s:\n\nChecked the diff. Verified the tests.", head, approved, approved)
	if got := comments[index].Body; got != want {
		t.Errorf("comment = %q, want %q", got, want)
	}
	if !slices.ContainsFunc(fake.CheckRuns(shop), func(run testkit.CheckRun) bool {
		return run.Name == "Mobius" && run.HeadSHA == head && run.Conclusion == "success" &&
			run.Output != nil && run.Output.Summary == "Mobius approved the head again: only clean merges of the default branch came after the approval of the Lead."
	}) {
		t.Errorf("no Mobius check run of the approval again with success on %s: %+v", head, fake.CheckRuns(shop))
	}
	events := leadEvents(t, server)
	var again []leadEvent
	ready := 0
	for _, event := range events {
		switch event.Kind {
		case "approved_again":
			again = append(again, event)
		case "ready_for_approval":
			ready++
		}
	}
	if len(again) != 1 {
		t.Fatalf("approved_again events = %+v", again)
	}
	wantStart := fmt.Sprintf("Mobius approved again #41 \"Add plan model\": pull request #42 https://github.com/owner/shop/pull/42, head %.8s. Only clean merges of main came after the approved head %.8s. %s", head, approved, result)
	if !strings.Contains(again[0].Payload, wantStart) {
		t.Errorf("event = %q, want %q", again[0].Payload, wantStart)
	}
	if again[0].Delivered {
		t.Errorf("the approval again event started a turn: %q", again[0].Payload)
	}
	if ready != 1 {
		t.Errorf("ready_for_approval events = %d", ready)
	}
}

func TestTheApprovalAgainAddsTheInboxItemAndWaitsForTheOwner(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, approved, head := approvalAgain(t, fake)

	setWorkstreamAutopilot(t, server, "off")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "off" })
	item := waitForItem(t, server, "ready for review")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })

	if item.PullRequest != 42 || item.Merging {
		t.Errorf("item = %+v", item)
	}
	if got := approvedHead(t, server); got != head {
		t.Errorf("approved_head = %q, want %q", got, head)
	}
	if got := approvedReport(t, server); got != "Checked the diff. Verified the tests." {
		t.Errorf("approved_report = %q", got)
	}
	if calls := fake.MergeCalls(); calls != 0 {
		t.Errorf("merge calls = %d", calls)
	}
	checkApprovalAgain(t, fake, server, approved, head, "The Owner got it for review.")
}

func TestAFailedUpdateOfTheCheckRunOfTheApprovalAgainPostsOneComment(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, approved, head := approvalAgain(t, fake)

	fake.FailUpdateCheckRuns(true)
	testkit.WaitFor(t, func() bool { return approvalAgainComments(fake) == 1 && fake.FailedCheckUpdates() > 0 })
	fake.FailUpdateCheckRuns(false)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	waitForPolls(t, fake)
	if got := approvalAgainComments(fake); got != 1 {
		t.Errorf("comments of the approval again = %d, want 1", got)
	}
	checkApprovalAgain(t, fake, server, approved, head, "Mobius merges it after its checks pass.")
}

// TestAFailedLabelWriteOfTheApprovalAgainGivesOneCommentAndNoReadyEvent checks that a 5xx answer after the comment of
// the approval again returns, and that the next poll does the missing writes. The stored comment keeps one comment, and
// the Lead gets no ready_for_approval for the head. The level is off, so the retry adds the Inbox item and does not
// merge the pull request.
func TestAFailedLabelWriteOfTheApprovalAgainGivesOneCommentAndNoReadyEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, approved, head := approvalAgainWith(t, fake, func(cfg *config.Config) { cfg.ReviewQuietPeriod = 10 * time.Second })

	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") })
	setWorkstreamAutopilot(t, server, "off")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "off" })
	fake.FailAddLabels(shop, 41, true)

	testkit.WaitFor(t, func() bool { return approvalAgainComments(fake) == 1 && taskState(t, server) == "checks" })
	fake.FailAddLabels(shop, 41, false)

	item := waitForItem(t, server, "ready for review")
	testkit.WaitFor(t, func() bool {
		return taskState(t, server) == "ready_for_review" && approvedHead(t, server) == head
	})
	waitForPolls(t, fake)

	if item.PullRequest != 42 {
		t.Errorf("item = %+v", item)
	}
	if got := approvalAgainComments(fake); got != 1 {
		t.Errorf("comments of the approval again = %d, want 1", got)
	}
	checkApprovalAgain(t, fake, server, approved, head, "The Owner got it for review.")
}

// TestARefusedCommentOfTheApprovalAgainGivesTheLeadReadyForApproval checks that a 4xx answer that stays ends the
// approval again and gives the Lead the event that starts the manual approval. The Lead idle timeout is long, so the
// event goes to the open session and the fake agent cannot approve the pull request a second time from the chat history.
func TestARefusedCommentOfTheApprovalAgainGivesTheLeadReadyForApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, approved, _ := approvalAgainWith(t, fake, func(cfg *config.Config) { cfg.LeadIdleTimeout = time.Hour })
	fake.RefuseAddComment(shop, 42, http.StatusUnprocessableEntity)

	event := eventAfterApproval(t, server, approved)
	if !strings.Contains(event, "The change of the pull request is the same as at the approved head") {
		t.Errorf("event = %q", event)
	}
	if !strings.Contains(event, "\nNo commit of main after the start of the CI of the head") &&
		!strings.Contains(event, "\nThe head has no CI run, so Mobius lists no commit of main.") {
		t.Errorf("event without the newer commits text: %q", event)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	waitForPolls(t, fake)
	if got := approvalAgainComments(fake); got != 0 {
		t.Errorf("comments of the approval again = %d, want 0", got)
	}
	ready := 0
	for _, event := range leadEvents(t, server) {
		switch {
		case event.Kind == "approved_again":
			t.Errorf("approved_again event after a refused comment: %q", event.Payload)
		case event.Kind == "ready_for_approval" && strings.Contains(event.Payload, fmt.Sprintf("The Lead approved the head %.8s before.", approved)):
			ready++
		}
	}
	if ready != 1 {
		t.Errorf("ready_for_approval events for the approved head = %d, want 1", ready)
	}
}

// TestARefusedLabelOfTheApprovalAgainGivesOneReadyEvent checks that a 4xx answer that stays on a label write of the
// approval again gives the Lead one ready_for_approval event for the head. The Mobius check of the head stays open, so
// the task waits in approval, and each poll does not try the refused approval again.
func TestARefusedLabelOfTheApprovalAgainGivesOneReadyEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, approved, head := approvalAgainWith(t, fake, func(cfg *config.Config) {
		cfg.ReviewQuietPeriod = 500 * time.Millisecond
		cfg.LeadIdleTimeout = time.Hour
	})

	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") })
	fake.RefuseAddLabels(shop, 41, http.StatusUnprocessableEntity)

	event := eventAfterApproval(t, server, approved)
	if !strings.Contains(event, "The change of the pull request is the same as at the approved head") {
		t.Errorf("event = %q", event)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	before := fake.NotModifiedCount()
	testkit.WaitFor(t, func() bool { return fake.NotModifiedCount() >= before+20 })

	if got := approvalAgainComments(fake); got != 1 {
		t.Errorf("comments of the approval again = %d, want 1", got)
	}
	if !slices.ContainsFunc(fake.CheckRuns(shop), func(run testkit.CheckRun) bool {
		return run.Name == "Mobius" && run.HeadSHA == head && run.Status == "in_progress" &&
			run.Output != nil && run.Output.Title == "Wait for the Lead approval"
	}) {
		t.Errorf("no open Mobius check run of the head %s: %+v", head, fake.CheckRuns(shop))
	}
	ready := 0
	events := leadEvents(t, server)
	for _, event := range events {
		switch event.Kind {
		case "approved_again":
			t.Errorf("approved_again event after a refused label write: %q", event.Payload)
		case "ready_for_approval":
			if strings.Contains(event.Payload, fmt.Sprintf("The Lead approved the head %.8s before.", approved)) {
				ready++
			}
		}
	}
	if ready != 1 {
		t.Errorf("ready_for_approval events for the approved head = %d, want 1: %+v", ready, events)
	}
}

// TestTwoUpdateHeadsAfterOneLeadApprovalNameTheLeadHeadInTheSecondComment checks that the report of the Lead stays with
// the head that the Lead approved, also after an earlier approval again changed approved_head.
func TestTwoUpdateHeadsAfterOneLeadApprovalNameTheLeadHeadInTheSecondComment(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, approved, first := approvalAgainWith(t, fake, func(cfg *config.Config) {
		cfg.ReviewQuietPeriod = 5 * time.Second
		cfg.LeadIdleTimeout = time.Hour
	})
	fake.CommitFile(shop, "other.txt", "other\n", "Add other")

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 2 })
	second := head(t, fake, "mobius/41")
	if second == first {
		t.Fatalf("the second update did not change the head: %s", first)
	}
	testkit.WaitFor(t, func() bool { return approvalAgainComments(fake) == 2 })

	var comments []string
	for _, comment := range fake.Comments(shop, 42) {
		if strings.HasPrefix(comment.Body, "Mobius approved the head ") {
			comments = append(comments, comment.Body)
		}
	}
	want := fmt.Sprintf("Mobius approved the head %.8s again. Only clean merges of main came after the head %.8s, and they change no file of the pull request. The report of the Lead for %.8s:\n\nChecked the diff. Verified the tests.", second, first, approved)
	if !slices.Contains(comments, want) {
		t.Errorf("comments = %q, want %q", comments, want)
	}
	if got := approvedReportHead(t, server); got != approved {
		t.Errorf("approved_report_head = %q, want %q", got, approved)
	}
}

func TestTheEventAfterAMergeWithAConflictThatTheBranchResolvedByHandNamesTheFile(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvablePlan(t, fake)
	approved := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "other.txt", "other\n", "Add other")
	fake.HoldBranchUpdates()

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 1 })
	fake.CommitFile(shop, "plan.txt", fmt.Sprintf(planLines, "top")+"dollars\n", "Append dollars")
	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
	merge := exec.Command("git", "-c", "user.name=owner", "-c", "user.email=owner@example.com", "merge", "--no-ff", "--no-edit", "origin/main")
	merge.Dir = work
	if err := merge.Run(); err == nil {
		t.Fatal("the merge of main has no conflict")
	}
	if err := os.WriteFile(filepath.Join(work, "plan.txt"), []byte(fmt.Sprintf(planLines, "top")+"cents\ndollars\nHAND\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, work, "add", "plan.txt")
	testkit.Git(t, work, "commit", "-m", "Merge main by hand")
	testkit.Git(t, work, "push", "origin", "mobius/41")
	hand := testkit.Git(t, work, "rev-parse", "HEAD")
	fake.ReleaseBranchUpdates()

	event := eventAfterApproval(t, server, approved)
	want := fmt.Sprintf("\n- %.8s holds a hand resolution in plan.txt", hand)
	if !strings.Contains(event, want) {
		t.Errorf("event = %q, want %q", event, want)
	}
	if strings.Contains(event, "is a clean merge") {
		t.Errorf("event = %q", event)
	}
}

func TestTheEventAfterANewCommitOfTheBranchListsItWithNoMergeMarkAndSaysThatTheChangeCanDiffer(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	approved := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.HoldBranchUpdates()

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 1 })
	pushed := pushCommit(t, fake, "mobius/41", "extra.txt", "extra\n", "Add extra")
	fake.ReleaseBranchUpdates()

	event := eventAfterApproval(t, server, approved)
	for _, want := range []string{
		fmt.Sprintf("Commits after it:\n- %.8s Add extra\n", pushed),
		"The change of the pull request can differ from the approved head:",
		fmt.Sprintf("\n- %.8s is not a merge of main", pushed),
	} {
		if !strings.Contains(event, want) {
			t.Errorf("event = %q, want %q", event, want)
		}
	}
	if strings.Contains(event, "Add extra (merge of main)") || strings.Contains(event, "is the same as at the approved head") {
		t.Errorf("event = %q", event)
	}
}

func TestTheEventAfterAMergeWithAHandChangeAndNoSharedBroughtInCommitGetsReadyForApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	approved := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "other.txt", "other\n", "Add other")
	fake.HoldBranchUpdates()

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 1 })
	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
	testkit.Git(t, work, "merge", "--no-ff", "--no-commit", "origin/main")
	if err := os.WriteFile(filepath.Join(work, "plan.txt"), []byte("cents\nHAND\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, work, "add", "plan.txt")
	testkit.Git(t, work, "commit", "-m", "Merge main by hand")
	testkit.Git(t, work, "push", "origin", "mobius/41")
	hand := testkit.Git(t, work, "rev-parse", "HEAD")
	fake.ReleaseBranchUpdates()

	event := eventAfterApproval(t, server, approved)
	if !strings.Contains(event, " ready for Lead approval of #41 ") {
		t.Errorf("event = %q", event)
	}
	want := fmt.Sprintf("\n- %.8s holds a hand resolution in plan.txt", hand)
	if !strings.Contains(event, want) {
		t.Errorf("event = %q, want %q", event, want)
	}
	if strings.Contains(event, "is the same as at the approved head") {
		t.Errorf("event = %q", event)
	}
	waitForPolls(t, fake)
	if got := approvalAgainComments(fake); got != 0 {
		t.Errorf("comments of the approval again = %d, want 0", got)
	}
	for _, event := range leadEvents(t, server) {
		if event.Kind == "approved_again" {
			t.Errorf("approved_again event for a hand change: %q", event.Payload)
		}
	}
}

func TestTheEventAfterAMergeOfAChangeOfAFileOfThePullRequestNamesTheMergeAndTheFile(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvablePlan(t, fake)
	approved := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "plan.txt", fmt.Sprintf(planLines, "TOP"), "Change the plan heading")
	added := head(t, fake, "main")

	sendChat(t, server, leadChat, "Approve #41")

	event := eventAfterApproval(t, server, approved)
	merge := head(t, fake, "mobius/41")
	for _, want := range []string{
		fmt.Sprintf("- %.8s Merge main (merge of main)\n", merge),
		fmt.Sprintf("Commits of main that these merges brought in:\n- %.8s Change the plan heading", added),
		"The change of the pull request can differ from the approved head:",
		fmt.Sprintf("\n- %.8s is a clean merge", merge),
	} {
		if !strings.Contains(event, want) {
			t.Errorf("event = %q, want %q", event, want)
		}
	}
	if strings.Contains(event, "is the same as at the approved head") {
		t.Errorf("event = %q", event)
	}
	if got := approvedHead(t, server); got != approved {
		t.Errorf("approved_head = %q, want %q", got, approved)
	}
}

func TestTheEventOfTheFirstApprovalHasNoApprovalLine(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)

	prompt := waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\"")

	if strings.Contains(prompt, "The Lead approved the head") {
		t.Errorf("prompt = %q", prompt)
	}
	if got := approvedHead(t, server); got != "" {
		t.Errorf("approved_head = %q", got)
	}
}

func approvedHead(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var approved string
	if err := server.DB.QueryRow("SELECT coalesce(max(approved_head), '') FROM tasks WHERE issue = 41").Scan(&approved); err != nil {
		t.Fatal(err)
	}
	return approved
}
