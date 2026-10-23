package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
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

func TestASessionStartsWhenTheSkillRepositoryHasNoDefaultBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	testkit.Git(t, fake.Remote(skills), "update-ref", "-d", "refs/heads/main")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"echo started\"\n")

	_, text := leadReply(t, server)

	if !strings.Contains(text, "started") {
		t.Errorf("reply = %q", text)
	}
}

func TestASessionStartsWhenTheFirstFetchOfTheSkillRepositoryFails(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.SetCloneURL(skills, "file:///missing/mobius-skills.git")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"echo started\"\n")

	_, text := leadReply(t, server)

	if !strings.Contains(text, "started") {
		t.Errorf("reply = %q", text)
	}
}

func TestALaterSessionInTheSameWorktreeGetsAFreshCopyOfTheSkills(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	implementer := "[[prompts]]\nwhen = \"Inspect the skills\"\nshell = \"cat .agents/skills/probe/SKILL.md .agents/skills/review/SKILL.md\"\n\n" +
		"[[prompts]]\nshell = \"echo changed >> .agents/skills/probe/SKILL.md && echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model'\"\n"
	server, dataDir := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	endedImplementers(t, server, 1)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in dollars.", "Change probe")
	fake.CommitFile(skills, "review/SKILL.md", "Review the price.", "Add review")
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	spec := engine.Spec{Role: engine.ImplementerRole, Organization: "owner", Repository: shop, Workstream: 12, Dir: worktree}

	text := reply(t, server, run(t, server, spec, "Inspect the skills"))

	if want := "Probe the price in dollars.Review the price."; !strings.Contains(text, want) || strings.Contains(text, "changed") {
		t.Errorf("reply = %q, want %q", text, want)
	}
	if found, err := runner.HasUncommittedChanges(context.Background(), dataDir, worktree); err != nil || found {
		t.Errorf("uncommitted changes = %v, %v", found, err)
	}
}

func TestALaterSessionKeepsASkillThatTheBranchTracksNow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	fake.CommitFile(skills, "probe/scripts/find.sh", "echo find", "Add the script of probe")
	implementer := "[[prompts]]\nwhen = \"Inspect the skills\"\nshell = \"cat .agents/skills/probe/SKILL.md\"\n\n" +
		"[[prompts]]\nshell = \"echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model'\"\n"
	server, dataDir := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	endedImplementers(t, server, 1)
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if err := os.WriteFile(filepath.Join(worktree, ".agents", "skills", "probe", "SKILL.md"), []byte("Probe the branch."), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, worktree, "add", "-f", ".agents/skills/probe/SKILL.md")
	testkit.Git(t, worktree, "commit", "-q", "-m", "Add the skill of the branch")
	spec := engine.Spec{Role: engine.ImplementerRole, Organization: "owner", Repository: shop, Workstream: 12, Dir: worktree}

	text := reply(t, server, run(t, server, spec, "Inspect the skills"))

	if !strings.Contains(text, "Probe the branch.") || strings.Contains(text, "Probe the price in cents.") {
		t.Errorf("reply = %q", text)
	}
	if got := testkit.Git(t, worktree, "show", "HEAD:.agents/skills/probe/SKILL.md"); got != "Probe the branch." {
		t.Errorf("skill of the branch = %q", got)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".agents", "skills", "probe", "scripts")); !os.IsNotExist(err) {
		t.Errorf("script of the earlier copy: %v", err)
	}
	if found, err := runner.HasUncommittedChanges(context.Background(), dataDir, worktree); err != nil || found {
		t.Errorf("uncommitted changes = %v, %v", found, err)
	}
}

func TestALaterSessionWithNoSkillsDoesNotHideTheSkillFolderOfTheBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	implementer := "[[prompts]]\nwhen = \"Write the skill\"\nshell = \"mkdir -p .agents/skills/mine && echo mine > .agents/skills/mine/SKILL.md\"\n\n" +
		"[[prompts]]\nshell = \"echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model'\"\n"
	server, dataDir := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	endedImplementers(t, server, 1)
	fake.RemoveRepository(skills)
	waitForPolls(t, fake)
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	spec := engine.Spec{Role: engine.ImplementerRole, Organization: "owner", Repository: shop, Workstream: 12, Dir: worktree}

	reply(t, server, run(t, server, spec, "Write the skill"))

	if _, err := os.Stat(filepath.Join(worktree, ".agents", "skills", "probe")); !os.IsNotExist(err) {
		t.Errorf("earlier copy: %v", err)
	}
	if got := testkit.Git(t, worktree, "status", "--porcelain"); !strings.Contains(got, ".agents/") {
		t.Errorf("status = %q", got)
	}
}

