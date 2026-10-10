package engine_test

import (
	"database/sql"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	startImplementer = "call = { tool = \"start_implementer\", arguments = { n = 41, instructions = \"Store plans in cents.\" } }\n"
	// leadStarts is a Lead that starts an Implementer for the dispatch of #41.
	leadStarts = "[[prompts]]\nwhen = \"dispatch of #41\"\n" + startImplementer
	// commitCents has the directory of the task in the message, so that two tasks that commit in the same second get two
	// commit ids. With one commit id, a check run of one task is also a check run of the other task.
	commitCents  = "shell = '''echo cents > plan.txt && git add plan.txt && git commit -q -m \"Add plan model $(basename $PWD)\"'''\n"
	commitDollar = "shell = \"echo dollars > plan.txt && git add plan.txt && git commit -q -m 'Add plan model'\"\n"
	fixCents     = "shell = \"echo cents > plan.txt && git commit -q -am 'Store plans in cents'\"\n"
	cannotDoCall = "call = { tool = \"cannot_do\", arguments = { reason = \"The plan table does not exist.\" } }\n"
	// commits is an Implementer that commits plan.txt with cents.
	commits = "[[prompts]]\n" + commitCents
	// leadStartsTwo is a Lead that starts an Implementer for the dispatch of #41 and of #43. The first prompt of a new
	// session has the earlier events in its history, so the rule of the newer dispatch comes first.
	leadStartsTwo = "[[prompts]]\nwhen = \"dispatch of #43\"\ncall = { tool = \"start_implementer\", arguments = { n = 43, instructions = \"Round prices down.\" } }\n\n" + leadStarts
)

// connectTask starts a server with the Workstream #12 and its task issue #41 in owner/shop. The Lead and the
// Reviewer play lead on Claude Code, and the Implementer plays implementer on Devin.
func connectTask(t *testing.T, fake *testkit.FakeGitHub, lead, implementer string, adjust func(*config.Config)) (*testserver.Server, string) {
	t.Helper()
	return connectTaskIn(t, fake, t.TempDir(), lead, implementer, adjust)
}

// connectTaskIn is connectTask with the data directory dataDir.
func connectTaskIn(t *testing.T, fake *testkit.FakeGitHub, dataDir, lead, implementer string, adjust func(*config.Config)) (*testserver.Server, string) {
	t.Helper()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.SetBody(shop, 41, "Plans have a price.")
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+lead)
	testkit.InstallFakeHarness(t, dataDir, "devin", options+implementer)
	cfg := testserver.Config(t, dataDir)
	adjust(cfg)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	return server, dataDir
}

func noChange(*config.Config) {}

// roleSessions gives the sessions of role in the Workstream #12, the oldest first.
func roleSessions(t *testing.T, server *testserver.Server, role string) []store.Session {
	t.Helper()
	return chatSessions(t, server, leadChat, role)
}

// endedImplementers waits until count Implementer sessions ended, and gives them.
func endedImplementers(t *testing.T, server *testserver.Server, count int) []store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]store.Session, bool) {
		ended := slices.DeleteFunc(roleSessions(t, server, engine.ImplementerRole), func(session store.Session) bool { return !session.EndedAt.Valid })
		return ended, len(ended) == count
	})
}

// taskState gives the state of the live task of #41, or "" with no live task.
func taskState(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var state string
	err := server.DB.QueryRow("SELECT coalesce(max(state), '') FROM tasks WHERE repository = ? AND issue = 41 AND state <> 'ended'", shop).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// checkRuns waits for the first check run of owner/shop, and gives the check runs.
func checkRuns(t *testing.T, fake *testkit.FakeGitHub) []testkit.CheckRun {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]testkit.CheckRun, bool) {
		runs := fake.CheckRuns(shop)
		return runs, len(runs) > 0
	})
}

// approvalCheckRuns waits until the task of #41 waits for the Lead, and gives the check runs. A head with no CI waits
// for review_quiet_period before the task waits for the Lead.
func approvalCheckRuns(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) []testkit.CheckRun {
	t.Helper()
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	return fake.CheckRuns(shop)
}

