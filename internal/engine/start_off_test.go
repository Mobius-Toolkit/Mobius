package engine_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	startOffReason  = "Autopilot paused: no task starts"
	startOffRefusal = "Mobius started no task: the Owner turned Start off on the Settings page. Try again after the Owner turns Start on."
)

func turnStartOff(t *testing.T, server *testserver.Server) {
	t.Helper()
	if status := putSwitch(t, server, "start", false); status != http.StatusNoContent {
		t.Fatalf("Start off: status %d", status)
	}
}

func turnStartOn(t *testing.T, server *testserver.Server) {
	t.Helper()
	if status := putSwitch(t, server, "start", true); status != http.StatusNoContent {
		t.Fatalf("Start on: status %d", status)
	}
}

// heldIssues gives the numbers of the issues in the held list of the Start switch.
func heldIssues(t *testing.T, server *testserver.Server) []int64 {
	t.Helper()
	var issues []int64
	for _, held := range getSwitches(t, server).Held {
		if held.Kind == "start" {
			issues = append(issues, held.Issue)
		}
	}
	return issues
}

func waitForStartHeld(t *testing.T, server *testserver.Server, issue int64) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return slices.Contains(heldIssues(t, server), issue) })
}

// waitForQueueReason waits until an Implementer session of the Workstream #12 shows reason.
func waitForQueueReason(t *testing.T, server *testserver.Server, reason string) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(roleSessions(t, server, engine.ImplementerRole), func(session store.Session) bool {
			return session.QueueReason.String == reason
		})
	})
}

func TestStartOffHoldsAnIssueWithMobiusReadyAndTheNextPollAfterStartIsOnDispatchesIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	turnStartOff(t, server)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForStartHeld(t, server, 41)
	if got := getSwitches(t, server).Held[0].Kind; got != "start" {
		t.Errorf("kind = %s", got)
	}
	waitForPolls(t, fake)
	if count := tasksOf(t, server, 41); count != 0 {
		t.Errorf("tasks = %d", count)
	}

	turnStartOn(t, server)

	if held := getSwitches(t, server).Held; len(held) != 0 {
		t.Errorf("held = %+v", held)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) != "" })
}

func TestStartOffHoldsASubIssueOfAnAutopilotWorkstreamAndNotItsBlockedSubIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	server := startAutopilot(t, fake, func(*config.Config) {}, "")
	server.WaitForFirstPoll(t, shop)
	turnStartOff(t, server)

	fake.AddIssue(shop, 44, "Blocker")
	addTaskIssue(fake, 43, "Blocked model")
	fake.AddBlockedBy(shop, 43, 44)
	addTaskIssue(fake, 41, "Add plan model")

	waitForStartHeld(t, server, 41)
	waitForPolls(t, fake)
	if held := heldIssues(t, server); !slices.Equal(held, []int64{41}) {
		t.Errorf("held = %v", held)
	}
	if count := tasksOf(t, server, 41); count != 0 {
		t.Errorf("tasks = %d", count)
	}

	turnStartOn(t, server)

	liveTaskOf(t, server, 41)
	if count := tasksOf(t, server, 43); count != 0 {
		t.Errorf("tasks of the blocked issue = %d", count)
	}
}

func TestStartOffMovesAnAskedTaskToAnsweredAtAReplyAndStartOnDispatchesIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	addTaskIssue(fake, 41, "Add plan model")
	server := startWithStatements(t, fake,
		`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, state_at)
		 VALUES ('owner/shop', 'owner/shop', 41, 12, 'asked', '2026-10-04T10:00:00Z', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`)
	server.WaitForFirstPoll(t, shop)
	turnStartOff(t, server)

	fake.AddComment(shop, 41, "owner", "Cents.")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "answered" })
	waitForStartHeld(t, server, 41)
	waitForPolls(t, fake)
	if state := taskState(t, server); state != "answered" {
		t.Fatalf("state = %s", state)
	}

	turnStartOn(t, server)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })
}

