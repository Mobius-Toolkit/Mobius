package engine_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// dnsFailure is the script of a Harness whose turn fails with the error of a failed DNS lookup.
const dnsFailure = "[[prompts]]\nerror = { code = -32603, message = \"getaddrinfo ENOTFOUND api.github.com\" }\n"

// apiNetwork gives the time of the loss of the network through the API, or "" while the network works.
func apiNetwork(t *testing.T, server *testserver.Server) string {
	t.Helper()
	status, text := send(t, server, http.MethodGet, "/api/network", "")
	var body struct {
		Data struct {
			LostAt *string `json:"lostAt"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil {
		t.Fatalf("network: status %d, %v: %s", status, err, text)
	}
	if body.Data.LostAt == nil {
		return ""
	}
	return *body.Data.LostAt
}

func TestAWorkerThatFailsWithANetworkErrorWaitsForTheNetworkAndKeepsItsRestarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	gate := filepath.Join(t.TempDir(), "gate")
	// The turn waits for the gate, so the test breaks the network check before the error comes.
	holdThenFail := "[[prompts]]\nshell = \"while [ ! -e '" + gate + "' ]; do sleep 0.05; done\"\nerror = { code = -32603, message = \"getaddrinfo ENOTFOUND api.github.com\" }\n"
	server, dataDir := connectTask(t, fake, leadStarts, holdThenFail, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })
	fail := func(sessions int) {
		t.Helper()
		testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.ImplementerRole)) >= sessions })
		fake.SetNetworkDown(true)
		if err := os.WriteFile(gate, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	fail(1)

	lostAt := testkit.WaitForValue(t, func() (string, bool) {
		text := apiNetwork(t, server)
		return text, text != ""
	})
	if _, err := time.Parse(time.RFC3339, lostAt); err != nil {
		t.Errorf("lostAt = %q", lostAt)
	}
	waitForPolls(t, fake)
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 {
		t.Errorf("Implementers = %+v", sessions)
	}
	if got := apiNetwork(t, server); got != lostAt {
		t.Errorf("lostAt = %q, want %q", got, lostAt)
	}
	if task := liveTask(t, server, 41); task.State != "queued" && task.State != "working" || task.WorkerRestarts != 0 {
		t.Errorf("task = %+v", task)
	}

	// A second failure after the end of the pause: more failures than max_worker_restarts.
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	fake.SetNetworkDown(false)
	testkit.WaitFor(t, func() bool { return apiNetwork(t, server) == "" })
	fail(2)

	testkit.WaitFor(t, func() bool { return apiNetwork(t, server) != "" })
	if task := liveTask(t, server, 41); task.State == "needs_human" || task.WorkerRestarts != 0 {
		t.Errorf("task = %+v", task)
	}
	if hasLabel(fake, "mobius:needs-human") {
		t.Error("the task has mobius:needs-human")
	}

	testkit.InstallFakeHarness(t, dataDir, "devin", options+"[[prompts]]\nshell = \""+commitShell+"\"\n")
	fake.SetNetworkDown(false)

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	testkit.WaitFor(t, func() bool { return apiNetwork(t, server) == "" })
}

func TestAWorkerThatFailsWithANetworkErrorWhileGitHubAnswersStillCountsARestart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\nerror = { code = -32603, message = \"connect ECONNREFUSED 127.0.0.1:9\" }\n", func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	if task := liveTask(t, server, 41); task.WorkerRestarts != 1 {
		t.Errorf("task = %+v", task)
	}
	if got := apiNetwork(t, server); got != "" {
		t.Errorf("lostAt = %q", got)
	}
}

func TestALeadTurnThatFailsWithANetworkErrorWhileGitHubAnswersCountsACrash(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nerror = { code = -32603, message = \"fetch failed: ENOTFOUND api.anthropic.com\" }\n", keepSessionOpen)

	dispatchTask(fake, 41, "Add plan model")

	testkit.WaitFor(t, func() bool {
		failed := 0
		for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
			if session.EndReason.String == "failed" {
				failed++
			}
		}
		return failed >= 4
	})
	if got := apiNetwork(t, server); got != "" {
		t.Errorf("lostAt = %q", got)
	}
}

func TestAWorkerThatFailsWithAnotherErrorStillCountsARestart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\nerror = { code = -32000, message = \"Failed\" }\n", func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	if task := liveTask(t, server, 41); task.WorkerRestarts != 1 {
		t.Errorf("task = %+v", task)
	}
	if got := apiNetwork(t, server); got != "" {
		t.Errorf("lostAt = %q", got)
	}
}

func TestALeadTurnThatFailsWithANetworkErrorKeepsTheEventUndeliveredUntilTheNetworkWorks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.SetNetworkDown(true)
	server, dataDir := connectWith(t, fake, "[[prompts]]\nerror = { code = -32603, message = \"fetch failed: ENOTFOUND api.anthropic.com\" }\n", keepSessionOpen)

	changes := listen(t, server)
	dispatchTask(fake, 41, "Add plan model")

	waitForChange(t, changes, func(change engine.Change) bool { return change.Network })
	if apiNetwork(t, server) == "" {
		t.Error("the event of the start came with no pause")
	}
	waitForPolls(t, fake)
	if got := undelivered(t, server); len(got) == 0 {
		t.Errorf("undelivered events = %+v", got)
	}
	failed := 0
	for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
		if session.EndReason.String == "failed" {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("failed Lead sessions = %d", failed)
	}

	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+"[[prompts]]\nreply = [\"Seen.\"]\n")
	fake.SetNetworkDown(false)

	waitForChange(t, changes, func(change engine.Change) bool { return change.Network })
	if got := apiNetwork(t, server); got != "" {
		t.Errorf("lostAt = %q at the event of the end", got)
	}
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	last := sessions[len(sessions)-1]
	if prompts := promptTexts(t, server, last.ID); len(prompts) != 1 || !strings.Contains(prompts[0], " dispatch of #41 ") {
		t.Errorf("prompts = %q", prompts)
	}
	if got := apiNetwork(t, server); got != "" {
		t.Errorf("lostAt = %q", got)
	}
}
