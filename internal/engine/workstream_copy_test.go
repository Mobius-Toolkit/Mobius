package engine_test

import (
	"database/sql"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type copiedWorkstream struct {
	Number    int64
	Title     string
	Body      string
	Autopilot bool
}

type copiedIssue struct {
	Workstream int64
	Number     int64
	Parent     int64
	State      string
	Author     string
}

// copiedBlocker is a blocker row: the blocked issue, the blocker, and the Workstream of the blocker with its title.
type copiedBlocker struct {
	Issue      int64
	Number     int64
	Workstream sql.NullInt64
	Title      sql.NullString
}

func query[T any](t *testing.T, server *testserver.Server, scan func(*sql.Rows, *T) error, statement string, args ...any) []T {
	t.Helper()
	rows, err := server.DB.Query(statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	found := []T{}
	for rows.Next() {
		var row T
		if err := scan(rows, &row); err != nil {
			t.Fatal(err)
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

func copiedWorkstreams(t *testing.T, server *testserver.Server) []copiedWorkstream {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, w *copiedWorkstream) error {
		return rows.Scan(&w.Number, &w.Title, &w.Body, &w.Autopilot)
	}, "SELECT number, title, body, autopilot FROM copied_workstreams WHERE repository = ? ORDER BY number", shop)
}

func workstreamNumbers(t *testing.T, server *testserver.Server) []int64 {
	t.Helper()
	numbers := []int64{}
	for _, workstream := range copiedWorkstreams(t, server) {
		numbers = append(numbers, workstream.Number)
	}
	return numbers
}

// copiedIssues gives the copied issues of owner/shop in the walk order of each Workstream.
func copiedIssues(t *testing.T, server *testserver.Server) []copiedIssue {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, i *copiedIssue) error {
		return rows.Scan(&i.Workstream, &i.Number, &i.Parent, &i.State, &i.Author)
	}, "SELECT workstream, number, parent, state, author FROM copied_issues WHERE repository = ? ORDER BY workstream, position", shop)
}

func issueNumbers(t *testing.T, server *testserver.Server) []int64 {
	t.Helper()
	numbers := []int64{}
	for _, issue := range copiedIssues(t, server) {
		numbers = append(numbers, issue.Number)
	}
	return numbers
}

func issueStates(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	states := []string{}
	for _, issue := range copiedIssues(t, server) {
		states = append(states, issue.State)
	}
	return states
}

func copiedLabels(t *testing.T, server *testserver.Server, number int64) []string {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, name *string) error { return rows.Scan(name) }, `SELECT l.name FROM copied_issue_labels l
		JOIN copied_issues i USING (repository, workstream, position)
		WHERE i.repository = ? AND i.number = ? ORDER BY l.name`, shop, number)
}

func copiedBlockers(t *testing.T, server *testserver.Server) []copiedBlocker {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, b *copiedBlocker) error {
		return rows.Scan(&b.Issue, &b.Number, &b.Workstream, &b.Title)
	}, `SELECT i.number, b.number, b.blocker_workstream, b.blocker_workstream_title FROM copied_blockers b
		JOIN copied_issues i USING (repository, workstream, position)
		WHERE b.repository = ? ORDER BY i.number, b.number`, shop)
}

func copiedTitle(t *testing.T, server *testserver.Server, number int64) string {
	t.Helper()
	var title string
	if err := server.DB.QueryRow("SELECT title FROM copied_issues WHERE repository = ? AND number = ?", shop, number).Scan(&title); err != nil {
		t.Fatal(err)
	}
	return title
}

// blocker gives the blocker row of the blocker number of the issue in the Workstream with title.
func blocker(issue, number, workstream int64, title string) copiedBlocker {
	return copiedBlocker{issue, number, sql.NullInt64{Int64: workstream, Valid: true}, sql.NullString{String: title, Valid: true}}
}

// startCopied starts a server with the issues of fake, and waits for the first poll, which copies the Workstreams first.
func startCopied(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	server := startServer(t, fake, t.TempDir(), "")
	server.WaitForFirstPoll(t, shop)
	return server
}

