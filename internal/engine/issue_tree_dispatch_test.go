package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// blockersOf41 gives the number of the rows in blockers of the issue 41 with a blocker in the state blockerState.
func blockersOf41(t *testing.T, server *testserver.Server, blockerState string) int {
	t.Helper()
	var count int
	err := server.DB.QueryRow(`SELECT count(*) FROM blockers b
		JOIN issues i ON i.id = b.issue
		JOIN issues bi ON bi.id = b.blocker
		WHERE i.number = 41 AND bi.state = ?`, blockerState).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func hasIssueRow(t *testing.T, server *testserver.Server, number int64) bool {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM issues WHERE number = ?", number).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}

func TestAutopilotStartsTheFirstOpenTaskOfTheTreeAndSkipsATaskWithAnOpenBlockerInTheTableBlockers(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	addTaskIssue(fake, 42, "Add plan price")
	addTaskIssue(fake, 43, "Add plan name")
	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddBlockedBy(shop, 41, 88)
	server := startAutopilot(t, fake, func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 }, "")

	liveTaskOf(t, server, 42)
	waitForPolls(t, fake)

	if blockersOf41(t, server, "open") != 1 {
		t.Error("the table blockers has no open blocker of #41")
	}
	if hasLiveTask(t, server, 41) || hasLiveTask(t, server, 43) || activeTasks(t, server) != 1 {
		t.Error("another task started")
	}
}

func TestAutopilotStartsATaskWhoseOnlyBlockerIsClosed(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddBlockedBy(shop, 41, 88)
	fake.CloseIssue(shop, 88)
	server := startAutopilot(t, fake, func(*config.Config) {}, "")

	liveTaskOf(t, server, 41)

	if blockersOf41(t, server, "closed") != 1 {
		t.Error("the table blockers has no closed blocker of #41")
	}
}

func TestAutopilotDoesNotStartAProjectAndStartsATaskBelowIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Loyalty project")
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	fake.AddIssue(shop, 42, "Add plan model")
	fake.AddSubIssue(shop, 41, 42)
	server := startAutopilot(t, fake, func(cfg *config.Config) { cfg.Roles.Implementer.Max = 2 }, "")

	liveTaskOf(t, server, 42)
	waitForPolls(t, fake)

	if got := issueRowOf(t, server, shop, 41).Kind; got != "project" {
		t.Errorf("kind of #41 = %s", got)
	}
	if hasLiveTask(t, server, 41) {
		t.Error("the project started as a task")
	}
}

func TestAnIssueWithMobiusReadyAndAnOpenBlockerInTheTableBlockersWaits(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 88, "Invoice totals")
	dispatchTask(fake, 41, "Plan API")
	fake.AddBlockedBy(shop, 41, 88)
	testkit.WaitFor(t, func() bool { return blockersOf41(t, server, "open") == 1 })
	dispatchTask(fake, 43, "Price rounding")

	liveTaskOf(t, server, 43)

	testkit.WaitFor(t, func() bool {
		return startByOf(t, server, shop, 41) == "owner" && len(fake.Labels(shop, 41)) == 0
	})
	if hasLiveTask(t, server, 41) {
		t.Errorf("the blocked issue started")
	}
}

func TestAnIssueWithMobiusReadyBelowAClosedWorkstreamDoesNotStartAndTheReadyListIsReadOnlyInTheFirstPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 13, "Old Workstream")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.CloseIssue(shop, 13)
	fake.AddIssue(shop, 51, "Old task")
	fake.AddSubIssue(shop, 13, 51)
	fake.AddLabel(shop, 51, "mobius:ready", "owner")
	dispatchTask(fake, 43, "Price rounding")
	liveTaskOf(t, server, 43)

	waitForPolls(t, fake)

	if hasLiveTask(t, server, 51) {
		t.Fatal("the issue below the closed Workstream started")
	}
	if row := issueRowOf(t, server, shop, 51); row.Workstream.Valid {
		t.Fatalf("workstream of #51 = %+v", row.Workstream)
	}
	requireOneReadyListRead(t, fake)
}

func TestANestedWorkstreamIssueWithMobiusReadyDoesNotStartAndTheReadyListIsReadOnlyInTheFirstPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 50, "Nested Workstream")
	fake.AddLabel(shop, 50, "mobius:workstream", "owner")
	fake.AddSubIssue(shop, 12, 50)
	fake.AddLabel(shop, 50, "mobius:ready", "owner")
	dispatchTask(fake, 43, "Price rounding")
	liveTaskOf(t, server, 43)

	waitForPolls(t, fake)

	if hasLiveTask(t, server, 50) {
		t.Fatal("the nested Workstream started as a task")
	}
	requireOneReadyListRead(t, fake)
}

