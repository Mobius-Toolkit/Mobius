package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// effortScript gives the efforts low and medium for the model sonnet, and each effort of options for each other model.
const effortScript = options + `
[efforts]
sonnet = ["low", "medium"]
`

// sonnetBinding is a binding of claude-code with the model sonnet, which gives the efforts low and medium in effortScript.
func sonnetBinding(effort string) config.RoleBinding {
	return config.RoleBinding{Harness: config.ClaudeCode, Model: "sonnet", Effort: effort, Max: 1}
}

// connectWithChoices starts a server whose Harnesses play effortScript, and waits until each read of the choices ended.
func connectWithChoices(t *testing.T) (*testserver.Server, string) {
	t.Helper()
	server, dataDir := connectScript(t, testkit.NewFakeGitHub(t), effortScript, func(*config.Config) {})
	readChoices(t, server)
	return server, dataDir
}

func readChoices(t *testing.T, server *testserver.Server) {
	t.Helper()
	for _, choice := range server.Engine.HarnessChoices(t.Context()) {
		if choice.Error != "" {
			t.Fatalf("%s: %s", choice.Harness, choice.Error)
		}
	}
}

func requireRefusal(t *testing.T, err error, want string) {
	t.Helper()
	if !engine.Refused(err) || err.Error() != want {
		t.Errorf("err = %v, want the refusal %q", err, want)
	}
}

func TestSetRoleRefusesAnEffortThatTheHarnessDoesNotGiveForTheModel(t *testing.T) {
	t.Parallel()
	server, _ := connectWithChoices(t)

	err := server.Engine.SetRole(t.Context(), "lead", sonnetBinding("high"))

	requireRefusal(t, err, "roles.lead.effort: claude-code gives no effort high for the model sonnet")
	if rows := roleRows(t, server); rows != 0 {
		t.Errorf("rows = %d, want 0", rows)
	}
}

func TestSetIssueBindingRefusesAnEffortThatTheHarnessDoesNotGiveForTheModel(t *testing.T) {
	t.Parallel()
	server, _ := connectWithChoices(t)

	err := server.Engine.SetIssueBinding(t.Context(), "implementer", shop, 12, config.ClaudeCode, "sonnet", "high")

	requireRefusal(t, err, "roles.implementer.effort: claude-code gives no effort high for the model sonnet")
	if _, found, err := server.Engine.IssueBinding(t.Context(), "implementer", shop, 12); found || err != nil {
		t.Errorf("found = %v, err = %v, want no setting", found, err)
	}
}

func TestAddImplementerBindingRefusesAnEffortThatTheHarnessDoesNotGiveForTheModel(t *testing.T) {
	t.Parallel()
	server, _ := connectWithChoices(t)

	_, err := server.Engine.AddImplementerBinding(t.Context(), sonnetBinding("high"))

	requireRefusal(t, err, "roles.implementer.effort: claude-code gives no effort high for the model sonnet")
	roles, rolesErr := server.Engine.Roles(t.Context())
	if rolesErr != nil {
		t.Fatal(rolesErr)
	}
	for _, role := range roles {
		if len(role.More) != 0 {
			t.Errorf("%s has the bindings %+v, want none", role.Role, role.More)
		}
	}
}

func TestSetRoleAcceptsAnEffortThatTheHarnessGivesAndAnEmptyEffort(t *testing.T) {
	t.Parallel()
	server, _ := connectWithChoices(t)

	for _, effort := range []string{"medium", ""} {
		if err := server.Engine.SetRole(t.Context(), "lead", sonnetBinding(effort)); err != nil {
			t.Errorf("effort %q: %v", effort, err)
		}
	}
}

func TestSetRoleRefusesNothingForTheEffortWhenNoReadOfTheChoicesEnded(t *testing.T) {
	t.Parallel()
	server, _ := connectScript(t, testkit.NewFakeGitHub(t), options, func(*config.Config) {})

	if err := server.Engine.SetRole(t.Context(), "lead", sonnetBinding("high")); err != nil {
		t.Errorf("SetRole = %v", err)
	}
}

func TestLoadRolesLoadsAStoredEffortThatTheHarnessDoesNotGiveAfterASuccessfulRead(t *testing.T) {
	t.Parallel()
	first, dataDir := connectScript(t, testkit.NewFakeGitHub(t), options, func(*config.Config) {})
	if err := first.Engine.SetRole(t.Context(), "lead", sonnetBinding("high")); err != nil {
		t.Fatal(err)
	}
	first.Stop()
	testkit.InstallFakeAgent(t, dataDir, effortScript)
	second := engine.New(first.DB, nil, testserver.Config(t, dataDir), engine.Agents{Path: filepath.Join(dataDir, "harnesses")})
	second.ReadHarnessChoices(t.Context())
	for _, choice := range second.HarnessChoices(t.Context()) {
		if choice.Error != "" {
			t.Fatalf("%s: %s", choice.Harness, choice.Error)
		}
	}

	if err := second.Recover(t.Context()); err != nil {
		t.Fatalf("Recover = %v", err)
	}

	roles, err := second.Roles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		if role.Role == "lead" && role.Binding.Effort != "high" {
			t.Errorf("lead = %+v, want the stored effort high", role.Binding)
		}
	}
}
