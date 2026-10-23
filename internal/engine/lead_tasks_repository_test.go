package engine_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestTheLeadCreatesAnIssueInAnotherRepositoryBelowTheWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("create_issue", `{ repository = "owner/garden", title = "Water the beds", body = "Beds need water.", parent = "owner/shop#12", blocked_by = [] }`))

	session := run(t, server, leadSpec(t), "Plan it")

	if got := reply(t, server, session); got != "Created owner/garden#8." {
		t.Errorf("reply = %q", got)
	}
	if title, body := fake.Issue(garden, 8); title != "Water the beds" || body != marked("Beds need water.", leadMarkElsewhere) {
		t.Errorf("owner/garden#8 = %q, %q", title, body)
	}
	if got := fake.SubIssueNumbers(shop, 12); !reflect.DeepEqual(got, []int64{8}) {
		t.Errorf("sub-issues = %v", got)
	}
	if fake.HasIssue(shop, 13) {
		t.Error("owner/shop#13 exists")
	}
}

func TestTheLeadCreatesAnIssueBelowAnIssueOfAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("create_issue", `{ repository = "owner/shop", title = "Water the beds", body = "", parent = "owner/garden#7", blocked_by = [] }`))
	fake.AddForeignSubIssue(shop, 12, garden, 7)

	session := run(t, server, leadSpec(t), "Plan it")

	if got := reply(t, server, session); got != "Created owner/shop#13." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.SubIssueNumbers(garden, 7); !reflect.DeepEqual(got, []int64{13}) {
		t.Errorf("sub-issues of owner/garden#7 = %v", got)
	}
}

func TestCreateIssueAddsABlockerOfAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("create_issue", `{ repository = "owner/shop", title = "Sell the plants", body = "", parent = "owner/shop#12", blocked_by = ["owner/garden#7"] }`))

	session := run(t, server, leadSpec(t), "Plan it")

	if got := reply(t, server, session); got != "Created owner/shop#13." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.BlockerNumbers(shop, 13); !reflect.DeepEqual(got, []int64{7}) {
		t.Errorf("blockers = %v", got)
	}
}

func TestCreateIssueAndMarkReadyWithNoRepositoryFailAndChangeNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("create_issue", `{ title = "Water the beds", body = "", parent = "owner/shop#12", blocked_by = [] }`)+
		call("mark_ready", `{ n = 7 }`))
	fake.AddForeignSubIssue(shop, 12, garden, 7)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	if got := reply(t, server, session); got != "error: repository must not be empty.error: repository must not be empty." {
		t.Errorf("reply = %q", got)
	}
	if fake.HasIssue(shop, 13) || fake.HasIssue(garden, 8) {
		t.Error("the call created an issue")
	}
	if got := fake.Labels(garden, 7); len(got) != 0 {
		t.Errorf("labels = %v", got)
	}
}

func TestCreateIssueRefusesARepositoryOfAnotherOrganizationAndABadReference(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository("other/thing")
	fake.AddIssue("other/thing", 7, "Foreign issue")
	server := issueRepositoryShop(t, fake, call("create_issue", `{ repository = "other/thing", title = "A", body = "", parent = "owner/shop#12", blocked_by = [] }`)+
		call("create_issue", `{ repository = "owner/shop", title = "A", body = "", parent = "other/thing#7", blocked_by = [] }`)+
		call("create_issue", `{ repository = "owner/shop", title = "A", body = "", parent = "owner/shop#12", blocked_by = ["other/thing#7"] }`)+
		call("create_issue", `{ repository = "owner/shop", title = "A", body = "", parent = "12", blocked_by = [] }`)+
		call("mark_ready", `{ repository = "other/thing", n = 7 }`))

	session := run(t, server, leadSpec(t), "1. ", "2. ", "3. ", "4. ", "5. ")

	organization := "error: other/thing is not in the organization owner."
	want := organization + organization + organization +
		"error: \"12\" is not an issue reference in the form owner/name#N." +
		organization
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if fake.HasIssue(shop, 13) || fake.HasIssue("other/thing", 8) {
		t.Error("the call created an issue")
	}
	if got := fake.Labels("other/thing", 7); len(got) != 0 {
		t.Errorf("labels = %v", got)
	}
}

