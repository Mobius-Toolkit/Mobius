package engine_test

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// declineRule is a Lead that declines the task of #41.
const declineRule = "[[prompts]]\nwhen = \"Decline it\"\ncall = { tool = \"decline\", arguments = { repository = \"owner/shop\", n = 41, reason = \"No.\" } }\n\n"

// keptLead starts a fix round and can decline the task.
const keptLead = noFinding + declineRule + leadFindings + leadStarts

// counting is a fake Harness script that counts each process of the Implementer in the file starts next to it.
const counting = "skip_tools_list = 1000\n"

const commitWithCost = "[[prompts]]\n" +
	"updates = ['{\"sessionUpdate\": \"usage_update\", \"cost\": {\"currency\": \"USD\", \"amount\": 1.5}}']\n" +
	commitCents + "\n"

const fixWithCost = "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\n" +
	"updates = ['{\"sessionUpdate\": \"usage_update\", \"cost\": {\"currency\": \"USD\", \"amount\": 3.0}}']\n" +
	"shell = \"true\"\n\n"

// installCountingImplementer replaces the script of the Implementer with script, which counts its processes.
func installCountingImplementer(t *testing.T, dataDir, script string) {
	t.Helper()
	testkit.InstallFakeHarness(t, dataDir, "devin", counting+options+script)
}

// keptServer starts a server with the Lead of keptLead and an Implementer that counts its processes with script.
func keptServer(t *testing.T, fake *testkit.FakeGitHub, script string, adjust func(*config.Config)) (*testserver.Server, string) {
	t.Helper()
	server, dataDir := connectTask(t, fake, keptLead, "", adjust)
	installCountingImplementer(t, dataDir, script)
	return server, dataDir
}

// harnessStarts gives the number of agent processes of the counting script that started.
func harnessStarts(t *testing.T, dataDir string) int {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "harnesses", "starts")))
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	starts, err := strconv.Atoi(strings.TrimSpace(string(text)))
	if err != nil {
		t.Fatal(err)
	}
	return starts
}

// firstRound starts the task of #41, waits until it waits for the Lead, and gives its ended Implementer session.
func firstRound(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) store.Session {
	t.Helper()
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	return waitForImplementerSessions(t, server, 1)[0]
}

// waitForImplementerSessions waits until the Workstream has count ended Implementer sessions.
func waitForImplementerSessions(t *testing.T, server *testserver.Server, count int) []store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) != count {
			return nil, false
		}
		for _, session := range sessions {
			if !session.EndedAt.Valid {
				return nil, false
			}
		}
		return sessions, true
	})
}

// waitForKept waits until the engine keeps a Harness session for the task of the issue.
func waitForKept(t *testing.T, server *testserver.Server, issue int64) *runner.Session {
	t.Helper()
	task := liveTask(t, server, issue)
	return testkit.WaitForValue(t, func() (*runner.Session, bool) {
		kept := server.Engine.KeptSession(task.ID)
		return kept, kept != nil
	})
}

// keptOf gives the Harness session that the engine keeps for the task of the issue, or nil.
func keptOf(t *testing.T, server *testserver.Server, issue int64) *runner.Session {
	t.Helper()
	return server.Engine.KeptSession(liveTask(t, server, issue).ID)
}

// waitForClosedKept waits until the engine no longer keeps the task session and its Harness process ended.
func waitForClosedKept(t *testing.T, server *testserver.Server, taskID int64, kept *runner.Session) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		return server.Engine.KeptSession(taskID) == nil && !kept.Alive()
	})
}

// requestSeen tells if the fake GitHub got a request with method to path.
func requestSeen(fake *testkit.FakeGitHub, method, path string) bool {
	return slices.ContainsFunc(fake.Requests(), func(request testkit.Request) bool {
		return request.Method == method && request.Path == path
	})
}

// startFixRound sends the findings of the Lead to the task of #41, so that a fix round starts.
func startFixRound(t *testing.T, server *testserver.Server) {
	t.Helper()
	sendChat(t, server, leadChat, "Send the findings to #41")
}

// sessionCost gives the sum of the cost of the turn usage rows of the session.
func sessionCost(t *testing.T, server *testserver.Server, session int64) float64 {
	t.Helper()
	var cost float64
	if err := server.DB.QueryRow("SELECT coalesce(sum(cost_usd), 0) FROM turn_usage WHERE session = ?", session).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	return cost
}

// waitForTask waits until the live task of the issue has state.
func waitForTask(t *testing.T, server *testserver.Server, issue int64, state string) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return taskStateOf(t, server, issue) == state })
}

