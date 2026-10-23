package engine_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// gardenTriagers is the key of the sessions of the Triagers of the issues of owner/garden.
var gardenTriagers = engine.ChatKey{Organization: "owner", Repository: garden}

const gardenTriager = `
[[prompts]]
when = "# Issue\n\nowner/garden#81 "
call = { tool = "message_lead", arguments = { workstream = 12, text = "Water the plants each day." } }

[[prompts]]
when = "# Issue\n\nowner/garden#82 "
call = { tool = "create_workstream", arguments = { title = "Watering", brief = "Water each plant." } }

[[prompts]]
when = "# Issue\n\nowner/garden#83 "
reply = ["Seen the Workstreams."]
`

func TestTheTriagerOfAnIssueInAnotherRepositorySendsAMessageToTheLeadOfAWorkstreamInTheIssueRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, gardenTriager)

	fake.CreateIssue(garden, 81, "Water the plants", "Each day.", "owner")
	decided(t, server, waitForActions(t, server, 1)[0].ID, "approve", "")

	want := "Message of the Triager, approved by the Owner:\n\nWater the plants each day.\n\nhttps://github.com/owner/garden/issues/81"
	testkit.WaitFor(t, func() bool {
		events := toLead12(t, server)
		return len(events) == 1 && events[0].Kind == "triager" && events[0].Payload == want
	})
	link := triagerComment(81, "The triage ends. The Workstream is https://github.com/owner/shop/issues/12")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(garden, 81), link) })
	if state, reason := fake.State(garden, 81); state != "closed" || reason != "completed" {
		t.Errorf("state = %s, %s", state, reason)
	}
}

func TestTheTriagerOfAnIssueInAnotherRepositoryCreatesTheWorkstreamInTheIssueRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, gardenTriager)

	fake.CreateIssue(garden, 82, "Water the plants", "Each day.", "owner")
	done := decided(t, server, waitForActions(t, server, 1)[0].ID, "approve", "")

	var number int64
	if _, err := fmt.Sscanf(done.Result, "Created the Workstream #%d.", &number); err != nil || done.State != "done" {
		t.Fatalf("action = %+v", done)
	}
	if title, body := fake.Issue(shop, number); title != "Watering" || body != "Water each plant." {
		t.Errorf("workstream = %q, %q", title, body)
	}
	if got := fake.Labels(shop, number); !slices.Equal(got, []string{"mobius:workstream"}) {
		t.Errorf("labels = %v", got)
	}
	link := triagerComment(82, fmt.Sprintf("The triage ends. The Workstream is https://github.com/owner/shop/issues/%d", number))
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(garden, 82), link) })
	if state, reason := fake.State(garden, 82); state != "closed" || reason != "completed" {
		t.Errorf("state = %s, %s", state, reason)
	}
}

func TestTheFirstPromptOfTheTriagerOfAnIssueInAnotherRepositoryListsTheWorkstreamsOfTheIssueRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, gardenTriager)
	fake.AddIssue(garden, 9, "Plant the garden")
	fake.AddLabel(garden, 9, "mobius:workstream", "owner")
	waitForPolls(t, fake)

	fake.CreateIssue(garden, 83, "Water the plants", "Each day.", "owner")

	session := waitForChatSession(t, server, gardenTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "#12 Integrate loyalty plans (owner/shop)") || strings.Contains(prompts[0], "Plant the garden") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTheTriagerOfAnIssueRefusesTheToolsWhenTheOrganizationHasNoIssueRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	fake.AddIssue(garden, 9, "Plant the garden")
	fake.AddLabel(garden, 9, "mobius:workstream", "owner")
	server, _ := connect(t, fake, gardenTriager)
	server.WaitForFirstPoll(t, garden)

	fake.CreateIssue(garden, 82, "Water the plants", "Each day.", "owner")

	session := waitForChatSession(t, server, gardenTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 || strings.Contains(prompts[0], "Plant the garden") || !strings.Contains(prompts[0], "# Open Workstreams\n\nThe tools that use a Workstream refuse: Mobius needs the issue repository of owner.") {
		t.Errorf("prompts = %q", prompts)
	}
	calls := mcpCalls(t, server, session.ID)
	if len(calls) != 1 || calls[0]["error"] != "Mobius needs the issue repository of owner. Set organizations.owner.issue_repository in config.toml." {
		t.Errorf("calls = %v", calls)
	}
	if got := actionRows(t, server); len(got) != 0 {
		t.Errorf("rows = %+v", got)
	}
	if state, _ := fake.State(garden, 82); state != "open" {
		t.Errorf("state = %s", state)
	}
}
