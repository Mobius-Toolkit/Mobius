package engine_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// stoppedForeignTask starts the Implementer of the task of owner/garden#7 below the Workstream #12 of owner/shop. The
// Implementer commits nothing, so the task stops.
func stoppedForeignTask(t *testing.T) (*testserver.Server, string) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	server, dataDir := connectWith(t, fake, seen, func(cfg *config.Config) { cfg.LeadIdleTimeout = 5 * time.Second })
	fake.AddForeignSubIssue(shop, 12, garden, 7)
	server.WaitForFirstPoll(t, garden)
	fake.AddLabel(garden, 7, "mobius:ready", "owner")
	task := gardenTask(t, server)
	if err := server.Engine.StartImplementer(t.Context(), task, "Water the plants", "Water the plants."); err != nil {
		t.Fatal(err)
	}
	return server, runner.TaskDir(dataDir, garden, 7)
}

type foreignAgent struct {
	Role                 string `json:"role"`
	Repository           string `json:"repository"`
	WorkstreamRepository string `json:"workstreamRepository"`
	Workstream           int64  `json:"workstream"`
	Issue                *int64 `json:"issue"`
}

func TestTheStoppedItemOfATaskInAnotherRepositoryLinksToTheWorkstream(t *testing.T) {
	t.Parallel()
	server, _ := stoppedForeignTask(t)

	item := waitForItem(t, server, "stopped")

	if item.Repository != garden || item.WorkstreamRepository != shop || item.Workstream != 12 || item.Issue != 7 {
		t.Errorf("item = %+v", item)
	}
}

func TestTheImplementerSessionOfATaskInAnotherRepositoryShowsInTheWorkstreamAndWorksInItsOwnWorktree(t *testing.T) {
	t.Parallel()
	server, dir := stoppedForeignTask(t)
	waitForItem(t, server, "stopped")

	response, err := server.Client.Get(server.URL + "/api/workstreams/owner/shop/12/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Data []foreignAgent `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	i := slices.IndexFunc(body.Data, func(agent foreignAgent) bool { return agent.Role == engine.ImplementerRole })
	if i < 0 {
		t.Fatalf("agents = %+v", body.Data)
	}
	agent := body.Data[i]
	if agent.Repository != garden || agent.WorkstreamRepository != shop || agent.Workstream != 12 || agent.Issue == nil || *agent.Issue != 7 {
		t.Errorf("agent = %+v", agent)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("worktree: %v", err)
	}
	if branch := testkit.Git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "mobius/7" {
		t.Errorf("branch = %s", branch)
	}
	if _, err := store.New(server.DB).GetLiveTask(t.Context(), store.GetLiveTaskParams{Repository: garden, Issue: 7}); err != nil {
		t.Error(err)
	}
}
