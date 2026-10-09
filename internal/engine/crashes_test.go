package engine_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

// dies is the script of a Harness that dies in its turn.
const dies = "[[prompts]]\nshell = \"kill -9 $PPID; sleep 5\"\n"

// liveTask gives the live task of the issue number.
func liveTask(t *testing.T, server *testserver.Server, number int64) store.Task {
	t.Helper()
	task, err := store.New(server.DB).GetLiveTask(t.Context(), store.GetLiveTaskParams{Repository: shop, Issue: number})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// dieOnce runs a Worker session of spec whose Harness dies in its first turn, and gives the error of the turn.
func dieOnce(t *testing.T, server *testserver.Server, spec engine.Spec) error {
	t.Helper()
	agent := start(t, server, spec)
	err := agent.Prompt(t.Context(), "Store plans in cents.", nil)
	if err == nil {
		t.Fatal("the turn did not fail")
	}
	return agent.Fail(t.Context(), err)
}

func TestAWorkerThatDiesStartsAgainAfterAGrowingWait(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, dies)
	spec := implementerSpec(t, server, fake, 41)
	failure := dieOnce(t, server, spec)

	var waits []time.Duration
	for range 2 {
		began := time.Now()
		restart, err := server.Engine.RestartWorker(t.Context(), liveTask(t, server, 41), "Add plan model", failure)
		if err != nil || !restart {
			t.Fatalf("restart = %v: %v", restart, err)
		}
		waits = append(waits, time.Since(began))
	}

	if waits[0] < 100*time.Millisecond || waits[1] < 200*time.Millisecond {
		t.Errorf("waits = %v", waits)
	}
	lines := transcript(t, server, tree(t, server)[0].Session.ID)
	last := lines[len(lines)-1]
	// The Transcript shows the real error of the Harness.
	if last.Kind != "error" || !strings.Contains(last.Text, "peer disconnected") || tree(t, server)[0].Session.EndReason.String != "failed" {
		t.Errorf("last line = %+v", last)
	}
}

func TestAWorkerThatDiesAfterMaxWorkerRestartsGoesToAHuman(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, dies, func(cfg *config.Config) {
		cfg.MaxWorkerRestarts = 1
		fake.AddIssue(shop, 41, "Add plan model")
		fake.AddSubIssue(shop, 12, 41)
	})
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+"[[prompts]]\nreply = [\"Seen\"]\n")
	fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
	spec := implementerSpec(t, server, fake, 41)

	var restarts []bool
	for {
		failure := dieOnce(t, server, spec)
		restart, err := server.Engine.RestartWorker(t.Context(), liveTask(t, server, 41), "Add plan model", failure)
		if err != nil {
			t.Fatal(err)
		}
		restarts = append(restarts, restart)
		if !restart {
			break
		}
		if _, err := server.DB.Exec("UPDATE tasks SET state = 'queued' WHERE id = ?", spec.Task); err != nil {
			t.Fatal(err)
		}
	}

	if !reflect.DeepEqual(restarts, []bool{true, false}) {
		t.Errorf("restarts = %v", restarts)
	}
	if state := liveTask(t, server, 41).State; state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if labels := fake.Labels(shop, 41); !reflect.DeepEqual(labels, []string{"mobius:needs-human"}) {
		t.Errorf("labels = %q", labels)
	}
	var kind, payload string
	var issue, message sql.NullInt64
	if err := server.DB.QueryRow("SELECT kind, payload, issue, chat_message FROM lead_events WHERE workstream = 12").Scan(&kind, &payload, &issue, &message); err != nil {
		t.Fatal(err)
	}
	want := " stop of #41 \"Add plan model\": the Worker failed after 1 restarts. Mobius added mobius:needs-human. The last error ends with these lines:\n\n```\n{\"code\":-32603"
	if kind != "stop" || !strings.Contains(payload, want) || issue.Int64 != 41 {
		t.Errorf("event = %s %q #%d", kind, payload, issue.Int64)
	}
	var author, text string
	if err := server.DB.QueryRow("SELECT author, text FROM chat_messages WHERE id = ?", message.Int64).Scan(&author, &text); err != nil || author != "Event" || text != payload {
		t.Errorf("chat message = %s %q: %v", author, text, err)
	}
	// The Lead gets the stop event.
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool {
			return strings.Contains(prompt, "# Event\n\n") && strings.Contains(prompt, want)
		})
	})
}

