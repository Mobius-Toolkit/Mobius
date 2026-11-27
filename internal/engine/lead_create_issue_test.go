package engine_test

import (
	"database/sql"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func countCreates(requests []testkit.Request) int {
	count := 0
	for _, request := range requests {
		if request.Method == http.MethodPost && request.Path == "/repos/owner/shop/issues" {
			count++
		}
	}
	return count
}

const createPlanModel = `{ repository = "owner/shop", title = "Add plan model", body = "", parent = "owner/shop#12", blocked_by = [] }`

func TestCreateIssueGivesTheNumberWhileTheCopyOfTheTreeIsHeld(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_issue", createPlanModel))
	copied := func(number int64) bool {
		var count int
		_ = server.DB.QueryRow("SELECT count(*) FROM issues i JOIN workstreams w ON w.id = i.workstream WHERE w.repository = ? AND w.number = 12 AND i.number = ?", shop, number).Scan(&count)
		return count == 1
	}
	testkit.WaitFor(t, func() bool {
		var count int
		_ = server.DB.QueryRow("SELECT count(*) FROM workstreams WHERE repository = ? AND number = 12 AND state = 'open'", shop).Scan(&count)
		return count == 1
	})

	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "Plan the work.", nil); err != nil {
		t.Fatal(err)
	}

	if got := reply(t, server, agent.ID()); got != "Created owner/shop#13." {
		t.Errorf("reply = %q", got)
	}
	if !copied(13) {
		t.Error("issues row for owner/shop#13 missing right after create_issue")
	}
}

func copiedRows(t *testing.T, server *testserver.Server, number int64) int {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM issues i JOIN workstreams w ON w.id = i.workstream WHERE w.repository = ? AND w.number = 12 AND i.number = ?", shop, number).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestListTasksShowsTheNewIssueOfCreateIssueWithItsBlockersWhileTheCopyOfTheTreeIsHeld(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	create := `{ repository = "owner/shop", title = "Add plan model", body = "", parent = "owner/shop#12", blocked_by = ["owner/shop#88"] }`
	server, _ := connect(t, fake, call("create_issue", create)+"[[prompts]]\ncall = { tool = \"list_tasks\" }\n")
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddSubIssue(shop, 20, 88)
	testkit.WaitFor(t, func() bool {
		var count int
		_ = server.DB.QueryRow("SELECT count(*) FROM issues i JOIN workstreams w ON w.id = i.workstream WHERE w.repository = ? AND w.number = 20 AND i.number = 88", shop).Scan(&count)
		return count == 1
	})

	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "Plan the work. ", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.Prompt(t.Context(), "List the tasks. ", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}

	if got := reply(t, server, agent.ID()); got != "Created owner/shop#89.owner/shop#89 Add plan model: open, blocked by owner/shop#88 (Workstream \"Billing\")\n" {
		t.Errorf("reply = %q", got)
	}
	waitForPolls(t, fake)
	if got := copiedRows(t, server, 89); got != 1 {
		t.Errorf("rows of owner/shop#89 = %d", got)
	}
}

func TestAPollThatReadsTheTreeBeforeGitHubListsTheNewIssueDoesNotRemoveItsCopy(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_issue", createPlanModel))
	fake.HideFromSubIssues(shop, 13, true)
	subIssueReads := func() int {
		count := 0
		for _, request := range fake.Requests() {
			if request.Method == http.MethodGet && request.Path == "/repos/owner/shop/issues/12/sub_issues" {
				count++
			}
		}
		return count
	}
	reads := subIssueReads()

	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "Plan the work. ", nil); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return subIssueReads() >= reads+6 })

	if got := copiedRows(t, server, 13); got != 1 {
		t.Errorf("rows of owner/shop#13 while GitHub hides it = %d", got)
	}
	fake.HideFromSubIssues(shop, 13, false)
	reads = subIssueReads()
	testkit.WaitFor(t, func() bool { return subIssueReads() >= reads+6 })
	if got := copiedRows(t, server, 13); got != 1 {
		t.Errorf("rows of owner/shop#13 after GitHub lists it = %d", got)
	}
}

func TestTwoCreateIssueCallsOfOneSessionWithTheSameParentAndTitleCreateOneIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	create := call("create_issue", createPlanModel)
	server, _ := connect(t, fake, create+create)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	if got := reply(t, server, session); got != "Created owner/shop#13.Created owner/shop#13." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); len(got) != 1 {
		t.Errorf("sub-issues = %v", got)
	}
}

