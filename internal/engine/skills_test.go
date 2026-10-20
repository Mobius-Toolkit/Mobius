package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const skills = "owner/mobius-skills"

func TestASessionGetsTheSkillsOfTheDefaultBranchOfTheSkillRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"cat .claude/skills/probe/SKILL.md\"\n")

	_, text := leadReply(t, server)

	if !strings.Contains(text, "Probe the price in cents.") {
		t.Errorf("reply = %q", text)
	}
}

func TestASessionDoesNotGetASkillThatIsOnlyOnAnotherBranchAndLosesAnOldSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	fake.CommitFileOnBranch(skills, "draft", "draft/SKILL.md", "Draft the price in dollars.", "Add draft")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"ls .claude/skills\"\n")
	spec := leadSpec(t)
	if err := os.MkdirAll(filepath.Join(spec.Dir, ".claude", "skills", "old"), 0o750); err != nil {
		t.Fatal(err)
	}

	text := reply(t, server, run(t, server, spec, "Read the work"))

	if !strings.Contains(text, "probe") || strings.Contains(text, "draft") || strings.Contains(text, "old") {
		t.Errorf("reply = %q", text)
	}
}

func TestAnImplementerWorktreeWithACopiedSkillHasNoUncommittedChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	implementer := "[[prompts]]\nshell = '''cat .agents/skills/probe/SKILL.md && echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model' && git ls-files'''\n"
	server, dataDir := connectTask(t, fake, leadStarts, implementer, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	session := endedImplementers(t, server, 1)[0]

	if text := reply(t, server, session.ID); !strings.Contains(text, "Probe the price in cents.") || strings.Contains(text, ".agents") {
		t.Errorf("reply = %q", text)
	}
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if _, err := os.Stat(filepath.Join(worktree, ".agents", "skills", "probe", "SKILL.md")); err != nil {
		t.Error(err)
	}
	if found, err := runner.HasUncommittedChanges(context.Background(), dataDir, worktree); err != nil || found {
		t.Errorf("uncommitted changes = %v, %v", found, err)
	}
}

func TestMobiusDoesNotCopyASkillThatTheWorktreeAlreadyHas(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	implementer := "[[prompts]]\nshell = \"cat .agents/skills/probe/SKILL.md && echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model'\"\n"
	server, dataDir := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.CommitFile(shop, ".agents/skills/probe/SKILL.md", "Probe the branch.", "Add the skill of the branch")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	session := endedImplementers(t, server, 1)[0]

	if text := reply(t, server, session.ID); !strings.Contains(text, "Probe the branch.") || strings.Contains(text, "Probe the price in cents.") {
		t.Errorf("reply = %q", text)
	}
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if got := testkit.Git(t, worktree, "status", "--porcelain"); got != "" {
		t.Errorf("status = %q", got)
	}
	if got := testkit.Git(t, worktree, "show", "HEAD:.agents/skills/probe/SKILL.md"); got != "Probe the branch." {
		t.Errorf("skill of the branch = %q", got)
	}
}