// startWithTasks starts a server with the Workstream #12, its task #41, and the task #50 of #41.
func startWithTasks(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	return startCopied(t, fake)
}

// waitForPoll waits for the end of the poll that read the last write of fake. The last write must change an issue of owner/shop.
func waitForPoll(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) {
	t.Helper()
	last := fake.Now()
	testkit.WaitFor(t, func() bool {
		since, _ := cursor(t, server)
		polled, err := time.Parse(time.RFC3339, since.String)
		return err == nil && !polled.Before(last)
	})
}

// listen gives each change of the engine from now on.
func listen(t *testing.T, server *testserver.Server) <-chan engine.Change {
	t.Helper()
	changes, stop := server.Engine.Listen()
	t.Cleanup(stop)
	return changes
}

// waitForWorkstreams waits for a change of the Workstream list. The test fails after one minute.
func waitForWorkstreams(t *testing.T, changes <-chan engine.Change) {
	t.Helper()
	deadline := time.After(time.Minute)
	for {
		select {
		case change, ok := <-changes:
			if !ok {
				t.Fatal("the engine closed the listener")
			}
			if change.Workstreams {
				return
			}
		case <-deadline:
			t.Fatal("no change of the Workstream list after one minute")
		}
	}
}

// noWorkstreams fails the test when changes has a change of the Workstream list.
func noWorkstreams(t *testing.T, changes <-chan engine.Change) {
	t.Helper()
	for {
		select {
		case change, ok := <-changes:
			if !ok {
				t.Fatal("the engine closed the listener")
			}
			if change.Workstreams {
				t.Fatal("a change of the Workstream list")
			}
		default:
			return
		}
	}
}

func TestTheFullSyncCopiesTheOpenWorkstreams(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.SetBody(shop, 12, "Brief of the plans")
	fake.AddIssue(shop, 13, "Fix the footer")
	fake.AddIssue(shop, 14, "Price rounding")
	fake.AddLabel(shop, 14, "mobius:workstream", "owner")
	fake.AddIssue(shop, 15, "Old Workstream")
	fake.AddLabel(shop, 15, "mobius:workstream", "owner")
	fake.CloseIssue(shop, 15)

	server := startCopied(t, fake)

	want := []copiedWorkstream{{12, "Integrate loyalty plans", "Brief of the plans", false}, {14, "Price rounding", "", false}}
	if got := copiedWorkstreams(t, server); !reflect.DeepEqual(got, want) {
		t.Errorf("workstreams = %+v", got)
	}
}

func TestTheFullSyncCopiesAutopilotOnlyFromATrustedUser(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	for number, actor := range map[int64]string{12: "owner", 13: "mallory"} {
		fake.AddIssue(shop, number, "Workstream")
		fake.AddLabel(shop, number, "mobius:workstream", "owner")
		fake.AddLabel(shop, number, "mobius:autopilot", actor)
	}
	fake.AddIssue(shop, 14, "Workstream")
	fake.AddLabel(shop, 14, "mobius:workstream", "owner")

	server := startCopied(t, fake)

	var autopilot []bool
	for _, workstream := range copiedWorkstreams(t, server) {
		autopilot = append(autopilot, workstream.Autopilot)
	}
	if !slices.Equal(autopilot, []bool{true, false, false}) {
		t.Errorf("autopilot of #12, #13 and #14 = %v", autopilot)
	}
}

func TestTheFullSyncCopiesTheNestedSubIssuesWithStateLabelsAndAuthor(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
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
	fake.AddIssue(shop, 51, "Task below the untrusted issue")
	fake.AddSubIssue(shop, 43, 51)

	server := startCopied(t, fake)

	want := []copiedIssue{
		{12, 41, 12, "open", "owner"},
		{12, 50, 41, "open", "owner"},
		{12, 42, 12, "closed", "owner"},
		{12, 43, 12, "open", "mallory"},
		{12, 51, 43, "open", "owner"},
	}
	if got := copiedIssues(t, server); !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %+v", got)
	}
	if got := copiedLabels(t, server, 41); !slices.Equal(got, []string{"bug", "mobius:ready"}) {
		t.Errorf("labels of #41 = %v", got)
	}
	if got := copiedLabels(t, server, 50); len(got) != 0 {
		t.Errorf("labels of #50 = %v", got)
	}
}

