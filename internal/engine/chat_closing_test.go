package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestAStopThatDropsAMessageWhileTheSaveTurnWaitsForAnAutonomousTurnEndsTheWritingState(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"First\"]\nlater = { after = \"10ms\", updates = ['"+workUpdate+"'] }\n")
	sendChat(t, server, leadChat, "Plan the loyalty API")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return lineID(t, server, session.ID, "update", "The task ended.") != 0
	})
	testkit.WaitFor(t, func() bool { return server.Engine.Closing(leadChat) })

	sendChat(t, server, leadChat, "Also add a plan price")
	stopChat(t, server, leadChat)

	if chatView(t, server, leadChat).Writing {
		t.Error("the chat is still writing")
	}
	if got := promptTexts(t, server, session.ID); len(got) != 1 {
		t.Errorf("prompts = %q", got)
	}
}
