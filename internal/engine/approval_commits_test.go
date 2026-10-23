package engine_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	planLines = "%s\n1\n2\n3\n4\n5\nbottom\n"
	// appendCents changes plan.txt below the first line, so a change of the first line on main merges cleanly.
	appendCents = "shell = '''echo cents >> plan.txt && git add plan.txt && git commit -q -m \"Add plan model $(basename $PWD)\"'''\n"
)

var (
	seededAt  = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	ciStarted = time.Date(2026, 10, 22, 23, 59, 54, 0, time.UTC)
	mergedAt  = time.Date(2026, 10, 23, 6, 4, 10, 0, time.UTC)
)

// readyAfterMainCommit starts the task of #41 with plan.txt on main, commits path on main at mergedAt while the head
// waits for its CI, and gives the text of the event ready for Lead approval and the commit.
func readyAfterMainCommit(t *testing.T, path, content string) (event, commit string) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\n"+appendCents, longGrace)
	fake.CommitFileAt(shop, "plan.txt", fmt.Sprintf(planLines, "top"), "Add the plan", seededAt)
	sha := checksHead(t, server, fake)
	fake.CommitFileAt(shop, path, content, "Change the plan heading\n\nA longer text.", mergedAt)
	commit = head(t, fake, "main")

	fake.AddWorkflowRun(shop, testkit.WorkflowRun{HeadSHA: sha, Status: "completed", Conclusion: "success", CreatedAt: ciStarted})

	return waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\""), commit
}

func TestTheApprovalEventListsTheCommitsOfMainAfterTheStartOfTheCIThatChangeAFileOfThePullRequest(t *testing.T) {
	t.Parallel()

	event, commit := readyAfterMainCommit(t, "plan.txt", fmt.Sprintf(planLines, "TOP"))

	want := fmt.Sprintf("Commits of main after the start of the CI of the head (2026-10-22 23:59 UTC) that change a file of the pull request:\n- %.8s 2026-10-23 06:04 UTC Change the plan heading. Shared files: plan.txt.\nReview rounds: ", commit)
	if !strings.Contains(event, want) {
		t.Errorf("event = %q, want %q", event, want)
	}
	if !strings.Contains(event, " of 10.") {
		t.Errorf("event = %q", event)
	}
}

func TestTheApprovalEventSaysThatNoCommitOfMainAfterTheStartOfTheCIChangesAFileOfThePullRequest(t *testing.T) {
	t.Parallel()

	event, _ := readyAfterMainCommit(t, "other.txt", "other\n")

	want := "No commit of main after the start of the CI of the head (2026-10-22 23:59 UTC) changes a file of the pull request."
	if !strings.Contains(event, want) {
		t.Errorf("event = %q, want %q", event, want)
	}
	if strings.Contains(event, "Change the plan heading") {
		t.Errorf("event = %q", event)
	}
}