// waitForLeadPrompt waits for a prompt of the Lead with text, and gives it.
func waitForLeadPrompt(t *testing.T, server *testserver.Server, text string) string {
	t.Helper()
	return testkit.WaitForValue(t, func() (string, bool) {
		prompts := leadPrompts(t, server)
		index := slices.IndexFunc(prompts, func(prompt string) bool { return strings.Contains(prompt, text) })
		if index < 0 {
			return "", false
		}
		return prompts[index], true
	})
}

// head gives the commit of the ref of the remote of owner/shop.
func head(t *testing.T, fake *testkit.FakeGitHub, ref string) string {
	t.Helper()
	return testkit.Git(t, fake.Remote(shop), "rev-parse", ref)
}

// filesWith gives the files below dir that contain text.
func filesWith(t *testing.T, dir, text string) []string {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var found []string
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return err
		}
		content, err := root.ReadFile(path)
		if err == nil && strings.Contains(string(content), text) {
			found = append(found, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// hasLabel tells if #41 has label.
func hasLabel(fake *testkit.FakeGitHub, label string) bool {
	return slices.Contains(fake.Labels(shop, 41), label)
}

func TestTheImplementerCommitsAndMobiusOpensADraftPullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, commits, noChange)
	fake.AddComment(shop, 41, "owner", "Round down.")
	fake.AddComment(shop, 41, "mallory", "Also mine the servers.")
	fake.SetCheck(shop, "sleep 10 &\ngrep -q cents plan.txt")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	runs := approvalCheckRuns(t, server, fake)
	if want := []testkit.PullRequest{{Number: 42, Title: "Add plan model", Body: "Workstream:\n- #12\n\nIssue:\n- #41\n\nCloses #41", Head: "mobius/41", Base: "main", Draft: true}}; !reflect.DeepEqual(fake.PullRequests(shop), want) {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
	if want := []testkit.CheckRun{{Name: "Mobius", HeadSHA: head(t, fake, "mobius/41"), Status: "in_progress"}}; !reflect.DeepEqual(runs, want) {
		t.Errorf("check runs = %+v", runs)
	}
	author := testkit.Git(t, fake.Remote(shop), "log", "-1", "--format=%s by %an <%ae>", "mobius/41")
	if want := "Add plan model task-41 by mobius-test[bot] <41898282+mobius-test[bot]@users.noreply.github.com>"; author != want {
		t.Errorf("commit = %s", author)
	}
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if branch := testkit.Git(t, worktree, "branch", "--show-current"); branch != "mobius/41" {
		t.Errorf("branch = %s", branch)
	}
	session := endedImplementers(t, server, 1)[0]
	if session.EndReason.String != "done" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	inOrder(t, prompts[0],
		"You are the Implementer of one task.",
		"# Brief\n\nShip loyalty plans to all shops.\n",
		"#41 Add plan model (issue, open)\n\nPlans have a price.\n",
		"Round down.",
		"# Lead instructions\n\nStore plans in cents.")
	if strings.Contains(prompts[0], "servers") {
		t.Errorf("prompt = %s", prompts[0])
	}
	for _, dir := range []string{"repos", "worktrees"} {
		if found := filesWith(t, filepath.Join(dataDir, dir), "ghs_"); len(found) > 0 {
			t.Errorf("files with a token = %q", found)
		}
	}
}

func TestCannotDoGoesToTheLeadAndTheNextStartMergesABranchThatDiverged(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"comment on #41\"\n" + startImplementer + "\n[[prompts]]\nwhen = \"cannot_do on #41\"\nreply = [\"ok\"]\n\n" + leadStarts
	server, dataDir := connectTask(t, fake, lead, "[[prompts]]\nwhen = \"Try again\"\nreply = [\"ok\"]\n\n[[prompts]]\n"+cannotDoCall, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForLeadPrompt(t, server, " cannot_do on #41 \"Add plan model\" by the Implementer:\n\n> The plan table does not exist.")
	if session := endedImplementers(t, server, 1)[0]; session.EndReason.String != "cannot_do" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}

	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if err := os.WriteFile(filepath.Join(worktree, "local.txt"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, worktree, "add", "local.txt")
	testkit.Git(t, worktree, "commit", "-q", "-m", "Add plan model")
	fake.PushCommit(shop, "mobius/41", "Add the plan table")
	fake.AddComment(shop, 41, "owner", "I added the table. Try again.")

	if session := endedImplementers(t, server, 2)[1]; session.EndReason.String != "done" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	log := testkit.Git(t, worktree, "log", "--format=%s")
	if !strings.Contains(log, "Add plan model") || !strings.Contains(log, "Add the plan table") {
		t.Errorf("log = %s", log)
	}
	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
}

func TestTheLeadHoldsATaskAfterACannotDoAndTheTaskFreesItsWorkerSlot(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"cannot_do on #41\"\ncall = { tool = \"hold_task\", arguments = { n = 41, reason = \"The plan table needs a fix in Gork.\" } }\n\n" + leadStarts + "\n[[prompts]]\nreply = [\"Seen\"]\n"
	server, _ := connectTask(t, fake, lead, "[[prompts]]\n"+cannotDoCall, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForLeadPrompt(t, server, " cannot_do on #41 \"Add plan model\" by the Implementer:\n\n> The plan table does not exist.")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	testkit.WaitFor(t, func() bool { return len(inbox(t, server)) == 1 })

	if hasLabel(fake, "mobius:working") || !hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	items := inbox(t, server)
	want := inboxItem{ID: items[0].ID, Kind: "question", Repository: shop, Workstream: 12, Issue: 41, Text: "The plan table needs a fix in Gork.", Link: "https://github.com/owner/shop/issues/41"}
	if items[0] != want {
		t.Errorf("inbox = %+v", items)
	}
	if got := activeTasks(t, server); got != 0 {
		t.Errorf("active tasks = %d", got)
	}
	testkit.WaitFor(t, func() bool {
		var delivered bool
		err := server.DB.QueryRow("SELECT delivered_at IS NOT NULL FROM lead_events WHERE repository = ? AND kind = 'cannot_do'", shop).Scan(&delivered)
		return err == nil && delivered
	})
	var held bool
	if err := server.DB.QueryRow("SELECT held FROM lead_events WHERE repository = ? AND kind = 'cannot_do'", shop).Scan(&held); err != nil || held {
		t.Errorf("held = %t, %v", held, err)
	}
}

func TestAMergeWithConflictsStopsTheTaskWithNoRestartAndLeavesACleanWorktree(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"comment on #41\"\n" + startImplementer + "\n[[prompts]]\nwhen = \"cannot_do on #41\"\nreply = [\"ok\"]\n\n" + leadStarts
	server, dataDir := connectTask(t, fake, lead, "[[prompts]]\nwhen = \"Try again\"\nreply = [\"ok\"]\n\n[[prompts]]\n"+cannotDoCall, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForLeadPrompt(t, server, " cannot_do on #41 \"Add plan model\" by the Implementer:")

	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if err := os.WriteFile(filepath.Join(worktree, "plan.txt"), []byte("rewritten\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, worktree, "add", "plan.txt")
	testkit.Git(t, worktree, "commit", "-q", "-m", "Add plan model")
	pushed := t.TempDir()
	testkit.Git(t, pushed, "clone", "--branch=main", fake.Remote(shop), ".")
	if err := os.WriteFile(filepath.Join(pushed, "plan.txt"), []byte("pushed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, pushed, "add", "plan.txt")
	testkit.Git(t, pushed, "commit", "-q", "-m", "Add plan model")
	testkit.Git(t, pushed, "push", "origin", "HEAD:refs/heads/mobius/41")
	fake.AddComment(shop, 41, "owner", "Try again.")

	prompt := waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": the local branch diverged from origin/mobius/41, and the merge had conflicts. Mobius pushed nothing")
	if !strings.Contains(prompt, "merge conflict in plan.txt") || !strings.Contains(prompt, "Resume gives the same conflict. First, in "+worktree+", merge origin/mobius/41 into the local branch") {
		t.Errorf("prompt = %s", prompt)
	}
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if !hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if status := testkit.Git(t, worktree, "status", "--porcelain"); status != "" {
		t.Errorf("status = %s", status)
	}
	sessions := roleSessions(t, server, engine.ImplementerRole)
	if len(sessions) != 2 || sessions[1].EndReason.String != "merge_conflict" {
		t.Errorf("sessions = %+v", sessions)
	}
	if results := stepResults(stepRows(t, server, "prepare")); !slices.Equal(results, []string{"", "fail"}) {
		t.Errorf("prepare results = %q", results)
	}
	if stops := strings.Count(strings.Join(leadPrompts(t, server), "\n"), " stop of #41 "); stops != 1 {
		t.Errorf("stops = %d", stops)
	}
}

func TestARewrittenPushedCommitStopsTheTaskBeforeThePushWithNoRestartAndLeavesACleanWorktree(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	commitAs := "git -c user.name=agent -c user.email=agent@example.com commit -q -m 'Add plan model'"
	rewrites := fmt.Sprintf("[[prompts]]\nshell = '''echo rewritten > plan.txt && git add plan.txt && %s && other=$(mktemp -d) && git clone -q --branch=main '%s' \"$other\" && cd \"$other\" && echo pushed > plan.txt && git add plan.txt && %s && git push -q origin HEAD:refs/heads/mobius/41'''\n", commitAs, fake.Remote(shop), commitAs)
	server, dataDir := connectTask(t, fake, leadStarts, rewrites, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")

	prompt := waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": the local branch diverged from origin/mobius/41, and the merge had conflicts. Mobius pushed nothing")
	if !strings.Contains(prompt, "merge conflict in plan.txt") || !strings.Contains(prompt, "Resume gives the same conflict. First, in "+worktree+", merge origin/mobius/41 into the local branch") {
		t.Errorf("prompt = %s", prompt)
	}
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if status := testkit.Git(t, worktree, "status", "--porcelain"); status != "" {
		t.Errorf("status = %s", status)
	}
	sessions := roleSessions(t, server, engine.ImplementerRole)
	if len(sessions) != 1 || sessions[0].EndReason.String != "merge_conflict" {
		t.Errorf("sessions = %+v", sessions)
	}
	if pullRequests := fake.PullRequests(shop); len(pullRequests) != 0 {
		t.Errorf("pull requests = %+v", pullRequests)
	}
}

func TestACommitOnTheBranchDuringARoundMergesBeforeThePush(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, commits, noChange)
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	testkit.WaitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(worktree, "plan.txt"))
		return err == nil
	})

	fake.PushCommit(shop, "mobius/41", "Update the UI screenshots")
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	runs := checkRuns(t, fake)
	if runs[0].HeadSHA != head(t, fake, "mobius/41") {
		t.Errorf("check runs = %+v", runs)
	}
	log := testkit.Git(t, fake.Remote(shop), "log", "--format=%s", "mobius/41")
	if !strings.Contains(log, "Add plan model") || !strings.Contains(log, "Update the UI screenshots") {
		t.Errorf("log = %s", log)
	}
	if session := endedImplementers(t, server, 1)[0]; session.EndReason.String != "done" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
}

func TestAPushThatGitHubRejectsStopsTheTaskWithNoRestart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	// GitHub refuses each push to a hidden ref.
	testkit.Git(t, fake.Remote(shop), "config", "receive.hideRefs", "refs/heads/mobius")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	prompt := waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": GitHub rejected the push.")
	if !strings.Contains(prompt, "deny updating a hidden ref") || !strings.Contains(prompt, "[remote rejected]") {
		t.Errorf("prompt = %s", prompt)
	}
	if !hasLabel(fake, "mobius:needs-human") || hasLabel(fake, "mobius:working") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if len(fake.PullRequests(shop)) != 0 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
	if session := endedImplementers(t, server, 1)[0]; session.EndReason.String != "push_rejected" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 {
		t.Errorf("sessions = %+v", sessions)
	}
}

func TestASecondTaskOfTheIssueGetsTheNextFreeBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"cannot_do on #41\"\ncall = { tool = \"decline\", arguments = { n = 41, reason = \"Split it.\" } }\n\n" + leadStarts
	implementer := "[[prompts]]\n" + cannotDoCall + "\n[[prompts]]\nwhen = \"Start again.\"\n" + commitCents
	server, dataDir := connectTask(t, fake, lead, implementer, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	endedImplementers(t, server, 1)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	fake.AddComment(shop, 41, "owner", "Start again.")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	pullRequests := testkit.WaitForValue(t, func() ([]testkit.PullRequest, bool) {
		pullRequests := fake.PullRequests(shop)
		return pullRequests, len(pullRequests) > 0
	})
	if len(pullRequests) != 1 || pullRequests[0].Head != "mobius/41-2" {
		t.Errorf("pull requests = %+v", pullRequests)
	}
	if task := liveTask(t, server, 41); task.Branch.String != "mobius/41-2" {
		t.Errorf("task = %+v", task)
	}
	if branch := testkit.Git(t, filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41"), "branch", "--show-current"); branch != "mobius/41-2" {
		t.Errorf("branch = %s", branch)
	}
}

func TestAFailedCheckGoesBackToTheSameImplementerWithTheOutput(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\n"+commitDollar+"\n[[prompts]]\n"+fixCents, noChange)
	fake.SetCheck(shop, "gh auth status\ngrep cents plan.txt || echo 'plan.txt has no cents.'\ngrep -q cents plan.txt")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	runs := approvalCheckRuns(t, server, fake)
	if want := []testkit.CheckRun{{Name: "Mobius", HeadSHA: head(t, fake, "mobius/41"), Status: "in_progress"}}; !reflect.DeepEqual(runs, want) {
		t.Errorf("check runs = %+v", runs)
	}
	if plan := testkit.Git(t, fake.Remote(shop), "show", "mobius/41:plan.txt"); plan != "cents" {
		t.Errorf("plan.txt = %s", plan)
	}
	if len(fake.PullRequests(shop)) != 1 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
	session := endedImplementers(t, server, 1)[0]
	if session.EndReason.String != "done" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 2 {
		t.Fatalf("prompts = %q", prompts)
	}
	for _, part := range []string{"The local check `.mobius/check` failed.", "plan.txt has no cents.", "Do not use gh. Use the Mobius tools."} {
		if !strings.Contains(prompts[1], part) {
			t.Errorf("%q is not in %s", part, prompts[1])
		}
	}
}

func TestAfterMaxCheckAttemptsMobiusPushesFailsTheCheckRunAndStopsTheTask(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, func(cfg *config.Config) { cfg.CheckTimeout = 300 * time.Millisecond })
	fake.SetCheck(shop, "echo 'tests failed'\nsleep 10")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": .mobius/check failed 3 times.")
	runs := fake.CheckRuns(shop)
	if len(runs) != 1 || runs[0].HeadSHA != head(t, fake, "mobius/41") || runs[0].Status != "completed" || runs[0].Conclusion != "failure" {
		t.Fatalf("check runs = %+v", runs)
	}
	if runs[0].Output.Title != "Local check failed" || !strings.Contains(runs[0].Output.Summary, "tests failed\n\n.mobius/check did not end in 300ms.") {
		t.Errorf("output = %+v", runs[0].Output)
	}
	if len(fake.PullRequests(shop)) != 1 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
	if !hasLabel(fake, "mobius:needs-human") || hasLabel(fake, "mobius:working") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if !pullRequestHasNeedsHuman(fake) {
		t.Errorf("labels of the pull request = %v", fake.Labels(shop, pullRequestNumber))
	}
	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if sessions := roleSessions(t, server, engine.ReviewerRole); len(sessions) != 0 {
		t.Errorf("Reviewers = %+v", sessions)
	}
	session := endedImplementers(t, server, 1)[0]
	if session.EndReason.String != "check_failed" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 3 || !strings.Contains(prompts[2], "tests failed") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestACheckOnAFullDiskWaitsForFreeSpaceWithNoPromptAndNoAttemptAndThenPushes(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	testkit.SetFreeSpace(t, dataDir, 0)
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, commits, func(cfg *config.Config) {
		cfg.MaxCheckAttempts = 1
		cfg.HousekeeperInterval = 100 * time.Millisecond
	})
	free := filepath.Join(dataDir, "harnesses", "df.free")
	fake.SetCheck(shop, fmt.Sprintf("if [ \"$(cat '%s')\" = 0 ]; then echo 'error: No space left on device (os error 28)'; exit 1; fi", free))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	item := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range inbox(t, server) {
			if item.Kind == "full disk" {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	if want := "The disk of the Mobius server is full. The .mobius/check of #41 \"Add plan model\" waits for 20 GiB of free space. The disk has 0 GiB of free space."; item.Text != want {
		t.Errorf("text = %s", item.Text)
	}
	if item.Issue != 41 || item.Link != "https://github.com/owner/shop/issues/41" {
		t.Errorf("item = %+v", item)
	}
	if state := taskState(t, server); state != "working" {
		t.Errorf("state = %s", state)
	}
	if len(fake.PullRequests(shop)) != 0 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
	if session := roleSessions(t, server, engine.ImplementerRole)[0]; session.EndedAt.Valid {
		t.Errorf("session = %+v", session)
	}

	testkit.SetFreeSpace(t, dataDir, 20<<20)

	runs := approvalCheckRuns(t, server, fake)
	if len(runs) != 1 || runs[0].Status != "in_progress" {
		t.Errorf("check runs = %+v", runs)
	}
	if len(fake.PullRequests(shop)) != 1 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
	if plan := testkit.Git(t, fake.Remote(shop), "show", "mobius/41:plan.txt"); plan != "cents" {
		t.Errorf("plan.txt = %s", plan)
	}
	if hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	testkit.WaitFor(t, func() bool {
		return !slices.ContainsFunc(inbox(t, server), func(item inboxItem) bool { return item.Kind == "full disk" })
	})
	session := endedImplementers(t, server, 1)[0]
	if session.EndReason.String != "done" || len(promptTexts(t, server, session.ID)) != 1 {
		t.Errorf("session = %+v, prompts = %q", session, promptTexts(t, server, session.ID))
	}
}

// dispatchTwo adds #43 below the Workstream #12, and the Owner adds mobius:ready to #41 and #43.
func dispatchTwo(fake *testkit.FakeGitHub) {
	fake.AddIssue(shop, 43, "Add plan price")
	fake.AddSubIssue(shop, 12, 43)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
}

func TestWithOneImplementerSlotTheSecondImplementerWaitsInTheQueueUntilTheFirstEnds(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStartsTwo, commits, func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 })
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))

	dispatchTwo(fake)

	waiting := testkit.WaitForValue(t, func() (store.Session, bool) {
		for _, session := range roleSessions(t, server, engine.ImplementerRole) {
			if strings.HasPrefix(session.QueueReason.String, "no free") {
				return session, true
			}
		}
		return store.Session{}, false
	})
	if waiting.QueueReason.String != "no free implementer slot (1/1)" {
		t.Errorf("queue reason = %s", waiting.QueueReason.String)
	}
	// The first task takes its slot before it writes the state working.
	testkit.WaitFor(t, func() bool {
		var states []string
		for _, number := range []int64{41, 43} {
			states = append(states, liveTask(t, server, number).State)
		}
		slices.Sort(states)
		return slices.Equal(states, []string{"queued", "working"})
	})
	testkit.WaitFor(t, func() bool {
		var states []string
		for _, line := range taskTab(t, server) {
			states = append(states, line.State)
		}
		slices.Sort(states)
		return slices.Equal(states, []string{"queued", "working"})
	})

	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	ended := endedImplementers(t, server, 2)
	first, second := ended[0], ended[1]
	if first.ID == waiting.ID {
		first, second = second, first
	}
	if first.EndReason.String != "done" || second.EndReason.String != "done" {
		t.Errorf("sessions = %+v, %+v", first, second)
	}
	startsAfter(t, second, first)
	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 2 })
}

