package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const noteScript = "[[prompts]]\nreply = [\"Noted.\"]\n"

// commentEventSQL is a comment event of the task issue number below the Workstream #12.
func commentEventSQL(issue int64, payload string) string {
	return fmt.Sprintf(`INSERT INTO lead_events (repository, workstream, issue_repository, issue, kind, payload, time)
		VALUES ('owner/shop', 12, 'owner/shop', %d, 'comment', '%s', '2026-10-04T10:00:00Z')`, issue, payload)
}

// startWithLeadEvents starts a server that already has statements in its database, so the first Lead chat gets each
// event of statements as a ready event. The fake GitHub has the task issues 41 and 42.
func startWithLeadEvents(t *testing.T, statements ...string) *testserver.Server {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	seed(t, dataDir, statements...)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	for _, number := range []int64{41, 42} {
		fake.AddIssue(shop, number, "Task")
		fake.AddSubIssue(shop, 12, number)
	}
	testkit.InstallFakeAgent(t, dataDir, options+noteScript+"\n"+noteScript)
	return startServer(t, fake, dataDir, "")
}

func firstTurnsOf(t *testing.T, server *testserver.Server, turns int) []string {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := leadPrompts(t, server)
		return prompts, len(prompts) == turns && eventDelivered(t, server)
	})
}

func testANewLeadChatStartsWithTheLaterEventOfACIFixTask(t *testing.T, record func(issue int64) string) {
	server := startWithLeadEvents(t, record(42), commentEventSQL(41, "Plain event."), commentEventSQL(42, "CI fix event."))

	prompts := firstTurnsOf(t, server, 2)

	if !strings.Contains(prompts[0], "CI fix event.") || !strings.Contains(prompts[1], "Plain event.") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestANewLeadChatStartsWithTheLaterEventOfAnEndedHardBreakTask(t *testing.T) {
	t.Parallel()
	testANewLeadChatStartsWithTheLaterEventOfACIFixTask(t, endedHardBreakSQL)
}

func TestANewLeadChatStartsWithTheLaterEventOfAFlakyBreakTask(t *testing.T) {
	t.Parallel()
	testANewLeadChatStartsWithTheLaterEventOfACIFixTask(t, flakyBreakSQL)
}

func TestANewLeadChatStartsWithTheLaterEventOfACIFailureRecordTask(t *testing.T) {
	t.Parallel()
	testANewLeadChatStartsWithTheLaterEventOfACIFixTask(t, openCIFailureSQL)
}

func TestANewLeadChatKeepsTheIdOrderOfEventsWithNoCIFixTask(t *testing.T) {
	t.Parallel()
	server := startWithLeadEvents(t, commentEventSQL(41, "First event."), commentEventSQL(42, "Second event."))

	prompts := firstTurnsOf(t, server, 2)

	if !strings.Contains(prompts[0], "First event.") || !strings.Contains(prompts[1], "Second event.") {
		t.Errorf("prompts = %q", prompts)
	}
}

// slowLeadScript holds the turn for the dispatch of #41 until the file release exists.
func slowLeadScript(release string) string {
	return fmt.Sprintf("[[prompts]]\nwhen = \" dispatch of #41 \"\nshell = 'until [ -e %s ]; do sleep 0.05; done'\n\n", release) + noteScript + "\n" + noteScript + "\n" + noteScript + "\n" + noteScript
}

// dispatchBehindASlowTurn starts the turn for the dispatch of #41, and then dispatches each issue of numbers in this
// order. It returns the path of the file whose creation ends the turn.
func dispatchBehindASlowTurn(t *testing.T, records map[int64]string, numbers ...int64) (*testserver.Server, string) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	release := filepath.Join(t.TempDir(), "release")
	server, _ := connectWith(t, fake, slowLeadScript(release), keepSessionOpen)
	for _, record := range records {
		addRecord(t, server, record)
	}
	dispatchTask(fake, 41, "Add plan model")
	waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return len(promptTexts(t, server, session.ID)) == 1
	})
	for index, number := range numbers {
		dispatchTask(fake, number, fmt.Sprintf("Task %d", number))
		testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == index+2 })
	}
	return server, release
}

