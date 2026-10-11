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

// checkErrorEvents gives the Lead events and the activities about the errors of the check.
func checkErrorEvents(t *testing.T, server *testserver.Server) ([]leadEvent, []activity) {
	t.Helper()
	var events []leadEvent
	for _, event := range leadEvents(t, server) {
		if event.Kind == "check_errors" {
			events = append(events, event)
		}
	}
	var found []activity
	for _, a := range activities(t, server) {
		if strings.Contains(a.Text, "Mobius failed to check") {
			found = append(found, a)
		}
	}
	return events, found
}

func TestNinePollsWithAnErrorOfTheCheckTellNobody(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 9)

	if events, found := checkErrorEvents(t, server); len(events) != 0 || len(found) != 0 {
		t.Errorf("events = %+v, found = %+v", events, found)
	}
}

func TestTenPollsWithAnErrorOfTheCheckGiveOneLeadEventAndOneActivity(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 10)

	events, found := checkErrorEvents(t, server)
	if len(events) != 1 || !strings.Contains(events[0].Payload, `Mobius failed to check #41 "Add plan model" on 10 polls in a row. The last error: `) {
		t.Errorf("events = %+v", events)
	}
	if len(found) != 1 || found[0].Issue != 41 || found[0].Workstream != 12 || !strings.Contains(found[0].Text, `Mobius failed to check #41 "Add plan model" on 10 polls in a row. The last error: `) || found[0].Link == "" {
		t.Errorf("found = %+v", found)
	}
	if items := inbox(t, server); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}

func TestMorePollsWithAnErrorOfTheCheckGiveNoSecondEvent(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 15)

	if events, found := checkErrorEvents(t, server); len(events) != 1 || len(found) != 1 {
		t.Errorf("events = %+v, found = %+v", events, found)
	}
}

func TestASuccessfulCheckResetsTheCountOfErrors(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 9)
	failPolls(t, server, fake, 9)

	if events, found := checkErrorEvents(t, server); len(events) != 0 || len(found) != 0 {
		t.Errorf("events = %+v, found = %+v", events, found)
	}
}

func TestTenMorePollsWithAnErrorOfTheCheckGiveANewEvent(t *testing.T) {
	t.Parallel()
	server, fake := connectDispatched(t)

	failPolls(t, server, fake, 10)
	failPolls(t, server, fake, 10)

	if events, found := checkErrorEvents(t, server); len(events) != 2 || len(found) != 2 {
		t.Errorf("events = %+v, found = %+v", events, found)
	}
}
