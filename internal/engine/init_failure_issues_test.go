package engine_test

import (
	"fmt"
	"path/filepath"
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

const failingInit = "echo 'pnpm install failed'\nexit 1"

func openInitFailures(t *testing.T, server *testserver.Server) int {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM init_failures WHERE ended_at = ''").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// endedImplementersOf41 waits for count ended Implementer sessions of #41.
func endedImplementersOf41(t *testing.T, server *testserver.Server, count int) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		ended := slices.DeleteFunc(roleSessions(t, server, engine.ImplementerRole), func(session store.Session) bool {
			return !session.EndedAt.Valid || session.Issue.Int64 != 41
		})
		return len(ended) == count
	})
}

// connectRetriedTask is connectTaskIn with an Implementer that calls cannot_do and a Lead that starts it again for a
// comment on #41. Its worktree can be removed after the first round, so that the next round makes it again.
func connectRetriedTask(t *testing.T, fake *testkit.FakeGitHub, dataDir string) *testserver.Server {
	t.Helper()
	lead := "[[prompts]]\nwhen = \"comment on #41\"\n" + startImplementer + "\n" + leadStarts
	server, _ := connectTaskIn(t, fake, dataDir, lead, "[[prompts]]\nwhen = \"Try again\"\nreply = [\"ok\"]\n\n[[prompts]]\n"+cannotDoCall, noChange)
	return server
}

func retryTask(t *testing.T, fake *testkit.FakeGitHub, dataDir string, rounds int, server *testserver.Server) {
	t.Helper()
	endedImplementersOf41(t, server, rounds)
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if err := runner.RemoveWorktree(t.Context(), dataDir, shop, worktree); err != nil {
		t.Fatal(err)
	}
	fake.AddComment(shop, 41, "owner", "Try again.")
	endedImplementersOf41(t, server, rounds+1)
}

func TestAFailedInitScriptGivesOneIssueBelowTheMaintenanceWorkstreamWithTheFields(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	server, dataDir := connectTask(t, fake, leadStarts, commits, noChange)
	fake.SetInit(shop, failingInit)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	number, body := checkTimeoutIssue(t, fake)
	if title, _ := fake.Issue(shop, number); title != "Fix the setup script .mobius/init of owner/shop" {
		t.Errorf("title = %q", title)
	}
	session := endedImplementers(t, server, 1)[0]
	commit, err := runner.RevParse(t.Context(), dataDir, fake.Remote(shop), "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"The setup script `.mobius/init` failed.",
		"Repository: owner/shop",
		"Task issue: https://github.com/owner/shop/issues/41",
		fmt.Sprintf("Session: %d", session.ID),
		"Commit: " + commit,
		"pnpm install failed",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not have %q:\n%s", want, body)
		}
	}
	if task := liveTaskOf(t, server, number); task.Workstream != maintenance {
		t.Errorf("workstream = %d", task.Workstream)
	}
	waitForPolls(t, fake)
	if created := createdIssues(fake); created != 1 {
		t.Errorf("created issues = %d", created)
	}
}

func TestAnInitScriptThatDoesNotEndInTheTimeLimitGivesAnIssueWithTheTimeLimit(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	server, _ := connectTask(t, fake, leadStarts, commits, func(cfg *config.Config) { cfg.CheckTimeout = 300 * time.Millisecond })
	fake.SetInit(shop, "echo 'pnpm install is slow'\nsleep 10")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	_, body := checkTimeoutIssue(t, fake)
	for _, want := range []string{"The setup script `.mobius/init` did not end in the time limit.", "Time limit: 300ms", "pnpm install is slow"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not have %q:\n%s", want, body)
		}
	}
	endedImplementers(t, server, 1)
}

func TestAFailedInitScriptWithoutAMaintenanceWorkstreamGivesNoIssueAndTheRecordEnds(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.SetInit(shop, failingInit)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	endedImplementers(t, server, 1)
	testkit.WaitFor(t, func() bool {
		var count int
		err := server.DB.QueryRow("SELECT count(*) FROM init_failures WHERE repository = 'owner/shop' AND issue = 0 AND ended_at != ''").Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		return count == 1
	})
	waitForPolls(t, fake)
	if created := createdIssues(fake); created != 0 {
		t.Errorf("created issues = %d", created)
	}
}

func TestASecondInitFailureInANewWorktreeWhileTheRecordIsOpenGivesNoSecondIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	dataDir := t.TempDir()
	server := connectRetriedTask(t, fake, dataDir)
	fake.SetInit(shop, failingInit)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	number := ciBreakIssue(t, fake)

	retryTask(t, fake, dataDir, 1, server)

	rows := stepRows(t, server, "init")
	if failures := slices.DeleteFunc(rows, func(row stepRow) bool { return row.Issue.Int64 != 41 }); len(failures) != 2 || !slices.Equal(stepResults(failures), []string{"fail", "fail"}) {
		t.Errorf("init steps = %+v", rows)
	}
	waitForPolls(t, fake)
	if created := createdIssues(fake); created != 1 {
		t.Errorf("created issues = %d", created)
	}
	if count := openInitFailures(t, server); count != 1 {
		t.Errorf("open failures = %d", count)
	}
	if numbers := fake.SubIssueNumbers(shop, maintenance); !slices.Equal(numbers, []int64{number}) {
		t.Errorf("sub-issues = %v", numbers)
	}
}

func TestAnInitFailureAfterTheIssueClosesGivesANewIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	dataDir := t.TempDir()
	server := connectRetriedTask(t, fake, dataDir)
	fake.SetInit(shop, failingInit)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	first := ciBreakIssue(t, fake)
	endedImplementersOf41(t, server, 1)
	fake.CloseIssue(shop, first)
	testkit.WaitFor(t, func() bool { return openInitFailures(t, server) == 0 })

	retryTask(t, fake, dataDir, 1, server)

	second := testkit.WaitForValue(t, func() (int64, bool) {
		numbers := fake.SubIssueNumbers(shop, maintenance)
		return numbers[len(numbers)-1], len(numbers) == 2
	})
	if second == first {
		t.Errorf("issue = %d", second)
	}
	if created := createdIssues(fake); created != 2 {
		t.Errorf("created issues = %d", created)
	}
}

func TestRepairsOffHoldsTheIssueOfAnInitFailureAndRepairsOnDispatchesIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.SetInit(shop, failingInit)
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