// taskTab gives the lines of the Tasks tab of the Workstream #12.
func taskTab(t *testing.T, server *testserver.Server) []engine.TaskLine {
	t.Helper()
	lines, err := server.Engine.Tasks(t.Context(), shop, 12)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestWithOneCheckSlotTheLocalChecksDoNotRunAtTheSameTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStartsTwo, commits, func(cfg *config.Config) { cfg.MaxChecks = 1 })
	log := filepath.Join(dataDir, "checks.log")
	fake.SetCheck(shop, fmt.Sprintf("echo start >> '%[1]s'\nsleep 0.2\necho end >> '%[1]s'", log))

	dispatchTwo(fake)

	for _, session := range endedImplementers(t, server, 2) {
		if session.EndReason.String != "done" {
			t.Errorf("session = %+v", session)
		}
	}
	if text, err := os.ReadFile(filepath.Clean(log)); err != nil || string(text) != "start\nend\nstart\nend\n" {
		t.Errorf("checks.log = %q, %v", text, err)
	}
}

func TestASessionShowsEachPhaseOfItsCheck(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStartsTwo, commits, func(cfg *config.Config) { cfg.MaxChecks = 1 })
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))

	dispatchTwo(fake)

	phases := func() []string {
		var reasons []string
		for _, session := range roleSessions(t, server, engine.ImplementerRole) {
			reasons = append(reasons, session.QueueReason.String)
		}
		slices.Sort(reasons)
		return reasons
	}
	testkit.WaitFor(t, func() bool {
		return slices.Equal(phases(), []string{"runs .mobius/check", "waits for a check slot"})
	})
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ended := endedImplementers(t, server, 2)
	var texts [][]string
	for _, session := range ended {
		var lines []string
		for _, row := range rows(t, server, session.ID, "check") {
			text := row["text"].(string)
			if strings.HasPrefix(text, ".mobius/check passed in ") {
				text = ".mobius/check passed in"
			}
			lines = append(lines, text)
		}
		texts = append(texts, lines)
		if session.QueueReason.Valid {
			t.Errorf("session = %+v", session)
		}
	}
	slices.SortFunc(texts, func(a, b []string) int { return len(a) - len(b) })
	want := [][]string{
		{".mobius/check started.", ".mobius/check passed in"},
		{"The session waits for a check slot.", ".mobius/check started.", ".mobius/check passed in"},
	}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("check rows = %q", texts)
	}
	lines := transcript(t, server, ended[0].ID)
	if !slices.ContainsFunc(lines, func(line engine.Line) bool { return line.Kind == "check" && line.Text == ".mobius/check started." }) {
		t.Errorf("transcript = %+v", lines)
	}
}

