package engine_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type skillUse struct {
	Session                                                int64
	Organization, Repository, Role, Harness, Skill, Source string
}

func skillUses(t *testing.T, server *testserver.Server, session int64) []skillUse {
	t.Helper()
	rows, err := server.DB.QueryContext(t.Context(), `SELECT session, organization, repository, role, harness, skill, source
		FROM skill_uses WHERE session = ? ORDER BY id`, session)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var found []skillUse
	for rows.Next() {
		var u skillUse
		if err := rows.Scan(&u.Session, &u.Organization, &u.Repository, &u.Role, &u.Harness, &u.Skill, &u.Source); err != nil {
			t.Fatal(err)
		}
		found = append(found, u)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

func sessionSkills(t *testing.T, server *testserver.Server, session int64) []string {
	t.Helper()
	rows, err := server.DB.QueryContext(t.Context(), "SELECT skill FROM session_skills WHERE session = ? ORDER BY skill", session)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var found []string
	for rows.Next() {
		var skill string
		if err := rows.Scan(&skill); err != nil {
			t.Fatal(err)
		}
		found = append(found, skill)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

const claudeSkillUpdates = `[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_1","_meta":{"claudeCode":{"toolName":"Skill"}},"rawInput":{},"title":"Load skill"}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"toolu_1","_meta":{"claudeCode":{"toolName":"Skill"}},"rawInput":{"skill":"mobius-local-state"},"title":"Load skill: mobius-local-state"}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"toolu_1","_meta":{"claudeCode":{"toolName":"Skill"}},"rawInput":{"args":"x","skill":"mobius-local-state"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_2","_meta":{"claudeCode":{"toolName":"Read"}},"rawInput":{"file_path":"a.txt"},"title":"Read a.txt"}',
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_3","_meta":{"claudeCode":{"toolName":"Skill"}},"rawInput":{},"title":"Load skill"}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"toolu_3","_meta":{"claudeCode":{"toolName":"Skill"}},"rawInput":{"skill":"frontend-design"}}',
]
reply = ["done"]
`

func TestAClaudeCodeSkillUseAddsOneRowWithTheSourceOfTheSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	server, _ := connect(t, fake, claudeSkillUpdates)

	session, _ := leadReply(t, server)

	want := []skillUse{
		{session, "owner", shop, "lead_chat", "claude-code", "mobius-local-state", "mobius"},
		{session, "owner", shop, "lead_chat", "claude-code", "frontend-design", "harness"},
	}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}

func TestADevinSkillUseAddsOneRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, `[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"call_1#a","title":"Invoked skill commit","rawInput":{"skill":"commit"},"_meta":{"cognition.ai/inferenceToolName":"skill"}}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"call_1#a","status":"completed"}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_2#b","title":"Read a.txt","rawInput":{"file_path":"a.txt"},"_meta":{"cognition.ai/inferenceToolName":"read"}}',
]
reply = ["done"]
`, devinLead)

	session, _ := leadReply(t, server)

	want := []skillUse{{session, "owner", shop, "lead_chat", "devin", "commit", "harness"}}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}

const dshModel = "deepseek-account/deepseek-v4-pro"

const dshModelValue = `["deepseek-account","deepseek-v4-pro"]`

func dshLead(cfg *config.Config) {
	keepSessionOpen(cfg)
	cfg.Roles.Lead = config.RoleBinding{Harness: config.DSH, Model: dshModel, Effort: "high", Max: 8}
}

func dshScript(updates string) string {
	return "[options]\nmodel = ['" + dshModelValue + "']\nthought_level = [\"high\"]\n" + `
[[prompts]]
updates = [
` + updates + `]
reply = ["done"]
`
}

func TestADSHSkillUseAddsOneRowWithTheSourceOfTheSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	server, _ := connectScript(t, fake, dshScript(`  '{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"skill","kind":"other","status":"in_progress","rawInput":{"name":"mobius-local-state"}}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"call_1","status":"completed"}',
`), dshLead)

	session, _ := leadReply(t, server)

	want := []skillUse{{session, "owner", shop, "lead_chat", "dsh", "mobius-local-state", "mobius"}}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}

