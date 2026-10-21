package engine_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// issueRepositoryShop starts a server for the organization owner with the repositories owner/shop and owner/garden. The
// config names owner/shop as the issue repository. The issue owner/garden#7 gives the repository a first poll.
func issueRepositoryShop(t *testing.T, fake *testkit.FakeGitHub, prompts string) *testserver.Server {
	t.Helper()
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	server, _ := connectWith(t, fake, prompts, func(cfg *config.Config) {
		cfg.Organizations = map[string]config.Organization{"owner": {IssueRepository: shop}}
	})
	server.WaitForFirstPoll(t, garden)
	return server
}

type researcherSession struct {
	Repository string
	EndReason  sql.NullString
}

// researcherSessions gives the Researcher sessions of each chat.
func researcherSessions(t *testing.T, server *testserver.Server) []researcherSession {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, s *researcherSession) error {
		return rows.Scan(&s.Repository, &s.EndReason)
	}, "SELECT repository, end_reason FROM sessions WHERE role = 'researcher' ORDER BY id")
}

func TestTheCoordinatorCreatesTheWorkstreamInTheIssueRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, approvalSeen+call("create_workstream", `{ title = "Billing", brief = "Bill the plans." }`))

	chat := run(t, server, coordinatorSpec(t), "Create it.")
	done := approveNext(t, server, 1)

	if got := reply(t, server, chat); !strings.HasPrefix(got, actionWaits) {
		t.Errorf("reply = %q", got)
	}
	if done.State != "done" || done.Result != "Created the Workstream #13." {
		t.Errorf("action = %+v", done)
	}
	title, body := fake.Issue(shop, 13)
	if labels := fake.Labels(shop, 13); title != "Billing" || body != "Bill the plans." || !reflect.DeepEqual(labels, []string{"mobius:workstream"}) {
		t.Errorf("shop#13 = %q, %q, %v", title, body, labels)
	}
}

func TestTheCoordinatorMovesAnIssueOfAnotherRepositoryBelowAWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("move_issue", `{ repository = "owner/garden", n = 7, workstream = 12 }`))
	fake.AddLabel(garden, 7, "mobius:no-workstream", "mobius-test[bot]")

	session := run(t, server, coordinatorSpec(t), "Move it.")

	if got := reply(t, server, session); got != "Moved #7 to the Workstream #12." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.Labels(garden, 7); !reflect.DeepEqual(got, []string{"mobius:ready"}) {
		t.Errorf("labels = %v", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); !reflect.DeepEqual(got, []int64{7}) {
		t.Errorf("sub-issues = %v", got)
	}
}

func TestTheCoordinatorSendsAMessageToTheLeadOfAWorkstreamInTheIssueRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)

	run(t, server, coordinatorSpec(t), "Send.")
	approveNext(t, server, 1)

	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	want := "Message of the Coordinator, approved by the Owner:\n\nCreate the task issues."
	if events := toLead12(t, server); !reflect.DeepEqual(events, []leadEvent{{Workstream: 12, Kind: "coordinator", Payload: want, Delivered: true}}) {
		t.Errorf("events = %v", events)
	}
}

func TestCreateWorkstreamNamesTheConfigKeyWhenTheOrganizationHasManyRepositories(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	server, _ := connect(t, fake, call("create_workstream", `{ title = "Billing", brief = "Bill the plans." }`))

	session := run(t, server, coordinatorSpec(t), "Create it.")

	calls := mcpCalls(t, server, session)
	if len(calls) != 1 || !strings.Contains(calls[0]["error"].(string), "organizations.owner.issue_repository") {
		t.Errorf("calls = %v", calls)
	}
	if got := actionRows(t, server); len(got) != 0 {
		t.Errorf("rows = %+v", got)
	}
}

func TestMoveIssueAndStartResearcherRefuseARepositoryOfAnotherOrganization(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository("other/thing")
	fake.AddIssue("other/thing", 7, "Foreign issue")
	server := issueRepositoryShop(t, fake,
		call("move_issue", `{ repository = "other/thing", n = 7, workstream = 12 }`)+
			call("start_researcher", `{ repository = "other/thing", question = "Where?" }`))

	session := run(t, server, coordinatorSpec(t), "1. ", "2. ")

	want := "error: other/thing is not in the organization owner."
	if got := reply(t, server, session); got != want+want {
		t.Errorf("reply = %q", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); len(got) != 0 {
		t.Errorf("sub-issues = %v", got)
	}
	if got := researcherSessions(t, server); len(got) != 0 {
		t.Errorf("researchers = %+v", got)
	}
}

func TestTheCoordinatorStartsAResearcherOnTheRepositoryOfTheCode(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	coordinator := `
[[prompts]]
when = "# Researcher message"
reply = ["I have the report."]

[[prompts]]
when = "Research it."
call = { tool = "start_researcher", arguments = { repository = "owner/garden", question = "Where are the plants?" } }
`
	server, _ := connectResearcher(t, fake, coordinator, quickResearcher)

	chat := run(t, server, coordinatorSpec(t), "Research it.")

	if got := reply(t, server, chat); !strings.HasPrefix(got, "Started the Researcher ") {
		t.Errorf("reply = %q", got)
	}
	session := testkit.WaitForValue(t, func() (researcherSession, bool) {
		sessions := researcherSessions(t, server)
		if len(sessions) == 0 {
			return researcherSession{}, false
		}
		return sessions[0], sessions[0].EndReason.Valid
	})
	if session.Repository != garden || session.EndReason.String != "done" {
		t.Errorf("session = %+v", session)
	}
}
