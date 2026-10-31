package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// autoUpgradeRuns gives the file that each run of the fake npm of prefix appends a line to.
func autoUpgradeRuns(prefix string) string {
	return filepath.Join(prefix, "runs")
}

// countRuns gives the number of runs of the fake npm.
func countRuns(t *testing.T, file string) int {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return strings.Count(string(text), "run\n")
}

// dshTool gives the row of the dsh package in checks.
func dshTool(t *testing.T, checks []engine.ToolCheck) engine.ToolCheck {
	t.Helper()
	index := slices.IndexFunc(checks, func(check engine.ToolCheck) bool { return check.Package == upgradePackage })
	if index < 0 {
		t.Fatalf("no package %s", upgradePackage)
	}
	return checks[index]
}

func TestTheAutoUpgradeIsOffAndRunsNoNPM(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	runs := autoUpgradeRuns(prefix)
	npm := "#!/bin/sh\necho run >> " + runs + "\nexit 0\n"

	server, fake, _ := upgradeServer(t, prefix, npm, program)

	if got := dshTool(t, server.Engine.Tools(context.Background())); got.AutoUpgrade {
		t.Errorf("auto-upgrade is on")
	}
	waitForPolls(t, fake)
	if got := countRuns(t, runs); got != 0 {
		t.Errorf("npm runs = %d, want 0", got)
	}
}

func TestTheAutoUpgradeInstallsTheNewestVersionWithTheNPMOfTheNodeInstall(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	runs := autoUpgradeRuns(prefix)
	args := filepath.Join(prefix, "args")
	npm := "#!/bin/sh\necho run >> " + runs + "\nprintf '%s\\n' \"$@\" > " + args + "\nprintf '#!/bin/sh\\necho 0.2.1-rc.5\\n' > " + program + "\n"

	server, _, _ := upgradeServer(t, prefix, npm, program)

	checks, err := server.Engine.SetToolAutoUpgrade(context.Background(), upgradePackage, true)
	if err != nil {
		t.Fatal(err)
	}
	if !dshTool(t, checks).AutoUpgrade {
		t.Errorf("the switch is off after the turn on")
	}
	testkit.WaitFor(t, func() bool {
		return dshVersion(t, server.Engine.Tools(context.Background())) == "0.2.1-rc.5"
	})
	if got := countRuns(t, runs); got != 1 {
		t.Errorf("npm runs = %d, want 1", got)
	}
	written, err := os.ReadFile(filepath.Clean(args))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "--global", "--prefix", resolved, upgradePackage + "@0.2.1-rc.5"}
	if got := strings.Split(strings.TrimSpace(string(written)), "\n"); !slices.Equal(got, want) {
		t.Errorf("npm arguments = %q, want %q", got, want)
	}
}

func TestAFailedAutoUpgradeAddsOneItemAndWaitsForANewerVersion(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	runs := autoUpgradeRuns(prefix)
	npm := "#!/bin/sh\necho run >> " + runs + "\n" +
		"case \"$5\" in\n" +
		"*@0.2.1-rc.6) printf '#!/bin/sh\\necho 0.2.1-rc.6\\n' > " + program + "; exit 0;;\n" +
		"esac\n" +
		"echo 'npm ERR! network down' >&2\n" +
		"exit 1\n"

	server, fake, registry := upgradeServer(t, prefix, npm, program)

	if _, err := server.Engine.SetToolAutoUpgrade(context.Background(), upgradePackage, true); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(inbox(t, server), func(item inboxItem) bool {
			return item.Kind == "tool upgrade" && item.Title == "Auto-upgrade of dsh failed" && strings.Contains(item.Reason, "npm ERR! network down")
		})
	})
	items := inbox(t, server)
	if len(items) != 1 || items[0].Issue != 0 {
		t.Errorf("items = %+v, want one item with no issue", items)
	}
	if got := countRuns(t, runs); got != 1 {
		t.Fatalf("npm runs = %d, want 1", got)
	}

	server.Engine.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	waitForPolls(t, fake)
	if got := countRuns(t, runs); got != 1 {
		t.Errorf("npm runs after the same newest version = %d, want 1", got)
	}
	if got := len(inbox(t, server)); got != 1 {
		t.Errorf("items after the same newest version = %d, want 1", got)
	}

	registry.SetVersion(upgradePackage, "0.2.1-rc.6")
	server.Engine.SetClock(func() time.Time { return time.Now().Add(4 * time.Hour) })
	testkit.WaitFor(t, func() bool {
		return dshVersion(t, server.Engine.Tools(context.Background())) == "0.2.1-rc.6"
	})
	if got := countRuns(t, runs); got != 2 {
		t.Errorf("npm runs for a newer version = %d, want 2", got)
	}
	if got := len(inbox(t, server)); got != 1 {
		t.Errorf("items after the newer version = %d, want 1", got)
	}
}

