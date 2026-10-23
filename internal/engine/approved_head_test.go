package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	waitForReadyEvents(t, server, 1)
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

func TestTheEventAfterAMergeOfMainListsTheMergeAndTheCommitsOfMainAndSaysThatTheChangeIsTheSame(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	approved := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	added := head(t, fake, "main")

	sendChat(t, server, leadChat, "Approve #41")

	event := eventAfterApproval(t, server, approved)
	merge := head(t, fake, "mobius/41")
	want := fmt.Sprintf("The Lead approved the head %.8s before. Commits after it:\n- %.8s Merge main (merge of main)\nCommits of main that these merges brought in:\n- %.8s Add price\nThe change of the pull request is the same as at the approved head: only merges of main came after it.", approved, merge, added)
	if !strings.Contains(event, want) {
		t.Errorf("event = %q, want %q", event, want)
	}
	if got := approvedHead(t, server); got != approved {
		t.Errorf("approved_head = %q, want %q", got, approved)
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

	sendChat(t, server, leadChat, "Approve #41")

	event := eventAfterApproval(t, server, approved)
	merge := head(t, fake, "mobius/41")
	for _, want := range []string{
		fmt.Sprintf("- %.8s Merge main (merge of main)\n", merge),
		"The change of the pull request can differ from the approved head:",
		fmt.Sprintf("\n- %.8s merged a change of plan.txt, so it can hold a conflict resolution", merge),
	} {
		if !strings.Contains(event, want) {
			t.Errorf("event = %q, want %q", event, want)
		}
	}
	if strings.Contains(event, "is the same as at the approved head") {
		t.Errorf("event = %q", event)
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
