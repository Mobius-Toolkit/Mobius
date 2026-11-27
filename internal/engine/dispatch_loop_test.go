package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// startWithHourlyPoll copies the Workstreams of fake with a first server, and then restarts the server on the same
// data with a Lead that answers each prompt and a poll interval of one hour. It returns after the first poll of the
// restart, so only the dispatch loop can act on a Start.
func startWithHourlyPoll(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nreply = [\"Seen\"]\n")
	first := startServer(t, fake, dataDir, "")
	first.WaitForFirstPoll(t, shop)
	first.Stop()
	cfg := testserver.Config(t, dataDir)
	cfg.PollInterval = time.Hour
	server := testserver.StartWith(t, cfg, fake.URL)
	testkit.WaitFor(t, server.Engine.PollEnded)
	return server
}

func TestAStartFromTheUIDispatchesTheTaskBeforeTheNextPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startWithHourlyPoll(t, fake)

	if err := server.Engine.StartIssue(t.Context(), shop, 41); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })
	if hasStart(t, server, 41) {
		t.Errorf("start_requested_by = %q", startByOf(t, server, shop, 41))
	}
}

func TestAReadyIssueWithAStoredStartDispatchesAfterARestartWhileTheFirstPollWaitsForGitHub(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nreply = [\"Seen\"]\n")
	first := startServer(t, fake, dataDir, "")
	first.WaitForFirstPoll(t, shop)
	// The failed reads of #41 stop the dispatch of the first server, so the Start stays in the database.
	fake.FailIssueReads(shop, 41, 1000)
	if err := first.Engine.StartIssue(t.Context(), shop, 41); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(fake.Requests(), func(request testkit.Request) bool {
			return request.Status == http.StatusInternalServerError && strings.HasSuffix(request.Path, "/issues/41")
		})
	})
	first.Stop()
	fake.FailIssueReads(shop, 41, 0)
	t.Cleanup(fake.HoldNext("GET /repos/{owner}/{repo}/issues"))

	second := testserver.Start(t, dataDir, fake.URL)

	testkit.WaitFor(t, func() bool { return taskState(t, second) == "dispatched" })
	if second.Engine.PollEnded() {
		t.Error("the first poll ended before the dispatch")
	}
}

func TestAFailedReadOfOneIssueDoesNotStopTheDispatchOfALaterIssueAndTheFailedIssueKeepsItsStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 42, "Add plan price")
	fake.AddSubIssue(shop, 12, 42)
	server := startWithHourlyPoll(t, fake)
	fake.FailIssueReads(shop, 41, 100)

	if err := server.Engine.StartIssue(t.Context(), shop, 41); err != nil {
		t.Fatal(err)
	}
	if err := server.Engine.StartIssue(t.Context(), shop, 42); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return taskStateOf(t, server, 42) == "dispatched" })
	if got := taskState(t, server); got != "" {
		t.Errorf("task state of #41 = %s", got)
	}
	if got := startByOf(t, server, shop, 41); got != "ui" {
		t.Errorf("start_requested_by of #41 = %q", got)
	}
}

func TestTheDispatchLoopWaitsForTheBlockerCopyOfThePollAfterAReadyLabelOfThePoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 43, "Price rounding")
	fake.AddSubIssue(shop, 12, 43)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = 'owner/shop' AND number IN (41, 43) AND workstream IS NOT NULL") == 2
	})
	release := fake.HoldNextOf("GET /repos/{owner}/{repo}/issues/{number}/dependencies/blocked_by", "/repos/owner/shop/issues/41/dependencies/blocked_by")
	t.Cleanup(release)
	// One poll must read the label and the blocker together.
	releaseList := fake.HoldNext("GET /repos/{owner}/{repo}/issues")
	t.Cleanup(releaseList)
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(fake.Requests(), func(request testkit.Request) bool {
			return request.Status == 0 && strings.HasSuffix(request.Path, "/issues")
		})
	})
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	fake.AddBlockedBy(shop, 41, 88)
	releaseList()
	testkit.WaitFor(t, func() bool {
		return startByOf(t, server, shop, 41) == "owner" && slices.ContainsFunc(fake.Requests(), func(request testkit.Request) bool {
			return request.Status == 0 && strings.HasSuffix(request.Path, "/issues/41/dependencies/blocked_by")
		})
	})
	reached, releaseIssue := fake.HoldIssue(shop, 41)
	t.Cleanup(releaseIssue)

	if err := server.Engine.StartIssue(t.Context(), shop, 43); err != nil {
		t.Fatal(err)
	}
	release()

	liveTaskOf(t, server, 43)
	testkit.WaitFor(t, func() bool { return blockersOf41(t, server, "open") == 1 })
	waitForPolls(t, fake)
	select {
	case <-reached:
		t.Error("the loop read #41 for a dispatch before the poll copied its blocker")
	default:
	}
	if hasLiveTask(t, server, 41) || startByOf(t, server, shop, 41) != "owner" {
		t.Errorf("live task = %t, start_requested_by = %q", hasLiveTask(t, server, 41), startByOf(t, server, shop, 41))
	}
}

func TestTheDispatchLoopAndAutopilotDispatchOneIssueOneTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	server := startAutopilot(t, fake, func(*config.Config) {}, "")
	server.WaitForFirstPoll(t, shop)
	turnStartOff(t, server)
	addTaskIssue(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = 'owner/shop' AND number = 41 AND workstream IS NOT NULL") == 1
	})
	reached, release := fake.HoldIssue(shop, 41)
	t.Cleanup(release)
	turnStartOn(t, server)
	<-reached
	if err := server.Engine.StartIssue(t.Context(), shop, 41); err != nil {
		t.Fatal(err)
	}
	// Without the lock, the loop dispatches the issue while Autopilot waits for its read.
	for deadline := time.Now().Add(2 * time.Second); taskState(t, server) == "" && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}

	release()

	liveTaskOf(t, server, 41)
	waitForPolls(t, fake)
	if got := countRows(t, server, "SELECT count(*) FROM tasks WHERE repository = 'owner/shop' AND issue = 41"); got != 1 {
		t.Errorf("tasks of #41 = %d", got)
	}
}