func TestADSHToolCallWithAnotherTitleAddsNoSkillUse(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, dshScript(`  '{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"read_file","kind":"other","status":"in_progress","rawInput":{"name":"a.txt"}}',
`), dshLead)

	session, _ := leadReply(t, server)

	if got := skillUses(t, server, session); len(got) != 0 {
		t.Errorf("skill uses = %+v", got)
	}
}

func TestASessionAddsOneSessionSkillRowForEachCopiedSkillAndNoneWithoutASkillRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	commitRepositorySkills(fake)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"done\"]\n")
	session, _ := leadReply(t, server)

	if got, want := sessionSkills(t, server, session), []string{"brew", "probe"}; !slices.Equal(got, want) {
		t.Errorf("session skills = %v, want %v", got, want)
	}

	bare := testkit.NewFakeGitHub(t)
	bareServer, _ := connect(t, bare, "[[prompts]]\nreply = [\"done\"]\n")
	bareSession, _ := leadReply(t, bareServer)

	if got := sessionSkills(t, bareServer, bareSession); len(got) != 0 {
		t.Errorf("session skills = %v", got)
	}
}

func TestASkillThatTheWorktreeAlreadyHasGetsNoSessionSkillRowAndItsUseHasTheSourceHarness(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "probe/SKILL.md", "Probe the price in cents.", "Add probe")
	fake.CommitFile(skills, "review/SKILL.md", "Review the price.", "Add review")
	implementer := `[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"call_1","rawInput":{"skill":"probe"},"_meta":{"cognition.ai/inferenceToolName":"skill"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_2","rawInput":{"skill":"review"},"_meta":{"cognition.ai/inferenceToolName":"skill"}}',
]
shell = "echo cents > plan.txt && git add -A && git commit -q -m 'Add plan model'"
`
	server, _ := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.CommitFile(shop, ".agents/skills/probe/SKILL.md", "Probe the branch.", "Add the skill of the branch")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	session := endedImplementers(t, server, 1)[0].ID

	if got, want := sessionSkills(t, server, session), []string{"review"}; !slices.Equal(got, want) {
		t.Errorf("session skills = %v, want %v", got, want)
	}
	var sources []string
	for _, use := range skillUses(t, server, session) {
		sources = append(sources, use.Skill+"="+use.Source)
	}
	if got := strings.Join(sources, ","); got != "probe=harness,review=mobius" {
		t.Errorf("skill uses = %q", got)
	}
}

func antigravityLead(cfg *config.Config) {
	keepSessionOpen(cfg)
	cfg.Roles.Lead = config.RoleBinding{Harness: config.Antigravity, Model: "gemini-3-pro", Max: 8}
}

func viewFile(call, file string) string {
	return `  '{"sessionUpdate":"tool_call","toolCallId":"` + call + `","title":"Running view_file","kind":"read","status":"in_progress","rawInput":{"absolute_path":"` + file + `"}}',
`
}

func TestAnAntigravityViewOfASkillFileAddsOneRowWithTheSourceOfTheSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	dir := t.TempDir()
	skillFile := filepath.Join(dir, config.Antigravity.SkillDirectory(), "mobius-local-state", "SKILL.md")
	server, _ := connectWith(t, fake, `[[prompts]]
updates = [
`+viewFile("call_1", skillFile)+`  '{"sessionUpdate":"tool_call_update","toolCallId":"call_1","status":"completed"}',
]
reply = ["done"]
`, antigravityLead)
	spec := leadSpec(t)
	spec.Dir = dir

	session := run(t, server, spec, "Read the work")
	reply(t, server, session)

	want := []skillUse{{session, "owner", shop, "lead_chat", "antigravity", "mobius-local-state", "mobius"}}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}

