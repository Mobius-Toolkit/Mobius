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
	for _, workstream := range []int64{50, 51} {
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
