package engine_test

import (
	"database/sql"
	"net/http"
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

	if got := reply(t, server, session); got != "Moved #7 to the Workstream #12 Integrate loyalty plans." {
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

func TestCreateWorkstreamNamesTheConfigKeyWhenTheOrganizationHasManyRepositoriesAndWorkstreamsInEach(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	addOpenWorkstream(fake, garden)
	server, _ := connect(t, fake, call("create_workstream", `{ title = "Billing", brief = "Bill the plans." }`))
	waitForCopiedWorkstream(t, server, garden)

	session := run(t, server, coordinatorSpec(t), "Create it.")

	calls := mcpCalls(t, server, session)
	if len(calls) != 1 || !strings.Contains(calls[0]["error"].(string), "organizations.owner.issue_repository") {
		t.Errorf("calls = %v", calls)
	}
	if got := actionRows(t, server); len(got) != 0 {
		t.Errorf("rows = %+v", got)
	}
}

// openWorkstream is the number of the Workstream that addOpenWorkstream adds.
const openWorkstream = 13

// addOpenWorkstream adds an open Workstream issue to repository.
func addOpenWorkstream(fake *testkit.FakeGitHub, repository string) {
	fake.AddIssue(repository, openWorkstream, "Workstream")
	fake.AddLabel(repository, openWorkstream, "mobius:workstream", "owner")
}

// waitForCopiedWorkstream waits until Mobius copied the Workstream of repository.
func waitForCopiedWorkstream(t *testing.T, server *testserver.Server, repository string) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		var count int
		if err := server.DB.QueryRow("SELECT count(*) FROM copied_workstreams WHERE repository = ? AND number = ?", repository, openWorkstream).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count == 1
	})
}

// startOrganization starts a server for the organization owner with repositories (default owner/shop and owner/garden),
// and the fake agent. Each repository has an issue that gives it a first poll. Each repository of workstreams has an
// open Workstream that Mobius copied before the function returns.
func startOrganization(t *testing.T, fake *testkit.FakeGitHub, adjust func(*config.Config), repositories []string, workstreams ...string) *testserver.Server {
	t.Helper()
	if len(repositories) == 0 {
		repositories = []string{shop, garden}
	}
	for _, repository := range repositories {
		fake.AddRepository(repository)
		fake.AddIssue(repository, 7, "Fix the footer")
	}
	for _, repository := range workstreams {
		addOpenWorkstream(fake, repository)
	}
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options)
	cfg := testserver.Config(t, dataDir)
	adjust(cfg)
	server := startServerOnAddedRepository(t, fake, cfg, "")
	testkit.WaitFor(t, server.Engine.AccessChecked)
	for _, repository := range repositories {
		server.WaitForFirstPoll(t, repository)
	}
	for _, repository := range workstreams {
		waitForCopiedWorkstream(t, server, repository)
	}
	return server
}

const refusedIssueRepository = "Mobius needs the issue repository of owner. Select it on the Checkup page \"Workstreams\", or set organizations.owner.issue_repository in config.toml."

func TestTheIssueRepositoryOfAnOrganizationFollowsTheOrderOfTheSources(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		repositories []string
		workstreams  []string
		before       func(t *testing.T, server *testserver.Server)
		config       func(*config.Config)
		repository   string
		source       string
		refusal      string
	}{
		{
			name:        "the config key wins over a stored choice",
			workstreams: []string{shop},
			before: func(t *testing.T, server *testserver.Server) {
				if _, err := server.DB.Exec("INSERT INTO issue_repositories (organization, repository) VALUES ('owner', ?)", shop); err != nil {
					t.Fatal(err)
				}
			},
			config: func(cfg *config.Config) {
				cfg.Organizations = map[string]config.Organization{"owner": {IssueRepository: garden}}
			},
			repository: garden,
			source:     "config",
		},
		{
			name:         "a stored choice wins over the one repository that the App sees",
			repositories: []string{shop},
			before: func(t *testing.T, server *testserver.Server) {
				if _, err := server.Engine.SetIssueRepository(t.Context(), "owner", shop); err != nil {
					t.Fatal(err)
				}
			},
			repository: shop,
			source:     "choice",
		},
		{
			name:        "a stored choice wins over the repository of the Workstreams",
			workstreams: []string{shop},
			before: func(t *testing.T, server *testserver.Server) {
				if _, err := server.Engine.SetIssueRepository(t.Context(), "owner", garden); err != nil {
					t.Fatal(err)
				}
			},
			repository: garden,
			source:     "choice",
		},
		{
			name:         "a stored choice that the App does not see gives a refusal",
			repositories: []string{shop},
			before: func(t *testing.T, server *testserver.Server) {
				if _, err := server.DB.Exec("INSERT INTO issue_repositories (organization, repository) VALUES ('owner', 'owner/gone')"); err != nil {
					t.Fatal(err)
				}
			},
			refusal: "The Mobius App has no access to owner/gone.",
		},
		{
			name:        "all open Workstreams in one repository",
			workstreams: []string{shop},
			repository:  shop,
			source:      "default",
		},
		{
			name:        "open Workstreams in two repositories",
			workstreams: []string{shop, garden},
			refusal:     refusedIssueRepository,
		},
		{
			name:    "no open Workstream",
			refusal: refusedIssueRepository,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			adjust := test.config
			if adjust == nil {
				adjust = func(*config.Config) {}
			}
			server := startOrganization(t, fake, adjust, test.repositories, test.workstreams...)
			if test.before != nil {
				test.before(t, server)
			}

			found, err := server.Engine.Checkup(t.Context(), "owner")

			if err != nil {
				t.Fatal(err)
			}
			wantSource := test.source
			if test.refusal != "" {
				wantSource = "none"
			}
			if found.IssueRepository != test.repository || found.IssueRepositoryError != test.refusal || found.IssueRepositorySource != wantSource {
				t.Errorf("issue repository = %q, error = %q, source = %q", found.IssueRepository, found.IssueRepositoryError, found.IssueRepositorySource)
			}
		})
	}
}

func TestTheCoordinatorCreatesTheWorkstreamInTheRepositoryThatTheOwnerChose(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	addOpenWorkstream(fake, garden)
	server, _ := connect(t, fake, approvalSeen+call("create_workstream", `{ title = "Billing", brief = "Bill the plans." }`))
	waitForCopiedWorkstream(t, server, garden)
	status, text := send(t, server, http.MethodPut, "/api/checkup/issue-repository", `{"organization": "owner", "repository": "`+garden+`"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, text)
	}

	run(t, server, coordinatorSpec(t), "Create it.")
	done := approveNext(t, server, 1)

	if done.State != "done" || done.Result != "Created the Workstream #14." {
		t.Errorf("action = %+v", done)
	}
	if title, _ := fake.Issue(garden, 14); title != "Billing" {
		t.Errorf("garden#14 = %q", title)
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