func TestTheFullSyncCopiesTheOpenBlockersWithTheWorkstreamOfEachBlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 13, "Billing")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 42, "Let customers change plans")
	fake.AddSubIssue(shop, 12, 42)
	fake.AddIssue(shop, 43, "A closed blocker")
	fake.CloseIssue(shop, 43)
	fake.AddSubIssue(shop, 12, 43)
	fake.AddIssue(shop, 60, "Invoice model")
	fake.AddSubIssue(shop, 13, 60)
	fake.AddIssue(shop, 61, "An issue of no Workstream")
	fake.AddIssue("other/repo", 70, "A blocker in another repository")
	fake.AddBlockedBy(shop, 41, 42)
	fake.AddBlockedBy(shop, 41, 60)
	fake.AddBlockedBy(shop, 41, 61)
	fake.AddBlockedBy(shop, 41, 43)
	fake.AddBlockedBy(shop, 42, 60)

	server := startCopied(t, fake)

	want := []copiedBlocker{
		blocker(41, 42, 12, "Integrate loyalty plans"),
		blocker(41, 60, 13, "Billing"),
		{Issue: 41, Number: 61},
		blocker(42, 60, 13, "Billing"),
	}
	if got := copiedBlockers(t, server); !reflect.DeepEqual(got, want) {
		t.Errorf("blockers = %+v", got)
	}
}

func TestTheFullSyncCopiesASubIssueOfAnotherRepositoryAsALeaf(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue("other/repo", 70, "A blocker in another repository")
	fake.AddIssue("other/repo", 77, "A task in another repository")
	fake.AddBlockedBy("other/repo", 77, 70)
	fake.AddIssue("other/repo", 78, "A child in another repository")
	fake.AddForeignSubIssue(shop, 12, "other/repo", 77)
	fake.AddForeignSubIssue("other/repo", 77, "other/repo", 78)
	// The different issue #77 of this repository has a child and a blocker.
	fake.AddIssue(shop, 77, "A different issue")
	fake.AddIssue(shop, 90, "Child of the different issue")
	fake.AddSubIssue(shop, 77, 90)
	fake.AddBlockedBy(shop, 77, 90)

	server := startCopied(t, fake)

	if got := copiedIssues(t, server); !reflect.DeepEqual(got, []copiedIssue{{12, 77, 12, "open", "owner"}}) {
		t.Errorf("issues = %+v", got)
	}
	var url string
	if err := server.DB.QueryRow("SELECT repository_url FROM copied_issues WHERE repository = ? AND number = 77", shop).Scan(&url); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(url, "/repos/other/repo") {
		t.Errorf("repository URL = %s", url)
	}
	if got := copiedBlockers(t, server); len(got) != 0 {
		t.Errorf("blockers = %+v", got)
	}
}

func TestTheFullSyncForgetsTheRowsOfARepositoryThatIsNotPolled(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")

	server := startServer(t, fake, t.TempDir(), `INSERT INTO copied_workstreams (repository, number, title, body, autopilot) VALUES ('owner/gone', 5, 'An old Workstream', '', 0)`)
	server.WaitForFirstPoll(t, shop)

	var stale int
	if err := server.DB.QueryRow("SELECT count(*) FROM copied_workstreams WHERE repository = 'owner/gone'").Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Errorf("rows of owner/gone = %d", stale)
	}
	if got := workstreamNumbers(t, server); !slices.Equal(got, []int64{12}) {
		t.Errorf("workstreams = %v", got)
	}
}

func TestANewIssueUnderAWorkstreamIsCopiedInTheWalkOrder(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)

	fake.AddSubIssueOf(shop, 12, 42, "Let customers change plans")

	testkit.WaitFor(t, func() bool { return slices.Equal(issueNumbers(t, server), []int64{41, 50, 42}) })
}