// taskStateOf gives the state of the live task of the issue, or "".
func taskStateOf(t *testing.T, server *testserver.Server, issue int64) string {
	t.Helper()
	var state string
	err := server.DB.QueryRow("SELECT coalesce(max(state), '') FROM tasks WHERE repository = ? AND issue = ? AND state <> 'ended'", shop, issue).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestARoundInKeepTimeContinuesTheKeptImplementerSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := keptServer(t, fake, fixWithCost+commitWithCost, noChange)
	first := firstRound(t, server, fake)
	kept := waitForKept(t, server, 41)
	if !kept.Alive() || harnessStarts(t, dataDir) != 1 {
		t.Fatalf("kept = %v, starts = %d", kept, harnessStarts(t, dataDir))
	}

	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 2)
	if sessions[0].Continued != 0 || sessions[1].Continued != 1 {
		t.Errorf("continued = %d, %d", sessions[0].Continued, sessions[1].Continued)
	}
	if sessions[1].AcpSessionID.String != first.AcpSessionID.String || sessions[1].AcpSessionID.String == "" {
		t.Errorf("acp session ids = %q, %q", sessions[0].AcpSessionID.String, sessions[1].AcpSessionID.String)
	}
	if got := harnessStarts(t, dataDir); got != 1 {
		t.Errorf("starts = %d, want 1 process for two rounds", got)
	}
	prompts := promptTexts(t, server, sessions[1].ID)
	if len(prompts) != 1 || strings.Contains(prompts[0], "# Brief") || !strings.Contains(prompts[0], "# Open items\n") ||
		!strings.Contains(prompts[0], "Remove the lines out of scope.") ||
		!strings.Contains(prompts[0], "If your change makes your report wrong, call `submit_report` with the full new report.") {
		t.Errorf("prompts = %q", prompts)
	}
	if cost := sessionCost(t, server, sessions[0].ID); math.Abs(cost-1.5) > 0.001 {
		t.Errorf("cost of the first row = %v", cost)
	}
	if cost := sessionCost(t, server, sessions[1].ID); math.Abs(cost-1.5) > 0.001 {
		t.Errorf("cost of the kept row = %v, want only the cost of that row", cost)
	}
}

func TestARoundAfterKeepTimeStartsANewImplementerSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := keptServer(t, fake, commits, noChange)
	first := firstRound(t, server, fake)
	waitForKept(t, server, 41)

	server.Engine.SetClock(func() time.Time { return time.Now().Add(61 * time.Minute) })
	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 2)
	if sessions[1].Continued != 0 || sessions[1].AcpSessionID.String == first.AcpSessionID.String {
		t.Errorf("session = %+v", sessions[1])
	}
	if got := harnessStarts(t, dataDir); got != 2 {
		t.Errorf("starts = %d, want a new process", got)
	}
	if prompts := promptTexts(t, server, sessions[1].ID); len(prompts) != 1 || !strings.Contains(prompts[0], "# Brief") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestARoundAfterARestartStartsANewImplementerSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := keptServer(t, fake, commits, noChange)
	first := firstRound(t, server, fake)
	waitForKept(t, server, 41)
	server.Stop()

	restarted := testserver.Start(t, dataDir, fake.URL)
	restarted.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, restarted.Engine.AccessChecked)
	startFixRound(t, restarted)

	sessions := waitForImplementerSessions(t, restarted, 2)
	if sessions[1].Continued != 0 || sessions[1].AcpSessionID.String == first.AcpSessionID.String {
		t.Errorf("session = %+v", sessions[1])
	}
	if prompts := promptTexts(t, restarted, sessions[1].ID); len(prompts) != 1 || !strings.Contains(prompts[0], "# Brief") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestARoundAfterAFailedImplementerSessionStartsANewSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fail := filepath.Join(t.TempDir(), "fail")
	server, dataDir := keptServer(t, fake, "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\n"+
		"shell = '''if [ ! -e "+fail+" ]; then touch "+fail+"; kill -9 $PPID; sleep 5; fi'''\n\n"+commits, noChange)
	first := firstRound(t, server, fake)
	waitForKept(t, server, 41)

	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 3)
	if sessions[1].Continued != 1 || sessions[1].EndReason.String != "failed" {
		t.Errorf("failed session = %+v", sessions[1])
	}
	if sessions[2].Continued != 0 || sessions[2].AcpSessionID.String == first.AcpSessionID.String {
		t.Errorf("new session = %+v", sessions[2])
	}
	if prompts := promptTexts(t, server, sessions[2].ID); len(prompts) != 1 || !strings.Contains(prompts[0], "# Brief") {
		t.Errorf("prompts = %q", prompts)
	}
	if got := harnessStarts(t, dataDir); got != 2 {
		t.Errorf("starts = %d, want a new process after the failure", got)
	}
}

