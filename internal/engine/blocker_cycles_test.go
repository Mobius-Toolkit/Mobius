package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func blockerCycleEvents(t *testing.T, server *testserver.Server) []leadEvent {
	t.Helper()
	events := []leadEvent{}
	for _, event := range leadEvents(t, server) {
		if event.Kind == "blocker_cycle" {
			events = append(events, event)
		}
	}
	return events
}

func blockerCycleRows(t *testing.T, server *testserver.Server) int {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM blocker_cycles").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// addCycle adds the tasks #41, #42 and #43 below the Workstream #12, and makes each task blocked by the next one and #43 blocked by #41.
func addCycle(fake *testkit.FakeGitHub) {
	addWorkstream(fake)
	fake.AddIssue(shop, 42, "Let customers change plans")
	fake.AddSubIssue(shop, 12, 42)
	fake.AddIssue(shop, 43, "Show the plan price")
	fake.AddSubIssue(shop, 12, 43)
	fake.AddBlockedBy(shop, 41, 42)
	fake.AddBlockedBy(shop, 42, 43)
	fake.AddBlockedBy(shop, 43, 41)
}

func TestACycleBetweenTwoWorkstreamsGivesOneEventToEachWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 13, "Billing")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 60, "Invoice model")
	fake.AddSubIssue(shop, 13, 60)
	fake.AddBlockedBy(shop, 41, 60)
	fake.AddBlockedBy(shop, 60, 41)

	server := startCopiedWithLead(t, fake)

	testkit.WaitFor(t, func() bool { return len(blockerCycleEvents(t, server)) == 2 })
	waitForPolls(t, fake)
	events := blockerCycleEvents(t, server)
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	for _, event := range events {
		if !strings.Contains(event.Payload, "cycle of blockers: #41 is blocked by #60, and #60 is blocked by #41.") {
			t.Errorf("event of Workstream %d = %q", event.Workstream, event.Payload)
		}
	}
	if events[0].Workstream == events[1].Workstream {
		t.Errorf("events = %+v", events)
	}
}

func TestACycleOfThreeIssuesGivesOneEventWithThePathInOrder(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addCycle(fake)

	server := startCopiedWithLead(t, fake)

	testkit.WaitFor(t, func() bool { return len(blockerCycleEvents(t, server)) == 1 })
	waitForPolls(t, fake)
	events := blockerCycleEvents(t, server)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	want := "cycle of blockers: #41 is blocked by #42, #42 is blocked by #43, and #43 is blocked by #41. Thus no issue of the cycle can start."
	if events[0].Workstream != 12 || !strings.Contains(events[0].Payload, want) {
		t.Errorf("event = %+v", events[0])
	}
	if strings.Contains(events[0].Payload, "also holds") {
		t.Errorf("event = %q", events[0].Payload)
	}
}

func TestACycleThatGoesAndComesBackGivesANewEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addCycle(fake)
	server := startCopiedWithLead(t, fake)
	testkit.WaitFor(t, func() bool { return len(blockerCycleEvents(t, server)) == 1 })

	fake.RemoveBlockedBy(shop, 43, 41)
	testkit.WaitFor(t, func() bool { return blockerCycleRows(t, server) == 0 })
	if events := blockerCycleEvents(t, server); len(events) != 1 {
		t.Fatalf("events after the removal = %+v", events)
	}

	fake.AddBlockedBy(shop, 43, 41)
	testkit.WaitFor(t, func() bool { return len(blockerCycleEvents(t, server)) == 2 })
	waitForPolls(t, fake)
	if events := blockerCycleEvents(t, server); len(events) != 2 {
		t.Errorf("events = %+v", events)
	}
}