// Serial: the test sets XDG_CONFIG_HOME for the git of the whole process.
func TestAFileThatTheUserIgnoreFileExcludesStaysExcludedInAWorktreeWithCopiedSkills(t *testing.T) {
	config := t.TempDir()
	if err := os.MkdirAll(filepath.Join(config, "git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "git", "ignore"), []byte("*.scratch"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", config)
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	implementer := "[[prompts]]\nshell = \"echo notes > plan.scratch && echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model' && git ls-files\"\n"
	server, dataDir := connectTask(t, fake, leadStarts, implementer, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	session := endedImplementers(t, server, 1)[0]

	if text := reply(t, server, session.ID); strings.Contains(text, "plan.scratch") {
		t.Errorf("reply = %q", text)
	}
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if _, err := os.Stat(filepath.Join(worktree, "plan.scratch")); err != nil {
		t.Error(err)
	}
	if found, err := runner.HasUncommittedChanges(context.Background(), dataDir, worktree); err != nil || found {
		t.Errorf("uncommitted changes = %v, %v", found, err)
	}
}

func commitRepositorySkills(fake *testkit.FakeGitHub) {
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	fake.CommitFile(skills, "repos/shop/brew/SKILL.md", "Brew the coffee.", "Add brew")
	fake.CommitFile(skills, "repos/garden/water/SKILL.md", "Water the plants.", "Add water")
}

func TestASessionGetsTheGeneralSkillsAndTheSkillsOfItsRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	commitRepositorySkills(fake)
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"ls .claude/skills && cat .claude/skills/brew/SKILL.md\"\n")

	_, text := leadReply(t, server)

	for _, want := range []string{"probe", "brew", "Brew the coffee."} {
		if !strings.Contains(text, want) {
			t.Errorf("reply = %q, want %q", text, want)
		}
	}
	for _, unwanted := range []string{"water", "repos"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("reply = %q, has %q", text, unwanted)
		}
	}
}

func TestASkillOfTheRepositoryWinsOverAGeneralSkillWithTheSameName(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "shared/SKILL.md", "Share in general.", "Add shared")
	fake.CommitFile(skills, "repos/shop/shared/SKILL.md", "Share in the shop.", "Add shared of the shop")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"cat .claude/skills/shared/SKILL.md\"\n")

	_, text := leadReply(t, server)

	if !strings.Contains(text, "Share in the shop.") || strings.Contains(text, "Share in general.") {
		t.Errorf("reply = %q", text)
	}
}

func TestASessionWithNoRepositoryGetsOnlyTheGeneralSkills(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	commitRepositorySkills(fake)
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"ls .claude/skills\"\n")
	spec := engine.Spec{Role: engine.CoordinatorRole, Organization: "owner", Dir: t.TempDir()}

	text := reply(t, server, run(t, server, spec, "Read the work"))

	if !strings.Contains(text, "probe") || strings.Contains(text, "brew") || strings.Contains(text, "water") || strings.Contains(text, "repos") {
		t.Errorf("reply = %q", text)
	}
}

func TestTheLeadChatGetsTheSkillsOfTheRepositoryOfItsWorkstreamIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	commitRepositorySkills(fake)
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"ls .claude/skills\"\n")

	sendChat(t, server, leadChat, "Plan the loyalty API")

	text := reply(t, server, endedChatSession(t, server, 0).ID)
	if !strings.Contains(text, "probe") || !strings.Contains(text, "brew") || strings.Contains(text, "water") {
		t.Errorf("reply = %q", text)
	}
}

func TestAWorktreeKeepsARepositorySkillThatItAlreadyHasAndLeavesNoChangeForACopiedOne(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	commitRepositorySkills(fake)
	fake.CommitFile(skills, "repos/shop/pour/SKILL.md", "Pour the milk.", "Add pour")
	implementer := "[[prompts]]\nshell = \"cat .agents/skills/brew/SKILL.md .agents/skills/pour/SKILL.md && echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model'\"\n"
	server, dataDir := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.CommitFile(shop, ".agents/skills/brew/SKILL.md", "Brew the branch.", "Add the skill of the branch")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	session := endedImplementers(t, server, 1)[0]

	if text := reply(t, server, session.ID); !strings.Contains(text, "Brew the branch.") || strings.Contains(text, "Brew the coffee.") || !strings.Contains(text, "Pour the milk.") {
		t.Errorf("reply = %q", text)
	}
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if got := testkit.Git(t, worktree, "status", "--porcelain"); got != "" {
		t.Errorf("status = %q", got)
	}
	if found, err := runner.HasUncommittedChanges(context.Background(), dataDir, worktree); err != nil || found {
		t.Errorf("uncommitted changes = %v, %v", found, err)
	}
}

func skillWithRoles(roles string) string {
	return "---\nname: probe\ndescription: Probe.\nmetadata:\n  roles: " + roles + "\n---\nUse it.\n"
}

func implementerSkillsReply(t *testing.T, fake *testkit.FakeGitHub) string {
	t.Helper()
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\nshell = \"ls .agents/skills && echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model'\"\n", noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	return reply(t, server, endedImplementers(t, server, 1)[0].ID)
}

func leadOnlyFake(t *testing.T) *testkit.FakeGitHub {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	fake.CommitFile(skills, "lead-only/SKILL.md", skillWithRoles("lead"), "Add lead-only")
	return fake
}