func releaseTurn(t *testing.T, release string) {
	t.Helper()
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAnEventOfACIFixTaskGetsTheTurnBeforeAQueuedEventAndAfterAnOwnerMessage(t *testing.T) {
	t.Parallel()
	server, release := dispatchBehindASlowTurn(t, map[int64]string{43: openCIFailureSQL(43)}, 42, 43)
	sendChat(t, server, leadChat, "Decision")

	releaseTurn(t, release)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := onlySession(t, server)
		return prompts, len(prompts) == 4 && eventDelivered(t, server)
	})
	if !strings.Contains(prompts[0], " dispatch of #41 ") || prompts[1] != "Decision" || !strings.Contains(prompts[2], " dispatch of #43 ") || !strings.Contains(prompts[3], " dispatch of #42 ") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTwoEventsOfCIFixTasksGetTheirTurnsInTheOrderOfTheirIds(t *testing.T) {
	t.Parallel()
	records := map[int64]string{43: openCIFailureSQL(43), 44: flakyBreakSQL(44)}
	server, release := dispatchBehindASlowTurn(t, records, 42, 43, 44)

	releaseTurn(t, release)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := onlySession(t, server)
		return prompts, len(prompts) == 4 && eventDelivered(t, server)
	})
	if !strings.Contains(prompts[1], " dispatch of #43 ") || !strings.Contains(prompts[2], " dispatch of #44 ") || !strings.Contains(prompts[3], " dispatch of #42 ") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestEventsWithNoCIFixTaskKeepTheirIdOrderBehindASlowTurn(t *testing.T) {
	t.Parallel()
	server, release := dispatchBehindASlowTurn(t, nil, 42, 43)

	releaseTurn(t, release)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := onlySession(t, server)
		return prompts, len(prompts) == 3 && eventDelivered(t, server)
	})
	if !strings.Contains(prompts[1], " dispatch of #42 ") || !strings.Contains(prompts[2], " dispatch of #43 ") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTheLeadSessionOfACIFixEventTakesTheLeadSlotBeforeAnEarlierLeadSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	for _, workstream := range []int64{50, 51, 60} {
		fake.AddIssue(shop, workstream, "Workstream")
		fake.AddLabel(shop, workstream, "mobius:workstream", "owner")
	}
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Plan the API\"\nhang = true\n\n"+noteScript, func(cfg *config.Config) {
		cfg.Roles.Lead.Max = 1
	})
	addRecord(t, server, openCIFailureSQL(53))
	sendChat(t, server, leadChat, "Plan the API")
	testkit.WaitFor(t, func() bool { return len(leadPrompts(t, server)) == 1 })
	plain := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 50}
	fix := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 51}
	for _, task := range []struct {
		key    engine.ChatKey
		number int64
	}{{plain, 52}, {fix, 53}} {
		fake.AddIssue(shop, task.number, "Task")
		fake.AddSubIssue(shop, task.key.Workstream, task.number)
		fake.AddLabel(shop, task.number, "mobius:ready", "owner")
		waitForChatSession(t, server, task.key, engine.LeadRole, func(session store.Session) bool { return session.QueueReason.Valid })
	}

	stopChat(t, server, leadChat)

	started := func(key engine.ChatKey) store.Session {
		return waitForChatSession(t, server, key, engine.LeadRole, func(session store.Session) bool {
			return !session.QueueReason.Valid && len(promptTexts(t, server, session.ID)) > 0
		})
	}
	plainSession, fixSession := started(plain), started(fix)
	if !parseTime(t, fixSession.StartedAt).Before(parseTime(t, plainSession.StartedAt)) {
		t.Errorf("the session of the CI fix event started at %s, the earlier session at %s", fixSession.StartedAt, plainSession.StartedAt)
	}
}

func TestFreedHeldEventsGoAfterTheCIFixEventsOfTheQueueAndTheFreedCIFixEventGoesFirst(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	release := filepath.Join(t.TempDir(), "release")
	script := fmt.Sprintf("[[prompts]]\n%s\n\n[[prompts]]\n%s\n\n[[prompts]]\nwhen = \"Decision\"\nshell = 'until [ -e %s ]; do sleep 0.05; done'\n\n",
		holdEvent, holdEvent, release) + strings.Repeat(noteScript+"\n", 4)
	server, _ := connectWith(t, fake, script, keepSessionOpen)
	addRecord(t, server, openCIFailureSQL(43))
	addRecord(t, server, endedHardBreakSQL(44))
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return held(t, server, 1) })
	dispatchTask(fake, 44, "Fix the plan test")
	testkit.WaitFor(t, func() bool { return held(t, server, 2) && len(onlySession(t, server)) == 2 })
	sendChat(t, server, leadChat, "Decision")
	testkit.WaitFor(t, func() bool { return len(onlySession(t, server)) == 3 })
	dispatchTask(fake, 43, "Fix the plan check")
	dispatchTask(fake, 42, "Add plan route")
	testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == 4 })

	releaseTurn(t, release)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := onlySession(t, server)
		return prompts, len(prompts) == 7 && eventDelivered(t, server)
	})
	for index, number := range []string{"#43", "#44", "#41", "#42"} {
		if !strings.Contains(prompts[3+index], " dispatch of "+number+" ") {
			t.Fatalf("prompts = %q", prompts)
		}
	}
}

