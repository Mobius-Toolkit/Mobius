package engine_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	chatWaitPrompt = "Check each background task that you started."
	savePrompt     = "Save in the Workstream memory what the next session needs."
)

// leadMessages gives the text of each Lead message of the Lead chat.
func leadMessages(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var texts []string
	for _, line := range chatLines(t, server, leadChat) {
		if line.Author == "Lead" {
			texts = append(texts, line.Text)
		}
	}
	return texts
}

// Serial: the test changes backgroundCheckInterval.
func TestAnIdleLeadWithABackgroundAgentWaitsAndTellsTheResultOfTheTurnThatCallsDone(t *testing.T) {
	shortBackgroundCheck(t, 50*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	lead := `
[[prompts]]
updates = ['` + asyncAgentStart + `']
reply = ["A helper starts."]

[[prompts]]
reply = ["The helper still runs."]
call = { tool = "background_state", arguments = { state = "running" } }

[[prompts]]
reply = ["The helper result."]
call = { tool = "background_state", arguments = { state = "done" } }
`
	server, _ := connect(t, fake, lead)

	sendChat(t, server, leadChat, "Start a helper")

	session := endedChatSession(t, server, 0)
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 4 || !strings.HasPrefix(prompts[1], chatWaitPrompt) || !strings.HasPrefix(prompts[2], chatWaitPrompt) || prompts[3] != savePrompt {
		t.Fatalf("prompts = %q", prompts)
	}
	if want := []string{"A helper starts.", "The helper result.Mobius has the state done."}; !slices.Equal(leadMessages(t, server), want) {
		t.Errorf("Lead messages = %q", leadMessages(t, server))
	}
}

// Serial: the test changes backgroundCheckInterval.
func TestAnOwnerMessageEndsTheSleepOfTheWaitOfAnIdleLead(t *testing.T) {
	shortBackgroundCheck(t, time.Hour)
	fake := testkit.NewFakeGitHub(t)
	lead := `
[[prompts]]
updates = ['` + asyncAgentStart + `']
reply = ["A helper starts."]

[[prompts]]
reply = ["The helper still runs."]
call = { tool = "background_state", arguments = { state = "running" } }

[[prompts]]
reply = ["The second answer."]

[[prompts]]
reply = ["The helper result."]
call = { tool = "background_state", arguments = { state = "done" } }
`
	server, _ := connect(t, fake, lead)
	sendChat(t, server, leadChat, "Start a helper")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(store.Session) bool { return true })
	testkit.WaitFor(t, func() bool { return len(promptTexts(t, server, session.ID)) == 2 })
	testkit.WaitFor(t, func() bool { return !server.Engine.BackgroundTurnRuns(leadChat) })

	sendChat(t, server, leadChat, "A second question")

	session = endedChatSession(t, server, 0)
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 5 || !strings.HasPrefix(prompts[1], chatWaitPrompt) || prompts[2] != "A second question" || !strings.HasPrefix(prompts[3], chatWaitPrompt) || prompts[4] != savePrompt {
		t.Fatalf("prompts = %q", prompts)
	}
	if want := []string{"A helper starts.", "The second answer.", "The helper result.Mobius has the state done."}; !slices.Equal(leadMessages(t, server), want) {
		t.Errorf("Lead messages = %q", leadMessages(t, server))
	}
}

func TestAnIdleLeadWithNoBackgroundAgentGetsNoWaitPrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"First answer\"]\n")

	sendChat(t, server, leadChat, "Plan the loyalty API")

	prompts := promptTexts(t, server, endedChatSession(t, server, 0).ID)
	if len(prompts) != 2 || prompts[1] != savePrompt {
		t.Errorf("prompts = %q", prompts)
	}
}

// Serial: the test changes backgroundCheckInterval.
func TestAnOwnerMessageCancelsTheWaitTurnOfAnIdleLead(t *testing.T) {
	shortBackgroundCheck(t, time.Hour)
	fake := testkit.NewFakeGitHub(t)
	lead := `
[[prompts]]
updates = ['` + asyncAgentStart + `']
reply = ["A helper starts."]

[[prompts]]
reply = ["The cancelled text."]
hang = true

[[prompts]]
reply = ["The second answer."]

[[prompts]]
reply = ["The helper result."]
call = { tool = "background_state", arguments = { state = "done" } }
`
	server, _ := connect(t, fake, lead)
	sendChat(t, server, leadChat, "Start a helper")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(store.Session) bool { return true })
	testkit.WaitFor(t, func() bool { return len(promptTexts(t, server, session.ID)) == 2 })

	sendChat(t, server, leadChat, "A second question")

	session = endedChatSession(t, server, 0)
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 5 || !strings.HasPrefix(prompts[1], chatWaitPrompt) || prompts[2] != "A second question" || !strings.HasPrefix(prompts[3], chatWaitPrompt) || prompts[4] != savePrompt {
		t.Fatalf("prompts = %q", prompts)
	}
	if want := []string{"A helper starts.", "The second answer.", "The helper result.Mobius has the state done."}; !slices.Equal(leadMessages(t, server), want) {
		t.Errorf("Lead messages = %q", leadMessages(t, server))
	}
}