func TestACreateIssueCallAfterAFailedSubIssueAddGivesTheIssueThatTheFirstCallCreated(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	create := call("create_issue", createPlanModel)
	server, _ := connect(t, fake, create+create)
	fake.FailAddSubIssue(shop, 12, true)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	if got := reply(t, server, session); got != "Created owner/shop#13.Created owner/shop#13." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.Requests(); countCreates(got) != 1 {
		t.Errorf("creates = %d", countCreates(got))
	}
}

func TestAFailedParentReadOfACreatedIssueDoesNotStopTheDispatchOfALaterIssueAndTheFailedIssueKeepsItsStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 43, "Price rounding")
	fake.AddSubIssue(shop, 12, 43)
	server, _ := connect(t, fake, call("create_issue", createPlanModel))
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = 'owner/shop' AND number = 43 AND workstream IS NOT NULL") == 1
	})
	fake.FailAddSubIssue(shop, 12, true)
	session := run(t, server, leadSpec(t), "Create it.")
	if got := reply(t, server, session); got != "Created owner/shop#44." {
		t.Fatalf("reply = %q", got)
	}
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field = 'parent' AND status = 'pending'") == 1
	})
	fake.FailParents(shop, 44, true)
	for _, number := range []int64{44, 43} {
		if err := server.Engine.StartIssue(t.Context(), shop, number); err != nil {
			t.Fatal(err)
		}
	}

	liveTaskOf(t, server, 43)
	if hasLiveTask(t, server, 44) || !hasStart(t, server, 44) {
		t.Errorf("live task of #44 = %t, start of #44 = %t", hasLiveTask(t, server, 44), hasStart(t, server, 44))
	}
}

