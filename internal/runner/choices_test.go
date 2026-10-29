package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const choicesScript = `
[options]
model = ["sonnet", "opus"]
thought_level = ["low", "medium", "high"]

[efforts]
sonnet = ["low", "medium"]
opus = ["low", "high", "max"]
`

func TestReadChoicesGivesEachModelTheEffortsThatTheHarnessGivesForIt(t *testing.T) {
	dir := t.TempDir()
	testkit.InstallFakeHarness(t, dir, Program(config.ClaudeCode), choicesScript)

	got, err := ReadChoices(t.Context(), config.ClaudeCode, dir, dir, filepath.Join(dir, "harnesses"))
	if err != nil {
		t.Fatal(err)
	}

	want := []ModelChoice{{Model: "sonnet", Efforts: []string{"low", "medium"}}, {Model: "opus", Efforts: []string{"low", "high", "max"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("choices = %+v, want %+v", got, want)
	}
}

func TestReadChoicesGivesNoEffortsForAHarnessWithNoEffortLevels(t *testing.T) {
	dir := t.TempDir()
	testkit.InstallFakeHarness(t, dir, Program(config.Antigravity), choicesScript)

	got, err := ReadChoices(t.Context(), config.Antigravity, dir, dir, filepath.Join(dir, "harnesses"))
	if err != nil {
		t.Fatal(err)
	}

	want := []ModelChoice{{Model: "sonnet"}, {Model: "opus"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("choices = %+v, want %+v", got, want)
	}
}

func TestReadChoicesSendsAnEmptyMCPServerList(t *testing.T) {
	dir := fakeAgentDir(t)

	if _, err := ReadChoices(t.Context(), config.ClaudeCode, dir, dir, dir); err != nil {
		t.Fatal(err)
	}

	servers, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "servers.json")))
	if err != nil {
		t.Fatal(err)
	}
	if string(servers) != "[]" {
		t.Errorf("mcpServers = %s", servers)
	}
}
