package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func TestALaterPollStartsTheLeadAfterAFailedStartAndGivesItTheEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, fmt.Sprintf("skip_tools_list = %d\n", 2+checkedKeys)+options+"[[prompts]]\nreply = [\"Seen\"]\n", func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })

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

// threeFailedLeadStarts fails the first three Lead starts. A start tries max_worker_restarts + 1 = 2 processes. The
// access checks start checkedKeys processes before the Lead.
var threeFailedLeadStarts = fmt.Sprintf("skip_tools_list = %d\n", 6+checkedKeys) + options + "[[prompts]]\nreply = [\"Seen\"]\n"

func leadNotTaken(t *testing.T, server *testserver.Server) activity {
	t.Helper()
	return testkit.WaitForValue(t, func() (activity, bool) {
		for _, a := range activities(t, server) {
			if strings.HasPrefix(a.Text, "The Lead did not take this event: ") {
				return a, true
			}
		}
		return activity{}, false
	})
}

func TestThreeFailedLeadStartsGiveTheEventToTheActivityFeedAndStartNoFourthSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, threeFailedLeadStarts, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })

	dispatchTask(fake, 41, "Add plan model")

	failed := leadNotTaken(t, server)
	if failed.Workstream != 12 || failed.Issue != 12 || !strings.Contains(failed.Text, " dispatch of #41 ") {
		t.Errorf("activity = %+v", failed)
	}
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 3 || len(undelivered(t, server)) != 0 || len(leadPrompts(t, server)) != 0 {
		t.Errorf("sessions = %+v", sessions)
	}
	for _, session := range sessions {
		if session.EndReason.String != "failed" {
			t.Errorf("session = %+v", session)
		}
	}
}

func TestANewLeadEventAfterTheLimitStartsANewLeadSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, threeFailedLeadStarts, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })
	dispatchTask(fake, 41, "Add plan model")
	leadNotTaken(t, server)

	dispatchTask(fake, 42, "Add plan form")

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := leadPrompts(t, server)
		return prompts, len(prompts) == 1 && len(undelivered(t, server)) == 0
	})
	_, event, _ := strings.Cut(prompts[0], "# Event\n\n")
	if !strings.Contains(event, " dispatch of #42 ") || strings.Contains(event, " dispatch of #41 ") {
		t.Errorf("prompt = %q", prompts[0][max(0, len(prompts[0])-200):])
	}
	if sessions := chatSessions(t, server, leadChat, engine.LeadRole); len(sessions) != 4 {
		t.Errorf("sessions = %+v", sessions)
	}
}