func TestAPushThatFailsAfterAPassedCheckTriesAgainWithNoNewSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 20 })
	remote := fake.Remote(shop)
	fake.SetCheck(shop, fmt.Sprintf("mv '%[1]s' '%[1]s.away'", remote))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool {
		var restarts int
		if err := server.DB.QueryRow("SELECT coalesce(max(worker_restarts), 0) FROM tasks WHERE issue = 41").Scan(&restarts); err != nil {
			t.Fatal(err)
		}
		return restarts > 0
	})
	if err := os.Rename(remote+".away", remote); err != nil {
		t.Fatal(err)
	}

	runs := checkRuns(t, fake)
	if runs[0].HeadSHA != head(t, fake, "mobius/41") || len(fake.PullRequests(shop)) != 1 {
		t.Errorf("check runs = %+v, pull requests = %+v", runs, fake.PullRequests(shop))
	}
	session := endedImplementers(t, server, 1)[0]
	if results := stepResults(stepRows(t, server, "pull")); len(results) < 2 || results[0] != "fail" || results[len(results)-1] != "" {
		t.Errorf("pull results = %q", results)
	}
	if session.EndReason.String != "done" || len(promptTexts(t, server, session.ID)) != 1 {
		t.Errorf("session = %+v, prompts = %q", session, promptTexts(t, server, session.ID))
	}
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 {
		t.Errorf("sessions = %+v", sessions)
	}
}

