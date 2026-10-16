package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const hangingCheck = "echo 'FAIL TestSlow'\nsleep 10"

func timeoutConfig(attempts int) func(*config.Config) {
	return func(cfg *config.Config) {
		cfg.CheckTimeout = 300 * time.Millisecond
		cfg.MaxCheckAttempts = attempts
	}
}

func openCheckTimeouts(t *testing.T, server *testserver.Server) int {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM check_timeouts WHERE ended_at = ''").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func checkTimeoutIssue(t *testing.T, fake *testkit.FakeGitHub) (int64, string) {
	t.Helper()
	number := ciBreakIssue(t, fake)
	_, body := fake.Issue(shop, number)
	return number, body
}

func TestATimeoutOfTheCheckSendsNoFixPromptAndRunsTheCheckAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, commits, timeoutConfig(3))
	marker := filepath.Join(dataDir, "timed-out")
	fake.SetCheck(shop, fmt.Sprintf("if [ ! -f '%s' ]; then touch '%s'; sleep 10; fi", marker, marker))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)
	rows := waitForSteps(t, server, "check", 2)
	if !slices.Equal(stepResults(rows), []string{"timeout", "pass"}) || !slices.Equal(stepAttempts(rows), []int64{1, 2}) {
		t.Errorf("rows = %+v", rows)
	}
	session := endedImplementers(t, server, 1)[0]
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 1 {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestACheckThatTimesOutMaxCheckAttemptsTimesStopsTheTaskAndGivesOneIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	server, _ := connectTask(t, fake, leadStarts, commits, timeoutConfig(3))
	fake.SetCheck(shop, hangingCheck)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": .mobius/check failed 3 times.")
	rows := waitForSteps(t, server, "check", 3)
	if !slices.Equal(stepResults(rows), []string{"timeout", "timeout", "timeout"}) || !slices.Equal(stepAttempts(rows), []int64{1, 2, 3}) {
		t.Errorf("rows = %+v", rows)
	}
	session := endedImplementers(t, server, 1)[0]
	if session.EndReason.String != "check_failed" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 1 {
		t.Errorf("prompts = %q", prompts)
	}
	ciBreakIssue(t, fake)
	waitForPolls(t, fake)
	if created := createdIssues(fake); created != 1 {
		t.Errorf("created issues = %d", created)
	}
}

func TestTheFirstTimeoutOfTheCheckGivesAnIssueThatTheUsualFlowDispatchesWithAutopilotOff(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	server, _ := connectTask(t, fake, leadStarts, commits, timeoutConfig(1))
	fake.SetCheck(shop, hangingCheck)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	number, body := checkTimeoutIssue(t, fake)
	if title, _ := fake.Issue(shop, number); title != "Examine the time of the local check of owner/shop" {
		t.Errorf("title = %q", title)
	}
	session := endedImplementers(t, server, 1)[0]
	for _, want := range []string{
		"Task issue: https://github.com/owner/shop/issues/41",
		fmt.Sprintf("Session: %d", session.ID),
		"Time limit: 300ms",
		"FAIL TestSlow",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not have %q:\n%s", want, body)
		}
	}
	if labels := fake.Labels(shop, number); slices.Contains(labels, "mobius:triage") {
		t.Errorf("labels = %v", labels)
	}
	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("workstream = %d", task.Workstream)
	}
	if breaks := ciBreaks(t, server); len(breaks) != 0 {
		t.Errorf("CI breaks = %+v", breaks)
	}
}

func TestTheIssueOfATimeoutHasNoLocalPath(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	dataDir := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	connectTaskIn(t, fake, dataDir, leadStarts, commits, timeoutConfig(1))
	fake.SetCheck(shop, fmt.Sprintf("echo 'error in %s/plan.txt'\necho 'data %s/harnesses'\necho 'home %s/.cache'\nsleep 10", worktree, dataDir, home))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	_, body := checkTimeoutIssue(t, fake)
	for _, want := range []string{"error in <worktree>/plan.txt", "data <data_dir>/harnesses", "home <home>/.cache"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not have %q:\n%s", want, body)
		}
	}
	for _, path := range []string{worktree, dataDir, home} {
		if strings.Contains(body, path) {
			t.Errorf("body has the path %s:\n%s", path, body)
		}
	}
}

func TestATimeoutWhileTheIssueOfAnEarlierTimeoutIsOpenGivesNoIssueAfterARestart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddIssue(shop, 30, "Examine the time of the local check of owner/shop")
	dataDir := t.TempDir()
	seed(t, dataDir, `INSERT INTO check_timeouts (repository, task_issue, session, timeout, output, issue, created_at)
		VALUES ('owner/shop', 41, 1, '15m0s', 'FAIL TestSlow', 30, '2026-10-04T10:00:00Z')`)
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, commits, timeoutConfig(1))
	fake.SetCheck(shop, hangingCheck)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": .mobius/check failed 1 times.")
	waitForPolls(t, fake)
	if created := createdIssues(fake); created != 0 {
		t.Errorf("created issues = %d", created)
	}
	if count := openCheckTimeouts(t, server); count != 1 {
		t.Errorf("open timeouts = %d", count)
	}
}

func TestATimeoutAfterTheIssueClosesGivesANewIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	fake.AddIssue(shop, 30, "Examine the time of the local check of owner/shop")
	fake.CloseIssue(shop, 30)
	dataDir := t.TempDir()
	seed(t, dataDir, `INSERT INTO check_timeouts (repository, task_issue, session, timeout, output, issue, created_at)
		VALUES ('owner/shop', 41, 1, '15m0s', 'FAIL TestSlow', 30, '2026-10-04T10:00:00Z')`)
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, commits, timeoutConfig(1))
	testkit.WaitFor(t, func() bool { return openCheckTimeouts(t, server) == 0 })
	fake.SetCheck(shop, hangingCheck)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	number, _ := checkTimeoutIssue(t, fake)
	if number == 30 {
		t.Errorf("issue = %d", number)
	}
	if created := createdIssues(fake); created != 1 {
		t.Errorf("created issues = %d", created)
	}
}

func TestATimeoutWithoutAMaintenanceWorkstreamGivesNoIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, timeoutConfig(1))
	fake.SetCheck(shop, hangingCheck)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": .mobius/check failed 1 times.")
	testkit.WaitFor(t, func() bool { return openCheckTimeouts(t, server) == 0 })
	waitForPolls(t, fake)
	if created := createdIssues(fake); created != 0 {
		t.Errorf("created issues = %d", created)
	}
}
