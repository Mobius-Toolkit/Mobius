package engine_test

import (
	"fmt"
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
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadApproves+leadStarts, commits, func(cfg *config.Config) {
		cfg.ReviewQuietPeriod = 3 * time.Second
		cfg.LeadIdleTimeout = 30 * time.Second
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
		return run.Name == "Mobius" && run.HeadSHA == head && run.Conclusion == "success"
	}) {
		t.Errorf("no Mobius check run with success on %s: %+v", head, fake.CheckRuns(shop))
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