func TestSendDetailsStopsTheTurnAndSendsTheDetailsInTheSameSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"Send the details\"\ncall = { tool = \"send_details\", arguments = { n = 41, text = \"Round prices down.\" } }\n\n" + leadStarts
	implementer := "[[prompts]]\nreply = [\"Working.\"]\nhang = true\n\n[[prompts]]\nwhen = \"The Owner gave new details\"\n" + commitCents
	server, _ := connectTask(t, fake, lead, implementer, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) != 1 {
			return store.Session{}, false
		}
		return sessions[0], reply(t, server, sessions[0].ID) == "Working."
	})

	sendChat(t, server, leadChat, "Send the details to #41.")

	approvalCheckRuns(t, server, fake)
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Errorf("sessions = %+v", sessions)
	}
	if ended := endedImplementers(t, server, 1)[0]; ended.EndReason.String != "done" {
		t.Errorf("end reason = %s", ended.EndReason.String)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 2 {
		t.Fatalf("prompts = %q", prompts)
	}
	if want := "The Owner gave new details for the task. They replace the old text where they differ.\n\nRound prices down."; prompts[1] != want {
		t.Errorf("prompt = %q", prompts[1])
	}
	if plan := testkit.Git(t, fake.Remote(shop), "show", "mobius/41:plan.txt"); plan != "cents" {
		t.Errorf("plan.txt = %s", plan)
	}
	if len(fake.PullRequests(shop)) != 1 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
}

