package engine_test

import (
	"strings"
	"testing"
	"time"

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
	return fake, server
}

func TestATaskThatWaitsInDispatchedForMoreThanTwoHoursGivesTheLeadOneReminderAndThenOneForEachTwoHours(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
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
	testkit.Slow(t)
	fake, server := dispatchedTask(t)
	fake.AddLabel(shop, 41, "mobius:question", testkit.AppSlug+"[bot]")
	server.Engine.SetClock(func() time.Time { return time.Now().Add(3 * time.Hour) })

	waitForPolls(t, fake)

	if reminders := dispatchReminders(t, server); reminders != 0 {
		t.Errorf("reminders = %d", reminders)
	}
}

func TestATaskThatLeavesDispatchedGetsNoReminder(t *testing.T) {
	t.Parallel()
	testkit.Slow(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	server.Engine.SetClock(func() time.Time { return time.Now().Add(3 * time.Hour) })

	waitForPolls(t, fake)

	if reminders := dispatchReminders(t, server); reminders != 0 {
		t.Errorf("reminders = %d", reminders)
	}
}
