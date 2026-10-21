package engine_test

import (
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const leadSeesFortyOne = "[[prompts]]\nwhen = \"dispatch of #41\"\nreply = [\"Seen\"]\n\n"

// connectGarden starts a server with the Workstream #12 of owner/shop and the issue #7 of owner/garden below it. The
// issue has no mobius:ready yet.
func connectGarden(t *testing.T, fake *testkit.FakeGitHub, lead, implementer string) *testserver.Server {
	t.Helper()
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	fake.SetBody(garden, 7, "Plants need water.")
	server, _ := connectTask(t, fake, lead, implementer, noChange)
	fake.AddForeignSubIssue(shop, 12, garden, 7)
	server.WaitForFirstPoll(t, garden)
	return server
}

func TestTheLeadStartsAnImplementerForATaskInAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"dispatch of owner/garden#7\"\ncall = { tool = \"start_implementer\", arguments = { repository = \"owner/garden\", n = 7, instructions = \"Water them twice a day.\" } }\n"
	server := connectGarden(t, fake, lead, commits)

	fake.AddLabel(garden, 7, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(garden)) == 1 })
	if pullRequests := fake.PullRequests(garden); pullRequests[0].Head != "mobius/7" {
		t.Errorf("pull requests = %+v", pullRequests)
	}
	if pullRequests := fake.PullRequests(shop); len(pullRequests) != 0 {
		t.Errorf("pull requests of owner/shop = %+v", pullRequests)
	}
	prompts := implementerPrompts(t, server)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	inOrder(t, prompts[0],
		"# Brief\n\nShip loyalty plans to all shops.\n",
		"Plants need water.",
		"# Lead instructions\n\nWater them twice a day.")
}

func TestTheLeadAsksOnTheIssueOfATaskInAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"dispatch of owner/garden#7\"\ncall = { tool = \"ask\", arguments = { repository = \"owner/garden\", n = 7, text = \"Which plants?\" } }\n"
	server := connectGarden(t, fake, lead, "")

	fake.AddLabel(garden, 7, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return len(inbox(t, server)) == 1 })
	if want := []testkit.Comment{{Author: "mobius-test[bot]", Body: "Which plants?"}}; !slices.Equal(fake.Comments(garden, 7), want) {
		t.Errorf("comments = %+v", fake.Comments(garden, 7))
	}
	if labels := fake.Labels(garden, 7); !slices.Contains(labels, "mobius:question") {
		t.Errorf("labels = %v", labels)
	}
	item := inbox(t, server)[0]
	if item.Kind != "question" || item.Repository != garden || item.WorkstreamRepository != shop || item.Workstream != 12 || item.Issue != 7 {
		t.Errorf("item = %+v", item)
	}
}

func TestTheImplementerOfATaskInAnotherRepositoryCallsCannotDo(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"cannot_do on owner/garden#7\"\nreply = [\"ok\"]\n\n" +
		"[[prompts]]\nwhen = \"dispatch of owner/garden#7\"\ncall = { tool = \"start_implementer\", arguments = { repository = \"owner/garden\", n = 7, instructions = \"Water them twice a day.\" } }\n"
	server := connectGarden(t, fake, lead, "[[prompts]]\n"+cannotDoCall)

	fake.AddLabel(garden, 7, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " cannot_do on owner/garden#7 \"Water the plants\" by the Implementer:\n\n> The plan table does not exist.")
	if session := endedImplementers(t, server, 1)[0]; session.EndReason.String != "cannot_do" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	var state string
	if err := server.DB.QueryRow("SELECT state FROM tasks WHERE repository = ? AND issue = 7", garden).Scan(&state); err != nil || state != "dispatched" {
		t.Errorf("state = %q, error = %v", state, err)
	}
}

func TestALeadCallWithNoRepositoryFailsAndChangesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := leadSeesFortyOne +
		"[[prompts]]\nwhen = \"Stop it\"\ncall = { tool = \"stop_task\", arguments = { n = 41 } }\n\n" +
		"[[prompts]]\nwhen = \"Start it\"\ncall = { tool = \"start_implementer\", arguments = { n = 41, instructions = \"Store plans in cents.\" } }\n"
	server, _ := connectTask(t, fake, lead, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })
	labels := fake.Labels(shop, 41)

	session := run(t, server, leadSpec(t), "Stop it", "Start it")

	calls := mcpCalls(t, server, session)
	if len(calls) != 2 || calls[0]["error"] != "repository must not be empty." || calls[1]["error"] != "repository must not be empty." {
		t.Errorf("calls = %+v", calls)
	}
	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}
	if got := fake.Labels(shop, 41); !slices.Equal(got, labels) {
		t.Errorf("labels = %v, want %v", got, labels)
	}
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 0 {
		t.Errorf("sessions = %+v", sessions)
	}
	if pullRequests := fake.PullRequests(shop); len(pullRequests) != 0 {
		t.Errorf("pull requests = %+v", pullRequests)
	}
}

func TestALeadCallWithARepositoryOfAnotherOrganizationFailsAndChangesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository("other/thing")
	fake.AddIssue("other/thing", 7, "Foreign issue")
	lead := leadSeesFortyOne +
		"[[prompts]]\nwhen = \"Ask it\"\ncall = { tool = \"ask\", arguments = { repository = \"other/thing\", n = 7, text = \"Which?\" } }\n\n" +
		"[[prompts]]\nwhen = \"Decline it\"\ncall = { tool = \"decline\", arguments = { repository = \"other/thing\", n = 7, reason = \"No.\" } }\n\n" +
		"[[prompts]]\nwhen = \"Start it\"\ncall = { tool = \"start_implementer\", arguments = { repository = \"other/thing\", n = 41, instructions = \"Store plans in cents.\" } }\n"
	server, _ := connectTask(t, fake, lead, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })

	session := run(t, server, leadSpec(t), "Ask it", "Decline it", "Start it")

	want := "other/thing is not in the organization owner."
	calls := mcpCalls(t, server, session)
	if len(calls) != 3 || calls[0]["error"] != want || calls[1]["error"] != want || calls[2]["error"] != want {
		t.Errorf("calls = %+v", calls)
	}
	if got := fake.Comments("other/thing", 7); len(got) != 0 {
		t.Errorf("comments = %+v", got)
	}
	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}
}

func TestAskOnAnIssueOfAnotherRepositoryWithTheNumberOfTheWorkstreamNeedsALiveTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	fake.AddIssue(garden, 12, "Mow the lawn")
	lead := "[[prompts]]\nwhen = \"Ask it\"\ncall = { tool = \"ask\", arguments = { repository = \"owner/garden\", n = 12, text = \"Which lawn?\" } }\n"
	server, _ := connect(t, fake, lead)

	session := run(t, server, leadSpec(t), "Ask it")

	calls := mcpCalls(t, server, session)
	if len(calls) != 1 || calls[0]["error"] != "#12 has no live task in this Workstream." {
		t.Errorf("calls = %+v", calls)
	}
	if got := fake.Comments(garden, 12); len(got) != 0 {
		t.Errorf("comments = %+v", got)
	}
}
