package engine_test

import (
	"reflect"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// currentOption gives the current value of the option id in the last config_option_update of the session, or "".
func currentOption(t *testing.T, rows []map[string]any, id string) string {
	t.Helper()
	value := ""
	for _, row := range rows {
		update := row["update"].(map[string]any)
		if update["sessionUpdate"] != "config_option_update" {
			continue
		}
		for _, option := range update["configOptions"].([]any) {
			if option := option.(map[string]any); option["id"] == id {
				value = option["currentValue"].(string)
			}
		}
	}
	return value
}

func TestEachRoleStartsOnTheHarnessModelAndEffortOfItsRoleBinding(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "")
	for _, program := range []string{"claude-agent-acp", "agy_acp_server", "devin"} {
		testkit.InstallFakeHarness(t, dataDir, program, options+"[[prompts]]\nreply = [\""+program+"\"]\n")
	}
	cfg := testserver.Config(t, dataDir)
	for _, c := range []struct {
		role    string
		binding config.RoleBinding
	}{
		{engine.LeadRole, cfg.Roles.Lead},
		{engine.TriagerRole, cfg.Roles.Triager},
		{engine.ImplementerRole, cfg.Roles.Implementer},
		{engine.ResearcherRole, cfg.Roles.Researcher},
		{engine.ReviewerRole, cfg.Roles.Reviewer},
		{engine.JudgeRole, cfg.Roles.Judge},
		{engine.CuratorRole, cfg.Roles.Curator},
	} {
		id := run(t, server, roleSpec(t, c.role), "Who runs this session?")

		updates := rows(t, server, id, "update")
		got := []string{reply(t, server, id), session(t, server, id).Harness, session(t, server, id).Model, currentOption(t, updates, "model"), currentOption(t, updates, "thought_level")}
		effort := c.binding.Effort
		if effort == "" {
			// The fake Harness keeps its first effort when the Role binding has no effort.
			effort = "low"
		}
		want := []string{runner.Program(c.binding.Harness), string(c.binding.Harness), c.binding.Model, c.binding.Model, effort}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %q, want %q", c.role, got, want)
		}
	}
}
