package engine_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	// noKey makes a fake Harness fail the access check prompt.
	noKey = "[[prompts]]\nwhen = \"the word OK\"\nerror = { code = -32603, message = \"turn failed: no API key for provider route\" }\n"
	// countStarts makes the fake Harnesses count each session/new in the file starts, so each access check counts one.
	countStarts = "skip_tools_list = 1000\n"
	// checkedKeys is the number of different Harness, model and effort triples of the Role bindings of testserver.Config.
	checkedKeys = 6
)

// accessServer starts a server whose Harnesses play header and options. Devin also plays the prompts of failing. The
// server has the repository owner/shop. It waits until each Role binding has an access check result.
func accessServer(t *testing.T, header, failing string) (*testserver.Server, *testkit.FakeGitHub, string) {
	t.Helper()
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, header+options)
	testkit.InstallFakeHarness(t, dataDir, "devin", header+options+failing)
	fake := testkit.NewFakeGitHub(t)
	server := startServerWith(t, fake, testserver.Config(t, dataDir), "")
	testkit.WaitFor(t, server.Engine.AccessChecked)
	return server, fake, dataDir
}

func accessSpec(t *testing.T) engine.Spec {
	t.Helper()
	return engine.Spec{Role: engine.ImplementerRole, Organization: "owner", Repository: shop, Workstream: 12, Dir: t.TempDir()}
}

func accessItems(t *testing.T, server *testserver.Server) []inboxItem {
	t.Helper()
	var items []inboxItem
	for _, item := range allInbox(t, server) {
		if item.Kind == "access check" {
			items = append(items, item)
		}
	}
	return items
}

func waitForAccessItems(t *testing.T, server *testserver.Server, count int) []inboxItem {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]inboxItem, bool) {
		items := accessItems(t, server)
		return items, len(items) == count
	})
}

func TestARoleWhoseHarnessFailsTheAccessCheckGetsNoSessionAndOneInboxItem(t *testing.T) {
	t.Parallel()
	server, _, _ := accessServer(t, "", noKey)
	held := startLater(t.Context(), server, accessSpec(t))

	waiting := queued(t, server, engine.ImplementerRole)
	items := waitForAccessItems(t, server, 1)

	if !strings.HasPrefix(waiting.QueueReason.String, "access check failed: devin: turn failed: no API key for provider route") {
		t.Errorf("queue reason = %q", waiting.QueueReason.String)
	}
	item := items[0]
	if item.Title != "Access check failed: devin swe-1.5 high" || item.Issue != 0 ||
		!strings.Contains(item.Text, "implementer") || !strings.Contains(item.Text, "no API key for provider route") ||
		!strings.HasSuffix(item.Link, "docs/install.md#3-install-and-log-in-to-the-harnesses") {
		t.Errorf("item = %+v", item)
	}
	lead := start(t, server, leadSpec(t))
	end(t, lead, "idle")
	select {
	case got := <-held:
		t.Errorf("the Implementer started: %+v", got)
	default:
	}
}

func TestRolesWithTheSameHarnessModelAndEffortShareOneCheckAndOneInboxItem(t *testing.T) {
	t.Parallel()
	server, _, dataDir := accessServer(t, countStarts, noKey)

	items := waitForAccessItems(t, server, 1)

	if starts := agentStarts(t, dataDir); starts != checkedKeys {
		t.Errorf("starts = %d, want %d", starts, checkedKeys)
	}
	if !strings.Contains(items[0].Text, "implementer") {
		t.Errorf("text = %q", items[0].Text)
	}
}

func TestAFailedKeyGetsACheckAfterOneHourAndAPassStartsTheSessionAndDismissesTheItem(t *testing.T) {
	t.Parallel()
	server, fake, dataDir := accessServer(t, countStarts, noKey)
	held := startLater(t.Context(), server, accessSpec(t))
	waitForAccessItems(t, server, 1)
	testkit.InstallFakeHarness(t, dataDir, "devin", countStarts+options)
	starts := agentStarts(t, dataDir)
	server.Engine.SetClock(func() time.Time { return time.Now().Add(59 * time.Minute) })
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if got := queued(t, server, engine.ImplementerRole); !strings.HasPrefix(got.QueueReason.String, "access check failed:") || agentStarts(t, dataDir) != starts {
		t.Errorf("queue reason = %q, starts = %d, want %d", got.QueueReason.String, agentStarts(t, dataDir), starts)
	}
	server.Engine.SetClock(func() time.Time { return time.Now().Add(time.Hour) })

	agent := await(t, held)
	defer end(t, agent, "idle")
	waitForAccessItems(t, server, 0)
}

