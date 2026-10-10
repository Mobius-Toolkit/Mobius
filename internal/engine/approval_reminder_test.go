package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const approvalReminderText = " #41 \"Add plan model\" waits for Lead approval for "

// approvalReminders gives the number of turns of the Lead for an approval reminder of #41.
func approvalReminders(t *testing.T, server *testserver.Server) int {
	t.Helper()
	count := 0
	for _, prompt := range leadPrompts(t, server) {
		parts := strings.Split(prompt, "# Event\n\n")
		if len(parts) > 1 && strings.Contains(parts[len(parts)-1], approvalReminderText) {
			count++
		}
	}
	return count
}

func TestATaskThatWaitsInApprovalForMoreThanTwoHoursGivesTheLeadOneReminderAndThenOneForEachTwoHours(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	start := time.Now()

	server.Engine.SetClock(func() time.Time { return start.Add(time.Hour + 59*time.Minute) })
	waitForPolls(t, fake)

	if reminders := approvalReminders(t, server); reminders != 0 {
		t.Errorf("reminders = %d", reminders)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })

	prompt := waitForLeadPrompt(t, server, approvalReminderText+"2h")
	if want := "pull request #42 https://github.com/owner/shop/pull/42."; !strings.Contains(prompt, want) {
		t.Errorf("prompt = %s, want %q", prompt, want)
	}
	waitForPolls(t, fake)
	if reminders := approvalReminders(t, server); reminders != 1 {
		t.Errorf("reminders = %d", reminders)
	}
	if state := taskState(t, server); state != "approval" {
		t.Errorf("state = %s", state)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(4 * time.Hour) })
	waitForPolls(t, fake)

	if reminders := approvalReminders(t, server); reminders != 1 {
		t.Errorf("reminders = %d", reminders)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(4*time.Hour + 2*time.Minute) })

	testkit.WaitFor(t, func() bool { return approvalReminders(t, server) == 2 })
}

func TestATaskThatLeavesApprovalGetsNoReminder(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadApproves+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got it for review.")
	server.Engine.SetClock(func() time.Time { return time.Now().Add(3 * time.Hour) })

	waitForPolls(t, fake)

	if reminders := approvalReminders(t, server); reminders != 0 {
		t.Errorf("reminders = %d", reminders)
	}
}
