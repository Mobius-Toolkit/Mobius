package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// fakeRegistry points the npm registry at a new fake registry until the end of the test.
func fakeRegistry(t *testing.T) *testkit.FakeNPM {
	t.Helper()
	registry := testkit.NewFakeNPM(t)
	was := NPMRegistry
	NPMRegistry = registry.URL
	t.Cleanup(func() { NPMRegistry = was })
	return registry
}

func toolEngine(t *testing.T, bin string) *Engine {
	t.Helper()
	fakeRegistry(t)
	t.Setenv(claudeCodeEnv, "")
	cfg := limits(t)
	cfg.DataDir = t.TempDir()
	return New(nil, nil, cfg, Agents{Path: bin})
}

func find(checks []ToolCheck, name string) ToolCheck {
	index := slices.IndexFunc(checks, func(c ToolCheck) bool { return c.Name == name })
	if index < 0 {
		return ToolCheck{}
	}
	return checks[index]
}

func TestAFoundProgramHasItsPathAndTheFirstLineOfItsVersion(t *testing.T) {
	bin := t.TempDir()
	testkit.InstallFakeProgram(t, bin, "devin", "devin 3000.11.3 (9c803229faa4)\nsecond line\n")
	testkit.InstallFakeProgram(t, bin, "git", "git version 2.50.1\n")

	checks := toolEngine(t, bin).Tools(context.Background())

	want := ToolCheck{Name: "devin", Path: filepath.Join(bin, "devin"), Version: "devin 3000.11.3 (9c803229faa4)", Required: true}
	if got := find(checks, "devin"); got != want {
		t.Errorf("devin = %+v, want %+v", got, want)
	}
	want = ToolCheck{Name: "git", Path: filepath.Join(bin, "git"), Version: "git version 2.50.1", Required: true}
	if got := find(checks, "git"); got != want {
		t.Errorf("git = %+v, want %+v", got, want)
	}
}

func TestAProgramThatIsNotFoundHasTheStatusNotFound(t *testing.T) {
	checks := toolEngine(t, t.TempDir()).Tools(context.Background())

	if got, want := find(checks, "dsh"), (ToolCheck{Name: "dsh", Status: NotFound}); got != want {
		t.Errorf("dsh = %+v, want %+v", got, want)
	}
}

func TestAProgramWithNoVersionOutputHasTheStatusNoVersion(t *testing.T) {
	bin := t.TempDir()
	testkit.InstallFakeProgram(t, bin, "git", "")
	fail, err := exec.LookPath("false")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fail, filepath.Join(bin, "dsh")); err != nil {
		t.Fatal(err)
	}

	checks := toolEngine(t, bin).Tools(context.Background())

	want := ToolCheck{Name: "git", Path: filepath.Join(bin, "git"), Status: NoVersion, Required: true}
	if got := find(checks, "git"); got != want {
		t.Errorf("git = %+v, want %+v", got, want)
	}
	want = ToolCheck{Name: "dsh", Path: filepath.Join(bin, "dsh"), Status: NoVersion}
	if got := find(checks, "dsh"); got != want {
		t.Errorf("dsh = %+v, want %+v", got, want)
	}
}

func TestTheToolsHaveEachHarnessAndGit(t *testing.T) {
	cfg := limits(t)
	cfg.DataDir = t.TempDir()
	cfg.Roles.Researcher.Harness = cfg.Roles.Judge.Harness
	cfg.Roles.Researcher.Effort = "low"
	cfg.Roles.Implementer.Harness = cfg.Roles.Judge.Harness
	cfg.Roles.Implementer.Effort = "low"
	t.Setenv(claudeCodeEnv, "")

	checks := New(nil, nil, cfg, Agents{Path: t.TempDir()}).Tools(context.Background())

	var names []string
	for _, check := range checks {
		names = append(names, check.Name)
	}
	if want := []string{"claude-agent-acp", "Claude Code CLI", "agy_acp_server", "devin", "dsh", "git"}; !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
	required := map[string]bool{}
	for _, check := range checks {
		required[check.Name] = check.Required
	}
	want := map[string]bool{
		"claude-agent-acp": true,
		"Claude Code CLI":  true,
		"agy_acp_server":   false,
		"devin":            false,
		"dsh":              false,
		"git":              true,
	}
	for name, value := range want {
		if required[name] != value {
			t.Errorf("required of %s = %t, want %t", name, required[name], value)
		}
	}
}

