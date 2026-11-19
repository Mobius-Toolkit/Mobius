package engine_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const closedEventKind = "workstream_closed"

// startCreated starts a server whose creator, the Lead of #12 or the Coordinator, creates the approved Workstream
// #13 "Billing" with the closed task #50.
func startCreated(t *testing.T, fake *testkit.FakeGitHub, spec engine.Spec) *testserver.Server {
	t.Helper()
	server, _ := connect(t, fake, approvalSeen+callCreateWorkstream("Create it.", "Billing", "Bill the plans.")+seenScript)
	run(t, server, spec, "Create it.")
	if done := approveNext(t, server, 1); done.Result != "Created the Workstream #13." {
		t.Fatalf("action = %+v", done)
	}
	testkit.WaitFor(t, func() bool { return hasWorkstreamRow(t, server, 13) })
	fake.AddIssue(shop, 50, "Add billing model")
	fake.AddSubIssue(shop, 13, 50)
	fake.CloseIssue(shop, 50)
	testkit.WaitFor(t, func() bool {
		for _, found := range workstreams(t, server) {
			if found.Number == 13 {
				return found.AllTasksClosed
			}
		}
		return false
	})
	return server
}

func creatorOf(t *testing.T, server *testserver.Server, number int64) (creator *int64) {
	t.Helper()
	if err := server.DB.QueryRow("SELECT creator_workstream FROM workstreams WHERE repository = ? AND number = ?", shop, number).Scan(&creator); err != nil {
		t.Fatal(err)
	}
	return creator
}

func closedEvents(t *testing.T, server *testserver.Server, workstream int64) []leadEvent {
	t.Helper()
	return eventsOfKind(t, server, closedEventKind, workstream)
}

func closedEventText(reason string) string {
	return " close of Workstream #13 \"Billing\" that you created: " + reason + "."
}

func requireClosedEvent(t *testing.T, event leadEvent, reason string) {
	t.Helper()
	if !strings.HasSuffix(event.Payload, closedEventText(reason)) || !strings.HasPrefix(event.Payload, "20") {
		t.Errorf("payload = %q, want the reason %q", event.Payload, reason)
	}
}

func TestAnApprovedCreateWorkstreamOfALeadRecordsItsWorkstreamAndOneOfTheCoordinatorRecordsNone(t *testing.T) {
	t.Parallel()
	lead := startCreated(t, testkit.NewFakeGitHub(t), leadSpec(t))
	coordinator := startCreated(t, testkit.NewFakeGitHub(t), coordinatorSpec(t))

	if got := creatorOf(t, lead, 13); got == nil || *got != 12 {
		t.Errorf("creator of the Workstream of the Lead = %v, want 12", got)
	}
	if got := creatorOf(t, coordinator, 13); got != nil {
		t.Errorf("creator of the Workstream of the Coordinator = %d, want none", *got)
	}
}

func TestACompletionOfACreatedWorkstreamGivesTheCreatorLeadOneEventWithTheReasonCompleted(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startCreated(t, fake, leadSpec(t))

	if status, body := complete(t, server, 13); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	events := closedEvents(t, server, 12)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	requireClosedEvent(t, events[0], "completed")
	if got := closedEvents(t, server, 13); len(got) != 0 {
		t.Errorf("events of the closed Workstream = %+v", got)
	}
}

func TestAWontDoCloseOfACreatedWorkstreamGivesTheCreatorLeadOneEventWithTheReasonNotPlanned(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startCreated(t, fake, leadSpec(t))

	if err := server.Engine.CloseWorkstreamWontDo(t.Context(), shop, 13); err != nil {
		t.Fatal(err)
	}

	events := closedEvents(t, server, 12)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	requireClosedEvent(t, events[0], "not planned")
}

func TestACloseOnGitHubOfACreatedWorkstreamGivesTheCreatorLeadOneEventWithTheReasonOfTheIssue(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ githubReason, want string }{{"not_planned", "not planned"}, {"completed", "completed"}} {
		t.Run(test.githubReason, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := startCreated(t, fake, leadSpec(t))

			fake.CloseIssueAs(shop, 13, test.githubReason)

			testkit.WaitFor(t, func() bool { return len(closedEvents(t, server, 12)) > 0 })
			waitForPolls(t, fake)
			events := closedEvents(t, server, 12)
			if len(events) != 1 {
				t.Fatalf("events = %+v", events)
			}
			requireClosedEvent(t, events[0], test.want)
		})
	}
}

func TestAClosedWorkstreamThatTheCoordinatorCreatedGivesNoCloseEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startCreated(t, fake, coordinatorSpec(t))

	fake.CloseIssueAs(shop, 13, "not_planned")

	testkit.WaitFor(t, func() bool { state, _, _ := workstreamRow(t, server, 13); return state == "closed" })
	waitForPolls(t, fake)
	if got := eventsOfKind(t, server, closedEventKind, 12); len(got) != 0 {
		t.Errorf("events = %+v", got)
	}
}

func TestAClosedWorkstreamWhoseCreatorWorkstreamIsClosedGivesNoCloseEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startCreated(t, fake, leadSpec(t))
	fake.CloseIssueAs(shop, 12, "completed")
	testkit.WaitFor(t, func() bool { state, _, _ := workstreamRow(t, server, 12); return state == "closed" })

	fake.CloseIssueAs(shop, 13, "completed")

	testkit.WaitFor(t, func() bool { state, _, _ := workstreamRow(t, server, 13); return state == "closed" })
	waitForPolls(t, fake)
	if got := closedEvents(t, server, 12); len(got) != 0 {
		t.Errorf("events = %+v", got)
	}
}

func TestAReopenAndASecondCloseGiveASecondEventAndARetryOfTheCloseGivesNoSecondEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startCreated(t, fake, leadSpec(t))
	fake.AddIssue(shop, 51, "Add billing view")
	fake.AddSubIssue(shop, 13, 51)
	fake.FailClose(shop, 51)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 51", shop) == 1
	})

	fake.CloseIssueAs(shop, 13, "completed")

	testkit.WaitFor(t, func() bool {
		return len(closedEvents(t, server, 12)) == 1 && countRows(t, server, "SELECT count(*) FROM workstreams WHERE number = 13 AND close_pending = 1") == 1
	})
	waitForPolls(t, fake)
	if got := closedEvents(t, server, 12); len(got) != 1 {
		t.Fatalf("events after the retries = %+v", got)
	}
	fake.AllowClose(shop, 51)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM workstreams WHERE number = 13 AND close_pending = 1") == 0
	})
	if got := closedEvents(t, server, 12); len(got) != 1 {
		t.Fatalf("events after the close ended = %+v", got)
	}

	fake.ReopenIssue(shop, 13)
	testkit.WaitFor(t, func() bool { state, _, _ := workstreamRow(t, server, 13); return state == "open" })
	testkit.WaitFor(t, func() bool { return len(eventsOfKind(t, server, "reopen", 13)) == 1 })
	fake.CloseIssueAs(shop, 13, "not_planned")

	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := closedEvents(t, server, 12)
		return events, len(events) == 2
	})
	requireClosedEvent(t, events[0], "completed")
	requireClosedEvent(t, events[1], "not planned")
}
