package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const approvalReminderText = " #41 \"Add plan model\" waits for Lead approval for "

// approvalReminders gives the approval reminder events of #41. A reminder starts no Lead turn, so the test reads the
// event instead of a prompt.
func approvalReminders(t *testing.T, server *testserver.Server) []leadEvent {
	t.Helper()
	var events []leadEvent
	for _, event := range leadEvents(t, server) {
		if event.Kind == "approval_reminder" && strings.Contains(event.Payload, approvalReminderText) {
			events = append(events, event)
		}
	}
	return events
}

func TestATaskThatWaitsInApprovalForMoreThanTwoHoursGivesTheLeadOneReminderAndThenOneForEachTwoHours(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	start := time.Now()

	server.Engine.SetClock(func() time.Time { return start.Add(time.Hour + 59*time.Minute) })
	waitForPolls(t, fake)

	if reminders := approvalReminders(t, server); len(reminders) != 0 {
		t.Errorf("reminders = %+v", reminders)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })

	reminders := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		reminders := approvalReminders(t, server)
		return reminders, len(reminders) == 1
	})
	if !strings.Contains(reminders[0].Payload, approvalReminderText+"2h") {
		t.Errorf("reminder = %s", reminders[0].Payload)
	}
	if want := "pull request #42 https://github.com/owner/shop/pull/42."; !strings.Contains(reminders[0].Payload, want) {
		t.Errorf("reminder = %s, want %q", reminders[0].Payload, want)
	}
	if reminders[0].Delivered {
		t.Error("the reminder turn was delivered, but it starts no turn")
	}
	waitForPolls(t, fake)
	if reminders := approvalReminders(t, server); len(reminders) != 1 {
		t.Errorf("reminders = %+v", reminders)
	}
	if state := taskState(t, server); state != "approval" {
		t.Errorf("state = %s", state)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(4 * time.Hour) })
	waitForPolls(t, fake)

	if reminders := approvalReminders(t, server); len(reminders) != 1 {
		t.Errorf("reminders = %+v", reminders)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(4*time.Hour + 2*time.Minute) })

	testkit.WaitFor(t, func() bool { return len(approvalReminders(t, server)) == 2 })
}

func TestATaskThatLeavesApprovalGetsNoReminder(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadApproves+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got it for review.")
	waitForPolls(t, fake)
	server.Engine.SetClock(func() time.Time { return time.Now().Add(3 * time.Hour) })

	waitForPolls(t, fake)

	if reminders := approvalReminders(t, server); len(reminders) != 0 {
		t.Errorf("reminders = %+v", reminders)
	}
}
