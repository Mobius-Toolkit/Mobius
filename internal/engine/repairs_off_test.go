package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const repairsOffRefusal = "Mobius started no task: the Owner turned Repairs off on the Settings page. Try again after the Owner turns Repairs on."

func turnRepairsOff(t *testing.T, server *testserver.Server) {
	t.Helper()
	if status := putSwitch(t, server, "repairs", false); status != http.StatusNoContent {
		t.Fatalf("Repairs off: status %d", status)
	}
}

func turnRepairsOn(t *testing.T, server *testserver.Server) {
	t.Helper()
	if status := putSwitch(t, server, "repairs", true); status != http.StatusNoContent {
		t.Fatalf("Repairs on: status %d", status)
	}
}

// heldIssuesOf gives the numbers of the issues in the held list of the switch kind.
func heldIssuesOf(t *testing.T, server *testserver.Server, kind string) []int64 {
	t.Helper()
	var issues []int64
	for _, held := range getSwitches(t, server).Held {
		if held.Kind == kind {
			issues = append(issues, held.Issue)
		}
	}
	return issues
}

func waitForHeldWork(t *testing.T, server *testserver.Server, kind string, issue int64) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return slices.Contains(heldIssuesOf(t, server, kind), issue) })
}

func TestRepairsOffGivesAHardBreakOfTheCINoTaskAndRepairsOnGivesItsTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	server := startWithStatements(t, fake)
	server.WaitForFirstPoll(t, shop)
	turnRepairsOff(t, server)
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "failure")

	number := ciBreakIssue(t, fake)
	waitForHeldWork(t, server, "repairs", number)
	waitForPolls(t, fake)
	if count := tasksOf(t, server, number); count != 0 {
		t.Errorf("tasks = %d", count)
	}

	turnRepairsOn(t, server)

	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
	if held := getSwitches(t, server).Held; len(held) != 0 {
		t.Errorf("held = %+v", held)
	}
}

func TestRepairsOffHoldsTheIssueOfAFlakyBreakWithReadyAndRepairsOnDispatchesIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := failedRunWithJob(fake)
	addMaintenanceWorkstream(fake, maintenance)
	server, _ := connect(t, fake, "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})
	turnRepairsOff(t, server)

	fake.SetWorkflowRunStatus(run, "completed", "success")

	number := ciBreakIssue(t, fake)
	waitForHeldWork(t, server, "repairs", number)
	waitForPolls(t, fake)
	if count := tasksOf(t, server, number); count != 0 {
		t.Errorf("tasks = %d", count)
	}
	if labels := fake.Labels(shop, number); !slices.Contains(labels, "mobius:ready") {
		t.Errorf("labels = %v", labels)
	}

	turnRepairsOn(t, server)

	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
}

func TestRepairsOffHoldsTheIssueOfACheckTimeoutAndRepairsOnDispatchesIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	server, _ := connectTask(t, fake, leadStarts, commits, timeoutConfig(1))
	fake.SetCheck(shop, hangingCheck)
	turnRepairsOff(t, server)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	number := ciBreakIssue(t, fake)
	waitForHeldWork(t, server, "repairs", number)
	waitForPolls(t, fake)
	if count := tasksOf(t, server, number); count != 0 {
		t.Errorf("tasks = %d", count)
	}

	turnRepairsOn(t, server)

	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
}

func TestRepairsOffHoldsACIFailureIssueOfAnAutopilotMaintenanceWorkstreamAndRepairsOnStartsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddLabel(shop, maintenance, "mobius:autopilot", "owner")
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHeadOf(t, server, fake, 41)
	second := approvalHeadOf(t, server, fake, 43)
	turnRepairsOff(t, server)

	failActions(fake, first)
	failActions(fake, second)

	number := ciBreakIssue(t, fake)
	waitForHeldWork(t, server, "repairs", number)
	waitForPolls(t, fake)
	if count := tasksOf(t, server, number); count != 0 {
		t.Errorf("tasks = %d", count)
	}

	turnRepairsOn(t, server)

	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
}

func TestRepairsOffStartsAnIssueWithReadyAndNoRecordAndASubIssueOfTheMaintenanceWorkstreamWithNoRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddLabel(shop, maintenance, "mobius:autopilot", "owner")
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	turnRepairsOff(t, server)

	fake.AddIssue(shop, 31, "Fix the typo")
	fake.AddSubIssue(shop, maintenance, 31)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	if task := liveTaskOf(t, server, 31); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
	liveTaskOf(t, server, 41)
	if held := getSwitches(t, server).Held; len(held) != 0 {
		t.Errorf("held = %+v", held)
	}
}

func TestRepairsOffRefusesStartImplementerAndMarkReadyForARepairIssueAndAcceptsAnIssueWithNoRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake,
		call("start_implementer", `{ repository = "owner/shop", n = 30, instructions = "Fix the timeout." }`)+
			call("mark_ready", `{ repository = "owner/shop", n = 30 }`)+
			call("mark_ready", `{ repository = "owner/shop", n = 41 }`))
	fake.AddIssue(shop, 30, "Examine the time of the local check")
	fake.AddSubIssue(shop, 12, 30)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	if _, err := server.DB.Exec(`INSERT INTO check_timeouts (repository, task_issue, session, timeout, output, issue, created_at)
		VALUES ('owner/shop', 41, 1, '1s', '', 30, '2026-10-04T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	turnRepairsOff(t, server)

	session := run(t, server, leadSpec(t), "1. ", "2. ", "3. ")

	want := strings.Repeat("error: "+repairsOffRefusal, 2) + "Marked owner/shop#41 ready."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
}

func TestStartOffAndRepairsOffHoldARepairIssueOnlyForStartAndStartOnHoldsItForRepairs(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	prepareCIIssue(fake, false)
	server := startWithStatements(t, fake)
	server.WaitForFirstPoll(t, shop)
	turnStartOff(t, server)
	turnRepairsOff(t, server)

	if _, err := server.DB.Exec(hardBreakSQL(ciIssue)); err != nil {
		t.Fatal(err)
	}

	waitForHeldWork(t, server, "start", ciIssue)
	waitForPolls(t, fake)
	if held := heldIssuesOf(t, server, "repairs"); len(held) != 0 {
		t.Errorf("repairs held = %v", held)
	}

	turnStartOn(t, server)

	waitForHeldWork(t, server, "repairs", ciIssue)
	waitForPolls(t, fake)
	if held := heldIssuesOf(t, server, "start"); len(held) != 0 {
		t.Errorf("start held = %v", held)
	}
	if count := tasksOf(t, server, ciIssue); count != 0 {
		t.Errorf("tasks = %d", count)
	}

	turnRepairsOn(t, server)

	liveTaskOf(t, server, ciIssue)
}
