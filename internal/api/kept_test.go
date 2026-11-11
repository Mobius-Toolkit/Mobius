package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const fakeHarnessOptions = `
[options]
model = ["sonnet", "opus", "haiku", "swe-1.5", "gemini-3-pro"]
thought_level = ["low", "medium", "high"]
mode = ["default", "bypassPermissions", "yolo"]
`

const holdingLead = `
[[prompts]]
when = "dispatch of #41"
call = { tool = "start_implementer", arguments = { repository = "owner/shop", n = 41, instructions = "Store plans in cents." } }
hang = true
`

const committingImplementer = `
[[prompts]]
shell = '''echo cents > plan.txt && git add plan.txt && git commit -q -m "Add plan model"'''
`

type listedAgent struct {
	ID        int64      `json:"id"`
	Role      string     `json:"role"`
	EndedAt   *time.Time `json:"endedAt"`
	KeptUntil *time.Time `json:"keptUntil"`
}

func listAgents(t *testing.T, server *testserver.Server) []listedAgent {
	t.Helper()
	var body struct {
		Data []listedAgent `json:"data"`
	}
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/agents", "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("agents: status %d", reply.StatusCode)
	}
	return body.Data
}

func TestListAgentsGivesKeptUntilOfAKeptImplementerRowAndNullOfAnOpenRow(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddIssue(shop, 12, "Integrate loyalty plans")
	github.AddLabel(shop, 12, "mobius:workstream", "owner")
	github.AddIssue(shop, 41, "Add plan model")
	github.AddSubIssue(shop, 12, 41)
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", fakeHarnessOptions+holdingLead)
	testkit.InstallFakeHarness(t, dataDir, "devin", fakeHarnessOptions+committingImplementer)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", fakeHarnessOptions)
	github.AddRepository(shop)
	cfg := testserver.Config(t, dataDir)
	cfg.PollInterval = 50 * time.Millisecond
	server := testserver.StartWith(t, cfg, github.URL)
	if _, err := server.DB.Exec("INSERT INTO github_apps (app_id, slug, private_key, client_id, client_secret) VALUES (?, ?, ?, ?, ?)",
		testkit.AppID, testkit.AppSlug, testkit.AppPrivateKey, testkit.AppClientID, testkit.AppClientSecret); err != nil {
		t.Fatal(err)
	}
	server.WaitForFirstPoll(t, shop)
	before := time.Now()

	github.AddLabel(shop, 41, "mobius:ready", "owner")
	agents := testkit.WaitForValue(t, func() ([]listedAgent, bool) {
		agents := listAgents(t, server)
		for _, agent := range agents {
			if agent.Role == "implementer" && agent.KeptUntil != nil {
				return agents, true
			}
		}
		return nil, false
	})

	after := time.Now()
	var implementers, open int
	for _, agent := range agents {
		switch {
		case agent.Role == "implementer":
			implementers++
			if agent.EndedAt == nil || agent.KeptUntil.Before(before.Add(60*time.Minute)) || agent.KeptUntil.After(after.Add(60*time.Minute)) {
				t.Errorf("implementer = %+v, want an ended row with keptUntil 60 minutes after the end of the round", agent)
			}
		case agent.EndedAt == nil:
			open++
			if agent.KeptUntil != nil {
				t.Errorf("open row = %+v, want keptUntil null", agent)
			}
		}
	}
	if implementers != 1 || open == 0 {
		t.Errorf("implementers = %d, open rows = %d, want 1 and at least 1; agents = %+v", implementers, open, agents)
	}
}
