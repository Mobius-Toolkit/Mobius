package engine_test

import (
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	holdingScript = "[[prompts]]\nwhen = \"Plan the loyalty API\"\nhang = true\n[[prompts]]\nreply = [\"Done.\"]\n"
	pointsPath    = "/api/chat?organization=owner&repository=owner/shop&workstream=50"
)

var pointsChat = engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 50}

// connectWaitingPoints starts a Lead chat that holds the only Lead slot, and gives a Workstream chat that waits for it.
func connectWaitingPoints(t *testing.T) (*testserver.Server, *testkit.FakeGitHub) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 50, "Loyalty points")
	fake.AddLabel(shop, 50, "mobius:workstream", "owner")
	server, _ := connectWith(t, fake, holdingScript, func(cfg *config.Config) {
		cfg.Roles.Lead.Max = 1
	})
	sendChat(t, server, leadChat, "Plan the loyalty API")
	testkit.WaitFor(t, func() bool { return len(leadPrompts(t, server)) == 1 })
	return server, fake
}

func endHoldingChat(t *testing.T, server *testserver.Server) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		stopChat(t, server, leadChat)
		sessions := chatSessions(t, server, leadChat, engine.LeadRole)
		return len(sessions) > 0 && sessions[0].EndedAt.Valid
	})
}

func TestAStopOfAWaitingChatMarksTheFirstOwnerMessageAsStopped(t *testing.T) {
	t.Parallel()
	server, _ := connectWaitingPoints(t)
	sendChat(t, server, pointsChat, "Message A")
	waitForChatSession(t, server, pointsChat, engine.LeadRole, func(session store.Session) bool { return session.QueueReason.Valid })
	sendChat(t, server, pointsChat, "Message B")
	changes, stop := server.Engine.Listen()
	defer stop()

	stopChat(t, server, pointsChat)

	stopped := waitForChange(t, changes, func(change engine.Change) bool {
		return change.Message != nil && change.Message.StoppedAt.Valid
	}).Message
	if stopped.Text != "Message A" || stopped.DeliveredAt.Valid {
		t.Errorf("stopped message = %+v", stopped)
	}
	messages := apiData[struct {
		Messages []apiChatMessage `json:"messages"`
	}](t, server, pointsPath).Messages
	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	stoppedAt, err := time.Parse(time.RFC3339Nano, stopped.StoppedAt.String)
	if err != nil {
		t.Fatal(err)
	}
	if got := messages[0]; got.Text != "Message A" || got.StoppedAt == nil || !got.StoppedAt.Equal(stoppedAt) {
		t.Errorf("message A = %+v, stop time = %s", got, stopped.StoppedAt.String)
	}
	if got := messages[1]; got.Text != "Message B" || got.StoppedAt != nil {
		t.Errorf("message B = %+v", got)
	}
	endHoldingChat(t, server)
}

func TestAStopOfAWaitingChatWithOneMessageMarksTheMessageAsStopped(t *testing.T) {
	t.Parallel()
	server, _ := connectWaitingPoints(t)
	sendChat(t, server, pointsChat, "Message A")
	waitForChatSession(t, server, pointsChat, engine.LeadRole, func(session store.Session) bool { return session.QueueReason.Valid })

	stopChat(t, server, pointsChat)

	messages := apiData[struct {
		Messages []apiChatMessage `json:"messages"`
	}](t, server, pointsPath).Messages
	if len(messages) != 1 || messages[0].StoppedAt == nil {
		t.Errorf("messages = %+v", messages)
	}
	endHoldingChat(t, server)
}

func TestAStopDuringTheTurnOfAnOwnerMessageDoesNotMarkTheMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, holdingScript)
	sendChat(t, server, leadChat, "Plan the loyalty API")
	testkit.WaitFor(t, func() bool { return len(leadPrompts(t, server)) == 1 })

	// The agent can miss a stop that comes before it reads the prompt, so the test stops again until the turn ends.
	testkit.WaitFor(t, func() bool {
		stopChat(t, server, leadChat)
		return !chatView(t, server, leadChat).Writing
	})

	message := chatView(t, server, leadChat).Messages[0]
	if message.StoppedAt.Valid || !message.DeliveredAt.Valid {
		t.Errorf("message = %+v", message)
	}
}

func TestAStopWhileTheFirstItemIsAnEventMarksNoMessage(t *testing.T) {
	t.Parallel()
	server, fake := connectWaitingPoints(t)
	fake.AddIssue(shop, 51, "Add points model")
	fake.AddSubIssue(shop, 50, 51)
	fake.AddLabel(shop, 51, "mobius:ready", "owner")
	waitForChatSession(t, server, pointsChat, engine.LeadRole, func(session store.Session) bool { return session.QueueReason.Valid })
	sendChat(t, server, pointsChat, "Message A")

	stopChat(t, server, pointsChat)

	for _, message := range chatView(t, server, pointsChat).Messages {
		if message.StoppedAt.Valid {
			t.Errorf("message = %+v", message)
		}
	}
	endHoldingChat(t, server)
}
