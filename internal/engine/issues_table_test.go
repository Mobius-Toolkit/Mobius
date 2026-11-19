package engine_test

import (
	"database/sql"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// issueRow is a row of the table issues, with the number of its Workstream and of its parent.
type issueRow struct {
	ID         int64
	Repository string
	Number     int64
	Title      string
	Body       string
	State      string
	Author     string
	Workstream sql.NullInt64
	Parent     sql.NullInt64
	Kind       string
	GitHubID   int64
	Labels     string
}

func issueRows(t *testing.T, server *testserver.Server) []issueRow {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, i *issueRow) error {
		return rows.Scan(&i.ID, &i.Repository, &i.Number, &i.Title, &i.Body, &i.State, &i.Author, &i.Workstream, &i.Parent, &i.Kind, &i.GitHubID, &i.Labels)
	}, `SELECT i.id, i.repository, i.number, i.title, i.body, i.state, i.author, w.number, p.number, i.kind,
		json_extract(i.metadata, '$.github.id'), json_extract(i.metadata, '$.github.labels')
		FROM issues i
		LEFT JOIN workstreams w ON w.id = i.workstream
		LEFT JOIN issues p ON p.id = i.parent
		ORDER BY i.number`)
}

func issueRowOf(t *testing.T, server *testserver.Server, repository string, number int64) issueRow {
	t.Helper()
	for _, row := range issueRows(t, server) {
		if row.Repository == repository && row.Number == number {
			return row
		}
	}
	t.Fatalf("no row of %s#%d in issues", repository, number)
	return issueRow{}
}

func nullNumber(number int64) sql.NullInt64 { return sql.NullInt64{Int64: number, Valid: true} }

func TestTheFirstPollWritesEachIssueOfATreeInTheTableIssuesWithItsBlockers(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.SetBody(shop, 41, "Add the table.\n\n[Open the Lead chat in Mobius](https://mobius.example.com/workstreams/owner/shop/12)")
	fake.AddLabel(shop, 41, "mobius:question", "owner")
	fake.AddLabel(shop, 41, "bug", "owner")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddIssue(shop, 42, "Let customers change plans")
	fake.CloseIssue(shop, 42)
	fake.AddSubIssue(shop, 12, 42)
	fake.AddIssue(shop, 43, "Mine the servers")
	fake.SetAuthor(shop, 43, "mallory")
	fake.AddSubIssue(shop, 12, 43)
	fake.AddIssue(shop, 44, "Add the invoice model")
	fake.AddSubIssue(shop, 12, 44)
	fake.AddBlockedBy(shop, 44, 41)
	fake.AddBlockedBy(shop, 44, 42)
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options)
	cfg := testserver.Config(t, dataDir)
	withUIURL(cfg)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)

	rows := issueRows(t, server)

	want := []struct {
		number int64
		title  string
		body   string
		state  string
		author string
		parent sql.NullInt64
		labels string
	}{
		{41, "Add plan model", "Add the table.", "open", "owner", sql.NullInt64{}, `["bug","mobius:question"]`},
		{42, "Let customers change plans", "", "closed", "owner", sql.NullInt64{}, `[]`},
		{43, "Mine the servers", "", "open", "mallory", sql.NullInt64{}, `[]`},
		{44, "Add the invoice model", "", "open", "owner", sql.NullInt64{}, `[]`},
		{50, "Store the price in cents", "", "open", "owner", nullNumber(41), `[]`},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	ids := map[int64]bool{}
	for i, row := range rows {
		w := want[i]
		if row.Repository != shop || row.Number != w.number || row.Title != w.title || row.Body != w.body || row.State != w.state || row.Author != w.author ||
			row.Workstream != nullNumber(12) || row.Parent != w.parent || row.Kind != "task" || row.Labels != w.labels {
			t.Errorf("row %d = %+v, want %+v", i, row, w)
		}
		if row.GitHubID == 0 || ids[row.GitHubID] {
			t.Errorf("GitHub id of #%d = %d", row.Number, row.GitHubID)
		}
		ids[row.GitHubID] = true
	}
	blockers := query(t, server, func(rows *sql.Rows, text *string) error { return rows.Scan(text) },
		`SELECT i.number || ' blocked by ' || bi.number || ' ' || bi.state FROM blockers b
		JOIN issues i ON i.id = b.issue JOIN issues bi ON bi.id = b.blocker ORDER BY i.number, bi.number`)
	if !slices.Equal(blockers, []string{"44 blocked by 41 open", "44 blocked by 42 closed"}) {
		t.Errorf("blockers = %v", blockers)
	}
}

func TestAnIssueOfAnotherRepositoryBelowAWorkstreamHasOneRowWithTheWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue("other/repo", 77, "A task in another repository")
	fake.AddForeignSubIssue(shop, 12, "other/repo", 77)
	fake.AddIssue(shop, 77, "A different issue")

	server := startCopied(t, fake)

	rows := issueRows(t, server)
	if len(rows) != 1 || rows[0].Repository != "other/repo" || rows[0].Number != 77 || rows[0].Workstream != nullNumber(12) || rows[0].Parent.Valid {
		t.Errorf("rows = %+v", rows)
	}
}