// Serial: the test changes backgroundCheckInterval.
func TestTheDrainCancelsTheWaitTurnOfAnIdleLeadAndClosesTheChatWithNoMoreWaitPrompt(t *testing.T) {
	shortBackgroundCheck(t, 50*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	lead := `
[[prompts]]
updates = ['` + asyncAgentStart + `']
reply = ["A helper starts."]

[[prompts]]
reply = ["The cancelled text."]
hang = true
`
	server, _ := connect(t, fake, lead)
	sendChat(t, server, leadChat, "Start a helper")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(store.Session) bool { return true })
	testkit.WaitFor(t, func() bool { return len(promptTexts(t, server, session.ID)) == 2 })

	ends := startDrain(t, server)

	if got := <-ends; got != "drained" {
		t.Fatalf("drain end = %s", got)
	}
	prompts := promptTexts(t, server, endedChatSession(t, server, 0).ID)
	if len(prompts) != 3 || !strings.HasPrefix(prompts[1], chatWaitPrompt) || prompts[2] != savePrompt {
		t.Fatalf("prompts = %q", prompts)
	}
	if want := []string{"A helper starts."}; !slices.Equal(leadMessages(t, server), want) {
		t.Errorf("Lead messages = %q", leadMessages(t, server))
	}
}

// Serial: the test changes backgroundCheckInterval.
func TestAnOwnerMessageDoesNotCancelAnAutonomousTurnThatHoldsTheWaitPromptOfAnIdleLead(t *testing.T) {
	shortBackgroundCheck(t, time.Hour)
	fake := testkit.NewFakeGitHub(t)
	lead := `
[[prompts]]
updates = ['` + asyncAgentStart + `']
reply = ["A helper starts."]
later = { after = "10ms", updates = ['` + workUpdate + `'] }

[[prompts]]
reply = ["The second answer."]

[[prompts]]
reply = ["The helper result."]
call = { tool = "background_state", arguments = { state = "done" } }
`
	server, _ := connect(t, fake, lead)
	sendChat(t, server, leadChat, "Start a helper")
	waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return lineID(t, server, session.ID, "update", "The task ended.") != 0
	})
	testkit.WaitFor(t, func() bool { return server.Engine.BackgroundTurnRuns(leadChat) })

	sendChat(t, server, leadChat, "A second question")

	testkit.WaitFor(t, func() bool { return server.Engine.ChatQueued(leadChat) == 0 })
	server.Engine.ChatAgent(leadChat).Update(fmt.Appendf(nil, agentUpdate, endUpdate))

	session := endedChatSession(t, server, 0)
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 4 || prompts[1] != "A second question" || !strings.HasPrefix(prompts[2], chatWaitPrompt) || prompts[3] != savePrompt {
		t.Fatalf("prompts = %q", prompts)
	}
	if want := []string{"A helper starts.The task ended.", "The second answer.", "The helper result.Mobius has the state done."}; !slices.Equal(leadMessages(t, server), want) {
		t.Errorf("Lead messages = %q", leadMessages(t, server))
	}
}

// Serial: the test changes backgroundCheckInterval.
func TestAnOwnerMessageThatCancelsAWaitTurnAfterDoneKeepsTheResultOfThatTurn(t *testing.T) {
	shortBackgroundCheck(t, time.Hour)
	fake := testkit.NewFakeGitHub(t)
	lead := `
[[prompts]]
updates = ['` + asyncAgentStart + `']
reply = ["A helper starts."]

[[prompts]]
reply = ["The helper result."]
call = { tool = "background_state", arguments = { state = "done" } }
hang = true

[[prompts]]
reply = ["The second answer."]
`
	server, _ := connect(t, fake, lead)
	sendChat(t, server, leadChat, "Start a helper")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(store.Session) bool { return true })
	testkit.WaitFor(t, func() bool { return len(promptTexts(t, server, session.ID)) == 2 })

	sendChat(t, server, leadChat, "A second question")

	session = endedChatSession(t, server, 0)
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 4 || !strings.HasPrefix(prompts[1], chatWaitPrompt) || prompts[2] != "A second question" || prompts[3] != savePrompt {
		t.Fatalf("prompts = %q", prompts)
	}
	if want := []string{"A helper starts.", "The helper result.Mobius has the state done.", "The second answer."}; !slices.Equal(leadMessages(t, server), want) {
		t.Errorf("Lead messages = %q", leadMessages(t, server))
	}
}
