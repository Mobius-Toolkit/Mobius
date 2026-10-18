package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestALaterPollStartsTheLeadAfterAFailedStartAndGivesItTheEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, "skip_tools_list = 2\n"+options+"[[prompts]]\nreply = [\"Seen\"]\n", func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })

	dispatchTask(fake, 41, "Add plan model")

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := leadPrompts(t, server)
		return prompts, len(prompts) == 1 && eventDelivered(t, server)
	})
	if !strings.Contains(prompts[0], "# Event\n\n") || !strings.Contains(prompts[0], " dispatch of #41 ") {
		t.Errorf("prompt = %q", prompts[0][max(0, len(prompts[0])-200):])
	}
	if failed := chatSessions(t, server, leadChat, engine.LeadRole)[0]; failed.EndReason.String != "failed" {
		t.Errorf("first session = %+v", failed)
	}
}

func TestMorePollsStartNoSecondLeadSessionWhileALeadTurnRuns(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nhang = true\n", keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return len(promptTexts(t, server, session.ID)) == 1
	})

	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if sessions := chatSessions(t, server, leadChat, engine.LeadRole); len(sessions) != 1 || len(undelivered(t, server)) != 1 {
		t.Errorf("sessions = %+v", sessions)
	}
}

func TestMorePollsStartNoLeadSessionForAHeldEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\n"+holdEvent+"\n")
	dispatchTask(fake, 41, "Add plan model")
	waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return session.EndedAt.Valid && held(t, server, 1)
	})

	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if sessions := chatSessions(t, server, leadChat, engine.LeadRole); len(sessions) != 1 || !held(t, server, 1) {
		t.Errorf("sessions = %+v", sessions)
	}
}