func TestAFullCopyAfterARestartKeepsTheIdOfEachRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	first := startServer(t, fake, dataDir, "")
	first.WaitForFirstPoll(t, shop)
	before := issueRows(t, first)
	if len(before) != 2 {
		t.Fatalf("rows = %+v", before)
	}
	first.Stop()
	fake.AddSubIssueOf(shop, 12, 42, "Let customers change plans")
	fake.SetTitleBy(shop, 41, "Add the plan model", "owner")

	restarted := testserver.Start(t, dataDir, fake.URL)

	testkit.WaitFor(t, func() bool { return slices.Equal(issueNumbers(t, restarted), []int64{41, 50, 42}) })
	for _, row := range before {
		if got := issueRowOf(t, restarted, row.Repository, row.Number); got.ID != row.ID || got.Parent != row.Parent {
			t.Errorf("row of #%d = %+v, want the id %d", row.Number, got, row.ID)
		}
	}
	if got := copiedTitle(t, restarted, 41); got != "Add the plan model" {
		t.Errorf("title of #41 = %q", got)
	}
}

func TestTheIssuesBelowANestedWorkstreamHaveItAndTheIssuesBelowABucketHaveNoParent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddSubIssue(shop, 12, 20)
	fake.AddIssue(shop, 60, "Invoice totals")
	fake.AddSubIssue(shop, 20, 60)
	fake.AddIssue(shop, 30, "Bucket 1 of #12")
	fake.AddLabel(shop, 30, "mobius:bucket", "owner")
	fake.AddSubIssue(shop, 12, 30)
	fake.AddIssue(shop, 70, "Task in the bucket")
	fake.AddSubIssue(shop, 30, 70)
	fake.AddIssue(shop, 71, "Task below the task in the bucket")
	fake.AddSubIssue(shop, 70, 71)

	server := startCopied(t, fake)

	got := map[int64]issueRow{}
	for _, row := range issueRows(t, server) {
		got[row.Number] = row
	}
	if len(got) != 4 {
		t.Fatalf("rows = %+v", got)
	}
	for number, want := range map[int64]struct {
		workstream int64
		parent     sql.NullInt64
	}{41: {12, sql.NullInt64{}}, 60: {20, sql.NullInt64{}}, 70: {12, sql.NullInt64{}}, 71: {12, nullNumber(70)}} {
		if row := got[number]; row.Workstream != nullNumber(want.workstream) || row.Parent != want.parent {
			t.Errorf("row of #%d = %+v, want the Workstream #%d and the parent %v", number, row, want.workstream, want.parent)
		}
	}
	if got[70].Parent.Valid || !reflect.DeepEqual(workstreamNumbers(t, server), []int64{12, 20}) {
		t.Errorf("workstreams = %v", workstreamNumbers(t, server))
	}
}

func TestAnIssueWithTheLabelProjectHasTheKindProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Loyalty project")
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 42, "Add plan model")
	fake.AddSubIssue(shop, 12, 42)

	server := startCopied(t, fake)

	if got := issueRowOf(t, server, shop, 41).Kind; got != "project" {
		t.Errorf("kind of #41 = %s", got)
	}
	if got := issueRowOf(t, server, shop, 42).Kind; got != "task" {
		t.Errorf("kind of #42 = %s", got)
	}
	fake.RemoveLabel(shop, 41, "mobius:project", "owner")
	testkit.WaitFor(t, func() bool { return issueRowOf(t, server, shop, 41).Kind == "task" })
}

func TestANestedWorkstreamCountsAsADirectSubIssueOfTheAllTasksClosedFlag(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.CloseIssue(shop, 41)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddSubIssue(shop, 12, 20)
	server := startCopied(t, fake)

	if workstreams(t, server)[0].AllTasksClosed {
		t.Fatal("all tasks closed with an open nested Workstream")
	}

	fake.CloseIssue(shop, 20)

	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })
}

func TestAWorkstreamWithOnlyANestedWorkstreamIsNotReportedAsFinished(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddSubIssue(shop, 12, 20)
	server := startCopied(t, fake)

	if _, err := server.DB.Exec("UPDATE workstreams SET finished_at = '2026-10-04T10:00:00Z' WHERE repository = ? AND number = 12", shop); err != nil {
		t.Fatal(err)
	}

	for _, w := range workstreams(t, server) {
		if w.Number == 12 && w.ReportedFinished {
			t.Error("the Workstream #12 is reported as finished")
		}
	}
}

func TestANestedWorkstreamCountsAsADirectSubIssueForTheBuckets(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 148, 0)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddSubIssue(shop, 12, 20)
	startCopied(t, fake)
	waitForPolls(t, fake)
	if got := len(fake.SubIssueNumbers(shop, 12)); got != 50 {
		t.Fatalf("sub-issues = %d", got)
	}

	fake.AddIssue(shop, 21, "Invoices")
	fake.AddLabel(shop, 21, "mobius:workstream", "owner")
	fake.AddSubIssue(shop, 12, 21)

	testkit.WaitFor(t, func() bool {
		for _, request := range fake.Requests() {
			if request.Method == "POST" && strings.HasSuffix(request.Path, "/issues") {
				return true
			}
		}
		return false
	})
}

func TestTheTitleOfTheWorkstreamOfABlockerStaysAfterTheCloseOfThatWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithBlocker(t, fake)

	fake.CloseIssue(shop, 13)

	testkit.WaitFor(t, func() bool { return !slices.Contains(workstreamNumbers(t, server), 13) })
	billing := "Billing"
	want := []taskLine{line(42, "Let customers change plans", "open", 0)}
	want[0].BlockedBy = []taskBlocker{{Number: 60, WorkstreamTitle: &billing}}
	if got := apiData[[]taskLine](t, server, "/api/workstreams/owner/shop/12/tasks"); !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %+v", got)
	}
}