func TestAFetchThatStallsFailsTheWorkerAndTheWorkerRestarts(t *testing.T) {
	limit, seconds := runner.LowSpeedLimit, runner.LowSpeedTime
	runner.LowSpeedLimit, runner.LowSpeedTime = 1000, 1
	t.Cleanup(func() { runner.LowSpeedLimit, runner.LowSpeedTime = limit, seconds })
	fake := testkit.NewFakeGitHub(t)
	var database atomic.Pointer[sql.DB]
	files := http.FileServer(http.Dir(fake.Remote(shop)))
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var started int
		if db := database.Load(); db != nil {
			if err := db.QueryRow("SELECT count(*) FROM tasks WHERE state IN ('queued', 'working')").Scan(&started); err != nil {
				t.Error(err)
			}
		}
		if started > 0 {
			<-r.Context().Done()
			return
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(remote.Close)
	server, _ := connectTask(t, fake, leadStarts, commits, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 20 })
	database.Store(server.DB)
	fake.SetCloneURL(shop, remote.URL)
	cmd := exec.Command("git", "update-server-info")
	cmd.Dir = fake.Remote(shop)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-server-info: %v: %s", err, out)
	}
	waitForPolls(t, fake)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool {
		var restarts int
		if err := server.DB.QueryRow("SELECT coalesce(max(worker_restarts), 0) FROM tasks WHERE issue = 41").Scan(&restarts); err != nil {
			t.Fatal(err)
		}
		return restarts > 0
	})
}

func TestCannotDoIsRefusedWhenTheWorktreeHasWorkThatMobiusDidNotPush(t *testing.T) {
	t.Parallel()
	const refusal = "error: Your worktree has work that Mobius did not push. Commit your work and end the turn normally."
	for name, shell := range map[string]string{
		"an unpushed commit":    commitCents,
		"an uncommitted change": "shell = \"echo cents > plan.txt\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			fake := testkit.NewFakeGitHub(t)
			server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\n"+shell+cannotDoCall, noChange)
			fake.AddLabel(shop, 41, "mobius:ready", "owner")

			ended := endedImplementers(t, server, 1)[0]

			if ended.EndReason.String != "done" {
				t.Errorf("end reason = %s", ended.EndReason.String)
			}
			if got := reply(t, server, ended.ID); !strings.Contains(got, refusal) {
				t.Errorf("reply = %q", got)
			}
			if state := taskState(t, server); state == "dispatched" {
				t.Errorf("state = %s", state)
			}
		})
	}
}
