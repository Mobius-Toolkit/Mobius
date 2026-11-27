package engine_test

import (
	"slices"
	"strings"
	"testing"
	"time"

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
	// The held read of #41 stops the dispatch of the first server, so the Start stays in the database.
	t.Cleanup(fake.HoldNext("GET /repos/{owner}/{repo}/issues/41"))
	if err := first.Engine.StartIssue(t.Context(), shop, 41); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(fake.Requests(), func(request testkit.Request) bool {
			return request.Status == 0 && strings.HasSuffix(request.Path, "/issues/41")
		})
	})
	first.Stop()
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
