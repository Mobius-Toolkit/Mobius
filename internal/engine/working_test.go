package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// isWorking gives the working field of the open session id in the agents list.
func isWorking(t *testing.T, server *testserver.Server, id int64) bool {
	t.Helper()
	for _, group := range overview(t, server).Groups {
		for _, row := range group.Agents {
			if row.Agent.ID == id {
				return row.Agent.Working
			}
		}
	}
	t.Fatalf("no open session %d", id)
	return false
}

func TestALeadWorksOnlyWhileATurnRuns(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nhang = true\n", keepSessionOpen)
	changes := listen(t, server)

	sendChat(t, server, leadChat, "Plan the loyalty API")

	lead := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return len(promptTexts(t, server, session.ID)) == 1
	})
	if !isWorking(t, server, lead.ID) {
		t.Error("the Lead in a turn does not work")
	}

	if _, err := server.DB.Exec("UPDATE sessions SET queue_reason = ? WHERE id = ?", "paused until 2026-10-04 10:00 UTC", lead.ID); err != nil {
		t.Fatal(err)
	}
	if isWorking(t, server, lead.ID) {
		t.Error("the paused Lead works")
	}
	if _, err := server.DB.Exec("UPDATE sessions SET queue_reason = NULL WHERE id = ?", lead.ID); err != nil {
		t.Fatal(err)
	}

	stopChat(t, server, leadChat)

	waitForChange(t, changes, func(change engine.Change) bool {
		return change.Node != nil && change.Node.Session.ID == lead.ID && !change.Node.Working
	})
	if isWorking(t, server, lead.ID) {
		t.Error("the idle Lead works")
	}
}

func TestAnImplementerWorksUnlessItWaitsForASlotOrAPause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 })
	running := start(t, server, implementerSpec(t, server, fake, 41))
	waiting := startLater(t.Context(), server, implementerSpec(t, server, fake, 43))
	waitingID := queued(t, server, engine.ImplementerRole).ID

	if !isWorking(t, server, running.ID()) || isWorking(t, server, waitingID) {
		t.Errorf("running = %v, waiting = %v", isWorking(t, server, running.ID()), isWorking(t, server, waitingID))
	}

	for reason, want := range map[string]bool{
		"runs .mobius/check":                true,
		"waits for a check slot":            false,
		"waits for a low load":              false,
		"paused until 2026-10-04 10:00 UTC": false,
	} {
		if _, err := server.DB.Exec("UPDATE sessions SET queue_reason = ? WHERE id = ?", reason, running.ID()); err != nil {
			t.Fatal(err)
		}
		if got := isWorking(t, server, running.ID()); got != want {
			t.Errorf("%s: working = %v", reason, got)
		}
	}

	end(t, running, "done")
	if node := tree(t, server)[0]; node.Session.ID != running.ID() || node.Working {
		t.Errorf("ended session = %+v", node)
	}
	end(t, await(t, waiting), "done")
}