func TestStartOffQueuesTheFixRoundOfAFailedCIHeadWithTheReasonAndStartOnRunsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := approvalHead(t, server, fake)
	turnStartOff(t, server)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "queued" })
	waitForQueueReason(t, server, startOffReason)
	waitForStartHeld(t, server, 41)
	waitForPolls(t, fake)
	if state := taskState(t, server); state != "queued" {
		t.Errorf("state = %s", state)
	}

	turnStartOn(t, server)

	if prompt := roundPrompt(t, server); !strings.Contains(prompt, "build") {
		t.Errorf("prompt = %s", prompt)
	}
	if held := heldIssues(t, server); len(held) != 0 {
		t.Errorf("held = %v", held)
	}
}

func TestStartOffQueuesTheRoundOfAResumeAndStartOnRunsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := needsHumanWithLabels(t, fake, false)
	authorize(t, server)
	turnStartOff(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/resume", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "queued" })
	waitForQueueReason(t, server, startOffReason)
	waitForStartHeld(t, server, 41)

	turnStartOn(t, server)

	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) == 1 && !sessions[0].QueueReason.Valid
	})
}

func TestStartOffStartsNoJudgeForANewReviewItemAndStartOnStartsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, func(cfg *config.Config) { cfg.ReviewQuietPeriod = 0 })
	waitForApproval(t, server, fake)
	turnStartOff(t, server)

	fake.AddComment(shop, 42, "owner", "Why cents?")

	waitForStartHeld(t, server, 41)
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if judges := roleSessions(t, server, engine.JudgeRole); len(judges) != 0 {
		t.Errorf("Judges = %+v", judges)
	}

	turnStartOn(t, server)

	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.JudgeRole)) > 0 })
}

func TestStartOffGivesAHardBreakOfTheCINoTaskAndStartOnGivesItsTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, false)
	prepareCIIssue(fake, false)
	server := startWithStatements(t, fake)
	server.WaitForFirstPoll(t, shop)
	turnStartOff(t, server)

	if _, err := server.DB.Exec(hardBreakSQL(ciIssue)); err != nil {
		t.Fatal(err)
	}

	waitForStartHeld(t, server, ciIssue)
	waitForPolls(t, fake)
	if count := tasksOf(t, server, ciIssue); count != 0 {
		t.Errorf("tasks = %d", count)
	}

	turnStartOn(t, server)

	if task := liveTaskOf(t, server, ciIssue); task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
}

func TestStartOffRefusesStartImplementerMarkReadyAndStartFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake,
		call("start_implementer", `{ repository = "owner/shop", n = 41, instructions = "Round prices down." }`)+
			call("mark_ready", `{ repository = "owner/shop", n = 41 }`)+
			call("start_fix_round", `{ repository = "owner/shop", n = 41, findings = "Use cents." }`))
	turnStartOff(t, server)

	session := run(t, server, leadSpec(t), "1. ", "2. ", "3. ")

	want := strings.Repeat("error: "+startOffRefusal, 3)
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
}

func TestStartOffStartsAResearcher(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearch(t, fake, researchLead)
	turnStartOff(t, server)

	sendChat(t, server, leadChat, "Plan the price model.")

	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ResearcherRole)
		return len(sessions) == 1 && sessions[0].EndedAt.Valid && sessions[0].EndReason.String == "done"
	})
}

func TestStartOffLetsARunningImplementerPushItsCommits(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	goFile := filepath.Join(t.TempDir(), "go")
	implementer := fmt.Sprintf("[[prompts]]\nshell = '''while [ ! -e '%s' ]; do sleep 0.05; done; echo cents > plan.txt && git add plan.txt && git commit -q -m 'Add plan model'; true'''\n", goFile)
	server, _ := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForState(t, server, 41, "working")
	turnStartOff(t, server)

	touch(t, goFile)

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	if sessions := endedImplementers(t, server, 1); sessions[0].EndReason.String != "done" {
		t.Errorf("session = %+v", sessions[0])
	}
}