func TestASessionThatWaitsOnAFailedKeyStartsOnTheNewBindingAfterAPutOfItsRole(t *testing.T) {
	t.Parallel()
	server, _, _ := accessServer(t, "", noKey)
	held := startLater(t.Context(), server, accessSpec(t))
	queued(t, server, engine.ImplementerRole)
	waitForAccessItems(t, server, 1)

	putRole(t, server, "implementer", `{"harness": "claude-code", "model": "haiku", "effort": "high", "max": 1}`)

	agent := await(t, held)
	defer end(t, agent, "idle")
	if got := session(t, server, agent.ID()); got.Harness != "claude-code" || got.Model != "haiku" || got.QueueReason.Valid {
		t.Errorf("session = %+v", got)
	}
	waitForAccessItems(t, server, 0)
}

func TestAPutWhoseAccessCheckFailsGives422AndKeepsTheValueAndAPutOfOnlyMaxRunsNoCheck(t *testing.T) {
	t.Parallel()
	server, _, dataDir := accessServer(t, countStarts, "")
	testkit.InstallFakeAgent(t, dataDir, countStarts+options+noKey)
	before := agentStarts(t, dataDir)

	status, text := sendRole(t, server, "lead", leadHaiku)

	if status != http.StatusUnprocessableEntity || !strings.Contains(text, "access check failed: claude-agent-acp: turn failed: no API key for provider route") {
		t.Errorf("status %d, answer %s", status, text)
	}
	if got := getRole(t, server, "lead"); got.Model != "opus" || got.UI != (roleFields{}) || roleRows(t, server) != 0 {
		t.Errorf("lead = %+v, rows = %d", got, roleRows(t, server))
	}
	if starts := agentStarts(t, dataDir); starts != before+1 {
		t.Errorf("starts after the PUT of the model = %d, want %d", starts, before+1)
	}
	got := putRole(t, server, "lead", `{"harness": "claude-code", "model": "opus", "effort": "high", "max": 3}`)
	if !got.UI.Max || agentStarts(t, dataDir) != before+1 {
		t.Errorf("answer = %+v, starts = %d, want %d", got, agentStarts(t, dataDir), before+1)
	}
}

func TestAPutWhoseAccessCheckPassesStartsTheNextSessionWithNoWait(t *testing.T) {
	t.Parallel()
	server, _, _ := accessServer(t, "", "")

	putRole(t, server, "lead", leadHaiku)

	id := run(t, server, leadSpec(t), "Who runs this session?")
	if got := session(t, server, id); got.Model != "haiku" || got.QueueReason.Valid {
		t.Errorf("session = %+v", got)
	}
}

func TestAnAccessCheckAddsNoSessionNoTranscriptLineAndNoUsage(t *testing.T) {
	t.Parallel()
	server, _, _ := accessServer(t, "", usageTurn)

	for _, table := range []string{"sessions", "transcript", "turn_usage", "step_times"} {
		var count int
		if err := server.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Errorf("rows of %s = %d, %v", table, count, err)
		}
	}
}

func TestAHarnessThatIsNotOnThePathFailsTheAccessCheckWithItsProgramName(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startServerWith(t, fake, testserver.Config(t, t.TempDir()), "")
	held := startLater(t.Context(), server, leadSpec(t))

	waiting := queued(t, server, engine.LeadRole)
	items := waitForAccessItems(t, server, checkedKeys)

	if waiting.QueueReason.String != "access check failed: claude-agent-acp is not on PATH" {
		t.Errorf("queue reason = %q", waiting.QueueReason.String)
	}
	reasons := map[string]string{}
	for _, item := range items {
		reasons[item.Title] = item.Reason
	}
	if reasons["Access check failed: claude-code opus high"] != "claude-agent-acp is not on PATH" || reasons["Access check failed: antigravity gemini-3-pro"] != "agy_acp_server is not on PATH" {
		t.Errorf("reasons = %q", reasons)
	}
	select {
	case got := <-held:
		t.Errorf("the Lead started: %+v", got)
	default:
	}
}

func TestANewStartDismissesTheItemOfAKeyThatNoRoleUses(t *testing.T) {
	t.Parallel()
	first, fake, dataDir := accessServer(t, "", noKey)
	waitForAccessItems(t, first, 1)
	first.Stop()
	cfg := testserver.Config(t, dataDir)
	cfg.Roles.Implementer = config.RoleBinding{Harness: config.ClaudeCode, Model: "sonnet", Effort: "high", Max: 2}

	second := testserver.StartWith(t, cfg, fake.URL)

	waitForAccessItems(t, second, 0)
}
