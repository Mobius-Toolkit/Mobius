package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func toolEngine(t *testing.T, bin string) *Engine {
	t.Helper()
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

	want := ToolCheck{Name: "devin", Path: filepath.Join(bin, "devin"), Version: "devin 3000.11.3 (9c803229faa4)"}
	if got := find(checks, "devin"); got != want {
		t.Errorf("devin = %+v, want %+v", got, want)
	}
	want = ToolCheck{Name: "git", Path: filepath.Join(bin, "git"), Version: "git version 2.50.1"}
	if got := find(checks, "git"); got != want {
		t.Errorf("git = %+v, want %+v", got, want)
	}
}

func TestAProgramThatIsNotFoundHasTheStatusNotFound(t *testing.T) {
	checks := toolEngine(t, t.TempDir()).Tools(context.Background())

	if got, want := find(checks, "tar"), (ToolCheck{Name: "tar", Status: NotFound}); got != want {
		t.Errorf("tar = %+v, want %+v", got, want)
	}
}

func TestAProgramWithNoVersionOutputHasTheStatusNoVersion(t *testing.T) {
	bin := t.TempDir()
	testkit.InstallFakeProgram(t, bin, "curl", "")
	fail, err := exec.LookPath("false")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fail, filepath.Join(bin, "tar")); err != nil {
		t.Fatal(err)
	}

	checks := toolEngine(t, bin).Tools(context.Background())

	for _, name := range []string{"curl", "tar"} {
		want := ToolCheck{Name: name, Path: filepath.Join(bin, name), Status: NoVersion}
		if got := find(checks, name); got != want {
			t.Errorf("%s = %+v, want %+v", name, got, want)
		}
	}
}

func TestTheToolsHaveOnlyTheHarnessesOfTheRoles(t *testing.T) {
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
	if want := []string{"claude-agent-acp", "Claude Code CLI", "git", "curl", "tar"}; !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
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
	bin := t.TempDir()
	adapter(t, bin, `{"name": "@anthropic-ai/claude-agent-sdk", "version": "0.2.0", "claudeCodeVersion": "2.1.284"}`)

	if got := builtInClaudeCodeVersion(filepath.Join(bin, "claude-agent-acp")); got != "2.1.284" {
		t.Errorf("version = %q", got)
	}
}

func TestAnSDKInTheNodeModulesOfAParentDirectoryGivesTheClaudeCodeCLIVersion(t *testing.T) {
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
	bin := t.TempDir()
	adapter(t, bin, `{"name": "@anthropic-ai/claude-agent-sdk"}`)

	if got := builtInClaudeCodeVersion(filepath.Join(bin, "claude-agent-acp")); got != "" {
		t.Errorf("version = %q", got)
	}
}

func TestAnAdapterThatIsNotFoundGivesTheStatusNoVersionForTheClaudeCodeCLI(t *testing.T) {
	checks := toolEngine(t, t.TempDir()).Tools(context.Background())

	if got, want := find(checks, "Claude Code CLI"), (ToolCheck{Name: "Claude Code CLI", Status: NoVersion}); got != want {
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

	if want := (ToolCheck{Name: "Claude Code CLI", Path: claude, Version: "2.1.285 (Claude Code)"}); got != want {
		t.Errorf("cli = %+v, want %+v", got, want)
	}
}

func TestAMissingClaudeCodeExecutableHasTheStatusNotFound(t *testing.T) {
	e := toolEngine(t, t.TempDir())
	missing := filepath.Join(t.TempDir(), "claude")
	t.Setenv(claudeCodeEnv, missing)

	got := find(e.Tools(context.Background()), "Claude Code CLI")

	if want := (ToolCheck{Name: "Claude Code CLI", Status: NotFound}); got != want {
		t.Errorf("cli = %+v, want %+v", got, want)
	}
}
