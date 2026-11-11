package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	dispatchReminderText = " #41 \"Add plan model\" waits for an Implementer for "
	leadWaits            = "[[prompts]]\nreply = [\"Noted.\"]\n"
)

// dispatchReminders gives the number of turns of the Lead for a dispatch reminder of #41.
func dispatchReminders(t *testing.T, server *testserver.Server) int {
	t.Helper()
	count := 0
	for _, prompt := range leadPrompts(t, server) {
		parts := strings.Split(prompt, "# Event\n\n")
		if len(parts) > 1 && strings.Contains(parts[len(parts)-1], dispatchReminderText) {
			count++
		}
	}
	return count
}

// dispatchedTask connects a Lead that does not start the Implementer, and dispatches #41.
func dispatchedTask(t *testing.T) (*testkit.FakeGitHub, *testserver.Server) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, leadWaits, keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	waitForLeadPrompt(t, server, "dispatch of #41")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	return fake, server
}

// reminderEvents gives the number of dispatch_reminder events of #41 and the number that the Lead took.
func reminderEvents(t *testing.T, server *testserver.Server) (events, delivered int) {
	t.Helper()
	err := server.DB.QueryRow("SELECT count(*), coalesce(sum(delivered_at IS NOT NULL), 0) FROM lead_events WHERE repository = ? AND workstream = 12 AND kind = 'dispatch_reminder' AND issue = 41", shop).Scan(&events, &delivered)
	if err != nil {
		t.Fatal(err)
	}
	return events, delivered
}

// gateFile gives the path of a gate file, and opens the gate at the end of the test.
func gateFile(t *testing.T) (path string, open func()) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "gate")
	open = func() {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(open)
	return path, open
}

func TestATaskThatWaitsInDispatchedForMoreThanTwoHoursGivesTheLeadOneReminderAndThenOneForEachTwoHours(t *testing.T) {
	t.Parallel()
	fake, server := dispatchedTask(t)
	start := time.Now()

	server.Engine.SetClock(func() time.Time { return start.Add(time.Hour + 59*time.Minute) })
	waitForPolls(t, fake)

	if reminders := dispatchReminders(t, server); reminders != 0 {
		t.Errorf("reminders = %d", reminders)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })

	prompt := waitForLeadPrompt(t, server, dispatchReminderText+"2h")
	if want := "holds an Autopilot slot."; !strings.Contains(prompt, want) {
		t.Errorf("prompt = %s, want %q", prompt, want)
	}
	waitForPolls(t, fake)
	if reminders := dispatchReminders(t, server); reminders != 1 {
		t.Errorf("reminders = %d", reminders)
	}
	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(4 * time.Hour) })
	waitForPolls(t, fake)

	if reminders := dispatchReminders(t, server); reminders != 1 {
		t.Errorf("reminders = %d", reminders)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(4*time.Hour + 2*time.Minute) })

	testkit.WaitFor(t, func() bool { return dispatchReminders(t, server) == 2 })
}

func TestATaskInDispatchedWithAQuestionGetsNoReminder(t *testing.T) {
	t.Parallel()
	fake, server := dispatchedTask(t)
	fake.AddLabel(shop, 41, "mobius:question", testkit.AppSlug+"[bot]")
	waitForPolls(t, fake)
	server.Engine.SetClock(func() time.Time { return time.Now().Add(3 * time.Hour) })

	waitForPolls(t, fake)

	if reminders := dispatchReminders(t, server); reminders != 0 {
		t.Errorf("reminders = %d", reminders)
	}
}

func TestATaskThatLeavesDispatchedGetsNoReminder(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	server.Engine.SetClock(func() time.Time { return time.Now().Add(3 * time.Hour) })

	waitForPolls(t, fake)

	if reminders := dispatchReminders(t, server); reminders != 0 {
		t.Errorf("reminders = %d", reminders)
	}
}

func TestADispatchReminderGetsNoTurnWhenTheLeadStartsTheImplementerBeforeItsDelivery(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	gate, open := gateFile(t)
	lead := fmt.Sprintf("[[prompts]]\nwhen = \"dispatch of #43\"\nshell = \"until [ -e %s ]; do sleep 0.05; done\"\n%s\n%s", gate, startImplementer, leadWaits)
	server, _ := connectTask(t, fake, lead, commits, func(cfg *config.Config) { cfg.LeadIdleTimeout = 30 * time.Second })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForLeadPrompt(t, server, "dispatch of #41")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	dispatchTask(fake, 43, "Round prices")
	waitForLeadPrompt(t, server, "dispatch of #43")

	server.Engine.SetClock(func() time.Time { return time.Now().Add(3 * time.Hour) })
	testkit.WaitFor(t, func() bool {
		events, _ := reminderEvents(t, server)
		return events == 1
	})
	open()

	testkit.WaitFor(t, func() bool {
		_, delivered := reminderEvents(t, server)
		return delivered == 1
	})
	if reminders := dispatchReminders(t, server); reminders != 0 {
		t.Errorf("reminders = %d", reminders)
	}
	if state := taskState(t, server); state == "dispatched" {
		t.Errorf("state = %s", state)
	}
}

func TestATaskGetsNoDispatchReminderWhileItsDispatchEventIsNotDelivered(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	gate, open := gateFile(t)
	lead := fmt.Sprintf("[[prompts]]\nwhen = \"dispatch of #41\"\nshell = \"until [ -e %s ]; do sleep 0.05; done\"\n\n%s", gate, leadWaits)
	server, _ := connectWith(t, fake, lead, keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	waitForLeadPrompt(t, server, "dispatch of #41")

	server.Engine.SetClock(func() time.Time { return time.Now().Add(3 * time.Hour) })
	waitForPolls(t, fake)

	if events, _ := reminderEvents(t, server); events != 0 {
		t.Errorf("reminder events = %d", events)
	}
	open()
	testkit.WaitFor(t, func() bool { return dispatchReminders(t, server) == 1 })
}
