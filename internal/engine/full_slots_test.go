package engine_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// fullSlotsServer starts an Autopilot server with one slot, which the task #41 holds, and the issues in numbers
// that Autopilot cannot start. It waits until Autopilot notes the full slot, and gives the start of the wait.
func fullSlotsServer(t *testing.T, numbers ...int64) (*testkit.FakeGitHub, *testserver.Server, time.Time) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	server := startAutopilot(t, fake, func(cfg *config.Config) { cfg.MaxAgents = 1 }, "")
	liveTaskOf(t, server, 41)
	for _, number := range numbers {
		addTaskIssue(fake, number, "Add plan price")
	}
	if len(numbers) == 0 {
		return fake, server, time.Now()
	}
	since := fullSlotsSince(t, server)
	return fake, server, since
}

// fullSlotsSince waits for a row of full_slots with no Inbox item, and gives its since.
func fullSlotsSince(t *testing.T, server *testserver.Server) time.Time {
	t.Helper()
	return testkit.WaitForValue(t, func() (time.Time, bool) {
		var text string
		err := server.DB.QueryRow("SELECT since FROM full_slots WHERE item_at IS NULL").Scan(&text)
		if err == sql.ErrNoRows {
			return time.Time{}, false
		}
		if err != nil {
			t.Fatal(err)
		}
		since, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			t.Fatal(err)
		}
		return since, true
	})
}

func fullSlotsItems(t *testing.T, server *testserver.Server) []inboxItem {
	t.Helper()
	var items []inboxItem
	for _, item := range inbox(t, server) {
		if item.Kind == "full slots" {
			items = append(items, item)
		}
	}
	return items
}

func setClock(server *testserver.Server, at time.Time) {
	server.Engine.SetClock(func() time.Time { return at })
}

func TestNoInboxItemBeforeThirtyMinutesWithAllSlotsFull(t *testing.T) {
	t.Parallel()
	fake, server, since := fullSlotsServer(t, 42)

	setClock(server, since.Add(29*time.Minute))
	waitForPolls(t, fake)

	if items := fullSlotsItems(t, server); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}

func TestAfterThirtyMinutesWithAllSlotsFullTheOwnerGetsOneItemWithTheTasks(t *testing.T) {
	t.Parallel()
	fake, server, since := fullSlotsServer(t, 42)

	setClock(server, since.Add(31*time.Minute))
	testkit.WaitFor(t, func() bool { return len(fullSlotsItems(t, server)) == 1 })

	item := fullSlotsItems(t, server)[0]
	if item.Repository != shop || item.Workstream != 12 || item.Issue != 42 || item.Link == "" {
		t.Errorf("item = %+v", item)
	}
	for _, want := range []string{
		`All 1 Autopilot slots are full for 31m0s.`,
		`Autopilot could not start #42 "Add plan price".`,
		shop + `#41 is dispatched for `,
	} {
		if !strings.Contains(item.Text, want) {
			t.Errorf("text = %q, want %q", item.Text, want)
		}
	}
	waitForPolls(t, fake)
	if items := fullSlotsItems(t, server); len(items) != 1 {
		t.Errorf("items = %+v", items)
	}
}

func TestAFreeSlotAndThirtyMoreMinutesWithAllSlotsFullGiveANewItem(t *testing.T) {
	t.Parallel()
	fake, server, since := fullSlotsServer(t, 42, 43)
	first := since.Add(31 * time.Minute)
	setClock(server, first)
	testkit.WaitFor(t, func() bool { return len(fullSlotsItems(t, server)) == 1 })

	if _, err := server.DB.Exec("UPDATE tasks SET state = 'ended' WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}
	liveTaskOf(t, server, 42)
	fullSince := fullSlotsSince(t, server)

	setClock(server, fullSince.Add(29*time.Minute))
	waitForPolls(t, fake)

	if items := fullSlotsItems(t, server); len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}

	setClock(server, fullSince.Add(31*time.Minute))
	testkit.WaitFor(t, func() bool { return len(fullSlotsItems(t, server)) == 2 })

	if item := fullSlotsItems(t, server)[1]; item.Issue != 43 || !strings.Contains(item.Text, shop+"#42 is dispatched for ") {
		t.Errorf("item = %+v", item)
	}
}

func TestWithAllSlotsFullAndNoIssueToStartTheOwnerGetsNoItem(t *testing.T) {
	t.Parallel()
	fake, server, start := fullSlotsServer(t)

	setClock(server, start.Add(time.Hour))
	waitForPolls(t, fake)

	if items := fullSlotsItems(t, server); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}