func TestTheProgramsComeFromThePathOfTheAgents(t *testing.T) {
	bin := t.TempDir()
	testkit.InstallFakeProgram(t, bin, "git", "git version 1\n")
	e := toolEngine(t, bin)
	testkit.InstallFakeProgram(t, filepath.Join(e.config.DataDir, "agent-env", "bin"), "git", "git version 2\n")

	if got := find(e.Tools(context.Background()), "git").Version; got != "git version 2" {
		t.Errorf("version = %q", got)
	}
}

// adapter writes an npm package of claude-agent-acp with a link to it in bin, and the SDK manifest with sdk.
func adapter(t *testing.T, bin, sdk string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "claude-agent-acp")
	for file, text := range map[string]string{
		filepath.Join(root, "dist", "index.js"):                                                  "",
		filepath.Join(root, "package.json"):                                                      "{}",
		filepath.Join(root, "node_modules", "@anthropic-ai", "claude-agent-sdk", "package.json"): sdk,
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "dist", "index.js"), filepath.Join(bin, "claude-agent-acp")); err != nil {
		t.Fatal(err)
	}
}

func TestTheClaudeCodeCLIVersionComesFromThePackageOfTheAdapter(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	adapter(t, bin, `{"name": "@anthropic-ai/claude-agent-sdk", "version": "0.2.0", "claudeCodeVersion": "2.1.284"}`)

	if got := builtInClaudeCodeVersion(filepath.Join(bin, "claude-agent-acp")); got != "2.1.284" {
		t.Errorf("version = %q", got)
	}
}

