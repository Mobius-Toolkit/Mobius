package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(filepath.Join(dir, name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// printNiceValue is the .mobius/check of TestCheckRunsWithTheNiceValueRaisedByTen.
func printNiceValue() int {
	output, err := exec.Command("sh", "-c", "ps -o nice= -p $PPID").Output()
	if err != nil {
		return 1
	}
	_, _ = os.Stdout.Write(output)
	return 0
}

func TestCheckRunsWithTheNiceValueRaisedByTen(t *testing.T) {
	worktree := t.TempDir()
	if err := os.Mkdir(filepath.Join(worktree, ".mobius"), 0o750); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(worktree, ".mobius", "check")); err != nil {
		t.Fatal(err)
	}
	// The test can run with a nice value above 0, and the kernel caps the nice value.
	want, err := exec.Command("nice", "-n", "10", "sh", "-c", "ps -o nice= -p $$").Output()
	if err != nil {
		t.Fatal(err)
	}

	got, passed, err := Check(context.Background(), t.TempDir(), worktree, os.Getenv("PATH"), time.Minute)

	if err != nil || !passed {
		t.Fatalf("Check = %q, %v, %v; want a pass", got, passed, err)
	}
	if got != string(want) {
		t.Errorf("nice value of the check = %q, want %q", got, want)
	}
	t.Logf("nice value of the check: %s", got)
}

func TestCheckAccessSendsOnePromptWithNoMCPServerAfterItSetsTheModelAndTheEffort(t *testing.T) {
	dir := fakeAgentDir(t)

	if err := CheckAccess(t.Context(), config.ClaudeCode, "sonnet", "low", dir, dir, dir); err != nil {
		t.Fatal(err)
	}

	if servers := readFile(t, dir, "servers.json"); servers != "[]" {
		t.Errorf("mcpServers = %s", servers)
	}
	if prompt := readFile(t, dir, "prompt.json"); !strings.Contains(prompt, `"text":"Reply with the word OK."`) {
		t.Errorf("prompt = %s", prompt)
	}
	if sets := readFile(t, dir, "sets.json"); sets != `["model=sonnet","effort=low","mode=bypassPermissions"]` {
		t.Errorf("sets = %s", sets)
	}
}

func TestCheckAccessGivesTheErrorTextOfAHarnessThatFailsThePrompt(t *testing.T) {
	dir := t.TempDir()
	script := `
[options]
model = ["sonnet"]

[[prompts]]
when = "the word OK"
error = { code = -32603, message = "turn failed: no API key for provider route", data = "store DEEPSEEK_API_KEY" }
`
	testkit.InstallFakeHarness(t, dir, Program(config.Devin), script)

	err := CheckAccess(t.Context(), config.Devin, "sonnet", "", dir, dir, filepath.Join(dir, "harnesses"))

	want := `devin: turn failed: no API key for provider route "store DEEPSEEK_API_KEY"`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
}

func TestCheckAccessFailsWhenTheProgramIsNotOnThePath(t *testing.T) {
	dir := t.TempDir()

	err := CheckAccess(t.Context(), config.DSH, "model", "", dir, dir, dir)

	if err == nil || err.Error() != "dsh is not on PATH" {
		t.Errorf("error = %v", err)
	}
}
