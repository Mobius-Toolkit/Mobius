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
	// changeTwoLines changes the lines "1" and "5" of plan.txt, so the same change of the line "1" on main merges
	// cleanly and takes that line out of the change of the pull request.
	changeTwoLines = "shell = '''printf 'top\\none\\n2\\n3\\n4\\nfive\\nbottom\\n' > plan.txt && git add plan.txt && git commit -q -m \"Add plan model $(basename $PWD)\"'''\n"
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

	prompt := waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\"")
	parts := strings.Split(prompt, "# Event\n\n")
	return parts[len(parts)-1], commit
}

func TestTheApprovalEventForAHeadWhoseCIStartedBeforeACommitOfTheDefaultBranchThatChangesAFileHasNoLineAboutThatCommit(t *testing.T) {
	t.Parallel()

	event, commit := readyAfterMainCommit(t, "plan.txt", fmt.Sprintf(planLines, "TOP"))

	if strings.Contains(event, commit[:8]) || strings.Contains(event, "Change the plan heading") {
		t.Errorf("event mentions commit %s: %q", commit, event)
	}
	if strings.Contains(event, "after the start of the CI") {
		t.Errorf("event mentions CI commits: %q", event)
	}
}

func TestTheApprovalEventForAHeadWhoseCIStartedBeforeACommitOfMainThatChangesNoFileOfThePullRequestHasNoLineAboutIt(t *testing.T) {
	t.Parallel()

	event, commit := readyAfterMainCommit(t, "other.txt", "other\n")

	if strings.Contains(event, commit[:8]) || strings.Contains(event, "Change the plan heading") {
		t.Errorf("event mentions commit %s: %q", commit, event)
	}
	if strings.Contains(event, "after the start of the CI") {
		t.Errorf("event mentions CI commits: %q", event)
	}
}