func TestALeadSkillGoesToTheLeadChat(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, leadOnlyFake(t), "[[prompts]]\nshell = \"ls .claude/skills\"\n")

	_, text := leadReply(t, server)

	if !strings.Contains(text, "probe") || !strings.Contains(text, "lead-only") {
		t.Errorf("reply = %q", text)
	}
}

func TestALeadSkillDoesNotGoToAnImplementer(t *testing.T) {
	t.Parallel()

	text := implementerSkillsReply(t, leadOnlyFake(t))

	if !strings.Contains(text, "probe") || strings.Contains(text, "lead-only") {
		t.Errorf("reply = %q", text)
	}
}

func pairFake(t *testing.T) *testkit.FakeGitHub {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "pair/SKILL.md", skillWithRoles("implementer, reviewer"), "Add pair")
	return fake
}

func TestASkillWithTwoRolesGoesToAnImplementer(t *testing.T) {
	t.Parallel()

	if text := implementerSkillsReply(t, pairFake(t)); !strings.Contains(text, "pair") {
		t.Errorf("reply = %q", text)
	}
}

func TestASkillWithTwoRolesGoesToAReviewerAndNotToTheLead(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, pairFake(t), "[[prompts]]\nshell = \"ls .claude/skills && echo done\"\n")
	reviewer := engine.Spec{Role: engine.ReviewerRole, Organization: "owner", Dir: t.TempDir()}

	review := reply(t, server, run(t, server, reviewer, "Read the work"))
	_, lead := leadReply(t, server)

	if !strings.Contains(review, "pair") {
		t.Errorf("reviewer reply = %q", review)
	}
	if strings.Contains(lead, "pair") {
		t.Errorf("lead reply = %q", lead)
	}
}

func sharedFake(t *testing.T) *testkit.FakeGitHub {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "shared/SKILL.md", "Share in general.", "Add shared")
	fake.CommitFile(skills, "repos/shop/shared/SKILL.md", "---\nmetadata:\n  roles: lead\n---\nShare in the shop.\n", "Add shared of the shop")
	return fake
}

func TestARepositorySkillThatTheRoleFilterKeepsReplacesTheGeneralSkill(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, sharedFake(t), "[[prompts]]\nshell = \"cat .claude/skills/shared/SKILL.md\"\n")

	_, text := leadReply(t, server)

	if !strings.Contains(text, "Share in the shop.") || strings.Contains(text, "Share in general.") {
		t.Errorf("reply = %q", text)
	}
}

func TestARepositorySkillThatTheRoleFilterRemovesAlsoHidesTheGeneralSkillOfTheSameName(t *testing.T) {
	t.Parallel()

	if text := implementerSkillsReply(t, sharedFake(t)); strings.Contains(text, "shared") {
		t.Errorf("reply = %q", text)
	}
}

func TestACoordinatorGetsAGeneralSkillForItsRoleAndNotOneForTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "for-coordinator/SKILL.md", skillWithRoles("coordinator"), "Add for-coordinator")
	fake.CommitFile(skills, "for-lead/SKILL.md", skillWithRoles("lead"), "Add for-lead")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"ls .claude/skills\"\n")
	spec := engine.Spec{Role: engine.CoordinatorRole, Organization: "owner", Dir: t.TempDir()}

	text := reply(t, server, run(t, server, spec, "Read the work"))

	if !strings.Contains(text, "for-coordinator") || strings.Contains(text, "for-lead") {
		t.Errorf("reply = %q", text)
	}
}

func TestASkillWithNoFrontmatterOrInvalidFrontmatterGoesToEachRole(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "plain/SKILL.md", "Plain text.", "Add plain")
	fake.CommitFile(skills, "broken/SKILL.md", "---\nmetadata: [unclosed\n---\nBroken.\n", "Add broken")

	text := implementerSkillsReply(t, fake)

	if !strings.Contains(text, "plain") || !strings.Contains(text, "broken") {
		t.Errorf("reply = %q", text)
	}
}

func TestAWorktreeLeavesNoExcludePatternAndNoChangeForASkillThatTheRoleFilterRemoves(t *testing.T) {
	t.Parallel()
	fake := leadOnlyFake(t)
	implementer := "[[prompts]]\nshell = \"cat .agents/skills/probe/SKILL.md && echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model'\"\n"
	server, dataDir := connectTask(t, fake, leadStarts, implementer, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	endedImplementers(t, server, 1)

	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if _, err := os.Stat(filepath.Join(worktree, ".agents", "skills", "lead-only")); !os.IsNotExist(err) {
		t.Errorf("skill folder error = %v", err)
	}
	if got := testkit.Git(t, worktree, "status", "--porcelain"); got != "" {
		t.Errorf("status = %q", got)
	}
	excluded, err := runner.Excluded(context.Background(), dataDir, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(excluded) != 1 || excluded[0] != "/.agents/skills/probe/" {
		t.Errorf("exclude patterns = %q", excluded)
	}
}
