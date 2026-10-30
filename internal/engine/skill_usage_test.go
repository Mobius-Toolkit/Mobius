package engine_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const readUsage = "[[prompts]]\nwhen = \"Read the usage.\"\ncall = { tool = \"read_skill_usage\", arguments = { days = 30 } }\n\n"

func addSkillRow(t *testing.T, server *testserver.Server, table string, session int64, organization, repository, role, harness, skill, source string, at time.Time) {
	t.Helper()
	var err error
	if table == "session_skills" {
		_, err = server.DB.ExecContext(t.Context(), `INSERT INTO session_skills (session, organization, repository, role, harness, skill, copied_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, session, organization, repository, role, harness, skill, at.UTC().Format(time.RFC3339Nano))
	} else {
		_, err = server.DB.ExecContext(t.Context(), `INSERT INTO skill_uses (session, organization, repository, role, harness, skill, source, used_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, session, organization, repository, role, harness, skill, source, at.UTC().Format(time.RFC3339Nano))
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadSkillUsageGivesEachSkillWithTheStatsOfThePeriod(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	addSkillsWorkstream(fake, 20)
	fake.CommitFile(skills, "probe/SKILL.md", "---\nname: probe\ndescription: Probe the\n  price.\nmetadata:\n  roles: implementer, reviewer\n---\nUse it.\n", "Add probe")
	fake.CommitFile(skills, "shared/SKILL.md", "---\nname: shared\ndescription: Share it.\n---\nUse it.\n", "Add shared")
	fake.CommitFile(skills, "unused/SKILL.md", "---\nname: unused\ndescription: Nobody uses it.\n---\nUse it.\n", "Add unused")
	fake.CommitFile(skills, "repos/shop/probe/SKILL.md", "---\nname: probe\ndescription: Probe the shop.\nmetadata:\n  roles: reviewer\n---\nUse it.\n", "Add probe of the shop")
	server, _ := connect(t, fake, readUsage+seenScript)
	now := time.Now().Add(100 * 24 * time.Hour)
	server.Engine.SetClock(func() time.Time { return now })
	day := 24 * time.Hour
	s1, s2, s3 := run(t, server, leadSpec(t), "x"), run(t, server, leadSpec(t), "x"), run(t, server, leadSpec(t), "x")
	copied := func(session int64, organization, repository, role, harness, skill string, at time.Time) {
		addSkillRow(t, server, "session_skills", session, organization, repository, role, harness, skill, "", at)
	}
	used := func(session int64, repository, role, harness, skill, source string, at time.Time) {
		addSkillRow(t, server, "skill_uses", session, "owner", repository, role, harness, skill, source, at)
	}
	copied(s1, "owner", "owner/garden", "implementer", "claude-code", "probe", now.Add(-2*day))
	copied(s2, "owner", "owner/garden", "implementer", "claude-code", "probe", now.Add(-3*day))
	copied(s3, "owner", "owner/garden", "reviewer", "devin", "probe", now.Add(-3*day))
	used(s1, "owner/garden", "implementer", "claude-code", "probe", "mobius", now.Add(-2*day+time.Hour))
	used(s1, "owner/garden", "implementer", "claude-code", "probe", "mobius", now.Add(-day))
	copied(s3, "owner", "owner/shop", "reviewer", "devin", "probe", now.Add(-2*day))
	used(s3, "owner/shop", "reviewer", "devin", "probe", "mobius", now.Add(-day))
	copied(s1, "owner", "owner/shop", "lead_chat", "claude-code", "shared", now.Add(-4*day))
	copied(s2, "owner", "owner/shop", "lead_chat", "claude-code", "shared", now.Add(-40*day))
	used(s2, "owner/shop", "lead_chat", "claude-code", "shared", "mobius", now.Add(-40*day))
	copied(s1, "other", "other/shop", "implementer", "claude-code", "unused", now.Add(-day))
	used(s1, "owner/shop", "lead_chat", "claude-code", "frontend-design", "harness", now.Add(-5*day))
	used(s2, "owner/shop", "lead_chat", "claude-code", "frontend-design", "harness", now.Add(-6*day))

	spec := leadSpec(t)
	spec.Workstream = 20
	session := run(t, server, spec, "Read the usage.")

	calls := mcpCalls(t, server, session)
	if len(calls) != 1 {
		t.Fatalf("calls = %v", calls)
	}
	want := fmt.Sprintf(`Skill usage from %s to %s (UTC).

## probe

Folder: probe/
Description: Probe the price.
Roles: implementer, reviewer

| Harness | Role | Had | Used | Uses | Last use |
|---|---|---|---|---|---|
| claude-code | implementer | 2 | 1 | 2 | %s |
| devin | reviewer | 1 | 0 | 0 | none |

## probe

Folder: repos/shop/probe/
Description: Probe the shop.
Roles: reviewer

| Harness | Role | Had | Used | Uses | Last use |
|---|---|---|---|---|---|
| devin | reviewer | 1 | 1 | 1 | %s |

## shared

Folder: shared/
Description: Share it.
Roles: all roles

| Harness | Role | Had | Used | Uses | Last use |
|---|---|---|---|---|---|
| claude-code | lead | 1 | 0 | 0 | none |

## unused

Folder: unused/
Description: Nobody uses it.
Roles: all roles

No copy and no use

## Skills of the source harness

| Skill | Harness | Role | Used | Uses | Last use |
|---|---|---|---|---|---|
| frontend-design | claude-code | lead | 2 | 2 | %s |
`, now.AddDate(0, 0, -30).UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339),
		now.Add(-day).UTC().Format(time.RFC3339), now.Add(-day).UTC().Format(time.RFC3339), now.Add(-5*day).UTC().Format(time.RFC3339))
	if got := calls[0]["result"]; got != want {
		t.Errorf("result = %q\nwant %q", got, want)
	}
}

func TestReadSkillUsageRefusesDaysOutsideTheRange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	addSkillsWorkstream(fake, 20)
	script := "[[prompts]]\nwhen = \"Zero.\"\ncall = { tool = \"read_skill_usage\", arguments = { days = 0 } }\n\n" +
		"[[prompts]]\nwhen = \"Long.\"\ncall = { tool = \"read_skill_usage\", arguments = { days = 91 } }\n\n" + seenScript
	server, _ := connect(t, fake, script)
	spec := leadSpec(t)
	spec.Workstream = 20

	for _, prompt := range []string{"Zero.", "Long."} {
		calls := mcpCalls(t, server, run(t, server, spec, prompt))
		if len(calls) != 1 || calls[0]["error"] != "days must be from 1 to 90." {
			t.Errorf("calls = %v", calls)
		}
	}
}

func TestReadSkillUsageSaysThatTheOrganizationHasNoSkillRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addSkillsWorkstream(fake, 20)
	server, _ := connect(t, fake, readUsage+seenScript)
	spec := leadSpec(t)
	spec.Workstream = 20

	calls := mcpCalls(t, server, run(t, server, spec, "Read the usage."))

	if len(calls) != 1 || calls[0]["result"] != "The organization has no skill repository." {
		t.Errorf("calls = %v", calls)
	}
}

func TestOnlyTheLeadOfTheWorkstreamSkillsGetsReadSkillUsage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addSkillsWorkstream(fake, 20)
	server, _ := connect(t, fake, "[[prompts]]\nlist_tools = true\n")
	spec := leadSpec(t)
	spec.Workstream = 20

	got := toolNames(t, reply(t, server, run(t, server, spec, "List the tools")))
	other := toolNames(t, reply(t, server, run(t, server, leadSpec(t), "List the tools")))

	if !slices.Contains(got, "read_skill_usage") || slices.Contains(other, "read_skill_usage") {
		t.Errorf("tools = %q, other tools = %q", got, other)
	}
}