func TestAnSDKInTheNodeModulesOfAParentDirectoryGivesTheClaudeCodeCLIVersion(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	store := filepath.Join(t.TempDir(), "node_modules")
	pkg := filepath.Join(store, "@zed-industries", "claude-agent-acp")
	sdk := filepath.Join(store, "@anthropic-ai", "claude-agent-sdk", "package.json")
	for file, text := range map[string]string{
		filepath.Join(pkg, "dist", "index.js"): "",
		filepath.Join(pkg, "package.json"):     "{}",
		sdk:                                    `{"claudeCodeVersion": "2.1.284"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(pkg, "dist", "index.js"), filepath.Join(bin, "claude-agent-acp")); err != nil {
		t.Fatal(err)
	}

	if got := builtInClaudeCodeVersion(filepath.Join(bin, "claude-agent-acp")); got != "2.1.284" {
		t.Errorf("version = %q", got)
	}
}

func TestAPackageWithNoClaudeCodeVersionGivesNoVersion(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	adapter(t, bin, `{"name": "@anthropic-ai/claude-agent-sdk"}`)

	if got := builtInClaudeCodeVersion(filepath.Join(bin, "claude-agent-acp")); got != "" {
		t.Errorf("version = %q", got)
	}
}

func TestAnAdapterThatIsNotFoundGivesTheStatusNoVersionForTheClaudeCodeCLI(t *testing.T) {
	checks := toolEngine(t, t.TempDir()).Tools(context.Background())

	if got, want := find(checks, "Claude Code CLI"), (ToolCheck{Name: "Claude Code CLI", Status: NoVersion, Required: true}); got != want {
		t.Errorf("cli = %+v, want %+v", got, want)
	}
}

func TestTheClaudeCodeExecutableGivesTheClaudeCodeCLI(t *testing.T) {
	e := toolEngine(t, t.TempDir())
	dir := t.TempDir()
	testkit.InstallFakeProgram(t, dir, "claude", "2.1.285 (Claude Code)\n")
	claude := filepath.Join(dir, "claude")
	t.Setenv(claudeCodeEnv, claude)

	got := find(e.Tools(context.Background()), "Claude Code CLI")

	if want := (ToolCheck{Name: "Claude Code CLI", Path: claude, Version: "2.1.285 (Claude Code)", Required: true}); got != want {
		t.Errorf("cli = %+v, want %+v", got, want)
	}
}

func TestAMissingClaudeCodeExecutableHasTheStatusNotFound(t *testing.T) {
	e := toolEngine(t, t.TempDir())
	missing := filepath.Join(t.TempDir(), "claude")
	t.Setenv(claudeCodeEnv, missing)

	got := find(e.Tools(context.Background()), "Claude Code CLI")

	if want := (ToolCheck{Name: "Claude Code CLI", Status: NotFound, Required: true}); got != want {
		t.Errorf("cli = %+v, want %+v", got, want)
	}
}

const adapterNPM, cliNPM = "@agentclientprotocol/claude-agent-acp", "@anthropic-ai/claude-code"

// npmEngine gives an engine with a fake adapter and a separate fake Claude Code CLI, and a fake npm registry.
func npmEngine(t *testing.T, adapterVersion, cliVersion string) (*Engine, *testkit.FakeNPM) {
	t.Helper()
	bin := t.TempDir()
	testkit.InstallFakeProgram(t, bin, "claude-agent-acp", adapterVersion)
	testkit.InstallFakeProgram(t, bin, "claude", cliVersion)
	e := toolEngine(t, bin)
	t.Setenv(claudeCodeEnv, filepath.Join(bin, "claude"))
	return e, fakeRegistry(t)
}

func TestAToolOlderThanTheNewestVersionIsOutdated(t *testing.T) {
	e, registry := npmEngine(t, "claude-agent-acp 0.9.12\n", "2.1.9 (Claude Code)\n")
	registry.SetVersion(adapterNPM, "0.10.0")
	registry.SetVersion(cliNPM, "2.1.10")

	checks := e.Tools(context.Background())

	if got := find(checks, "claude-agent-acp"); got.Newest != "0.10.0" || !got.Outdated {
		t.Errorf("claude-agent-acp = %+v", got)
	}
	if got := find(checks, "Claude Code CLI"); got.Newest != "2.1.10" || !got.Outdated {
		t.Errorf("Claude Code CLI = %+v", got)
	}
	if got := find(checks, "git"); got.Newest != "" || got.Outdated {
		t.Errorf("git = %+v", got)
	}
}

func TestAToolWithTheNewestVersionIsNotOutdated(t *testing.T) {
	e, registry := npmEngine(t, "claude-agent-acp 0.10.0\n", "2.1.10 (Claude Code)\n")
	registry.SetVersion(adapterNPM, "0.10.0")
	registry.SetVersion(cliNPM, "2.1.9")

	checks := e.Tools(context.Background())

	if got := find(checks, "claude-agent-acp"); got.Newest != "0.10.0" || got.Outdated {
		t.Errorf("claude-agent-acp = %+v", got)
	}
	if got := find(checks, "Claude Code CLI"); got.Newest != "2.1.9" || got.Outdated {
		t.Errorf("Claude Code CLI = %+v", got)
	}
}

func TestARegistryWithNoAnswerGivesNoNewestVersion(t *testing.T) {
	e, registry := npmEngine(t, "claude-agent-acp 0.9.12\n", "2.1.9 (Claude Code)\n")
	registry.SetVersion(adapterNPM, "0.10.0")
	registry.SetSilent(true)

	checks := e.Tools(context.Background())

	want := ToolCheck{Name: "claude-agent-acp", Path: find(checks, "claude-agent-acp").Path, Version: "claude-agent-acp 0.9.12", Required: true}
	if got := find(checks, "claude-agent-acp"); got != want {
		t.Errorf("claude-agent-acp = %+v, want %+v", got, want)
	}
	if got := find(checks, "Claude Code CLI"); got.Newest != "" || got.Outdated || got.Status != "" {
		t.Errorf("Claude Code CLI = %+v", got)
	}
}

func TestAnErrorOfTheRegistryGivesNoNewestVersion(t *testing.T) {
	e, _ := npmEngine(t, "claude-agent-acp 0.9.12\n", "2.1.9 (Claude Code)\n")

	got := find(e.Tools(context.Background()), "claude-agent-acp")

	if got.Newest != "" || got.Outdated || got.Status != "" {
		t.Errorf("claude-agent-acp = %+v", got)
	}
}

func TestAVersionOutputWithNoNumberGivesNoNewestVersion(t *testing.T) {
	e, registry := npmEngine(t, "claude-agent-acp dev\n", "2.1.9 (Claude Code)\n")
	registry.SetVersion(adapterNPM, "0.10.0")

	got := find(e.Tools(context.Background()), "claude-agent-acp")

	if got.Newest != "" || got.Outdated || got.Status != "" || got.Version != "claude-agent-acp dev" {
		t.Errorf("claude-agent-acp = %+v", got)
	}
}

func TestTheCompareUsesThePreReleasePart(t *testing.T) {
	cases := []struct {
		name         string
		installed    string
		newest       string
		wantNewest   string
		wantOutdated bool
	}{
		{"older pre-release", "0.2.1-rc.4", "0.2.1-rc.5", "0.2.1-rc.5", true},
		{"same pre-release", "0.2.1-rc.5", "0.2.1-rc.5", "0.2.1-rc.5", false},
		{"pre-release of the newest", "0.2.1-rc.5", "0.2.1", "0.2.1", true},
		{"output with a name", "2.1.284 (Claude Code)", "2.1.300", "2.1.300", true},
		{"output with no version", "dev", "2.1.300", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, registry := npmEngine(t, "claude-agent-acp 0.9.12\n", "2.1.9 (Claude Code)\n")
			registry.SetVersion(adapterNPM, tc.newest)
			check := ToolCheck{Version: tc.installed}

			e.compareNewest(context.Background(), &check, adapterNPM)

			if check.Newest != tc.wantNewest || check.Outdated != tc.wantOutdated {
				t.Errorf("check = %+v", check)
			}
		})
	}
}

func TestTheDSHRowReadsTheNewestVersionOfItsPackage(t *testing.T) {
	bin := t.TempDir()
	testkit.InstallFakeProgram(t, bin, "dsh", "0.2.1-rc.4\n")
	registry := fakeRegistry(t)
	t.Setenv(claudeCodeEnv, "")
	cfg := limits(t)
	cfg.DataDir = t.TempDir()
	registry.SetVersion("@deepseek-ai/dsh", "0.2.1-rc.5")

	got := find(New(nil, nil, cfg, Agents{Path: bin}).Tools(context.Background()), "dsh")

	if got.Newest != "0.2.1-rc.5" || !got.Outdated {
		t.Errorf("dsh = %+v", got)
	}
}

func TestTheNewestVersionIsReadOneTimeInAnHour(t *testing.T) {
	e, registry := npmEngine(t, "claude-agent-acp 0.9.12\n", "2.1.9 (Claude Code)\n")
	registry.SetVersion(adapterNPM, "0.10.0")
	registry.SetVersion(cliNPM, "2.1.9")
	now := time.Now()
	e.SetClock(func() time.Time { return now })

	e.Tools(context.Background())
	registry.SetVersion(adapterNPM, "0.11.0")
	now = now.Add(59 * time.Minute)
	if got := find(e.Tools(context.Background()), "claude-agent-acp").Newest; got != "0.10.0" {
		t.Errorf("newest after 59 minutes = %q", got)
	}
	if got := registry.Reads(); got != 2 {
		t.Errorf("reads after 59 minutes = %d, want 2", got)
	}
	now = now.Add(2 * time.Minute)
	if got := find(e.Tools(context.Background()), "claude-agent-acp").Newest; got != "0.11.0" {
		t.Errorf("newest after 61 minutes = %q", got)
	}
}

func TestTheClaudeCodeCLIInTheAdapterHasNoNewestVersion(t *testing.T) {
	e, registry := npmEngine(t, "claude-agent-acp 0.9.12\n", "2.1.9\n")
	t.Setenv(claudeCodeEnv, "")
	registry.SetVersion(cliNPM, "9.0.0")

	if got := find(e.Tools(context.Background()), "Claude Code CLI"); got.Newest != "" || got.Outdated {
		t.Errorf("Claude Code CLI = %+v", got)
	}
}

func TestAReadOfTheRegistryThatTheContextEndsIsNotKept(t *testing.T) {
	e, registry := npmEngine(t, "claude-agent-acp 0.9.12\n", "2.1.9 (Claude Code)\n")
	registry.SetVersion(adapterNPM, "0.10.0")
	ended, cancel := context.WithCancel(context.Background())
	cancel()

	if got := e.newestVersion(ended, adapterNPM); got != "" {
		t.Errorf("newest with an ended context = %q", got)
	}
	if got := e.newestVersion(context.Background(), adapterNPM); got != "0.10.0" {
		t.Errorf("newest after the ended context = %q", got)
	}
}