func TestANewIssueUnderANestedTaskIsCopiedBelowItsParent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	fake.AddSubIssueOf(shop, 12, 42, "Let customers change plans")
	testkit.WaitFor(t, func() bool { return slices.Equal(issueNumbers(t, server), []int64{41, 50, 42}) })

	fake.AddSubIssueOf(shop, 41, 51, "Round the price")

	testkit.WaitFor(t, func() bool { return slices.Equal(issueNumbers(t, server), []int64{41, 50, 51, 42}) })
	var parents []int64
	for _, issue := range copiedIssues(t, server) {
		parents = append(parents, issue.Parent)
	}
	if !slices.Equal(parents, []int64{12, 41, 41, 12}) {
		t.Errorf("parents = %v", parents)
	}
}

func TestANewLabelAndARemovedLabelChangeTheLabelsOfTheCopiedIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)

	fake.AddLabel(shop, 41, "bug", "owner")
	fake.AddLabel(shop, 41, "mobius:working", "owner")

	testkit.WaitFor(t, func() bool { return slices.Equal(copiedLabels(t, server, 41), []string{"bug", "mobius:working"}) })

	fake.RemoveLabel(shop, 41, "bug", "owner")

	testkit.WaitFor(t, func() bool { return slices.Equal(copiedLabels(t, server, 41), []string{"mobius:working"}) })
}

func TestAClosedIssueHasTheStateClosedInTheCopy(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)

	fake.CloseIssue(shop, 41)

	testkit.WaitFor(t, func() bool { return slices.Equal(issueStates(t, server), []string{"closed", "open"}) })
}

func TestANewTitleAndANewBodyChangeTheCopiedIssueAndTheCopiedWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)

	fake.SetTitle(shop, 41, "Add the plan table")
	fake.SetTitle(shop, 12, "Integrate loyalty tiers")
	fake.SetBody(shop, 12, "Brief of the tiers")

	testkit.WaitFor(t, func() bool { return copiedTitle(t, server, 41) == "Add the plan table" })
	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(copiedWorkstreams(t, server), []copiedWorkstream{{12, "Integrate loyalty tiers", "Brief of the tiers", false}})
	})
}

func TestANewWorkstreamIsCopiedWithItsTree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	fake.AddIssue(shop, 13, "Billing")
	fake.AddSubIssueOf(shop, 13, 60, "Invoice model")

	fake.AddLabel(shop, 13, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool { return slices.Equal(workstreamNumbers(t, server), []int64{12, 13}) })
	if got := copiedIssues(t, server)[2]; got != (copiedIssue{13, 60, 13, "open", "owner"}) {
		t.Errorf("issue = %+v", got)
	}
}

func TestAClosedWorkstreamIsRemovedWithItsTree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)

	fake.CloseIssue(shop, 12)

	testkit.WaitFor(t, func() bool { return len(workstreamNumbers(t, server)) == 0 })
	if got := issueNumbers(t, server); len(got) != 0 {
		t.Errorf("issues = %v", got)
	}
	if got := copiedLabels(t, server, 41); len(got) != 0 {
		t.Errorf("labels = %v", got)
	}
}

func TestAWorkstreamThatLosesTheLabelIsRemovedWithItsTree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	fake.AddLabel(shop, 41, "bug", "owner")
	testkit.WaitFor(t, func() bool { return slices.Equal(copiedLabels(t, server, 41), []string{"bug"}) })

	fake.RemoveLabel(shop, 12, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool { return len(workstreamNumbers(t, server)) == 0 })
	if got := issueNumbers(t, server); len(got) != 0 {
		t.Errorf("issues = %v", got)
	}
	if got := copiedLabels(t, server, 41); len(got) != 0 {
		t.Errorf("labels = %v", got)
	}
}

