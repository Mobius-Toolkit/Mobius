package engine_test

import (
	"slices"
	"strings"
	"testing"

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

const airSkillUpdates = `[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_1","_meta":{"claudeCode":{"toolName":"Skill"}},"title":"Load skill"}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"toolu_1","rawInput":{"skill":"frontend-design"}}',
]
reply = ["done"]
`

func TestAClaudeCodeSkillUseWithTheToolNameOnlyInTheFirstUpdateAddsOneRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, airSkillUpdates)

	session, _ := leadReply(t, server)

	want := []skillUse{{session, "owner", shop, "lead_chat", "claude-code", "frontend-design", "harness"}}
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
