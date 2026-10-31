package engine_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const upgradePackage = "@deepseek-ai/dsh"

// fakeProgramMode makes the shell script of a fake program executable, as the test program that takes its place.
const fakeProgramMode = 0o700

// nodeProgram writes the shell script text at file, and gives it the executable permission.
func nodeProgram(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, fakeProgramMode); err != nil {
		t.Fatal(err)
	}
}

// upgradeServer starts a server whose dsh row is a program of the Node install at prefix. The install prints version,
// and the fake npm at prefix/bin/npm runs the script npm.
func upgradeServer(t *testing.T, prefix, npm, program string) *testserver.Server {
	t.Helper()
	nodeProgram(t, program, "#!/bin/sh\necho 0.2.1-rc.4\n")
	nodeProgram(t, filepath.Join(prefix, "bin", "npm"), npm)
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "harnesses"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(program, filepath.Join(dataDir, "harnesses", "dsh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_EXECUTABLE", "")
	registry := testkit.NewFakeNPM(t)
	registry.SetVersion(upgradePackage, "0.2.1-rc.5")
	was := engine.NPMRegistry
	engine.NPMRegistry = registry.URL
	t.Cleanup(func() { engine.NPMRegistry = was })
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	server := startServerWith(t, fake, testserver.Config(t, dataDir), "")
	server.WaitForFirstPoll(t, shop)
	return server
}

// toolVersion gives the version of the tool name in checks.
func toolVersion(t *testing.T, checks []engine.ToolCheck, name string) string {
	t.Helper()
	index := slices.IndexFunc(checks, func(check engine.ToolCheck) bool { return check.Name == name })
	if index < 0 {
		t.Fatalf("no row %s", name)
	}
	return checks[index].Version
}

func TestTheUpgradeRunsTheNPMOfTheNodeInstallOfTheProgram(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	args := filepath.Join(prefix, "args")
	npm := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + args + "\nprintf '#!/bin/sh\\necho 0.2.1-rc.5\\n' > " + program + "\n"
	other := t.TempDir()
	otherRan := filepath.Join(other, "ran")
	nodeProgram(t, filepath.Join(other, "npm"), "#!/bin/sh\ntouch "+otherRan+"\n")
	t.Setenv("PATH", other+string(filepath.ListSeparator)+os.Getenv("PATH"))

	server := upgradeServer(t, prefix, npm, program)

	checks, err := server.Engine.UpgradeTool(context.Background(), upgradePackage)
	if err != nil {
		t.Fatal(err)
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
	if _, err := os.Stat(otherRan); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the npm of the PATH of Mobius ran: %v", err)
	}
	if got := toolVersion(t, checks, "dsh"); got != "0.2.1-rc.5" {
		t.Errorf("dsh version = %q", got)
	}
	if got := records(t, server, engine.ChatKey{Organization: "owner"}); !slices.Equal(got, []string{"You upgraded dsh to 0.2.1-rc.5."}) {
		t.Errorf("records = %q", got)
	}
}

func TestAFailedUpgradeGivesTheNPMOutputAndKeepsTheVersion(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	npm := "#!/bin/sh\necho 'npm ERR! network down' >&2\nexit 1\n"

	server := upgradeServer(t, prefix, npm, program)

	if _, err := server.Engine.UpgradeTool(context.Background(), upgradePackage); err == nil || !strings.Contains(err.Error(), "npm ERR! network down") {
		t.Fatalf("error = %v", err)
	}
	if got := toolVersion(t, server.Engine.Tools(context.Background()), "dsh"); got != "0.2.1-rc.4" {
		t.Errorf("dsh version = %q", got)
	}
}

func TestASecondUpgradeOfTheSamePackageIsRefusedWhileTheFirstRuns(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	started := filepath.Join(prefix, "started")
	release := filepath.Join(prefix, "release")
	npm := "#!/bin/sh\ntouch " + started + "\nwhile [ ! -e " + release + " ]; do sleep 0.05; done\nprintf '#!/bin/sh\\necho 0.2.1-rc.5\\n' > " + program + "\n"

	server := upgradeServer(t, prefix, npm, program)

	done := make(chan error, 1)
	go func() {
		_, err := server.Engine.UpgradeTool(context.Background(), upgradePackage)
		done <- err
	}()
	testkit.WaitFor(t, func() bool { _, err := os.Stat(started); return err == nil })

	if _, err := server.Engine.UpgradeTool(context.Background(), upgradePackage); !engine.Refused(err) {
		t.Errorf("second upgrade error = %v", err)
	}
	touch(t, release)
	if err := <-done; err != nil {
		t.Errorf("first upgrade: %v", err)
	}
}

func TestTheUpgradeRefusesAPackageThatNoRowHas(t *testing.T) {
	prefix := t.TempDir()
	program := filepath.Join(prefix, "lib", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")

	server := upgradeServer(t, prefix, "#!/bin/sh\nexit 0\n", program)

	if _, err := server.Engine.UpgradeTool(context.Background(), "@example/none"); !engine.Refused(err) {
		t.Errorf("error = %v", err)
	}
}
