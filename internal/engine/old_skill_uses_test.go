package engine_test

import (
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const oldSkillUses = `INSERT INTO sessions (id, role, harness, model, repository, workstream, started_at, organization)
	VALUES (1000, 'implementer', 'claude-code', 'opus', 'owner/shop', 12, '2026-10-01T10:00:00Z', 'owner');
INSERT INTO old_skill_uses (session, organization, repository, role, harness, skill, used_at) VALUES
	(1000, 'owner', 'owner/shop', 'implementer', 'claude-code', 'old-skill', '2026-10-01T10:00:01Z'),
	(1000, 'owner', 'owner/shop', 'implementer', 'claude-code', 'frontend-design', '2026-10-01T10:00:02Z'),
	(1000, 'owner', 'owner/shop', 'implementer', 'claude-code', 'repo-skill', '2026-10-01T10:00:03Z')`

// movedSkillUses waits until old_skill_uses is empty and gives the skill and the source of each skill_uses row.
func movedSkillUses(t *testing.T, server *testserver.Server) [][2]string {
	t.Helper()
	return testkit.WaitForValue(t, func() ([][2]string, bool) {
		var left int
		if err := server.DB.QueryRow("SELECT count(*) FROM old_skill_uses").Scan(&left); err != nil {
			t.Fatal(err)
		}
		rows, err := server.DB.Query("SELECT skill, source FROM skill_uses ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var moved [][2]string
		for rows.Next() {
			var use [2]string
			if err := rows.Scan(&use[0], &use[1]); err != nil {
				t.Fatal(err)
			}
			moved = append(moved, use)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return moved, left == 0 && len(moved) > 0
	})
}

func TestTheEngineMovesTheOldSkillUsesWithTheSourceOfTheHistoryOfTheSkillRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	fake.CommitFile(skills, "old-skill/SKILL.md", "Do the old work.", "Add old-skill")
	fake.CommitFile(skills, "repos/shop/repo-skill/SKILL.md", "Do the work of the shop.", "Add repo-skill")
	fake.RemoveFile(skills, "old-skill/SKILL.md", "Remove old-skill")
	server := startServer(t, fake, t.TempDir(), oldSkillUses)

	got := movedSkillUses(t, server)

	want := [][2]string{{"old-skill", "mobius"}, {"frontend-design", "harness"}, {"repo-skill", "mobius"}}
	if !slices.Equal(got, want) {
		t.Errorf("skill uses = %v, want %v", got, want)
	}
}

func TestTheEngineMovesTheOldSkillUsesOfAnOrganizationWithNoSkillRepositoryWithTheSourceHarness(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startServer(t, fake, t.TempDir(), oldSkillUses)

	got := movedSkillUses(t, server)

	want := [][2]string{{"old-skill", "harness"}, {"frontend-design", "harness"}, {"repo-skill", "harness"}}
	if !slices.Equal(got, want) {
		t.Errorf("skill uses = %v, want %v", got, want)
	}
}
