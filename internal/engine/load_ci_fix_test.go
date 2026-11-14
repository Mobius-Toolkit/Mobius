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
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// useLoadTimes sets checkGap and loadPoll until the end of the test.
func useLoadTimes(t *testing.T, gap, poll time.Duration) {
	t.Helper()
	gapBefore, pollBefore := *engine.CheckGap, *engine.LoadPoll
	*engine.CheckGap, *engine.LoadPoll = gap, poll
	t.Cleanup(func() { *engine.CheckGap, *engine.LoadPoll = gapBefore, pollBefore })
}

// leadStartsFortyOneLast is leadStartsTwo for a test that dispatches #43 before #41: the first prompt of a new session
// has the earlier events in its history, so the rule of the later dispatch comes first.
var leadStartsFortyOneLast = leadStarts + strings.TrimSuffix(leadStartsTwo, leadStarts)

// connectWaitingChecks connects a server with three check slots and the Lead lead, in which the check of each task
// writes the branch of its task to a log. The issues #41 and #43 can start. It gives the path of the log.
func connectWaitingChecks(t *testing.T, fake *testkit.FakeGitHub, lead string) (*testserver.Server, string) {
	t.Helper()
	server, dataDir := connectTask(t, fake, lead, commits, func(cfg *config.Config) { cfg.MaxChecks = 3 })
	fake.AddIssue(shop, 43, "Add plan price")
	fake.AddSubIssue(shop, 12, 43)
	log := filepath.Join(dataDir, "checks.log")
	fake.SetCheck(shop, fmt.Sprintf("git rev-parse --abbrev-ref HEAD >> '%s'", log))
	return server, log
}

// waitForLowLoadOf waits until the check of the Implementer session of the issue number waits for a low load, and
// gives the session.
func waitForLowLoadOf(t *testing.T, server *testserver.Server, number int64) store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := issueImplementers(t, server, number)
		if len(sessions) != 1 {
			return store.Session{}, false
		}
		return sessions[0], sessions[0].QueueReason.String == "waits for a low load"
	})
}

// startedBranches gives the branches of the checks that started, in their order.
func startedBranches(t *testing.T, log string) []string {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(log))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(text))
}

// testACIFixCheckThatWaitsSecondStartsFirst waits for the check of #41, then for the check of #43, whose issue has the
// record. The load average is low from the read after the next read of the check of #41. Without an order, the check
// of #41 starts at that read.
func testACIFixCheckThatWaitsSecondStartsFirst(t *testing.T, record func(issue int64) string) {
	load := installLoad(t, highLoad())
	useLoadTimes(t, 300*time.Millisecond, time.Second)
	fake := testkit.NewFakeGitHub(t)
	server, log := connectWaitingChecks(t, fake, leadStartsTwo)
	addRecord(t, server, record(43))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForLowLoadOf(t, server, 41)
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForLowLoadOf(t, server, 43)

	// The next read is the read of #41, because it waited first. The read after it is the read of #43.
	load.lowFrom.Store(load.reads.Load() + 1)

	endedImplementers(t, server, 2)
	if got, want := startedBranches(t, log), []string{"mobius/43", "mobius/41"}; !slices.Equal(got, want) {
		t.Errorf("checks = %q, want %q", got, want)
	}
}

// Serial: the test changes loadAverage, checkGap and loadPoll.
func TestTheCheckOfAnEndedHardBreakTaskThatWaitsSecondStartsFirst(t *testing.T) {
	testACIFixCheckThatWaitsSecondStartsFirst(t, endedHardBreakSQL)
}

// Serial: the test changes loadAverage, checkGap and loadPoll.
func TestTheCheckOfAFlakyBreakTaskThatWaitsSecondStartsFirst(t *testing.T) {
	testACIFixCheckThatWaitsSecondStartsFirst(t, flakyBreakSQL)
}

// Serial: the test changes loadAverage, checkGap and loadPoll.
func TestTheCheckOfACIFailureRecordTaskThatWaitsSecondStartsFirst(t *testing.T) {
	testACIFixCheckThatWaitsSecondStartsFirst(t, openCIFailureSQL)
}

// Serial: the test changes loadAverage, checkGap and loadPoll.
func TestTwoChecksOfCIFixTasksStartInTheOrderOfTheirWaitStart(t *testing.T) {
	load := installLoad(t, highLoad())
	useLoadTimes(t, 300*time.Millisecond, time.Second)
	fake := testkit.NewFakeGitHub(t)
	server, log := connectWaitingChecks(t, fake, leadStartsTwo)
	addRecord(t, server, openCIFailureSQL(41))
	addRecord(t, server, flakyBreakSQL(43))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForLowLoadOf(t, server, 41)
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForLowLoadOf(t, server, 43)

	// The next read is the read of #41, and the read after it is the read of #43. Without an order, the check of
	// #43 starts at its read.
	load.lowFrom.Store(load.reads.Load() + 2)

	endedImplementers(t, server, 2)
	if got, want := startedBranches(t, log), []string{"mobius/41", "mobius/43"}; !slices.Equal(got, want) {
		t.Errorf("checks = %q, want %q", got, want)
	}
}

// Serial: the test changes loadAverage, checkGap and loadPoll.
func TestACheckThatHasNoWaitYetDoesNotTakeTheStartFromAWaitingCIFixCheck(t *testing.T) {
	load := installLoad(t, highLoad())
	useLoadTimes(t, 300*time.Millisecond, 3*time.Second)
	fake := testkit.NewFakeGitHub(t)
	server, log := connectWaitingChecks(t, fake, leadStartsFortyOneLast)
	addRecord(t, server, openCIFailureSQL(43))
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForLowLoadOf(t, server, 43)

	load.set(0)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	endedImplementers(t, server, 2)
	if got, want := startedBranches(t, log), []string{"mobius/43", "mobius/41"}; !slices.Equal(got, want) {
		t.Errorf("checks = %q, want %q", got, want)
	}
}

// Serial: the test changes loadAverage, checkGap and loadPoll.
func TestChecksWithNoCIFixTaskStartWhenTheLoadIsLow(t *testing.T) {
	load := installLoad(t, highLoad())
	useLoadTimes(t, 100*time.Millisecond, 50*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	server, log := connectWaitingChecks(t, fake, leadStartsTwo)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForLowLoadOf(t, server, 41)
	waitForLowLoadOf(t, server, 43)

	load.set(0)

	endedImplementers(t, server, 2)
	if got := startedBranches(t, log); len(got) != 2 || !slices.Contains(got, "mobius/41") || !slices.Contains(got, "mobius/43") {
		t.Errorf("checks = %q", got)
	}
}

// Serial: the test changes loadAverage, checkGap and loadPoll.
func TestStartCheckNowStartsACheckWithNoCIFixTaskAtOnceWhileACIFixCheckWaits(t *testing.T) {
	load := installLoad(t, highLoad())
	useLoadTimes(t, 300*time.Millisecond, 50*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	server, log := connectWaitingChecks(t, fake, leadStartsTwo)
	addRecord(t, server, openCIFailureSQL(43))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	session := waitForLowLoadOf(t, server, 41)
	waitForLowLoadOf(t, server, 43)

	if err := server.Engine.StartCheckNow(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}

	waitForImplementerEnd(t, server, 41, 1)
	if got, want := startedBranches(t, log), []string{"mobius/41"}; !slices.Equal(got, want) {
		t.Errorf("checks = %q, want %q", got, want)
	}
	if got := waitForLowLoadOf(t, server, 43); got.EndedAt.Valid {
		t.Errorf("session = %+v", got)
	}
	load.set(0)
	endedImplementers(t, server, 2)
}