func TestAnAntigravityViewOfAnotherFileAddsNoSkillUse(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	fake.CommitFile(skills, "mobius-local-state/notes.md", "Notes.", "Add notes")
	dir := t.TempDir()
	skillFolder := filepath.Join(dir, config.Antigravity.SkillDirectory(), "mobius-local-state")
	server, _ := connectWith(t, fake, `[[prompts]]
updates = [
`+viewFile("call_1", filepath.Join(skillFolder, "notes.md"))+
		viewFile("call_2", filepath.Join(dir, "other", "mobius-local-state", "SKILL.md"))+
		viewFile("call_3", filepath.Join(skillFolder, "..", "..", "SKILL.md"))+`]
reply = ["done"]
`, antigravityLead)
	spec := leadSpec(t)
	spec.Dir = dir

	session := run(t, server, spec, "Read the work")
	reply(t, server, session)

	if got := skillUses(t, server, session); len(got) != 0 {
		t.Errorf("skill uses = %+v", got)
	}
}

func TestAClaudeCodeReadOfASkillFileAddsOneRowWithTheSourceOfTheSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	dir := t.TempDir()
	skillFile := filepath.Join(dir, config.ClaudeCode.SkillDirectory(), "mobius-local-state", "SKILL.md")
	server, _ := connect(t, fake, `[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_1","_meta":{"claudeCode":{"toolName":"Read"}},"rawInput":{},"title":"Read SKILL.md"}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"toolu_1","_meta":{"claudeCode":{"toolName":"Read"}},"rawInput":{"file_path":"`+skillFile+`"},"title":"Read SKILL.md"}',
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_2","_meta":{"claudeCode":{"toolName":"Read"}},"rawInput":{"file_path":"`+filepath.Join(dir, "notes.md")+`"},"title":"Read notes.md"}',
]
reply = ["done"]
`)
	spec := leadSpec(t)
	spec.Dir = dir

	session := run(t, server, spec, "Read the work")

	want := []skillUse{{session, "owner", shop, "lead_chat", "claude-code", "mobius-local-state", "mobius"}}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}

func TestAClaudeCodeBashCommandWithASkillFileAddsOneRowWithTheSourceOfTheSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	dir := t.TempDir()
	skillFile := filepath.Join(dir, config.ClaudeCode.SkillDirectory(), "mobius-local-state", "SKILL.md")
	server, _ := connect(t, fake, `[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_1","_meta":{"claudeCode":{"toolName":"Bash"}},"rawInput":{},"title":"Run cat"}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"toolu_1","_meta":{"claudeCode":{"toolName":"Bash"}},"rawInput":{"command":"cat `+skillFile+`"},"title":"Run cat"}',
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_2","_meta":{"claudeCode":{"toolName":"Bash"}},"rawInput":{"command":"cat notes.md"},"title":"Run cat"}',
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_3","_meta":{"claudeCode":{"toolName":"Bash"}},"rawInput":{"command":"cat /tmp/.claude/skills/mobius-local-state/SKILL.md"},"title":"Run cat"}',
]
reply = ["done"]
`)
	spec := leadSpec(t)
	spec.Dir = dir

	session := run(t, server, spec, "Read the work")

	want := []skillUse{{session, "owner", shop, "lead_chat", "claude-code", "mobius-local-state", "mobius"}}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}

func TestADevinReadOfASkillFileAddsOneRowWithTheSourceOfTheSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	dir := t.TempDir()
	skillFile := filepath.Join(dir, config.Devin.SkillDirectory(), "mobius-local-state", "SKILL.md")
	server, _ := connectWith(t, fake, `[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"call_1#a","title":"Read SKILL.md","rawInput":{"file_path":"`+skillFile+`"},"_meta":{"cognition.ai/inferenceToolName":"read"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_2#b","title":"Read notes.md","rawInput":{"file_path":"`+filepath.Join(dir, "notes.md")+`"},"_meta":{"cognition.ai/inferenceToolName":"read"}}',
]
reply = ["done"]
`, devinLead)
	spec := leadSpec(t)
	spec.Dir = dir

	session := run(t, server, spec, "Read the work")

	want := []skillUse{{session, "owner", shop, "lead_chat", "devin", "mobius-local-state", "mobius"}}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}