func TestMarkReadyAddsTheLabelToATaskIssueInAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, "[[prompts]]\nwhen = \"dispatch of #7\"\nreply = [\"Seen\"]\n\n"+
		"[[prompts]]\nwhen = \"Start it\"\ncall = { tool = \"mark_ready\", arguments = { repository = \"owner/garden\", n = 7 } }\n")
	fake.AddForeignSubIssue(shop, 12, garden, 7)

	session := run(t, server, leadSpec(t), "Start it")

	if got := reply(t, server, session); got != "Marked owner/garden#7 ready." {
		t.Errorf("reply = %q", got)
	}
	testkit.WaitFor(t, func() bool {
		var count int
		if err := server.DB.QueryRow("SELECT count(*) FROM tasks WHERE repository = ? AND workstream_repository = ? AND issue = 7 AND workstream = 12", garden, shop).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count == 1
	})
}

func TestListTasksWritesTheRepositoryOfEachTaskAndEachBlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("list_tasks", "{}"))
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddForeignSubIssue(shop, 12, garden, 7)
	fake.AddForeignBlockedBy(shop, 41, garden, 7)
	testkit.WaitFor(t, func() bool {
		lines, err := server.Engine.Tasks(t.Context(), shop, 12)
		return err == nil && len(lines) == 2 && len(lines[0].BlockedBy) == 1
	})

	_, text := leadReply(t, server)

	want := "owner/shop#41 Add plan model: open, blocked by owner/garden#7\nowner/garden#7 Water the plants: open\n"
	if text != want {
		t.Errorf("reply = %q", text)
	}
}

func TestTheFirstPromptOfTheLeadNamesTheRepositoriesOfTheOrganization(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	sendChat(t, server, leadChat, "Plan the next step")

	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(store.Session) bool { return true })
	prompt := testkit.WaitForValue(t, func() (string, bool) {
		prompts := promptTexts(t, server, session.ID)
		return strings.Join(prompts, ""), len(prompts) > 0
	})
	inOrder(t, prompt,
		"# Repositories\n\nThe repositories of the organization:\n- owner/garden\n- owner/shop\n\nThe issue repository of the organization: owner/shop\n\n",
		"# Task list\n\n")
}

func TestTheLeadMovesATaskInAnotherRepositoryAfterTheOwnerApproves(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, approvalSeen+call("move_task", `{ repository = "owner/garden", n = 7, workstream = 20 }`)+seenScript)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddForeignSubIssue(shop, 12, garden, 7)

	waitForBilling(t, server)
	run(t, server, leadSpec(t), "Move it")
	action := approveNext(t, server, 1)

	if action.Result != "Moved owner/garden#7 to the Workstream #20 Billing." {
		t.Errorf("action = %+v", action)
	}
	if got := fake.SubIssueNumbers(shop, 20); !reflect.DeepEqual(got, []int64{7}) {
		t.Errorf("sub-issues of #20 = %v", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); len(got) != 0 {
		t.Errorf("sub-issues of #12 = %v", got)
	}
}

func TestMoveTaskWithNoRepositoryFailsAtTheCall(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("move_task", `{ n = 7, workstream = 20 }`))

	session := run(t, server, leadSpec(t), "Move it")

	if got := reply(t, server, session); got != "error: repository must not be empty." {
		t.Errorf("reply = %q", got)
	}
}

const gardenResearchLead = `
[[prompts]]
when = "# Researcher message"
reply = ["I have the report."]

[[prompts]]
when = "Begin garden"
call = { tool = "start_researcher", arguments = { repository = "owner/garden", question = "Garden question" } }

[[prompts]]
when = "Stop garden"
call = { tool = "stop_researcher", arguments = { id = 2 } }
`

func TestTheLeadStartsAndStopsAResearcherOnTheCodeOfAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	server, _ := connectResearcher(t, fake, gardenResearchLead, hangingResearcher)
	server.WaitForFirstPoll(t, garden)

	sendChat(t, server, leadChat, "Begin garden.")
	session := waitForResearchers(t, server, "Looking.")[0]

	if got := researcherSessions(t, server); len(got) != 1 || got[0].Repository != garden {
		t.Errorf("Researcher sessions = %+v", got)
	}
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 1 || !strings.Contains(prompts[0], "# Brief\n\nShip loyalty plans to all shops.") {
		t.Errorf("prompts = %q", prompts)
	}
	sendChat(t, server, leadChat, "Stop garden.")
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadCalls(t, server), func(call map[string]any) bool { return call["result"] == "Stopped the Researcher 2." })
	})
}
