package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// connectDispatched starts a server with the dispatched task #41, and waits until its row exists.
func connectDispatched(t *testing.T) (*testserver.Server, *testkit.FakeGitHub) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return live(t, server) })
	return server, fake
}

// failPolls makes the check of #41 fail on the next count polls, and waits until the poll after them resets the count.
func failPolls(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, count int) {
	t.Helper()
	fake.FailIssueReads(shop, 41, count)
	testkit.WaitFor(t, func() bool {
		var errors int
		if err := server.DB.QueryRow("SELECT check_errors FROM tasks WHERE repository = ? AND issue = 41", shop).Scan(&errors); err != nil {
			t.Fatal(err)
		}
		return fake.PendingIssueReadFailures(shop, 41) == 0 && errors == 0
	})
}

// checkErrorEvents gives the Lead events and the Inbox items about the errors of the check.
func checkErrorEvents(t *testing.T, server *testserver.Server) ([]leadEvent, []inboxItem) {
	t.Helper()
	var events []leadEvent
	for _, event := range leadEvents(t, server) {
		if event.Kind == "check_errors" {
			events = append(events, event)
		}
	}
	var items []inboxItem
	for _, item := range inbox(t, server) {
		if item.Kind == "check errors" {
			items = append(items, item)
		}
	}
	return events, items
}

func TestNinePollsWithAnErrorOfTheCheckTellNobody(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 9)

	if events, items := checkErrorEvents(t, server); len(events) != 0 || len(items) != 0 {
		t.Errorf("events = %+v, items = %+v", events, items)
	}
}

func TestTenPollsWithAnErrorOfTheCheckGiveOneLeadEventAndOneInboxItem(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 10)

	events, items := checkErrorEvents(t, server)
	if len(events) != 1 || !strings.Contains(events[0].Payload, `Mobius failed to check #41 "Add plan model" on 10 polls in a row. The last error: `) {
		t.Errorf("events = %+v", events)
	}
	if len(items) != 1 || items[0].Issue != 41 || items[0].Workstream != 12 || !strings.Contains(items[0].Text, `Mobius failed to check #41 "Add plan model" on 10 polls in a row. The last error: `) || items[0].Link == "" {
		t.Errorf("items = %+v", items)
	}
}

func TestMorePollsWithAnErrorOfTheCheckGiveNoSecondEvent(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 15)

	if events, items := checkErrorEvents(t, server); len(events) != 1 || len(items) != 1 {
		t.Errorf("events = %+v, items = %+v", events, items)
	}
}

func TestASuccessfulCheckResetsTheCountOfErrors(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 9)
	failPolls(t, server, fake, 9)

	if events, items := checkErrorEvents(t, server); len(events) != 0 || len(items) != 0 {
		t.Errorf("events = %+v, items = %+v", events, items)
	}
}

func TestTenMorePollsWithAnErrorOfTheCheckGiveANewEvent(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 10)
	failPolls(t, server, fake, 10)

	if events, items := checkErrorEvents(t, server); len(events) != 2 || len(items) != 2 {
		t.Errorf("events = %+v, items = %+v", events, items)
	}
}
