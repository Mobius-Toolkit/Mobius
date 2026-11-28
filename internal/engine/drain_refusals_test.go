package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	drainEndedRestarted = "The drain of an upgrade ended. Mobius restarted. Mobius refused the calls in the JSON below during the drain. Call each tool again when the work still needs it.\n\n"
	drainEndedCancelled = "The drain of an upgrade ended. The upgrade did not happen. Mobius refused the calls in the JSON below during the drain. Call each tool again when the work still needs it.\n\n"
)

// drainEndedAnswer answers the text of a drain_ended event and of the Mobius message. The Role prompt names the first
// sentence of that text, so the entry names its second sentence. The newest prompt comes first, because the first
// prompt of a new session also has the history of the chat.
const drainEndedAnswer = `
[[prompts]]
when = "Mobius refused the calls in the JSON below"
reply = ["I call the tool again."]
`

func drainRefusalCount(t *testing.T, server *testserver.Server) int {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM drain_refusals").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func drainEndedEvents(t *testing.T, server *testserver.Server) []leadEvent {
	t.Helper()
	return slices.DeleteFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind != "drain_ended" })
}

func TestALeadGetsOneDrainEndedEventWithItsRefusedOperatorCallAfterTheRestart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectOperator(t, fake, drainEndedAnswer+operatorLead+operatorWork)
	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}
	sendChat(t, server, leadChat, "Start the operator.")
	testkit.WaitFor(t, func() bool { return drainRefusalCount(t, server) == 1 })
	server.Stop()

	restarted := testserver.Start(t, dataDir, fake.URL)

	want := drainEndedRestarted + `{"restarted":true,"release":"","calls":[{"tool":"start_operator","arguments":{"text":"Update the listings."}}]}`
	testkit.WaitFor(t, func() bool {
		events := drainEndedEvents(t, restarted)
		return len(events) == 1 && events[0].Delivered
	})
	if events := drainEndedEvents(t, restarted); events[0] != (leadEvent{Workstream: 12, Kind: "drain_ended", Payload: want, Delivered: true}) {
		t.Errorf("event = %+v", events[0])
	}
	if prompts := leadPrompts(t, restarted); !slices.ContainsFunc(prompts, func(prompt string) bool { return strings.HasSuffix(prompt, "# Event\n\n"+want) }) {
		t.Errorf("prompts = %q", prompts)
	}
	if count := drainRefusalCount(t, restarted); count != 0 {
		t.Errorf("refusals = %d", count)
	}
	restarted.Stop()

	again := testserver.Start(t, dataDir, fake.URL)
	waitForPolls(t, fake)

	if events := drainEndedEvents(t, again); len(events) != 1 {
		t.Errorf("events = %+v", events)
	}
}

func TestTheCoordinatorGetsOneMessageWithItsRefusedResearcherCallAfterACancelOfTheDrain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearch(t, fake, drainEndedAnswer+researchCoordinator)
	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}
	sendChat(t, server, coordinatorChat, "Where do plans store the price?")
	testkit.WaitFor(t, func() bool { return drainRefusalCount(t, server) == 1 })

	cancelDrain(t, server)

	want := drainEndedCancelled + `{"restarted":false,"release":"","calls":[{"tool":"start_researcher","arguments":{"repository":"owner/shop","question":"Where do plans store the price?"}}]}`
	waitForChat(t, server, coordinatorChat, "Mobius", want)
	testkit.WaitFor(t, func() bool {
		return strings.Contains(strings.Join(coordinatorPrompts(t, server), "\n"), "# Mobius message\n\n"+want)
	})
	var messages int
	for _, line := range chatLines(t, server, coordinatorChat) {
		if line.Author == "Mobius" {
			messages++
		}
	}
	if messages != 1 {
		t.Errorf("Mobius messages = %d", messages)
	}
	if count := drainRefusalCount(t, server); count != 0 {
		t.Errorf("refusals = %d", count)
	}
	if events := drainEndedEvents(t, server); len(events) != 0 {
		t.Errorf("events = %+v", events)
	}
}

func TestADrainWithNoRefusedCallGivesNoEventAndNoMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearch(t, fake, drainEndedAnswer+researchCoordinator)
	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}

	cancelDrain(t, server)

	if events := drainEndedEvents(t, server); len(events) != 0 {
		t.Errorf("events = %+v", events)
	}
	for _, key := range []engine.ChatKey{leadChat, coordinatorChat} {
		lines := slices.DeleteFunc(chatLines(t, server, key), func(line chatLine) bool { return line.Author == "Record" })
		if len(lines) != 0 {
			t.Errorf("chat %+v = %+v", key, lines)
		}
	}
	if got, want := records(t, server, coordinatorChat), []string{"You turned on the drain.", "You turned off the drain."}; !slices.Equal(got, want) {
		t.Errorf("records = %q, want %q", got, want)
	}
}