func TestADevinExecCommandWithASkillFileAddsOneRowWithTheSourceOfTheSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	server, _ := connectWith(t, fake, `[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"call_1#a","title":"Run cat","rawInput":{"command":"cat .agents/skills/mobius-local-state/SKILL.md","workdir":"`+t.TempDir()+`"},"_meta":{"cognition.ai/inferenceToolName":"exec"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_2#b","title":"Run cat","rawInput":{"command":"cat notes.md","workdir":"`+t.TempDir()+`"},"_meta":{"cognition.ai/inferenceToolName":"exec"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_3#c","title":"Run cat","rawInput":{"command":"cat ~/.agents/skills/mobius-local-state/SKILL.md","workdir":"`+t.TempDir()+`"},"_meta":{"cognition.ai/inferenceToolName":"exec"}}',
]
reply = ["done"]
`, devinLead)

	session, _ := leadReply(t, server)

	want := []skillUse{{session, "owner", shop, "lead_chat", "devin", "mobius-local-state", "mobius"}}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}

func TestADSHReadOfARelativeSkillFileAddsOneRowWithTheSourceOfTheSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	server, _ := connectScript(t, fake, dshScript(`  '{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"read","kind":"other","status":"in_progress","rawInput":{"file_path":".agents/skills/mobius-local-state/SKILL.md"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_2","title":"read","kind":"other","status":"in_progress","rawInput":{"file_path":"internal/engine/x.go"}}',
`), dshLead)

	session, _ := leadReply(t, server)

	want := []skillUse{{session, "owner", shop, "lead_chat", "dsh", "mobius-local-state", "mobius"}}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}

func TestADSHBashCommandWithASkillFileAddsOneRowWithTheSourceOfTheSkill(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "mobius-local-state/SKILL.md", "Read the state.", "Add mobius-local-state")
	server, _ := connectScript(t, fake, dshScript(`  '{"sessionUpdate":"tool_call","toolCallId":"call_1","title":"bash","kind":"other","status":"in_progress","rawInput":{"command":"cat .agents/skills/mobius-local-state/SKILL.md"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_2","title":"bash","kind":"other","status":"in_progress","rawInput":{"command":"cat internal/engine/x.go"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_3","title":"bash","kind":"other","status":"in_progress","rawInput":{"command":"cat ~/.agents/skills/mobius-local-state/SKILL.md"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_4","title":"bash","kind":"other","status":"in_progress","rawInput":{"command":"cat .agents/skills/*/SKILL.md"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_5","title":"bash","kind":"other","status":"in_progress","rawInput":{"command":"cat .agents/skills/{mobius-local-state,commit}/SKILL.md"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_6","title":"bash","kind":"other","status":"in_progress","rawInput":{"command":"cat .agents/skills/mobius-local-state/SKILL.md.bak"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_7","title":"bash","kind":"other","status":"in_progress","rawInput":{"command":"for s in commit mobius-local-state; do cat .agents/skills/$s/SKILL.md; done"}}',
  '{"sessionUpdate":"tool_call","toolCallId":"call_8","title":"bash","kind":"other","status":"in_progress","rawInput":{"command":"cat .agents/skills/\"$s\"/SKILL.md"}}',
`), dshLead)

	session, _ := leadReply(t, server)

	want := []skillUse{{session, "owner", shop, "lead_chat", "dsh", "mobius-local-state", "mobius"}}
	if got := skillUses(t, server, session); !slices.Equal(got, want) {
		t.Errorf("skill uses = %+v, want %+v", got, want)
	}
}