func TestARoundAfterAChangeOfTheImplementerBindingStartsANewSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := keptServer(t, fake, commits, noChange)
	first := firstRound(t, server, fake)
	waitForKept(t, server, 41)

	settings, err := server.Engine.Roles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var binding config.RoleBinding
	for _, setting := range settings {
		if setting.Role == engine.ImplementerRole {
			binding = setting.Binding
		}
	}
	binding.Model = "opus"
	if err := server.Engine.SetRole(t.Context(), engine.ImplementerRole, binding); err != nil {
		t.Fatal(err)
	}
	before := harnessStarts(t, dataDir)

	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 2)
	if sessions[1].Continued != 0 || sessions[1].AcpSessionID.String == first.AcpSessionID.String {
		t.Errorf("session = %+v", sessions[1])
	}
	if sessions[1].Model != "opus" {
		t.Errorf("model = %s", sessions[1].Model)
	}
	if got := harnessStarts(t, dataDir); got != before+1 {
		t.Errorf("starts = %d, want %d", got, before+1)
	}
	if prompts := promptTexts(t, server, sessions[1].ID); len(prompts) != 1 || !strings.Contains(prompts[0], "# Brief") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAConflictRoundStartsANewImplementerSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := keptServer(t, fake, "[[prompts]]\nwhen = \"Merge the base branch\"\nshell = \"true\"\n\n"+commits, noChange)
	first := firstRound(t, server, fake)
	waitForKept(t, server, 41)

	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	fake.SetBehind(shop, 42)

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) != 2 || !sessions[1].EndedAt.Valid {
			return nil, false
		}
		return sessions, true
	})
	if sessions[1].Continued != 0 || sessions[1].AcpSessionID.String == first.AcpSessionID.String {
		t.Errorf("session = %+v", sessions[1])
	}
	if sessions[1].EndReason.String != "not_merged" {
		t.Errorf("end reason = %s", sessions[1].EndReason.String)
	}
	if kept := keptOf(t, server, 41); kept != nil {
		t.Errorf("the kept session of the earlier round is still open")
	}
	if got := harnessStarts(t, dataDir); got != 2 {
		t.Errorf("starts = %d", got)
	}
	prompts := promptTexts(t, server, sessions[1].ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "# Brief") || !strings.Contains(prompts[0], "# Base branch") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTheEndOfTheTaskClosesTheKeptImplementerSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, noChange)
	firstRound(t, server, fake)
	kept := waitForKept(t, server, 41)
	taskID := liveTask(t, server, 41).ID

	sendChat(t, server, leadChat, "Decline it")
	testkit.WaitFor(t, func() bool {
		var ended int
		if err := server.DB.QueryRow("SELECT count(*) FROM tasks WHERE repository = ? AND issue = 41 AND state = 'ended'", shop).Scan(&ended); err != nil {
			t.Fatal(err)
		}
		return ended == 1
	})

	waitForClosedKept(t, server, taskID, kept)
}

func TestANeedsHumanStopClosesTheKeptImplementerSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, longGrace)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForTask(t, server, 41, "checks")
	sha := head(t, fake, "mobius/41")
	kept := waitForKept(t, server, 41)
	taskID := liveTask(t, server, 41).ID

	fake.AddWorkflowRun(shop, testkit.WorkflowRun{HeadSHA: sha, Status: "completed", Conclusion: "startup_failure"})

	waitForTask(t, server, 41, "needs_human")
	waitForClosedKept(t, server, taskID, kept)
}

func TestAStoppedTaskClosesTheKeptImplementerSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, noChange)
	firstRound(t, server, fake)
	kept := waitForKept(t, server, 41)
	taskID := liveTask(t, server, 41).ID
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") })

	fake.RemoveLabel(shop, 41, "mobius:working", "owner")

	waitForTask(t, server, 41, "stopped")
	waitForClosedKept(t, server, taskID, kept)
}