// A Claude Code session with no Mobius tools must not run (Mobius-rust#254).
func TestAClaudeCodeSessionWithNoToolsListStartsAgain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, "", func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })
	testkit.InstallFakeAgent(t, dataDir, "skip_tools_list = 1\n"+options+"[[prompts]]\ncall = { tool = \"list_tasks\" }\n")

	session, _ := leadReply(t, server)

	var lines []string
	for _, line := range transcript(t, server, session) {
		if line.Kind != "update" {
			lines = append(lines, line.Kind+": "+line.Text)
		}
	}
	want := []string{"error: The session sent no tools/list, so it has no Mobius tools. Mobius starts the session again.", "prompt: Read the work", "mcp_call: mobius · list_tasks"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q", lines)
	}
}

func TestAClaudeCodeSessionWithNoToolsListAfterEachRestartFails(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, "", func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })
	testkit.InstallFakeAgent(t, dataDir, "skip_tools_list = 2\n"+options)

	_, err := server.Engine.Start(t.Context(), leadSpec(t))

	if err == nil || !strings.Contains(err.Error(), "the session sent no tools/list") {
		t.Fatalf("error = %v", err)
	}
	if got := tree(t, server)[0].Session.EndReason.String; got != "failed" {
		t.Errorf("end reason = %s", got)
	}
}

func TestTheHousekeeperRemovesTheDirectoriesThatNothingOwns(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	var stale, owned []string
	connectWith(t, fake, "", func(cfg *config.Config) {
		fake.AddIssue(shop, 41, "Add plan model")
		seed(t, cfg.DataDir, `INSERT INTO tasks (repository, issue, workstream, state, dispatched_at) VALUES ('owner/shop', 41, 12, 'working', '2026-10-04T10:00:00Z')`)
		worktrees := filepath.Join(cfg.DataDir, "worktrees", "owner", "shop")
		stale = []string{filepath.Join(worktrees, "task-99"), filepath.Join(worktrees, "review-12345"), filepath.Join(cfg.DataDir, "scratch", "777")}
		owned = []string{filepath.Join(worktrees, "task-41"), filepath.Join(cfg.DataDir, "leads", "owner", "shop", "12")}
		for _, dir := range append(stale, owned...) {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
		}
	})

	testkit.WaitFor(t, func() bool {
		for _, dir := range stale {
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				return false
			}
		}
		return true
	})

	for _, dir := range owned {
		if _, err := os.Stat(dir); err != nil {
			t.Error(err)
		}
	}
}

func TestALeadThatCrashesGetsTheSameEventInANewSession(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	flag := filepath.Join(t.TempDir(), "died")
	server, _ := connect(t, fake, "[[prompts]]\nwhen = \"dispatch of #41\"\nshell = \"if [ -e '"+flag+"' ]; then true; else touch '"+flag+"'; kill -9 $PPID; sleep 5; fi\"\n")

	dispatchTask(fake, 41, "Add plan model")

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := chatSessions(t, server, leadChat, engine.LeadRole)
		return sessions, len(sessions) == 2 && len(promptTexts(t, server, sessions[1].ID)) > 0
	})
	if sessions[0].EndReason.String != "failed" || !strings.Contains(promptTexts(t, server, sessions[1].ID)[0], "dispatch of #41") {
		t.Errorf("sessions = %+v", sessions)
	}
	testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == 0 })
}

