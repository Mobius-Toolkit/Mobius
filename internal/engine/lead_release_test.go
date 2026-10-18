package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const releaseScript = `
[[prompts]]
when = "dispatch of #42"
reply = ["Seen"]

[[prompts]]
call = { tool = "ask", arguments = { n = 41, text = "Cents or dollars?" } }

[[prompts]]
` + holdEvent + `

[[prompts]]
reply = ["Seen"]

[[prompts]]
reply = ["Seen"]

[[prompts]]
reply = ["Seen"]
`

func addLeadEvent(t *testing.T, server *testserver.Server, kind, payload string) {
	t.Helper()
	if _, err := server.DB.Exec(`INSERT INTO lead_events (repository, workstream, issue, kind, payload, time) VALUES ('owner/shop', 12, 41, ?, ?, '2026-10-04T10:00:00Z')`, kind, payload); err != nil {
		t.Fatal(err)
	}
}

// heldAfterQuestion asks a question on #41 and holds a later event of #41. The dispatch of #42 wakes the Lead for the
// event that the test adds to the database. One more event of #41 waits behind the held event.
func heldAfterQuestion(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, inboxItem) {
	t.Helper()
	server, _ := connectWith(t, fake, releaseScript, keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	item := waitForItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	addLeadEvent(t, server, "follow_up", "Held follow-up.")
	dispatchTask(fake, 42, "Add plan route")
	testkit.WaitFor(t, func() bool { return held(t, server, 1) && len(onlySession(t, server)) == 3 })
	addLeadEvent(t, server, "ready_for_approval", "Ready for approval.")
	waitForPolls(t, fake)
	if got := onlySession(t, server); len(got) != 3 || !held(t, server, 2) {
		t.Fatalf("prompts = %q", got)
	}
	return server, item
}

func checkReleased(t *testing.T, server *testserver.Server) {
	t.Helper()
	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := onlySession(t, server)
		return prompts, len(prompts) >= 6 && eventDelivered(t, server)
	})
	if len(prompts) != 6 || !strings.Contains(prompts[3], "Held follow-up.") || !strings.Contains(prompts[4], "Ready for approval.") || !strings.Contains(prompts[5], " answer of #41 ") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAnAnswerFromTheInboxFreesTheHeldEventsInIdOrder(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, item := heldAfterQuestion(t, fake)
	authorize(t, server)

	answer(t, server, item.ID, "Cents.")

	checkReleased(t, server)
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:question") })
}

func TestACommentOfATrustedUserFreesTheHeldEventsInIdOrder(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := heldAfterQuestion(t, fake)

	fake.AddComment(shop, 41, "owner", "Cents.")

	checkReleased(t, server)
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:question") })
}

func TestACommentOfTheLeadWithGhKeepsTheEventsHeld(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := heldAfterQuestion(t, fake)

	fake.AddAppComment(shop, 41, "owner", "Cents.")
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if got := onlySession(t, server); len(got) != 3 || !held(t, server, 2) || !slices.Contains(fake.Labels(shop, 41), "mobius:question") {
		t.Errorf("prompts = %q, labels = %v", got, fake.Labels(shop, 41))
	}
}

const lingeringHoldScript = `
[[prompts]]
when = "dispatch of #42"
reply = ["Seen"]

[[prompts]]
call = { tool = "ask", arguments = { n = 41, text = "Cents or dollars?" } }

[[prompts]]
` + holdEvent + `
linger = "3s"

[[prompts]]
reply = ["Seen"]

[[prompts]]
reply = ["Seen"]
`

// Serial: the answer must come before the lingering turn of the hold ends.
func TestAnAnswerDuringTheTurnOfTheHoldDeliversTheHeldEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, lingeringHoldScript, keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	waitForItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	addLeadEvent(t, server, "follow_up", "Held follow-up.")
	dispatchTask(fake, 42, "Add plan route")
	testkit.WaitFor(t, func() bool {
		for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
			for _, call := range rows(t, server, session.ID, "mcp_call") {
				if call["tool"] == "hold_event" {
					return true
				}
			}
		}
		return false
	})

	fake.AddComment(shop, 41, "owner", "Cents.")

	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:question") })
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	if prompts := onlySession(t, server); !slices.ContainsFunc(prompts, func(prompt string) bool { return strings.Contains(prompt, "Held follow-up.") }) {
		t.Errorf("prompts = %q", prompts)
	}
}
