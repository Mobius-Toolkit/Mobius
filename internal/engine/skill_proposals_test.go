package engine_test

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// addSkillsWorkstream adds the open Workstream number with mobius:skills.
func addSkillsWorkstream(fake *testkit.FakeGitHub, number int64) {
	fake.AddIssue(shop, number, "Skills")
	fake.AddLabel(shop, number, "mobius:workstream", "owner")
	fake.AddLabel(shop, number, "mobius:skills", "owner")
}

// callProposeSkill gives the prompt entry that calls propose_skill when the prompt has when. A file is a path and a
// content.
func callProposeSkill(when, name, reason string, files ...[2]string) string {
	var list []string
	for _, file := range files {
		list = append(list, fmt.Sprintf("{ path = %q, content = %q }", file[0], file[1]))
	}
	return fmt.Sprintf("[[prompts]]\nwhen = %q\ncall = { tool = \"propose_skill\", arguments = { name = %q, reason = %q, files = [%s] } }\n\n", when, name, reason, strings.Join(list, ", "))
}

func skillProposals(events []leadEvent) []leadEvent {
	return slices.DeleteFunc(events, func(event leadEvent) bool { return event.Kind != "skill_proposal" })
}

func TestTheCoordinatorCreatesAWorkstreamWithTheLabelSkills(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callCreateWorkstream("Create.", "Skills", "Keep the skills.", "mobius:skills")+seenScript)
	run(t, server, coordinatorSpec(t), "Create.")
	action := waitForActions(t, server, 1)[0]

	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Result != "Created the Workstream #13." {
		t.Errorf("action = %+v", done)
	}
	if labels := fake.Labels(shop, 13); !slices.Equal(labels, []string{"mobius:workstream", "mobius:skills"}) {
		t.Errorf("labels of #13 = %v", labels)
	}
}

func TestAProposalOfASkillGoesToTheLeadOfTheWorkstreamSkills(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addSkillsWorkstream(fake, 20)
	server, _ := connect(t, fake, callProposeSkill("Propose.", "failed-tests", "Find the failed tests in a CI log.",
		[2]string{"SKILL.md", "Run scripts/find.sh.\n```\nfind.sh log\n```\n"},
		[2]string{"scripts/find.sh", "grep -n FAIL \"$1\"\n"})+seenScript)
	spec := roleSpec(t, engine.ImplementerRole)
	spec.Issue = sql.NullInt64{Int64: 41, Valid: true}

	session := run(t, server, spec, "Propose.")

	want := `Sent the proposal to the Lead of the Workstream "Skills" (owner/shop#20). A change reaches the agents only after the Owner approves its pull request.`
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	events := skillProposals(leadEvents(t, server))
	if len(events) != 1 || events[0].Workstream != 20 {
		t.Fatalf("events = %+v", events)
	}
	for _, part := range []string{
		`"failed-tests"`,
		"Reason: Find the failed tests in a CI log.",
		fmt.Sprintf("Session %d, role implementer, repository owner/shop, issue owner/shop#41.", session),
		"File SKILL.md:\n````\nRun scripts/find.sh.\n```\nfind.sh log\n```\n\n````",
		"File scripts/find.sh:\n```\ngrep -n FAIL \"$1\"\n\n```",
	} {
		if !strings.Contains(events[0].Payload, part) {
			t.Errorf("payload %q does not have %q", events[0].Payload, part)
		}
	}
}

func TestAProposalOfASkillIsRefusedAndAddsNoLeadEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addSkillsWorkstream(fake, 20)
	valid := [2]string{"SKILL.md", "Text."}
	cases := []struct {
		name, skill, reason string
		files               [][2]string
		want                string
	}{
		{"uppercase", "Failed", "Reason.", [][2]string{valid}, "name must match"},
		{"hyphen first", "-failed", "Reason.", [][2]string{valid}, "name must match"},
		{"empty name", "", "Reason.", [][2]string{valid}, "name must match"},
		{"empty reason", "failed", " ", [][2]string{valid}, "reason must not be empty."},
		{"no files", "failed", "Reason.", nil, "files must have SKILL.md."},
		{"no SKILL.md", "failed", "Reason.", [][2]string{{"scripts/find.sh", "x"}}, "files must have SKILL.md."},
		{"SKILL.md in a folder", "failed", "Reason.", [][2]string{{"docs/SKILL.md", "x"}}, "files must have SKILL.md."},
		{"absolute path", "failed", "Reason.", [][2]string{valid, {"/etc/passwd", "x"}}, `The path "/etc/passwd" must be relative`},
		{"parent path", "failed", "Reason.", [][2]string{valid, {"scripts/../../x", "x"}}, `The path "scripts/../../x" must be relative`},
		{"empty path", "failed", "Reason.", [][2]string{valid, {"", "x"}}, `The path "" must be relative`},
		{"equal paths", "failed", "Reason.", [][2]string{valid, {"a.sh", "x"}, {"a.sh", "y"}}, `The path "a.sh" occurs two times.`},
		{"too long", "failed", "Reason.", [][2]string{valid, {"big.txt", strings.Repeat("x", 50000)}}, "The maximum is 50000."},
	}
	var script strings.Builder
	for _, c := range cases {
		script.WriteString(callProposeSkill(c.name, c.skill, c.reason, c.files...))
	}
	server, _ := connect(t, fake, script.String()+seenScript)
	var prompts []string
	for _, c := range cases {
		prompts = append(prompts, c.name)
	}

	session := run(t, server, roleSpec(t, engine.ImplementerRole), prompts...)

	calls := mcpCalls(t, server, session)
	if len(calls) != len(cases) {
		t.Fatalf("calls = %v", calls)
	}
	for i, c := range cases {
		if got, _ := calls[i]["error"].(string); !strings.Contains(got, c.want) {
			t.Errorf("%s: error = %q, want %q", c.name, got, c.want)
		}
	}
	if events := skillProposals(leadEvents(t, server)); len(events) != 0 {
		t.Errorf("events = %+v", events)
	}
}

func TestAProposalOfASkillIsRefusedWhenTheOrganizationHasNoWorkstreamSkills(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Skills")
	fake.AddLabel(shop, 20, "mobius:skills", "owner")
	addMaintenanceWorkstream(fake, 21)
	server, _ := connect(t, fake, callProposeSkill("Propose.", "failed", "Reason.", [2]string{"SKILL.md", "Text."})+seenScript)

	session := run(t, server, roleSpec(t, engine.CuratorRole), "Propose.")

	calls := mcpCalls(t, server, session)
	if len(calls) != 1 || calls[0]["error"] != "owner has no open Workstream with the label mobius:skills." {
		t.Errorf("calls = %v", calls)
	}
	if events := skillProposals(leadEvents(t, server)); len(events) != 0 {
		t.Errorf("events = %+v", events)
	}
}
