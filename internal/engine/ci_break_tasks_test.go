package engine_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const ciIssue = 30

// hardBreakSQL is a hard break of the workflow 7 in the run 1 whose task issue is the number issue.
func hardBreakSQL(issue int64) string {
	return fmt.Sprintf(`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, rerun_at, result, issue, started_at)
		VALUES ('owner/shop', 7, 'ci', 1, '', 1, '2026-10-04T10:00:00Z', 'hard', %d, '2026-10-04T10:00:00Z')`, issue)
}

// prepareCIIssue adds the maintenance Workstream #20 with autopilot or not, and the open issue #30 below it.
func prepareCIIssue(fake *testkit.FakeGitHub, autopilot bool) {
	addMaintenanceWorkstream(fake, maintenance)
	if autopilot {
		fake.AddLabel(shop, maintenance, "mobius:autopilot", "owner")
	}
	fake.AddIssue(shop, ciIssue, "Fix the failed workflow")
	fake.AddSubIssue(shop, maintenance, ciIssue)
}

// startWithStatements starts a server with a Lead and an Implementer that answer with "Seen". The database has
// statements before the first poll, as after a restart.
func startWithStatements(t *testing.T, fake *testkit.FakeGitHub, statements ...string) *testserver.Server {
	t.Helper()
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+seen)
	seed(t, dataDir, statements...)
	return startServerWith(t, fake, testserver.Config(t, dataDir), "")
}

func countRows(t *testing.T, server *testserver.Server, query string, args ...any) int {
	t.Helper()
	var count int
	if err := server.DB.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func implementerSessions(t *testing.T, server *testserver.Server, issue int64) int {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM sessions WHERE role = 'implementer' AND issue = ?", issue)
}

func tasksOf(t *testing.T, server *testserver.Server, issue int64) int {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM tasks WHERE issue = ?", issue)
}

func endedBreaks(t *testing.T, server *testserver.Server) int {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM ci_breaks WHERE ended_at <> ''")
}

func TestAHardBreakStartsItsTaskInTheMaintenanceWorkstreamWithAutopilotOff(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	prepareCIIssue(fake, false)

	server := startWithStatements(t, fake, hardBreakSQL(ciIssue))

	if task := liveTaskOf(t, server, ciIssue); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
	testkit.WaitFor(t, func() bool { return implementerSessions(t, server, ciIssue) == 1 })
}

func TestWithAutopilotOnInTheMaintenanceWorkstreamTheIssueOfAHardBreakGetsOneTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareCIIssue(fake, true)
	server := startWithStatements(t, fake, hardBreakSQL(ciIssue))
	liveTaskOf(t, server, ciIssue)

	waitForPolls(t, fake)

	if count := tasksOf(t, server, ciIssue); count != 1 {
		t.Errorf("tasks = %d", count)
	}
}

func TestAHardBreakStartsNoNewTaskForAnIssueThatHadATask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareCIIssue(fake, false)
	server := startWithStatements(t, fake, hardBreakSQL(ciIssue),
		`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, state_at)
		 VALUES ('owner/shop', 'owner/shop', 30, 20, 'ended', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z')`)
	server.WaitForFirstPoll(t, shop)

	waitForPolls(t, fake)

	if count := tasksOf(t, server, ciIssue); count != 1 {
		t.Errorf("tasks = %d", count)
	}
}

