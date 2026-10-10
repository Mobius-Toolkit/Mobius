package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// longCIEvents gives the number of turns of the Lead for a long CI event of #41.
func longCIEvents(t *testing.T, server *testserver.Server) int {
	t.Helper()
	count := 0
	for _, prompt := range leadPrompts(t, server) {
		parts := strings.Split(prompt, "# Event\n\n")
		if len(parts) > 1 && strings.Contains(parts[len(parts)-1], " the CI of #41 \"Add plan model\" runs for more than 2h0m0s") {
			count++
		}
	}
	return count
}

func TestACIThatRunsForMoreThanTwoHoursGivesTheLeadOneEventAndThenOneEventForEachTwoHours(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("lint", sha, "in_progress", ""))
	fake.AddCheckRun(shop, checkRun("done", sha, "completed", "success"))
	fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "build", HeadSHA: sha, Status: "waiting"})
	start := time.Now()

	server.Engine.SetClock(func() time.Time { return start.Add(time.Hour + 59*time.Minute) })
	waitForPolls(t, fake)

	if events := longCIEvents(t, server); events != 0 {
		t.Errorf("events = %d", events)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })

	prompt := waitForLeadPrompt(t, server, " the CI of #41 \"Add plan model\" runs for more than 2h0m0s")
	for _, want := range []string{"pull request #42 https://github.com/owner/shop/pull/42", "head " + sha, "did not complete: lint, build."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt = %s, want %q", prompt, want)
		}
	}
	waitForPolls(t, fake)
	if events := longCIEvents(t, server); events != 1 {
		t.Errorf("events = %d", events)
	}
	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s", state)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(4 * time.Hour) })
	waitForPolls(t, fake)

	if events := longCIEvents(t, server); events != 1 {
		t.Errorf("events = %d", events)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(4*time.Hour + 2*time.Minute) })

	testkit.WaitFor(t, func() bool { return longCIEvents(t, server) == 2 })
}

func TestACIThatCompletesGivesNoLongCIEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "success"))
	server.Engine.SetClock(func() time.Time { return time.Now().Add(3 * time.Hour) })

	waitForReadyEvents(t, server, 1)
	waitForPolls(t, fake)

	if events := longCIEvents(t, server); events != 0 {
		t.Errorf("events = %d", events)
	}
}