// TestAStopWhileTheRoundEndsClosesTheKeptImplementerSession stops the task in the window between the work of the round
// and its keep. The stop closes the kept session of the task, but the map has no entry then. The keep must close the
// process, because the task is stopped.
//
// Serial: the test changes beforeKeep.
func TestAStopWhileTheRoundEndsClosesTheKeptImplementerSession(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, noChange)
	kept := make(chan *runner.Session, 1)
	timedOut := make(chan struct{})
	restore := engine.SetBeforeKeep(func(_ int64, session *runner.Session) {
		kept <- session
		fake.RemoveLabel(shop, 41, "mobius:working", "owner")
		deadline := time.Now().Add(time.Minute)
		for !requestSeen(fake, "DELETE", "/repos/owner/shop/issues/41/labels/mobius:working") {
			if time.Now().After(deadline) {
				close(timedOut)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	defer restore()

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") })
	taskID := liveTask(t, server, 41).ID

	var session *runner.Session
	select {
	case session = <-kept:
	case <-time.After(time.Minute):
		t.Fatal("the round did not end with a keep")
	}
	select {
	case <-timedOut:
		t.Fatal("the stop did not reach its label write")
	default:
	}
	waitForTask(t, server, 41, "stopped")
	waitForClosedKept(t, server, taskID, session)
}

func TestAKeptSessionDoesNotHoldTheImplementerSlot(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 43, "Add plan price")
	fake.AddSubIssue(shop, 12, 43)
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+noFinding)
	testkit.InstallFakeHarness(t, dataDir, "devin", counting+options+commits)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options)
	cfg := testserver.Config(t, dataDir)
	cfg.Roles.Implementer.Max = 1
	cfg.ReviewQuietPeriod = time.Hour
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, server.Engine.AccessChecked)
	waitForTask(t, server, 41, "dispatched")

	if err := server.Engine.StartImplementerForIssue(t.Context(), shop, 12, 41, "Store plans in cents."); err != nil {
		t.Fatal(err)
	}

	waitForTask(t, server, 41, "checks")
	waitForKept(t, server, 41)
	waitForTask(t, server, 43, "dispatched")
	if err := server.Engine.StartImplementerForIssue(t.Context(), shop, 12, 43, "Round prices down."); err != nil {
		t.Fatal(err)
	}

	waitForTask(t, server, 43, "checks")

	sessions := waitForImplementerSessions(t, server, 2)
	if sessions[0].AcpSessionID.String == sessions[1].AcpSessionID.String {
		t.Errorf("both tasks used the same Harness process: %+v", sessions)
	}
	for _, issue := range []int64{41, 43} {
		if kept := waitForKept(t, server, issue); !kept.Alive() {
			t.Errorf("the kept session of #%d is not alive", issue)
		}
	}
}

func TestAShortPromptHasTheNewIssueBody(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, noChange)
	firstRound(t, server, fake)
	waitForKept(t, server, 41)

	fake.SetBody(shop, 41, "Plans have a new price.")
	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 2)
	prompts := promptTexts(t, server, sessions[1].ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	if !strings.Contains(prompts[0], "# Issue\n") || !strings.Contains(prompts[0], "Plans have a new price.") ||
		!strings.Contains(prompts[0], "This issue text replaces the issue text that the earlier round read.") {
		t.Errorf("prompt = %s", prompts[0])
	}
	if strings.Contains(prompts[0], "Plans have a price.") {
		t.Errorf("prompt still has the old issue body: %s", prompts[0])
	}
}