func TestAutopilotIsOnAfterATrustedUserAddsTheLabelAndOffAfterTheRemoval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	autopilot := func() bool { return copiedWorkstreams(t, server)[0].Autopilot }

	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")

	testkit.WaitFor(t, autopilot)

	fake.RemoveLabel(shop, 12, "mobius:autopilot", "owner")

	testkit.WaitFor(t, func() bool { return !autopilot() })
}

func TestAutopilotStaysOffAfterAnUntrustedUserAddsTheLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)

	fake.AddLabel(shop, 12, "mobius:autopilot", "mallory")

	waitForPoll(t, server, fake)
	if copiedWorkstreams(t, server)[0].Autopilot {
		t.Error("autopilot is on")
	}
}

func TestAChangeOfALocalIssueDoesNotChangeTheForeignRowWithTheSameNumber(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue("other/repo", 77, "A task in another repository")
	fake.AddForeignSubIssue(shop, 12, "other/repo", 77)
	fake.AddIssue(shop, 77, "A different issue")
	server := startCopied(t, fake)

	fake.SetTitle(shop, 77, "A renamed issue")

	waitForPoll(t, server, fake)
	if got := copiedTitle(t, server, 77); got != "A task in another repository" {
		t.Errorf("title = %q", got)
	}
}

func TestATaskThatGetsTheWorkstreamLabelBecomesALeafOfItsWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)

	fake.AddLabel(shop, 41, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool { return slices.Equal(workstreamNumbers(t, server), []int64{12, 41}) })
	testkit.WaitFor(t, func() bool { return slices.Equal(issueNumbers(t, server), []int64{41, 50}) })
	if got := copiedIssues(t, server); got[0].Workstream != 12 || got[1].Workstream != 41 {
		t.Errorf("issues = %+v", got)
	}
}

func TestATaskThatLosesTheWorkstreamLabelGetsItsTreeBackInItsWorkstream(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddLabel(shop, 41, "mobius:workstream", "owner")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	server := startCopied(t, fake)
	if got := workstreamNumbers(t, server); !slices.Equal(got, []int64{12, 41}) {
		t.Fatalf("workstreams = %v", got)
	}

	fake.RemoveLabel(shop, 41, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool { return slices.Equal(workstreamNumbers(t, server), []int64{12}) })
	testkit.WaitFor(t, func() bool { return slices.Equal(issueNumbers(t, server), []int64{41, 50}) })
	want := []copiedIssue{{12, 41, 12, "open", "owner"}, {12, 50, 41, "open", "owner"}}
	if got := copiedIssues(t, server); !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %+v", got)
	}
}

func TestAClosedIssueOfAnotherRepositoryHasTheStateClosedInTheCopy(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository("other/repo")
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue("other/repo", 77, "A task in another repository")
	fake.AddForeignSubIssue(shop, 12, "other/repo", 77)
	server := startCopied(t, fake)
	server.WaitForFirstPoll(t, "other/repo")

	fake.CloseIssue("other/repo", 77)

	testkit.WaitFor(t, func() bool { return slices.Equal(issueStates(t, server), []string{"closed"}) })
}

func TestAPollWithNoChangeInTheCopySendsNoWorkstreamsChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	changes := listen(t, server)

	fake.AddIssue(shop, 13, "Fix the footer")
	fake.AddComment(shop, 50, "owner", "A comment changes no copied value.")

	waitForPoll(t, server, fake)
	noWorkstreams(t, changes)
}

func TestAChangeInTheCopySendsAWorkstreamsChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	changes := listen(t, server)

	fake.SetTitle(shop, 41, "Add the plan table")

	waitForWorkstreams(t, changes)
	if got := copiedTitle(t, server, 41); got != "Add the plan table" {
		t.Errorf("title = %q", got)
	}
}

func TestAFailedUpdateOfTheCopyDoesNotRepeatTheEventsOfThePoll(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	fake.AddIssue(shop, 13, "Billing")
	fake.FailSubIssues(shop, 12, true)

	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddSubIssueOf(shop, 12, 42, "Let customers change plans")

	waitForPoll(t, server, fake)
	count := 0
	for _, activity := range activities(t, server) {
		if activity.Text == `New Workstream "Billing"` {
			count++
		}
	}
	if count != 1 {
		t.Errorf("activities of the new Workstream = %d", count)
	}
}