func TestAnUpgradeByTheOwnerClearsTheFailedVersion(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	npm := "#!/bin/sh\necho 'npm ERR! network down' >&2\nexit 1\n"

	server, _, _ := upgradeServer(t, prefix, npm, program)

	if _, err := server.Engine.SetToolAutoUpgrade(context.Background(), upgradePackage, true); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return len(inbox(t, server)) == 1 })
	if _, err := server.Engine.UpgradeTool(context.Background(), upgradePackage); err == nil {
		t.Fatal("the upgrade did not fail")
	}
	rows, err := store.New(server.DB).ListToolAutoUpgrades(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].FailedVersion != "0.2.1-rc.5" {
		t.Fatalf("rows = %+v, want the failed version 0.2.1-rc.5", rows)
	}

	nodeProgram(t, filepath.Join(prefix, "bin", "npm"), "#!/bin/sh\nprintf '#!/bin/sh\\necho 0.2.1-rc.5\\n' > "+program+"\n")
	if _, err := server.Engine.UpgradeTool(context.Background(), upgradePackage); err != nil {
		t.Fatal(err)
	}
	rows, err = store.New(server.DB).ListToolAutoUpgrades(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].FailedVersion != "" {
		t.Errorf("rows = %+v, want no failed version", rows)
	}
}

func TestSetToolAutoUpgradeKeepsTheValueAndAddsTheRecord(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	npm := "#!/bin/sh\nexit 0\n"

	server, _, _ := upgradeServer(t, prefix, npm, program)

	ctx := context.Background()
	if _, err := server.Engine.SetToolAutoUpgrade(ctx, upgradePackage, true); err != nil {
		t.Fatal(err)
	}
	if !dshTool(t, server.Engine.Tools(ctx)).AutoUpgrade {
		t.Errorf("the switch is off after a new tool list read")
	}
	checks, err := server.Engine.SetToolAutoUpgrade(ctx, upgradePackage, false)
	if err != nil {
		t.Fatal(err)
	}
	if dshTool(t, checks).AutoUpgrade {
		t.Errorf("the switch is on after the turn off")
	}
	if dshTool(t, server.Engine.Tools(ctx)).AutoUpgrade {
		t.Errorf("the switch is on after a new tool list read")
	}
	want := []string{"You turned Auto-upgrade of dsh on.", "You turned Auto-upgrade of dsh off."}
	if got := records(t, server, engine.ChatKey{Organization: "owner"}); !slices.Equal(got, want) {
		t.Errorf("records = %q, want %q", got, want)
	}
}

func TestSetToolAutoUpgradeRefusesAPackageThatNoRowHas(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")

	server, _, _ := upgradeServer(t, prefix, "#!/bin/sh\nexit 0\n", program)

	if _, err := server.Engine.SetToolAutoUpgrade(context.Background(), "@example/none", true); !engine.Refused(err) {
		t.Errorf("error = %v", err)
	}
}