// Serial: the test sets the hook beforeLeadIssueWait.
func TestACreateIssueCallWaitsForTheCallWithTheSameParentAndTitleThatRuns(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")
	start(t, server, leadSpec(t))
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0.1.0"}, nil)
	connection, err := client.Connect(t.Context(), &sdk.StreamableClientTransport{Endpoint: mcpURL(t, dataDir)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	createdPlanModels := func() int { return countCreates(fake.Requests()) }
	release := sync.OnceFunc(fake.HoldNext("POST /repos/{owner}/{repo}/issues"))
	t.Cleanup(release)
	arguments := map[string]any{"repository": "owner/shop", "title": "Add plan model", "body": "", "parent": "owner/shop#12", "blocked_by": []string{}}
	results := make(chan string, 2)
	createIssue := func() {
		result, err := connection.CallTool(t.Context(), &sdk.CallToolParams{Name: "create_issue", Arguments: arguments})
		if err != nil {
			t.Error(err)
			results <- ""
			return
		}
		results <- result.Content[0].(*sdk.TextContent).Text
	}

	waiting := make(chan struct{})
	*engine.BeforeLeadIssueWait = sync.OnceFunc(func() { close(waiting) })
	t.Cleanup(func() { *engine.BeforeLeadIssueWait = nil })

	go createIssue()
	testkit.WaitFor(t, func() bool { return createdPlanModels() == 1 })
	go createIssue()
	<-waiting
	release()

	if first, second := <-results, <-results; first != "Created owner/shop#13." || second != first {
		t.Errorf("results = %q, %q", first, second)
	}
	if got := fake.SubIssueNumbers(shop, 12); len(got) != 1 {
		t.Errorf("sub-issues = %v", got)
	}
}

func TestCreateIssueAddsOneUnnumberedRowAndThreeOutgoingChangesAndFillsThemAfterSend(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	script := "[[prompts]]\nwhen = \"Plan the work.\"\ncall = { tool = \"create_issue\", arguments = { repository = \"owner/shop\", title = \"Add plan model\", body = \"Plans have a price.\", parent = \"owner/shop#41\", blocked_by = [\"owner/shop#40\"] } }\n\n" + seenScript
	server, _ := connectWith(t, fake, script, withUIURL)
	fake.AddIssue(shop, 41, "Plan table")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 40, "Plan schema")
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 41", shop) == 1
	})
	fake.RefuseIssueCreates(http.StatusInternalServerError)

	session := run(t, server, leadSpec(t), "Plan the work.")

	if got, want := reply(t, server, session), "Mobius creates the issue \"Add plan model\" on GitHub later. A Lead event gives its number."; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
	var rowID, parentNumber int64
	var rowBody, rowState string
	var rowNumber sql.NullInt64
	if err := server.DB.QueryRow(`SELECT i.id, i.number, i.body, i.state, p.number FROM issues i
		JOIN issues p ON p.id = i.parent
		WHERE i.repository = ? AND i.title = 'Add plan model'`, shop).Scan(&rowID, &rowNumber, &rowBody, &rowState, &parentNumber); err != nil {
		t.Fatal(err)
	}
	if rowNumber.Valid || rowBody != "Plans have a price." || rowState != "open" || parentNumber != 41 {
		t.Fatalf("row = (%v, %q, %q, parent=%d)", rowNumber, rowBody, rowState, parentNumber)
	}
	if got := countRows(t, server, "SELECT count(*) FROM blockers b JOIN issues bi ON bi.id = b.blocker WHERE b.issue = ? AND bi.repository = ? AND bi.number = 40", rowID, shop); got != 1 {
		t.Fatalf("blockers = %d", got)
	}
	fields := query(t, server, func(rows *sql.Rows, f *string) error { return rows.Scan(f) },
		"SELECT field FROM outgoing_changes WHERE issue = ? AND status = 'pending' ORDER BY id", rowID)
	if !slices.Equal(fields, []string{"create", "parent", "blockers"}) {
		t.Fatalf("pending fields = %v", fields)
	}

	fake.RefuseIssueCreates(0)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE id = ? AND number = 42 AND json_extract(metadata, '$.github.id') > 0", rowID) == 1 &&
			countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE issue = ? AND status = 'pending'", rowID) == 0 &&
			countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE issue = ? AND status = 'sent'", rowID) == 3
	})
	if !fake.HasIssue(shop, 42) || fake.HasIssue(shop, 43) {
		t.Fatalf("has #42=%t, #43=%t", fake.HasIssue(shop, 42), fake.HasIssue(shop, 43))
	}
	if title, body := fake.Issue(shop, 42); title != "Add plan model" {
		t.Errorf("title = %q", title)
	} else if got, ok := withoutWriteMarkerText(body); !ok || got != "Plans have a price.\n\n"+chatLink+"\n\n"+leadLinkMark {
		t.Errorf("body = %q", body)
	}
	if got := fake.SubIssueNumbers(shop, 41); !reflect.DeepEqual(got, []int64{42}) {
		t.Errorf("sub-issues of #41 = %v", got)
	}
	if got := fake.BlockerNumbers(shop, 42); !reflect.DeepEqual(got, []int64{40}) {
		t.Errorf("blockers of #42 = %v", got)
	}
	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := eventsOfKind(t, server, "issue_created", 12)
		return events, len(events) == 1 && events[0].Delivered
	})
	if !strings.Contains(events[0].Payload, `Mobius created owner/shop#42 "Add plan model" of your earlier create_issue call.`) {
		t.Errorf("event = %+v", events[0])
	}
}

func TestCreateIssueFindsTheIssueByMarkerAfter500AndGivesOneIssueCreatedEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	script := "[[prompts]]\nwhen = \"Plan the work.\"\ncall = { tool = \"create_issue\", arguments = { repository = \"owner/shop\", title = \"Add plan model\", body = \"Plans have a price.\", parent = \"owner/shop#12\", blocked_by = [] } }\n\n" + seenScript
	server, _ := connectWith(t, fake, script, withUIURL)
	fake.LoseIssueCreateAnswers(1)

	session := run(t, server, leadSpec(t), "Plan the work.")

	if got, want := reply(t, server, session), "Mobius creates the issue \"Add plan model\" on GitHub later. A Lead event gives its number."; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
	if !fake.HasIssue(shop, 13) {
		t.Fatal("GitHub made no issue")
	}
	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := eventsOfKind(t, server, "issue_created", 12)
		return events, copiedRows(t, server, 13) == 1 && len(events) == 1 && events[0].Delivered
	})
	waitForPolls(t, fake)
	if fake.HasIssue(shop, 14) {
		t.Error("GitHub has two issues")
	}
	if got := countCreates(fake.Requests()); got != 1 {
		t.Errorf("creates = %d", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); !reflect.DeepEqual(got, []int64{13}) {
		t.Errorf("sub-issues = %v", got)
	}
	if !strings.Contains(events[0].Payload, `Mobius created owner/shop#13 "Add plan model" of your earlier create_issue call.`) {
		t.Errorf("event = %+v", events[0])
	}
	if got := eventsOfKind(t, server, "issue_created", 12); len(got) != 1 {
		t.Errorf("issue_created events = %+v", got)
	}
}