// After a failed update, the next polls copy the repository again until the copy works.
func TestARepositoryWithAFailedUpdateGetsAFullCopyThatSendsAWorkstreamsChange(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	fake.FailSubIssues(shop, 12, true)
	fake.AddSubIssueOf(shop, 12, 42, "Let customers change plans")
	waitForPoll(t, server, fake)
	changes := listen(t, server)

	fake.FailSubIssues(shop, 12, false)

	waitForWorkstreams(t, changes)
	if got := issueNumbers(t, server); !slices.Equal(got, []int64{41, 50, 42}) {
		t.Errorf("issues = %v", got)
	}
}

func TestAClosedBlockerIsRemovedFromTheCopy(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	server := startWithBlocker(t, fake)
	if got := copiedBlockers(t, server); len(got) != 1 {
		t.Fatalf("blockers = %+v", got)
	}

	fake.CloseIssue(shop, 60)

	testkit.WaitFor(t, func() bool { return len(copiedBlockers(t, server)) == 0 })
}

func TestANewTitleOfAWorkstreamChangesTheTitleInTheBlockersOfTheCopy(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	server := startWithBlocker(t, fake)

	fake.SetTitle(shop, 13, "Invoices")

	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(copiedBlockers(t, server), []copiedBlocker{blocker(42, 60, 13, "Invoices")})
	})
}

// startWithBlocker starts a server where the task #42 of the Workstream #12 has the blocker #60 of the Workstream #13.
func startWithBlocker(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 13, "Billing")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 42, "Let customers change plans")
	fake.AddSubIssue(shop, 12, 42)
	fake.AddIssue(shop, 60, "Invoice model")
	fake.AddSubIssue(shop, 13, 60)
	fake.AddBlockedBy(shop, 42, 60)
	return startCopied(t, fake)
}

// startWithBlockerBelowATask starts a server where the task #60 of the Workstream #20 has the blocker #50, and #50
// is below the task #41 of the Workstream #12.
func startWithBlockerBelowATask(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 60, "Invoice model")
	fake.AddSubIssue(shop, 20, 60)
	fake.AddBlockedBy(shop, 60, 50)
	return startCopied(t, fake)
}

func TestATaskThatGetsTheWorkstreamLabelChangesTheWorkstreamOfABlockerInAnotherTree(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	server := startWithBlockerBelowATask(t, fake)
	if got, want := copiedBlockers(t, server), []copiedBlocker{blocker(60, 50, 12, "Integrate loyalty plans")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blockers = %+v", got)
	}

	fake.AddLabel(shop, 41, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(copiedBlockers(t, server), []copiedBlocker{blocker(60, 50, 41, "Add plan model")})
	})
}

func TestATaskThatLosesTheWorkstreamLabelChangesTheWorkstreamOfABlockerInAnotherTree(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	server := startWithBlockerBelowATask(t, fake)
	fake.AddLabel(shop, 41, "mobius:workstream", "owner")
	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(copiedBlockers(t, server), []copiedBlocker{blocker(60, 50, 41, "Add plan model")})
	})

	fake.RemoveLabel(shop, 41, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(copiedBlockers(t, server), []copiedBlocker{blocker(60, 50, 12, "Integrate loyalty plans")})
	})
}

func TestANewWorkstreamChangesTheWorkstreamOfABlockerInAnotherTree(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 60, "Invoice model")
	fake.AddSubIssue(shop, 20, 60)
	fake.AddBlockedBy(shop, 60, 50)
	server := startCopied(t, fake)
	if got := copiedBlockers(t, server); !reflect.DeepEqual(got, []copiedBlocker{{Issue: 60, Number: 50}}) {
		t.Fatalf("blockers = %+v", got)
	}

	fake.AddLabel(shop, 41, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(copiedBlockers(t, server), []copiedBlocker{blocker(60, 50, 41, "Add plan model")})
	})
}
