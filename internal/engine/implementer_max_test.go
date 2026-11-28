package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// twoBindings starts a server with two Implementer bindings of 4 slots each, and holds the second binding: its
// Harness has a pause (kind "pause"), or the first binding fails the access check (kind "access"). It gives the server
// and the function that ends the hold.
func twoBindings(t *testing.T, fake *testkit.FakeGitHub, kind string) (*testserver.Server, func()) {
	t.Helper()
	prepareAutopilot(fake, true)
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+seen)
	if kind == "pause" {
		installLimitedAntigravity(t, dataDir)
	} else {
		testkit.InstallFakeHarness(t, dataDir, "devin", options+noKey)
	}
	cfg := testserver.Config(t, dataDir)
	cfg.Roles.Implementer.Max = 4
	server := startServerWith(t, fake, cfg, "")
	testkit.WaitFor(t, server.Engine.AccessChecked)
	if kind == "pause" {
		testkit.WaitFor(t, func() bool { _, found := harnessPause(t, server, "antigravity"); return found })
		addBinding(t, server, config.Antigravity, "gemini-3-pro", "", 4)
		return server, func() {
			if _, err := server.Engine.Resume(t.Context(), "antigravity"); err != nil {
				t.Fatal(err)
			}
		}
	}
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 4)
	testkit.WaitFor(t, server.Engine.AccessChecked)
	return server, func() {
		testkit.InstallFakeHarness(t, dataDir, "devin", options+seen)
		server.Engine.SetClock(func() time.Time { return time.Now().Add(time.Hour) })
	}
}

// installLimitedAntigravity makes the access check of Antigravity hit the usage limit, and the next start of the
// Harness plays the clean script.
func installLimitedAntigravity(t *testing.T, dataDir string) {
	t.Helper()
	cleanPath := filepath.Join(dataDir, "clean.toml")
	if err := os.WriteFile(cleanPath, []byte(options), 0o600); err != nil {
		t.Fatal(err)
	}
	harnessPath := filepath.Join(dataDir, "harnesses", "agy_acp_server.toml")
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", fmt.Sprintf(`%s
[[prompts]]
when = "the word OK"
shell = "cp '%s' '%s'"
error = { code = -32603, message = "Usage Limit Reached" }
`, options, cleanPath, harnessPath))
}

func harnessPause(t *testing.T, server *testserver.Server, harness string) (pause, bool) {
	t.Helper()
	var found pause
	err := server.DB.QueryRow("SELECT paused_until FROM harness_pauses WHERE harness = ?", harness).Scan(&found.until)
	return found, err == nil
}

func TestAutopilotStartsTasksOnlyUpToTheMaxOfTheBindingsWithNoHoldUntilTheHoldEnds(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"pause", "access"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, release := twoBindings(t, fake, kind)
			for number := int64(41); number <= 49; number++ {
				addTaskIssue(fake, number, "Add plan model")
			}

			testkit.WaitFor(t, func() bool { return activeTasks(t, server) == 4 })
			waitForPolls(t, fake)
			if got := activeTasks(t, server); got != 4 {
				t.Fatalf("active tasks with a hold = %d, want 4", got)
			}

			release()

			testkit.WaitFor(t, func() bool { return activeTasks(t, server) == 8 })
			waitForPolls(t, fake)
			if got := activeTasks(t, server); got != 8 {
				t.Errorf("active tasks after the hold = %d, want 8", got)
			}
		})
	}
}

func TestTheImplementerGroupOfTheAgentsPageCountsOnlyTheBindingsWithNoHoldAsItsMax(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"pause", "access"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			server, release := twoBindings(t, testkit.NewFakeGitHub(t), kind)
			implementerMax := func() int64 {
				for _, group := range overview(t, server).Groups {
					if group.Name == "Implementer" {
						return group.Max
					}
				}
				t.Fatal("no Implementer group")
				return 0
			}
			if got := implementerMax(); got != 4 {
				t.Fatalf("max with a hold = %d, want 4", got)
			}

			release()

			testkit.WaitFor(t, func() bool { return implementerMax() == 8 })
		})
	}
}