func TestCreateIssueDeletesTheRowAndAddsOneRefusedEventOnPermanentRefusal(t *testing.T) {
	t.Parallel()
	for _, immediate := range []bool{true, false} {
		name := "after a temporary failure"
		if immediate {
			name = "immediate"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			fake.AddIssue(shop, 40, "Plan schema")
			script := "[[prompts]]\nwhen = \"Plan the work.\"\ncall = { tool = \"create_issue\", arguments = { repository = \"owner/shop\", title = \"Add plan model\", body = \"\", parent = \"owner/shop#12\", blocked_by = [\"owner/shop#40\"] } }\n\n" + seenScript
			server, _ := connect(t, fake, script)
			if immediate {
				fake.RefuseIssueCreates(http.StatusUnprocessableEntity)
			} else {
				fake.RefuseIssueCreates(http.StatusInternalServerError)
			}

			session := run(t, server, leadSpec(t), "Plan the work.")

			want := "Mobius creates the issue \"Add plan model\" on GitHub later. A Lead event gives its number."
			if immediate {
				want = "error: GitHub refused the write."
			}
			if got := reply(t, server, session); got != want {
				t.Fatalf("reply = %q, want %q", got, want)
			}
			if !immediate {
				fake.RefuseIssueCreates(http.StatusUnprocessableEntity)
			}
			events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
				events := eventsOfKind(t, server, "issue_create_refused", 12)
				return events, len(events) == 1 && events[0].Delivered
			})
			waitForPolls(t, fake)
			if got := countRows(t, server, "SELECT count(*) FROM issues WHERE title = 'Add plan model'"); got != 0 {
				t.Errorf("issues rows = %d", got)
			}
			if got := countRows(t, server, "SELECT count(*) FROM blockers"); got != 0 {
				t.Errorf("blockers rows = %d", got)
			}
			if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field IN ('create', 'parent', 'blockers') AND status = 'refused'"); got != 3 {
				t.Errorf("refused changes = %d", got)
			}
			if !strings.Contains(events[0].Payload, `GitHub refused the create of the issue "Add plan model": `) || !strings.Contains(events[0].Payload, "Create it again with create_issue.") {
				t.Errorf("event = %+v", events[0])
			}
			if got := eventsOfKind(t, server, "issue_create_refused", 12); len(got) != 1 {
				t.Errorf("events = %+v", got)
			}
		})
	}
}

func TestCreateIssueStoresDelayedImplementerSettingInIssueBindingsAfterSend(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	script := "[[prompts]]\nwhen = \"Plan the work.\"\ncall = { tool = \"create_issue\", arguments = { repository = \"owner/shop\", title = \"Add plan model\", body = \"Plans have a price.\", parent = \"owner/shop#12\", blocked_by = [], implementer = { harness = \"devin\", model = \"opus\", effort = \"low\" } } }\n\n" + seenScript
	server, _ := connect(t, fake, script)
	fake.RefuseIssueCreates(http.StatusInternalServerError)

	session := run(t, server, leadSpec(t), "Plan the work.")

	if got, want := reply(t, server, session), "Mobius creates the issue \"Add plan model\" on GitHub later. A Lead event gives its number."; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
	if got := countRows(t, server, "SELECT count(*) FROM issue_bindings"); got != 0 {
		t.Fatalf("issue_bindings before send = %d", got)
	}

	fake.RefuseIssueCreates(0)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issue_bindings WHERE repository = ? AND issue = 13 AND role = 'implementer' AND harness = 'devin' AND model = 'opus' AND effort = 'low'", shop) == 1
	})
	if got := copiedRows(t, server, 13); got != 1 {
		t.Errorf("rows of #13 = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 13 AND json_extract(metadata, '$.mobius') IS NULL", shop); got != 1 {
		t.Errorf("rows with empty mobius metadata = %d", got)
	}
}
