package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// quietSection gives the section of a Lead prompt that holds the payloads.
func quietSection(payloads ...string) string {
	return "# Events with no turn\n\n" + strings.Join(payloads, "\n\n") + "\n\n"
}

// waitForLeadPrompts waits until the Lead sessions of the Workstream #12 have count prompts.
func waitForLeadPrompts(t *testing.T, server *testserver.Server, count int) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return len(leadPrompts(t, server)) == count })
}

// waitForAutopilotEvents waits until the Workstream #12 has count Autopilot events, and gives them.
func waitForAutopilotEvents(t *testing.T, server *testserver.Server, count int) []string {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]string, bool) {
		payloads := autopilotEvents(t, server)
		return payloads, len(payloads) == count
	})
}

// waitForNoUndeliveredEvents waits until the Lead took each event of the Workstream #12.
func waitForNoUndeliveredEvents(t *testing.T, server *testserver.Server) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == 0 })
}

func TestAQuietEventStartsNoTurnAndComesBeforeTheNextEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n", keepSessionOpen)

	sendChat(t, server, leadChat, "Plan the loyalty API")
	waitForChat(t, server, leadChat, "Lead", "Seen")
	waitForLeadPrompts(t, server, 1)
	session := chatSessions(t, server, leadChat, engine.LeadRole)[0]

	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	payloads := waitForAutopilotEvents(t, server, 1)
	waitForPolls(t, fake)

	if got := promptTexts(t, server, session.ID); len(got) != 1 {
		t.Fatalf("the quiet event started a turn: prompts = %q", got)
	}
	if ready := undelivered(t, server); len(ready) != 1 || ready[0].Kind != "autopilot" {
		t.Fatalf("ready events = %+v", ready)
	}

	dispatchTask(fake, 41, "Add plan model")
	waitForLeadPrompts(t, server, 2)

	prompt := leadPrompts(t, server)[1]
	if !strings.Contains(prompt, quietSection(payloads[0])+"# Event\n\n") {
		t.Errorf("the prompt has no quiet section before the event: %s", prompt)
	}
	waitForNoUndeliveredEvents(t, server)
}

func TestTwoQuietEventsComeInTheNextPromptInTheOrderOfTheEvents(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n", keepSessionOpen)

	sendChat(t, server, leadChat, "Plan the loyalty API")
	waitForChat(t, server, leadChat, "Lead", "Seen")
	waitForLeadPrompts(t, server, 1)
	session := chatSessions(t, server, leadChat, engine.LeadRole)[0]

	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	waitForAutopilotEvents(t, server, 1)
	fake.RemoveLabel(shop, 12, "mobius:autopilot", "owner")
	payloads := waitForAutopilotEvents(t, server, 2)
	waitForPolls(t, fake)

	if got := promptTexts(t, server, session.ID); len(got) != 1 {
		t.Fatalf("the quiet events started a turn: prompts = %q", got)
	}

	dispatchTask(fake, 41, "Add plan model")
	waitForLeadPrompts(t, server, 2)

	prompt := leadPrompts(t, server)[1]
	if !strings.Contains(prompt, quietSection(payloads...)+"# Event\n\n") {
		t.Errorf("the prompt has no quiet section before the event: %s", prompt)
	}
	inOrder(t, prompt, quietSection(payloads...), payloads[0], payloads[1], "# Event\n\n")
	waitForNoUndeliveredEvents(t, server)
}

func TestAQuietEventWithNoLeadSessionWaitsForTheFirstPromptOfTheNextSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n", keepSessionOpen)

	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	payloads := waitForAutopilotEvents(t, server, 1)
	waitForPolls(t, fake)

	if sessions := chatSessions(t, server, leadChat, engine.LeadRole); len(sessions) != 0 {
		t.Fatalf("the quiet event started a session: %+v", sessions)
	}

	sendChat(t, server, leadChat, "Plan the next step")
	waitForChat(t, server, leadChat, "Lead", "Seen")

	session := chatSessions(t, server, leadChat, engine.LeadRole)[0]
	prompt := promptTexts(t, server, session.ID)[0]
	if got := strings.Count(prompt, payloads[0]); got != 1 {
		t.Errorf("the quiet event shows %d times in the first prompt: %s", got, prompt)
	}
	inOrder(t, prompt, "# Chat history", quietSection(payloads[0]), "# Owner message", "Plan the next step")
	waitForNoUndeliveredEvents(t, server)
}

func TestTheSavePromptOfAnIdleCloseTakesNoQuietEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Plan the next step\"\nreply = [\"Next answer\"]\n\n[[prompts]]\nreply = [\"Seen\"]\n", func(cfg *config.Config) {
		cfg.LeadIdleTimeout = 3 * time.Second
	})

	sendChat(t, server, leadChat, "Plan the loyalty API")
	waitForChat(t, server, leadChat, "Lead", "Seen")

	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	payloads := waitForAutopilotEvents(t, server, 1)
	waitForPolls(t, fake)

	ended := endedChatSession(t, server, 0)
	prompts := promptTexts(t, server, ended.ID)
	if got := prompts[len(prompts)-1]; got != "Save in the Workstream memory what the next session needs." {
		t.Errorf("the last prompt is not the save prompt: %q", got)
	}
	if len(prompts) != 2 {
		t.Errorf("prompts = %q", prompts)
	}
	if ready := undelivered(t, server); len(ready) != 1 || ready[0].Kind != "autopilot" {
		t.Fatalf("the save prompt took the quiet event: %+v", ready)
	}

	sendChat(t, server, leadChat, "Plan the next step")
	waitForChat(t, server, leadChat, "Lead", "Next answer")

	prompt := promptTexts(t, server, chatSessions(t, server, leadChat, engine.LeadRole)[1].ID)[0]
	if !strings.Contains(prompt, quietSection(payloads[0])+"# Owner message\n\n") {
		t.Errorf("the next session has no quiet section before the message: %s", prompt)
	}
	waitForNoUndeliveredEvents(t, server)
}
