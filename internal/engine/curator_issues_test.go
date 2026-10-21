package engine_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// addMaintenanceWorkstream adds the open Workstream number with mobius:maintenance.
func addMaintenanceWorkstream(fake *testkit.FakeGitHub, number int64) {
	fake.AddIssue(shop, number, "Maintenance")
	fake.AddLabel(shop, number, "mobius:workstream", "owner")
	fake.AddLabel(shop, number, "mobius:maintenance", "owner")
}

func TestTheCuratorCreatesATaskIssueWithTriageBelowTheMaintenanceWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_issue", `{ title = "Fix the flaky test", body = "The test fails in CI.", blocked_by = [40] }`))
	addMaintenanceWorkstream(fake, 20)
	fake.AddIssue(shop, 40, "Plan table")

	session := run(t, server, roleSpec(t, engine.CuratorRole), "Create the issue")

	if got := reply(t, server, session); got != "Created #41." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.SubIssueNumbers(shop, 20); !reflect.DeepEqual(got, []int64{41}) {
		t.Errorf("sub-issues = %v", got)
	}
	if title, body := fake.Issue(shop, 41); title != "Fix the flaky test" || body != "The test fails in CI." {
		t.Errorf("issue = %q, %q", title, body)
	}
	if got := fake.Labels(shop, 41); !reflect.DeepEqual(got, []string{"mobius:triage"}) {
		t.Errorf("labels = %v", got)
	}
	if got := fake.BlockerNumbers(shop, 41); !reflect.DeepEqual(got, []int64{40}) {
		t.Errorf("blockers = %v", got)
	}
}

func TestTheCuratorCreatesTheIssueInTheOpenMaintenanceWorkstreamWithTheLowestNumber(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_issue", `{ title = "Fix the flaky test", body = "", blocked_by = [] }`))
	addMaintenanceWorkstream(fake, 30)
	addMaintenanceWorkstream(fake, 20)
	addMaintenanceWorkstream(fake, 10)
	fake.CloseIssue(shop, 10)
	fake.AddIssue(shop, 5, "Not a Workstream")
	fake.AddLabel(shop, 5, "mobius:maintenance", "owner")

	run(t, server, roleSpec(t, engine.CuratorRole), "Create the issue")

	if got := fake.SubIssueNumbers(shop, 20); len(got) != 1 {
		t.Errorf("sub-issues of #20 = %v", got)
	}
	for _, other := range []int64{5, 10, 30} {
		if got := fake.SubIssueNumbers(shop, other); len(got) != 0 {
			t.Errorf("sub-issues of #%d = %v", other, got)
		}
	}
}

func TestTheCuratorHasToolsToSearchAndCreateIssuesOnlyWithAMaintenanceWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nlist_tools = true\n")
	fake.AddIssue(shop, 5, "Closed")
	fake.AddLabel(shop, 5, "mobius:workstream", "owner")
	fake.AddLabel(shop, 5, "mobius:maintenance", "owner")
	fake.CloseIssue(shop, 5)

	without := run(t, server, roleSpec(t, engine.CuratorRole), "List the tools")
	addMaintenanceWorkstream(fake, 20)
	with := run(t, server, roleSpec(t, engine.CuratorRole), "List the tools")

	if got := toolNames(t, reply(t, server, without)); !reflect.DeepEqual(got, []string{"edit_memory", "propose_skill"}) {
		t.Errorf("tools without a maintenance Workstream = %q", got)
	}
	if got := toolNames(t, reply(t, server, with)); !reflect.DeepEqual(got, []string{"create_issue", "edit_memory", "propose_skill", "search_issues"}) {
		t.Errorf("tools with a maintenance Workstream = %q", got)
	}
}

func TestMobiusRefusesTheFourthIssueOfACuratorSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	create := call("create_issue", `{ title = "A problem", body = "", blocked_by = [] }`)
	server, _ := connect(t, fake, create+create+create+create)
	addMaintenanceWorkstream(fake, 20)

	session := run(t, server, roleSpec(t, engine.CuratorRole), "1. ", "2. ", "3. ", "4. ")

	want := "Created #21.Created #22.Created #23.error: You created 3 issues in this session. This is the maximum."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := fake.SubIssueNumbers(shop, 20); len(got) != 3 {
		t.Errorf("sub-issues = %v", got)
	}
}

