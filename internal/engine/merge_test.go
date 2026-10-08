package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// passMobius adds the Mobius check run with the conclusion success to the commit sha.
func passMobius(fake *testkit.FakeGitHub, sha string) {
	fake.AddCheckRun(shop, checkRun("Mobius", sha, "completed", "success"))
}

// approvedReview gives the kept approval of the task of #41, or "".
func approvedReview(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var id string
	if err := server.DB.QueryRow("SELECT coalesce(max(approved_review), '') FROM tasks WHERE issue = 41").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// dismissedApproval tells if the kept approval of the task of #41 is dismissed or refused.
func dismissedApproval(t *testing.T, server *testserver.Server) bool {
	t.Helper()
	var dismissed bool
	if err := server.DB.QueryRow("SELECT coalesce(max(approved_review = refused_review), 0) FROM tasks WHERE issue = 41").Scan(&dismissed); err != nil {
		t.Fatal(err)
	}
	return dismissed
}

func TestAnApprovalOfATrustedUserSquashMergesThePullRequestAndEndsTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha := seedWaiting(t, fake, "approval")
	passMobius(fake, sha)

	fake.AddReview(shop, 42, "owner", "APPROVED", "")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if got := testkit.Git(t, fake.Remote(shop), "rev-parse", "main^{tree}"); got != testkit.Git(t, fake.Remote(shop), "rev-parse", sha+"^{tree}") {
		t.Errorf("tree of main = %s", got)
	}
	if got := testkit.Git(t, fake.Remote(shop), "rev-list", "--parents", "-n", "1", "main"); len(strings.Fields(got)) != 2 {
		t.Errorf("the merge commit has parents %q", got)
	}
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d", calls)
	}
	waitForLeadPrompt(t, server, " end of #41 \"Add plan model\": pull request #42 merged.")
}

func TestAnApprovedPullRequestMergesOnALaterPollWhenTheConditionBecomesTrue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// block makes the condition false before the approval and gives the function that makes it true.
		block func(fake *testkit.FakeGitHub, sha string) func()
	}{
		{"draft", func(fake *testkit.FakeGitHub, _ string) func() {
			fake.SetDraft(shop, 42, true)
			return func() { fake.SetDraft(shop, 42, false) }
		}},
		{"mergeable not calculated", func(fake *testkit.FakeGitHub, _ string) func() {
			fake.SetMergeableUnknown(shop, 42, true)
			return func() { fake.SetMergeableUnknown(shop, 42, false) }
		}},
		{"running check run", func(fake *testkit.FakeGitHub, sha string) func() {
			id := fake.AddCheckRun(shop, checkRun("build", sha, "in_progress", ""))
			return func() { fake.SetCheckRunStatus(id, "completed", "skipped") }
		}},
		{"Mobius check run", func(fake *testkit.FakeGitHub, sha string) func() {
			id := fake.AddCheckRun(shop, checkRun("Mobius", sha, "in_progress", ""))
			return func() { fake.SetCheckRunStatus(id, "completed", "success") }
		}},
		{"open review thread of an untrusted user", func(fake *testkit.FakeGitHub, _ string) func() {
			root := fake.AddReviewComment(shop, 42, 0, "mallory", "Rename this.")
			return func() { fake.ResolveReviewThread(root) }
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := testkit.NewFakeGitHub(t)
			server, sha := seedWaiting(t, fake, "approval")
			passMobius(fake, sha)
			unblock := test.block(fake, sha)

			fake.AddReview(shop, 42, "owner", "APPROVED", "")
			testkit.WaitFor(t, func() bool { return approvedReview(t, server) != "" })
			waitForPolls(t, fake)

			if state := taskState(t, server); state != "approval" {
				t.Errorf("state = %s", state)
			}
			if calls := fake.MergeCalls(); calls != 0 {
				t.Errorf("merge calls = %d", calls)
			}

			unblock()

			testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
			if calls := fake.MergeCalls(); calls != 1 {
				t.Errorf("merge calls = %d", calls)
			}
		})
	}
}