func TestAShortPromptHasTheEarlierReportAfterAConflictRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	// The first round submits a report. The conflict round starts a new process that gets no report in its prompt,
	// and it keeps that process. Thus the kept session never read the report of the task.
	merges := "[[prompts]]\nwhen = \"Merge the base branch and remove the conflicts.\"\n" +
		"shell = \"git merge -q origin/main; echo cents > plan.txt && git add plan.txt && git commit -q --no-edit\"\n\n"
	server, _ := keptServer(t, fake, merges+fixWithCost+commitsWithReport, noChange)
	firstRound(t, server, fake)
	waitForKept(t, server, 41)

	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) != 2 || !sessions[1].EndedAt.Valid {
			return nil, false
		}
		return sessions, true
	})
	if sessions[1].EndReason.String != "done" {
		t.Fatalf("conflict round end reason = %s", sessions[1].EndReason.String)
	}
	testkit.WaitFor(t, func() bool {
		kept := keptOf(t, server, 41)
		return kept != nil && kept.ID() == sessions[1].AcpSessionID.String
	})
	testkit.WaitFor(t, func() bool {
		state := taskStateOf(t, server, 41)
		return state == "checks" || state == "approval"
	})

	startFixRound(t, server)

	sessions = waitForImplementerSessions(t, server, 3)
	if sessions[2].Continued != 1 || sessions[2].AcpSessionID.String != sessions[1].AcpSessionID.String {
		t.Errorf("session = %+v", sessions[2])
	}
	prompts := promptTexts(t, server, sessions[2].ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "# Report\n\n"+firstReport+"\n") ||
		!strings.Contains(prompts[0], "This report replaces the report that the earlier round read.") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAShortPromptHasTheIssueBodyOfARestartedRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	// The fix round hangs, so that the test can stop the server while worker_input holds the old issue body.
	hang := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nhang = true\n\n"
	server, dataDir := keptServer(t, fake, hang+commits, noChange)
	firstRound(t, server, fake)
	waitForKept(t, server, 41)

	startFixRound(t, server)
	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) == 2 && len(promptTexts(t, server, sessions[1].ID)) == 1
	})
	server.Stop()

	// The restart sends worker_input with the issue body of its round, so the kept session reads no new body.
	fake.SetBody(shop, 41, "Plans have a new price.")
	commitsMore := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\n" +
		"shell = \"echo more >> plan.txt && git commit -q -am 'Add more'\"\n\n"
	testkit.InstallFakeHarness(t, dataDir, "devin", counting+options+commitsMore+commits)
	restarted := testserver.Start(t, dataDir, fake.URL)
	restarted.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, restarted.Engine.AccessChecked)
	waitForTask(t, restarted, 41, "approval")
	waitForImplementerSessions(t, restarted, 3)
	waitForKept(t, restarted, 41)

	startFixRound(t, restarted)

	sessions := waitForImplementerSessions(t, restarted, 4)
	if sessions[3].Continued != 1 {
		t.Errorf("continued = %d", sessions[3].Continued)
	}
	prompts := promptTexts(t, restarted, sessions[3].ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	if !strings.Contains(prompts[0], "# Issue\n") || !strings.Contains(prompts[0], "Plans have a new price.") ||
		!strings.Contains(prompts[0], "This issue text replaces the issue text that the earlier round read.") {
		t.Errorf("prompt = %s", prompts[0])
	}
	if strings.Contains(prompts[0], "Plans have a price.") {
		t.Errorf("prompt has the old issue body: %s", prompts[0])
	}
}

// Serial: it sets DSH_HOME, which dsh and the reader of the dsh session log read.
func TestAContinuedDSHRoundCountsOnlyTheNewLogRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	// The linger of the first turn gives the test the time to write the dsh session log of that turn before the
	// engine reads the log at the end of the turn.
	script := counting + "[options]\nmodel = ['" + dshModelValue + "']\nthought_level = [\"high\"]\n" +
		"[[prompts]]\nlinger = \"3s\"\n" + commitCents +
		"\n[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo cents >> plan.txt && git commit -q -am 'Store plans in cents'\"\n"
	server, _ := connectTaskIn(t, fake, dataDir, keptLead, "", func(cfg *config.Config) {
		cfg.Roles.Implementer = config.RoleBinding{Harness: config.DSH, Model: dshModel, Effort: "high", Max: 2}
		testkit.InstallFakeHarness(t, cfg.DataDir, "dsh", script)
	})
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	first := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) != 1 || !sessions[0].AcpSessionID.Valid {
			return store.Session{}, false
		}
		return sessions[0], true
	})
	logPath := engine.DSHLogPath(home, runner.TaskDir(dataDir, shop, 41), first.AcpSessionID.String)
	writeDSHLog(t, logPath, false, dshLogHeader, dshLogMessage(1, "deepseek-v4-pro", 100, 10, 1000, 5))

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	waitForKept(t, server, 41)
	writeDSHLog(t, logPath, true, dshLogMessage(2, "deepseek-flash", 30, 3, 200, 0))

	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 2)
	if rows := usageRows(t, server, sessions[0].ID); len(rows) != 1 || rows[0].Input != nullInt(100) {
		t.Errorf("tokens of the first row = %+v", rows)
	}
	if sessions[1].Continued != 1 || sessions[1].AcpSessionID.String != sessions[0].AcpSessionID.String {
		t.Errorf("second row = %+v", sessions[1])
	}
	if got := harnessStarts(t, dataDir); got != 2 {
		t.Errorf("starts = %d, want the access check and one Implementer process", got)
	}
	rows := usageRows(t, server, sessions[1].ID)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Input != nullInt(30) || rows[0].Output != nullInt(3) || rows[0].CacheRead != nullInt(200) {
		t.Errorf("tokens of the continued row = %+v, want only the records after the earlier row", rows[0])
	}
}