func TestACITaskTakesTheNextSlotBeforeAQueuedTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	fake.RefuseRerun(http.StatusForbidden)
	goFile := filepath.Join(t.TempDir(), "go")
	server, _ := connectThree(t, fake, fmt.Sprintf("[[prompts]]\nshell = '''while [ ! -e '%s' ]; do sleep 0.05; done; echo cents > plan.txt && git add plan.txt && git commit -q -m \"Add plan model $(basename $PWD)\"'''\n", goFile))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForState(t, server, 41, "working")
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForState(t, server, 43, "queued")

	fake.AddWorkflowRun(shop, failedMainRun(7))
	number := ciBreakIssue(t, fake)
	waitForState(t, server, number, "queued")
	for _, queued := range []int64{number, 43} {
		testkit.WaitFor(t, func() bool {
			return countRows(t, server, "SELECT count(*) FROM sessions WHERE role = 'implementer' AND issue = ? AND queue_reason IS NOT NULL", queued) == 1
		})
	}
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var ciEnd, queuedStart string
	testkit.WaitFor(t, func() bool {
		err := server.DB.QueryRow("SELECT coalesce(max(ended_at), '') FROM sessions WHERE role = 'implementer' AND issue = ?", number).Scan(&ciEnd)
		if err != nil {
			t.Fatal(err)
		}
		err = server.DB.QueryRow("SELECT coalesce(max(started_at), '') FROM sessions WHERE role = 'implementer' AND issue = 43 AND queue_reason IS NULL").Scan(&queuedStart)
		if err != nil {
			t.Fatal(err)
		}
		return ciEnd != "" && queuedStart != ""
	})
	if parseTime(t, queuedStart).Before(parseTime(t, ciEnd)) {
		t.Errorf("the queued task started at %s, before the CI task ended at %s", queuedStart, ciEnd)
	}
}

func TestAHardBreakWithoutAnIssueBelowTheWorkstreamStopsTheDispatchAndStartsNoTaskUntilTheIssueIsBelowIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	dispatchTask(fake, 41, "Add plan model")
	fake.AddIssue(shop, ciIssue, "Fix the failed workflow")
	server := startWithStatements(t, fake, hardBreakSQL(-ciIssue))
	server.WaitForFirstPoll(t, shop)

	waitForPolls(t, fake)

	if hasLiveTask(t, server, 41) || hasLiveTask(t, server, ciIssue) {
		t.Fatal("a task started")
	}
	if labels := fake.Labels(shop, 41); !slices.Contains(labels, "mobius:ready") {
		t.Errorf("labels = %v", labels)
	}

	addMaintenanceWorkstream(fake, maintenance)

	liveTaskOf(t, server, ciIssue)
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 41) {
		t.Error("the stop did not hold #41")
	}
}

func TestDuringTheStopReadyAndAutopilotStartNoOtherTaskUntilAPassedRunEndsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	dispatchTask(fake, 43, "Add plan price")
	prepareCIIssue(fake, false)
	fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startWithStatements(t, fake, hardBreakSQL(ciIssue))
	liveTaskOf(t, server, ciIssue)

	waitForPolls(t, fake)

	if hasLiveTask(t, server, 41) || hasLiveTask(t, server, 43) {
		t.Fatal("a task started during the stop")
	}
	if labels := fake.Labels(shop, 43); !slices.Contains(labels, "mobius:ready") {
		t.Errorf("labels = %v", labels)
	}

	passed := failedMainRun(7)
	passed.Conclusion = "success"
	fake.AddWorkflowRun(shop, passed)

	liveTaskOf(t, server, 41)
	liveTaskOf(t, server, 43)
	if ended := endedBreaks(t, server); ended != 1 {
		t.Errorf("ended breaks = %d", ended)
	}
}

func TestAClosedCIIssueEndsTheBreakAndTheStopAndStartsNoNewBreak(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	dispatchTask(fake, 43, "Add plan price")
	prepareCIIssue(fake, false)
	fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startWithStatements(t, fake, hardBreakSQL(ciIssue))
	liveTaskOf(t, server, ciIssue)
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 43) {
		t.Fatal("a task started during the stop")
	}
	created := createdIssues(fake)

	fake.CloseIssue(shop, ciIssue)

	liveTaskOf(t, server, 43)
	waitForPolls(t, fake)
	var endedRun int64
	if err := server.DB.QueryRow("SELECT ended_run_id FROM ci_breaks WHERE ended_at <> ''").Scan(&endedRun); err != nil || endedRun != 0 {
		t.Errorf("ended run = %d, error = %v", endedRun, err)
	}
	if count := countRows(t, server, "SELECT count(*) FROM ci_breaks"); count != 1 {
		t.Errorf("breaks = %d", count)
	}
	if got := createdIssues(fake); got != created {
		t.Errorf("created issues = %d, want %d", got, created)
	}
}