func TestAnApprovedPullRequestWithAMergeConflictInNeedsHumanMergesAfterTheConflictIsGone(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := seedWaiting(t, fake, "needs_human")
	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
	if err := os.WriteFile(filepath.Join(work, "plan.txt"), []byte("cents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, work, "add", "plan.txt")
	testkit.Git(t, work, "commit", "-m", "Use cents")
	testkit.Git(t, work, "push", "origin", "HEAD:refs/heads/mobius/41")
	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")
	passMobius(fake, head(t, fake, "mobius/41"))

	fake.AddReview(shop, 42, "owner", "APPROVED", "")
	testkit.WaitFor(t, func() bool { return approvedReview(t, server) != "" })
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if calls := fake.MergeCalls(); calls != 0 {
		t.Errorf("merge calls = %d", calls)
	}

	testkit.Git(t, work, "fetch", "origin", "main")
	testkit.Git(t, work, "merge", "-X", "ours", "origin/main")
	testkit.Git(t, work, "push", "origin", "HEAD:refs/heads/mobius/41")
	passMobius(fake, head(t, fake, "mobius/41"))

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d", calls)
	}
}

func TestANewCommitDoesNotCancelTheApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := seedWaiting(t, fake, "approval")
	fake.AddReview(shop, 42, "owner", "APPROVED", "")
	testkit.WaitFor(t, func() bool { return approvedReview(t, server) != "" })
	waitForPolls(t, fake)

	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
	testkit.Git(t, work, "commit", "--allow-empty", "-m", "Second change")
	testkit.Git(t, work, "push", "origin", "HEAD:refs/heads/mobius/41")
	next := head(t, fake, "mobius/41")
	waitForPolls(t, fake)

	if calls := fake.MergeCalls(); calls != 0 {
		t.Errorf("merge calls = %d", calls)
	}

	passMobius(fake, next)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if got := head(t, fake, "main^{tree}"); got != head(t, fake, next+"^{tree}") {
		t.Errorf("tree of main = %s", got)
	}
}

func TestAnApprovalOfAnotherReviewerOrAnotherStateDoesNotMerge(t *testing.T) {
	t.Parallel()
	tests := []struct{ author, state string }{
		{"mallory", "APPROVED"},
		{"coderabbitai[bot]", "APPROVED"},
		{"mobius-test[bot]", "APPROVED"},
		{"owner", "CHANGES_REQUESTED"},
		{"owner", "COMMENTED"},
	}
	for _, test := range tests {
		t.Run(test.author+" "+test.state, func(t *testing.T) {
			fake := testkit.NewFakeGitHub(t)
			server, sha := seedWaiting(t, fake, "approval")
			passMobius(fake, sha)

			fake.AddReview(shop, 42, test.author, test.state, "")
			waitForPolls(t, fake)

			if state := taskState(t, server); state != "approval" {
				t.Errorf("state = %s", state)
			}
			if calls := fake.MergeCalls(); calls != 0 {
				t.Errorf("merge calls = %d", calls)
			}
			if id := approvedReview(t, server); id != "" {
				t.Errorf("approval = %s", id)
			}
		})
	}
}

func TestADismissedApprovalDoesNotMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha := seedWaiting(t, fake, "approval")
	review := fake.AddReview(shop, 42, "owner", "APPROVED", "")
	testkit.WaitFor(t, func() bool { return approvedReview(t, server) != "" })

	fake.DismissReview(shop, 42, review)

	testkit.WaitFor(t, func() bool { return dismissedApproval(t, server) })
	passMobius(fake, sha)
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "approval" {
		t.Errorf("state = %s", state)
	}
	if calls := fake.MergeCalls(); calls != 0 {
		t.Errorf("merge calls = %d", calls)
	}
}

func TestADismissalOfTheNewerApprovalDoesNotBringBackTheOlderApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha := seedWaiting(t, fake, "approval")
	fake.AddReview(shop, 42, "owner", "APPROVED", "")
	testkit.WaitFor(t, func() bool { return approvedReview(t, server) != "" })
	older := approvedReview(t, server)
	newer := fake.AddReview(shop, 42, "owner", "APPROVED", "")
	testkit.WaitFor(t, func() bool { return approvedReview(t, server) != older })

	fake.DismissReview(shop, 42, newer)

	testkit.WaitFor(t, func() bool { return dismissedApproval(t, server) })
	passMobius(fake, sha)
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "approval" {
		t.Errorf("state = %s", state)
	}
	if calls := fake.MergeCalls(); calls != 0 {
		t.Errorf("merge calls = %d", calls)
	}
}

