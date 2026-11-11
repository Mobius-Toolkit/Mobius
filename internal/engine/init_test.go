package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func readText(t *testing.T, file string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func TestTheInitScriptRunsBeforeTheFirstPromptOfAnImplementerInANewWorktree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	ran, seen := filepath.Join(dataDir, "ran"), filepath.Join(dataDir, "seen")
	implementer := fmt.Sprintf("[[prompts]]\nshell = '''cp '%s' '%s' && echo cents > plan.txt && git add plan.txt && git commit -q -m 'Add plan model''''\n", ran, seen)
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, implementer, noChange)
	fake.SetInit(shop, fmt.Sprintf("pwd > '%s'", ran))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	session := endedImplementers(t, server, 1)[0]
	if got := strings.TrimSpace(readText(t, seen)); !strings.HasSuffix(got, "/worktrees/owner/shop/task-41") {
		t.Errorf("the directory of the init script = %q", got)
	}
	steps := waitForSteps(t, server, "init", 1)
	if !slices.Equal(stepResults(steps), []string{"pass"}) {
		t.Errorf("init steps = %+v", steps)
	}
	texts := checkTexts(t, server, session)
	if len(texts) < 2 || texts[0] != ".mobius/init started." || !strings.HasPrefix(texts[1], ".mobius/init passed in ") {
		t.Errorf("check rows = %q", texts)
	}
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 1 || strings.Contains(prompts[0], "Setup failed") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTheInitScriptRunsAgainForAnExistingBranchWhoseWorktreeIsMissing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	lead := "[[prompts]]\nwhen = \"comment on #41\"\n" + startImplementer + "\n" + leadStarts
	server, _ := connectTaskIn(t, fake, dataDir, lead, "[[prompts]]\nwhen = \"Try again\"\nreply = [\"ok\"]\n\n[[prompts]]\n"+cannotDoCall, noChange)
	ran := filepath.Join(dataDir, "ran")
	fake.SetInit(shop, fmt.Sprintf("echo run >> '%s'", ran))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	endedImplementers(t, server, 1)
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if err := runner.RemoveWorktree(t.Context(), dataDir, shop, worktree); err != nil {
		t.Fatal(err)
	}

	fake.AddComment(shop, 41, "owner", "Try again.")

	endedImplementers(t, server, 2)
	if got := readText(t, ran); got != "run\nrun\n" {
		t.Errorf("runs of the init script = %q", got)
	}
	if steps := waitForSteps(t, server, "init", 2); !slices.Equal(stepResults(steps), []string{"pass", "pass"}) {
		t.Errorf("init steps = %+v", steps)
	}
}

func TestTheInitScriptDoesNotRunForAReviewer(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, commits, noChange)
	ran := filepath.Join(dataDir, "ran")
	fake.SetInit(shop, fmt.Sprintf("echo run >> '%s'", ran))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	endedReviewers(t, server, 1)
	if got := readText(t, ran); got != "run\n" {
		t.Errorf("runs of the init script = %q", got)
	}
}

func TestARepositoryWithNoInitScriptGivesNoInitRowAndNoSetupSection(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	session := endedImplementers(t, server, 1)[0]
	if steps := stepRows(t, server, "init"); len(steps) != 0 {
		t.Errorf("init steps = %+v", steps)
	}
	for _, text := range checkTexts(t, server, session) {
		if strings.Contains(text, ".mobius/init") {
			t.Errorf("check row = %q", text)
		}
	}
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 1 || strings.Contains(prompts[0], "Setup failed") || !strings.HasPrefix(prompts[0], "You are the Implementer") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAFailedInitScriptGivesTheEndOfItsOutputToTheFirstPromptAndTheAgentStillStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.SetInit(shop, "echo 'pnpm install failed'\nexit 1")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	session := endedImplementers(t, server, 1)[0]
	if session.EndReason.String != "done" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	if steps := waitForSteps(t, server, "init", 1); !slices.Equal(stepResults(steps), []string{"fail"}) {
		t.Errorf("init steps = %+v", steps)
	}
	texts := checkTexts(t, server, session)
	if len(texts) < 2 || !strings.HasPrefix(texts[1], ".mobius/init failed in ") {
		t.Errorf("check rows = %q", texts)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	inOrder(t, prompts[0],
		"# Setup failed\n\nThe setup script `.mobius/init` of the new worktree failed.",
		"The output ends with these lines:\n\n```\npnpm install failed\n\n```\n\n",
		"You are the Implementer of one task.")
	if !strings.HasPrefix(prompts[0], "# Setup failed") {
		t.Errorf("prompt = %s", prompts[0])
	}
}

func TestAnInitScriptThatDoesNotEndInTheTimeLimitGivesTheSectionToTheFirstPromptAndTheAgentStillStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, func(cfg *config.Config) { cfg.CheckTimeout = 300 * time.Millisecond })
	fake.SetInit(shop, "echo 'pnpm install is slow'\nsleep 10")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	session := endedImplementers(t, server, 1)[0]
	if session.EndReason.String != "done" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	if steps := waitForSteps(t, server, "init", 1); !slices.Equal(stepResults(steps), []string{"timeout"}) {
		t.Errorf("init steps = %+v", steps)
	}
	texts := checkTexts(t, server, session)
	if len(texts) < 2 || texts[1] != ".mobius/init did not end in 300ms." {
		t.Errorf("check rows = %q", texts)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	inOrder(t, prompts[0],
		"# Setup failed\n\nThe setup script `.mobius/init` of the new worktree did not end in 300ms.",
		"pnpm install is slow\n",
		"You are the Implementer of one task.")
}