func TestALeadThatAlwaysCrashesSendsTheEventToTheInbox(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nwhen = \"dispatch of #41\"\nshell = \"kill -9 $PPID; sleep 5\"\n")

	dispatchTask(fake, 41, "Add plan model")

	item := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range inbox(t, server) {
			if item.Kind == "Lead failed" {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	if !strings.Contains(item.Text, ` dispatch of #41 "Add plan model" by @owner:`) {
		t.Errorf("item = %+v", item)
	}
	if got := chatSessions(t, server, leadChat, engine.LeadRole); len(got) != 4 {
		t.Errorf("sessions = %d", len(got))
	}
	if got := undelivered(t, server); len(got) != 0 {
		t.Errorf("undelivered = %+v", got)
	}
}

// dieOnceThen is the shell of an Implementer whose Harness dies in its first turn and runs then after that. The flag
// file tells that a Harness died.
func dieOnceThen(flag, then string) string {
	return fmt.Sprintf("[[prompts]]\nshell = \"if [ -e '%[1]s' ]; then %[2]s; else touch '%[1]s'; kill -9 $PPID; sleep 5; fi\"\n", flag, then)
}

const commitShell = "echo cents > plan.txt && git add plan.txt && git commit -q -m 'Add plan model'"

func TestAWorkerThatDiesStartsAgainAndDoesTheWork(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, "", noChange)
	testkit.InstallFakeHarness(t, dataDir, "devin", options+dieOnceThen(filepath.Join(dataDir, "died"), commitShell))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	sessions := endedImplementers(t, server, 2)
	if sessions[0].EndReason.String != "failed" || sessions[1].EndReason.String != "done" {
		t.Errorf("sessions = %+v", sessions)
	}
}

func TestAWorkerWhoseHarnessStartDoesNotAnswerStartsAgainAfterStartTimeout(t *testing.T) {
	defer func(limit time.Duration) { runner.StartTimeout = limit }(runner.StartTimeout)
	runner.StartTimeout = 2 * time.Second
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, "", noChange)
	testkit.InstallFakeHarness(t, dataDir, "devin", "hang_start = 1\n"+options+"[[prompts]]\n"+"shell = \""+commitShell+"\"\n")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	sessions := endedImplementers(t, server, 2)
	if sessions[0].EndReason.String != "failed" || sessions[1].EndReason.String != "done" {
		t.Errorf("sessions = %+v", sessions)
	}
	if restarts := liveTask(t, server, 41).WorkerRestarts; restarts != 1 {
		t.Errorf("Worker restarts = %d", restarts)
	}
}

// handedToHuman starts a server with an Implementer that always dies and max_worker_restarts 1, dispatches #41, and
// waits until its task waits for a human.
func handedToHuman(t *testing.T, fake *testkit.FakeGitHub, implementer string) *testserver.Server {
	t.Helper()
	server, _ := connectTask(t, fake, leadStarts, implementer, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") && !hasLabel(fake, "mobius:working")
	})
	return server
}

func TestATaskInNeedsHumanStaysInNeedsHumanAfterTheNextPolls(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := handedToHuman(t, fake, dies)

	waitForPolls(t, fake)

	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
}

func TestMobiusReadyOnATaskInNeedsHumanWithNoPullRequestStartsTheImplementerOnTheSameBranch(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	goFile := filepath.Join(t.TempDir(), "go")
	server := handedToHuman(t, fake, fmt.Sprintf("[[prompts]]\nshell = \"if [ -e '%s' ]; then %s; else kill -9 $PPID; sleep 5; fi\"\n", goFile, commitShell))
	stopped := liveTask(t, server, 41)
	if len(fake.PullRequests(shop)) != 0 || !stopped.Branch.Valid {
		t.Fatalf("pull requests = %+v, task = %+v", fake.PullRequests(shop), stopped)
	}
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	task := testkit.WaitForValue(t, func() (store.Task, bool) {
		task := liveTask(t, server, 41)
		return task, task.PullRequest.Valid
	})
	if task.ID != stopped.ID || task.Branch != stopped.Branch {
		t.Errorf("task = %+v", task)
	}
	if pullRequests := fake.PullRequests(shop); len(pullRequests) != 1 || pullRequests[0].Head != stopped.Branch.String {
		t.Errorf("pull requests = %+v", pullRequests)
	}
	testkit.WaitFor(t, func() bool { return !hasLabel(fake, "mobius:ready") })
	if !hasLabel(fake, "mobius:working") || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return sessions[len(sessions)-1].EndReason.String == "done"
	})
}

func TestMobiusReadyOfTheAppOnATaskInNeedsHumanHasNoEffectWhenAutopilotIsOff(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := handedToHuman(t, fake, dies)
	implementers := len(roleSessions(t, server, engine.ImplementerRole))

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")
	fake.AddLabel(shop, 41, "mobius:ready", testkit.AppSlug+"[bot]")
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if got := len(roleSessions(t, server, engine.ImplementerRole)); got != implementers {
		t.Errorf("Implementers = %d", got)
	}
	if !hasLabel(fake, "mobius:ready") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
}
