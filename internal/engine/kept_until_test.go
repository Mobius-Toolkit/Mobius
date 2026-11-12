package engine_test

import (
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// keptUntilOf gives KeptUntil of the node of the session in the Workstream #12.
func keptUntilOf(t *testing.T, server *testserver.Server, session int64) time.Time {
	t.Helper()
	for _, node := range tree(t, server) {
		if node.Session.ID == session {
			return node.KeptUntil
		}
	}
	t.Fatalf("no node of the session %d", session)
	return time.Time{}
}

// waitForKeptChange waits for a change of the node of the session whose KeptUntil is set when kept, and zero when not.
func waitForKeptChange(t *testing.T, changes <-chan engine.Change, session int64, kept bool) {
	t.Helper()
	waitForChange(t, changes, func(change engine.Change) bool {
		return change.Node != nil && change.Node.Session.ID == session && !change.Node.KeptUntil.IsZero() == kept
	})
}

// requireKeptUntilClears starts the first round, waits for its kept session, runs remove, and checks that the node of
// the round loses KeptUntil, in the change and in the tree.
func requireKeptUntilClears(t *testing.T, remove func(server *testserver.Server, fake *testkit.FakeGitHub, kept *runner.Session)) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, noChange)
	first := firstRound(t, server, fake)
	kept := waitForKept(t, server, 41)
	testkit.WaitFor(t, func() bool { return !keptUntilOf(t, server, first.ID).IsZero() })
	changes, stop := server.Engine.Listen()
	defer stop()

	remove(server, fake, kept)

	waitForKeptChange(t, changes, first.ID, false)
	if until := keptUntilOf(t, server, first.ID); !until.IsZero() {
		t.Errorf("KeptUntil = %v, want the zero time", until)
	}
}

// fixCommits is an Implementer that commits a new line of plan.txt in a fix round.
const fixCommits = "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo more >> plan.txt && git commit -q -am 'Add a line'\"\n\n"

func TestADoneRoundSetsKeptUntilOfItsRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, noChange)
	changes, stop := server.Engine.Listen()
	defer stop()
	before := time.Now()

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	change := waitForChange(t, changes, func(change engine.Change) bool {
		return change.Node != nil && change.Node.Session.Role == engine.ImplementerRole && !change.Node.KeptUntil.IsZero()
	})

	row := change.Node.Session.ID
	after := time.Now()
	if until := change.Node.KeptUntil; until.Before(before.Add(60*time.Minute)) || until.After(after.Add(60*time.Minute)) {
		t.Errorf("KeptUntil of the change = %v, want 60 minutes after the end of the round", until)
	}
	if until := keptUntilOf(t, server, row); until.Before(before.Add(60*time.Minute)) || until.After(after.Add(60*time.Minute)) {
		t.Errorf("KeptUntil of the tree = %v, want 60 minutes after the end of the round", until)
	}
	for _, node := range tree(t, server) {
		if node.Session.ID != row && !node.KeptUntil.IsZero() {
			t.Errorf("KeptUntil of the node of session %d (%s) = %v, want the zero time", node.Session.ID, node.Session.Role, node.KeptUntil)
		}
	}
}

func TestAContinuedRoundMovesKeptUntilToItsOwnRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, fixCommits+commits, noChange)
	first := firstRound(t, server, fake)
	waitForKept(t, server, 41)
	testkit.WaitFor(t, func() bool { return !keptUntilOf(t, server, first.ID).IsZero() })
	changes, stop := server.Engine.Listen()
	defer stop()

	startFixRound(t, server)

	waitForKeptChange(t, changes, first.ID, false)
	if until := keptUntilOf(t, server, first.ID); !until.IsZero() {
		t.Errorf("KeptUntil of the first row = %v, want the zero time", until)
	}
	second := waitForChange(t, changes, func(change engine.Change) bool {
		return change.Node != nil && change.Node.Session.Role == engine.ImplementerRole && change.Node.Session.ID != first.ID && !change.Node.KeptUntil.IsZero()
	}).Node.Session.ID
	if until := keptUntilOf(t, server, second); until.IsZero() {
		t.Error("KeptUntil of the continued row is the zero time")
	}
	if until := keptUntilOf(t, server, first.ID); !until.IsZero() {
		t.Errorf("KeptUntil of the first row = %v, want the zero time", until)
	}
}

func TestTheEndOfKeepTimeRemovesKeptUntil(t *testing.T) {
	t.Parallel()
	requireKeptUntilClears(t, func(server *testserver.Server, _ *testkit.FakeGitHub, _ *runner.Session) {
		server.Engine.SetClock(func() time.Time { return time.Now().Add(61 * time.Minute) })
	})
}

func TestTheEndOfTheHarnessProcessRemovesKeptUntil(t *testing.T) {
	t.Parallel()
	requireKeptUntilClears(t, func(_ *testserver.Server, _ *testkit.FakeGitHub, kept *runner.Session) {
		kept.Close()
	})
}

func TestTheEndOfTheTaskRemovesKeptUntil(t *testing.T) {
	t.Parallel()
	requireKeptUntilClears(t, func(server *testserver.Server, _ *testkit.FakeGitHub, _ *runner.Session) {
		sendChat(t, server, leadChat, "Decline it")
	})
}

func TestTheStopOfTheTaskRemovesKeptUntil(t *testing.T) {
	t.Parallel()
	requireKeptUntilClears(t, func(_ *testserver.Server, fake *testkit.FakeGitHub, _ *runner.Session) {
		testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") })
		fake.RemoveLabel(shop, 41, "mobius:working", "owner")
	})
}

func TestAChangeOfTheImplementerRoleRemovesKeptUntil(t *testing.T) {
	t.Parallel()
	requireKeptUntilClears(t, func(server *testserver.Server, _ *testkit.FakeGitHub, _ *runner.Session) {
		setRoleModel(t, server, engine.ImplementerRole, "opus")
	})
}