func TestAnAnsweredTaskWaitsInAnsweredDuringTheStopAndDispatchesAfterIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	addTaskIssue(fake, 41, "Add plan model")
	prepareCIIssue(fake, false)
	fake.AddWorkflowRun(shop, failedMainRun(7))
	server := startWithStatements(t, fake, hardBreakSQL(ciIssue),
		`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, state_at)
		 VALUES ('owner/shop', 'owner/shop', 41, 12, 'asked', '2026-10-04T10:00:00Z', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`)
	server.WaitForFirstPoll(t, shop)

	fake.AddComment(shop, 41, "owner", "Cents.")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "answered" })
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "answered" {
		t.Fatalf("state = %s", state)
	}

	passed := failedMainRun(7)
	passed.Conclusion = "success"
	fake.AddWorkflowRun(shop, passed)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })
}

func TestARefusedReadOfTheCITaskIssueDoesNotStopThePoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	addTaskIssue(fake, 41, "Add plan model")
	prepareCIIssue(fake, false)
	fake.ForbidIssue(shop, ciIssue)
	server := startWithStatements(t, fake, hardBreakSQL(ciIssue),
		`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, state_at)
		 VALUES ('owner/shop', 'owner/shop', 41, 12, 'ended', '2026-10-04T10:00:00Z', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`)
	server.WaitForFirstPoll(t, shop)

	fake.AddLabel(shop, 41, "mobius:question", testkit.AppSlug+"[bot]")

	testkit.WaitFor(t, func() bool { return len(fake.Labels(shop, 41)) == 0 })
	if count := tasksOf(t, server, ciIssue); count != 0 {
		t.Errorf("tasks = %d", count)
	}
}

func TestDuringTheStopStartImplementerRefusesWithTheLinkToTheCITaskIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("start_implementer", `{ repository = "owner/shop", n = 41, instructions = "Round prices down." }`))
	addTask(t, server, fake, 41, 12, 45)
	fake.AddIssue(shop, ciIssue, "Fix the failed workflow")
	if _, err := server.DB.Exec(`UPDATE tasks SET state = 'dispatched'`); err != nil {
		t.Fatal(err)
	}
	if _, err := server.DB.Exec(hardBreakSQL(ciIssue)); err != nil {
		t.Fatal(err)
	}

	session := run(t, server, leadSpec(t), "1. ")

	want := "error: Mobius started no task: the CI of main has a hard break. Fix it first in https://github.com/owner/shop/issues/30."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
}

func TestDuringTheStopARunningImplementerTheLeadAndTheTriagerContinue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	goFile := filepath.Join(t.TempDir(), "go")
	implementer := fmt.Sprintf("[[prompts]]\nshell = '''while [ ! -e '%s' ]; do sleep 0.05; done; %s'''\n", goFile,
		"echo cents > plan.txt && git add plan.txt && git commit -q -m 'Add plan model'")
	triager := "[[prompts]]\nwhen = \"You are the Triager\"\nreply = [\"A proposal.\"]\n\n"
	server, _ := connectTask(t, fake, triager+leadStarts, implementer, func(cfg *config.Config) {
		cfg.ReviewQuietPeriod = 200 * time.Millisecond
		keepSessionOpen(cfg)
	})
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForState(t, server, 41, "working")
	fake.AddIssue(shop, ciIssue, "Fix the failed workflow")
	if _, err := server.DB.Exec(hardBreakSQL(ciIssue)); err != nil {
		t.Fatal(err)
	}

	fake.AddComment(shop, 12, "owner", "Plan the next step.")
	waitForLeadPrompt(t, server, ` comment on #12 "Integrate loyalty plans" by @owner:`)
	fake.AddIssue(shop, 50, "Change request")
	fake.AddLabel(shop, 50, "mobius:triage", "owner")
	testkit.WaitFor(t, func() bool { return len(chatSessions(t, server, issueTriagers, engine.TriagerRole)) > 0 })
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	waitForState(t, server, 41, "approval")
}
