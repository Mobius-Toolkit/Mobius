package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// holdImplementerStart makes the Harness of the Implementer wait at session/new until releaseHarnessReads. The access
// check of the start has passed before the call, and it is not due again.
func holdImplementerStart(t *testing.T, dataDir, script string) {
	t.Helper()
	testkit.InstallFakeHarness(t, dataDir, "devin", "session_new_hold = true\n"+script)
}

func TestTheSessionOfAnImplementerWaitsForTheStartOfItsHarness(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, "[[prompts]]\nhang = true\n", noChange)
	holdImplementerStart(t, dataDir, options+"[[prompts]]\nhang = true\n")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) == 1 && sessions[0].QueueWait.String == "harness_start" && sessions[0].QueueReason.String == "starts devin"
	})
	releaseHarnessReads(t, dataDir)

	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) == 1 && !sessions[0].QueueWait.Valid && !sessions[0].QueueReason.Valid && len(rows(t, server, sessions[0].ID, "prompt")) > 0
	})
}

func TestAFailedStartOfTheHarnessEndsTheWaitForTheStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, "[[prompts]]\nhang = true\n", noChange)
	holdImplementerStart(t, dataDir, "login_required = true\n"+options+"[[prompts]]\nhang = true\n")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) == 1 && sessions[0].QueueWait.String == "harness_start"
	})
	releaseHarnessReads(t, dataDir)

	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return sessions[0], sessions[0].EndedAt.Valid
	})
	if session.EndReason.String != "failed" || session.QueueWait.Valid || session.QueueReason.Valid {
		t.Errorf("session = %+v", session)
	}
}