// leadSlotServer starts a server with one Lead slot, the Workstreams #50, #51 and #60, and a direct Lead session that
// holds the slot.
func leadSlotServer(t *testing.T, fake *testkit.FakeGitHub, script string) (*testserver.Server, *engine.Agent) {
	t.Helper()
	for _, workstream := range []int64{50, 51, 60} {
		fake.AddIssue(shop, workstream, "Workstream")
		fake.AddLabel(shop, workstream, "mobius:workstream", "owner")
	}
	server, _ := connectWith(t, fake, script, func(cfg *config.Config) {
		keepSessionOpen(cfg)
		cfg.Roles.Lead.Max = 1
	})
	return server, start(t, server, leadSpec(t))
}

// dispatchBelow adds the task issue number below the Workstream workstream with mobius:ready, and waits for its
// Lead session in the queue of the Lead slot.
func dispatchBelow(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, workstream, number int64) {
	t.Helper()
	fake.AddIssue(shop, number, "Task")
	fake.AddSubIssue(shop, workstream, number)
	fake.AddLabel(shop, number, "mobius:ready", "owner")
	key := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: workstream}
	waitForChatSession(t, server, key, engine.LeadRole, func(session store.Session) bool { return session.QueueReason.Valid })
}

func TestAWaitingLeadSessionThatGetsACIFixEventTakesTheLeadSlotBeforeAnEarlierLeadSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, holder := leadSlotServer(t, fake, noteScript+"\n"+noteScript+"\n"+noteScript)
	addRecord(t, server, openCIFailureSQL(54))
	dispatchBelow(t, server, fake, 51, 53)
	dispatchBelow(t, server, fake, 50, 52)
	fake.AddIssue(shop, 54, "Task")
	fake.AddSubIssue(shop, 50, 54)
	fake.AddLabel(shop, 54, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM lead_events WHERE workstream = 50") == 2
	})
	waitForPolls(t, fake)

	end(t, holder, "done")

	fix := waitForChatSession(t, server, engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 50}, engine.LeadRole, func(session store.Session) bool {
		return !session.QueueReason.Valid && len(promptTexts(t, server, session.ID)) > 0
	})
	earlier := chatSessions(t, server, engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 51}, engine.LeadRole)
	if len(earlier) != 1 || !earlier[0].QueueReason.Valid {
		t.Errorf("the session of the CI fix event started at %s, the earlier session is %+v", fix.StartedAt, earlier)
	}
}

func TestANewSessionOfALeadChatWhoseFirstItemIsNoCIFixEventWaitsBehindALaterCIFixSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	crash := "[[prompts]]\nwhen = \" dispatch of #54 \"\nerror = { code = -32000, message = \"The agent crashed\" }\n\n"
	server, holder := leadSlotServer(t, fake, crash+strings.Repeat(noteScript+"\n", 3))
	addRecord(t, server, openCIFailureSQL(52))
	addRecord(t, server, openCIFailureSQL(61))
	crashing := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 50}
	later := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 60}
	dispatchBelow(t, server, fake, 50, 52)
	fake.AddIssue(shop, 54, "Task")
	fake.AddSubIssue(shop, 50, 54)
	fake.AddLabel(shop, 54, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM lead_events WHERE workstream = 50") == 2
	})
	waitForPolls(t, fake)
	// The session of a direct start takes the slot that the crashed session frees, so the new session of the chat
	// queues while the slot is held.
	taker := startLater(t.Context(), server, leadSpec(t))
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM sessions WHERE workstream = 12 AND queue_reason IS NOT NULL") == 1
	})

	end(t, holder, "done")

	_, takerAgent := nextToStart(t, []<-chan started{taker})
	waitForChatSession(t, server, crashing, engine.LeadRole, func(session store.Session) bool {
		return session.QueueReason.Valid && session.ID != chatSessions(t, server, crashing, engine.LeadRole)[0].ID
	})
	dispatchBelow(t, server, fake, 60, 61)

	end(t, takerAgent, "done")

	waitForChatSession(t, server, later, engine.LeadRole, func(session store.Session) bool {
		return !session.QueueReason.Valid && len(promptTexts(t, server, session.ID)) > 0
	})
	for _, session := range chatSessions(t, server, crashing, engine.LeadRole)[1:] {
		if !session.QueueReason.Valid {
			t.Errorf("the session %+v started before the session of the CI fix event", session)
		}
	}
}