// requireOneReadyListRead fails when the polls listed the issues with mobius:ready other than one time, in the first poll.
func requireOneReadyListRead(t *testing.T, fake *testkit.FakeGitHub) {
	t.Helper()
	reads := 0
	for _, request := range fake.Requests() {
		if request.Method == http.MethodGet && (strings.Contains(request.Query, "labels=mobius:ready") || strings.Contains(request.Query, "labels=mobius%3Aready")) {
			reads++
		}
	}
	if reads != 1 {
		t.Errorf("reads of the list with mobius:ready = %d", reads)
	}
}

func TestAnIssueWithMobiusReadyThatIsAProjectDoesNotStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 41, "Loyalty project")
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	fake.AddSubIssue(shop, 12, 41)
	testkit.WaitFor(t, func() bool { return hasIssueRow(t, server, 41) })
	if got := issueRowOf(t, server, shop, 41).Kind; got != "project" {
		t.Fatalf("kind of #41 = %s", got)
	}
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	dispatchTask(fake, 43, "Price rounding")

	liveTaskOf(t, server, 43)
	testkit.WaitFor(t, func() bool {
		return startByOf(t, server, shop, 41) == "owner" && slices.Equal(fake.Labels(shop, 41), []string{"mobius:project"})
	})
	if hasLiveTask(t, server, 41) {
		t.Errorf("the project started")
	}
}

func TestACloseAsWontDoClosesEachOpenIssueOfTheTreeInTheTableIssues(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	addTaskIssue(fake, 41, "Add plan model")
	fake.AddSubIssueOf(shop, 41, 42, "Store the price in cents")
	addTaskIssue(fake, 43, "Old idea")
	fake.CloseIssue(shop, 43)
	addTaskIssue(fake, 44, "Loyalty project")
	fake.AddLabel(shop, 44, "mobius:project", "owner")
	fake.AddSubIssueOf(shop, 44, 45, "Add plan name")
	fake.AddLabel(shop, 45, "mobius:ready", "owner")
	server := startAutopilot(t, fake, func(*config.Config) {}, "")
	testkit.WaitFor(t, func() bool { return len(issueRows(t, server)) == 5 })
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 45), "mobius:ready") })

	if status, body := closeWontDo(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	for _, number := range []int64{41, 42, 44, 45} {
		if state, reason := fake.State(shop, number); state != "closed" || reason != "not_planned" {
			t.Errorf("state of #%d = %s, %s", number, state, reason)
		}
		if comments := fake.Comments(shop, number); !slices.Equal(comments, []testkit.Comment{wontDoComment}) {
			t.Errorf("comments of #%d = %v", number, comments)
		}
	}
	if labels := fake.Labels(shop, 45); slices.Contains(labels, "mobius:ready") {
		t.Errorf("labels of #45 = %v", labels)
	}
	if start := startByOf(t, server, shop, 45); start != "" {
		t.Errorf("start of #45 = %q", start)
	}
	unchanged(t, fake, shop, 43, "closed")
}

func TestALostAccessRemovesTheTreeOfAWorkstreamWithAPendingClose(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddBlockedBy(shop, 41, 88)
	server, _ := connect(t, fake, curatorScript)
	testkit.WaitFor(t, func() bool { return blockersOf41(t, server, "open") == 1 })
	fake.FailClose(shop, 41)
	fake.CloseIssue(shop, 12)
	testkit.WaitFor(t, func() bool {
		var pending int
		err := server.DB.QueryRow("SELECT close_pending FROM workstreams WHERE repository = ? AND number = 12", shop).Scan(&pending)
		return err == nil && pending == 1
	})

	fake.RemoveRepository(shop)

	testkit.WaitFor(t, func() bool {
		var workstreams, issues, blockers int
		err := server.DB.QueryRow(`SELECT (SELECT count(*) FROM workstreams), (SELECT count(*) FROM issues), (SELECT count(*) FROM blockers)`).Scan(&workstreams, &issues, &blockers)
		if err != nil {
			t.Fatal(err)
		}
		return workstreams == 0 && issues == 0 && blockers == 0
	})
}

func TestAnIssueWithMobiusReadyBelowAnOpenWorkstreamStartsWhenTheCopyOfTheTreeComes(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.FailSubIssues(shop, 12, true)
	dispatchTask(fake, 41, "Add plan model")
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 41) {
		t.Fatal("the issue has a task before the copy of the tree")
	}
	if row := issueRowOf(t, server, shop, 41); row.Workstream.Valid {
		t.Fatalf("workstream of #41 = %+v", row.Workstream)
	}

	fake.FailSubIssues(shop, 12, false)

	liveTaskOf(t, server, 41)
}
