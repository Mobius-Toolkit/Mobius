package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if reply := readFile(t, dir, "reply.txt"); !strings.Contains(reply, "set model=sonnet effort=low mode=bypassPermissions;") {
		t.Errorf("reply = %s", reply)
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