func TestThePromptOfTheCuratorHasTheDuplicateRulesAndTheMaintenanceWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, curatorScript)
	addMaintenanceWorkstream(fake, 20)

	endLeads(t, server, 10)

	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := curatorSessions(t, server)
		return sessions[0], len(sessions) == 1 && sessions[0].EndedAt.Valid
	})
	prompt := promptTexts(t, server, session.ID)[0]
	for _, part := range []string{
		"The maintenance Workstream is #20.",
		"- An open issue matches: create no issue.",
		"- A closed issue with the close reason `not_planned` or `duplicate` matches: create no issue.",
		"- A closed issue with the close reason `completed` matches: create a new issue only if the problem occurs again after the close. Put a link to the old issue in the body.",
		"- A comment of a trusted author on a closed issue that matches rejects the problem: create no issue. This rule applies to each close reason, and to a close with no reason.",
		"last 3 comments of trusted authors",
		"a maximum of 3 task issues",
	} {
		if !strings.Contains(prompt, part) {
			t.Errorf("%q is not in %s", part, prompt)
		}
	}
}

func TestThePromptOfTheCuratorTellsThatTheRepositoryHasNoMaintenanceWorkstream(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), curatorScript)

	endLeads(t, server, 10)

	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := curatorSessions(t, server)
		return sessions[0], len(sessions) == 1 && sessions[0].EndedAt.Valid
	})
	if prompt := promptTexts(t, server, session.ID)[0]; !strings.Contains(prompt, "The repository has no maintenance Workstream. You have no tool to create task issues.") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestSearchIssuesGivesOpenAndClosedIssuesWithTheCloseReasonAndTheLastThreeCommentsOfTrustedAuthors(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("search_issues", `{ query = "flaky" }`)+call("search_issues", `{ query = "nothing" }`)+call("search_issues", `{ query = " " }`))
	addMaintenanceWorkstream(fake, 20)
	fake.AddIssue(shop, 40, "Flaky CI test")
	fake.SetBody(shop, 40, "The test fails.")
	for _, comment := range []string{"Comment 1.", "Comment 2.", "Comment 3.", "Comment 4."} {
		fake.AddComment(shop, 40, "owner", comment)
	}
	fake.AddComment(shop, 40, "mallory", "Comment of Mallory.")
	fake.CloseIssueAs(shop, 40, "not_planned")
	fake.AddIssue(shop, 41, "Flaky CI deploy")
	fake.AddComment(shop, 41, "owner", "Comment on the open issue.")
	fake.AddIssue(shop, 42, "Flaky CI cache")
	fake.CloseIssue(shop, 42)
	fake.AddIssue(shop, 43, "Flaky CI from Mallory")
	fake.SetAuthor(shop, 43, "mallory")
	fake.AddPullRequest(shop, 44, "Flaky CI pull request")

	session := run(t, server, roleSpec(t, engine.CuratorRole), "1. ", "2. ", "3. ")

	text := reply(t, server, session)
	for _, part := range []string{
		"#40 Flaky CI test (closed, not_planned)\n\nThe test fails.\n\n# Last comments\n",
		" UTC:\nComment 2.\n",
		" UTC:\nComment 4.\n",
		"#41 Flaky CI deploy (open)\n\n\n\n#42 Flaky CI cache (closed, no reason)\n",
		"No issue matches.error: query must not be empty.",
	} {
		if !strings.Contains(text, part) {
			t.Errorf("%q is not in %q", part, text)
		}
	}
	for _, part := range []string{"Comment 1.", "Mallory", "Comment on the open issue.", "#43", "#44"} {
		if strings.Contains(text, part) {
			t.Errorf("%q is in %q", part, text)
		}
	}
}