func TestADismissalOfARequestForChangesKeepsTheApprovalAndMerges(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha := seedWaiting(t, fake, "approval")
	fake.AddReview(shop, 42, "owner", "APPROVED", "")
	testkit.WaitFor(t, func() bool { return approvedReview(t, server) != "" })
	approved := approvedReview(t, server)
	request := fake.AddReview(shop, 42, "owner", "CHANGES_REQUESTED", "")
	waitForPolls(t, fake)
	fake.DismissReview(shop, 42, request)
	waitForPolls(t, fake)

	if id := approvedReview(t, server); id != approved || dismissedApproval(t, server) {
		t.Errorf("approval = %s, dismissed = %t", id, dismissedApproval(t, server))
	}

	passMobius(fake, sha)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d", calls)
	}
}

func TestADismissalOfARefusedApprovalDoesNotTryTheMergeAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	_, sha := seedWaiting(t, fake, "approval")
	passMobius(fake, sha)
	fake.RefuseMerge(shop, 42, "Required status check is expected.")
	fake.AddReview(shop, 42, "owner", "APPROVED", "")
	testkit.WaitFor(t, func() bool { return fake.MergeCalls() == 1 })
	newer := fake.AddReview(shop, 42, "owner", "APPROVED", "")
	testkit.WaitFor(t, func() bool { return fake.MergeCalls() == 2 })

	fake.DismissReview(shop, 42, newer)
	waitForPolls(t, fake)

	if calls := fake.MergeCalls(); calls != 2 {
		t.Errorf("merge calls = %d", calls)
	}
}

func TestARefusedMergeGivesTheLeadOneEventAndWaitsForANewApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha := seedWaiting(t, fake, "approval")
	passMobius(fake, sha)
	fake.RefuseMerge(shop, 42, "Required status check is expected.")

	fake.AddReview(shop, 42, "owner", "APPROVED", "")

	prompt := waitForLeadPrompt(t, server, " merge refused for #41 \"Add plan model\"")
	if !strings.Contains(prompt, "GitHub gave this reason: \"Required status check is expected.\"") {
		t.Errorf("prompt = %s", prompt)
	}
	waitForPolls(t, fake)
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d", calls)
	}
	count := 0
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, " merge refused for #41") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("events = %d", count)
	}

	fake.RefuseMerge(shop, 42, "")
	fake.AddReview(shop, 42, "owner", "APPROVED", "")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls := fake.MergeCalls(); calls != 2 {
		t.Errorf("merge calls = %d", calls)
	}
}

func TestAPollWithNoApprovalMakesNoCheckRunCallAndNoMergeCallForATaskInNeedsHuman(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha := seedWaiting(t, fake, "approval")
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'needs_human' WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}
	passMobius(fake, sha)
	waitForPolls(t, fake)
	reads, calls := fake.CheckRunReads(), fake.MergeCalls()

	waitForPolls(t, fake)

	if got := fake.CheckRunReads(); got != reads {
		t.Errorf("check run reads = %d, want %d", got, reads)
	}
	if got := fake.MergeCalls(); got != calls || got != 0 {
		t.Errorf("merge calls = %d", got)
	}
}

func TestAPushAfterTheCheckRunReadGivesNoLeadEventAndTheNextPollMergesTheNewHead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha := seedWaiting(t, fake, "approval")
	passMobius(fake, sha)
	var next string
	fake.AfterNextReviewedCheckRunRead(func() {
		work := t.TempDir()
		testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
		testkit.Git(t, work, "commit", "--allow-empty", "-m", "Second change")
		testkit.Git(t, work, "push", "origin", "HEAD:refs/heads/mobius/41")
		next = head(t, fake, "mobius/41")
	})

	fake.AddReview(shop, 42, "owner", "APPROVED", "")

	testkit.WaitFor(t, func() bool { return fake.MergeCalls() == 1 })
	waitForPolls(t, fake)
	if state := taskState(t, server); state != "approval" {
		t.Errorf("state = %s", state)
	}
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, " merge refused for #41") {
			t.Errorf("prompt = %s", prompt)
		}
	}

	passMobius(fake, next)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if got := head(t, fake, "main^{tree}"); got != head(t, fake, next+"^{tree}") {
		t.Errorf("tree of main = %s", got)
	}
}
