package engine_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// harnessCount is the number of Harnesses of config.Harnesses, so the number of session/new requests of one read of all of them.
const harnessCount = 4

const choicesScript = `
skip_tools_list = 1000

[options]
model = ["sonnet", "opus"]
thought_level = ["low", "medium", "high"]

[efforts]
sonnet = ["low", "medium"]
opus = ["low", "high", "max"]
`

// noModelScript has no model option, so each read fails.
const noModelScript = `
skip_tools_list = 1000
`

// holdChoicesScript makes each read wait at session/new until the test writes the file session_new_release.
const holdChoicesScript = "session_new_hold = true\n" + choicesScript

// choicesServer starts a server whose Harnesses play script. It has no repository work, so only the reads start agents.
// The access checks of the start do not count in agentStarts.
func choicesServer(t *testing.T, script string) (*testserver.Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, script)
	server := startServerWith(t, testkit.NewFakeGitHub(t), testserver.Config(t, dataDir), "")
	testkit.WaitFor(t, server.Engine.AccessChecked)
	if err := os.WriteFile(filepath.Join(dataDir, "harnesses", "starts"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	return server, dataDir
}

// agentStarts gives the number of session/new requests that the fake agents received.
func agentStarts(t *testing.T, dataDir string) int {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "harnesses", "starts")))
	if err != nil {
		t.Fatal(err)
	}
	starts, err := strconv.Atoi(string(text))
	if err != nil {
		t.Fatal(err)
	}
	return starts
}

// sessionNewStarts gives the number of reads of the hold script that reached session/new.
func sessionNewStarts(t *testing.T, dataDir string) int {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "harnesses", "session_new_started")))
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(text), "\n")
}

// waitForSessionNewStarts waits until count reads of the hold script reached session/new. A sequential read cannot
// reach the count, so this wait gives up after two seconds.
func waitForSessionNewStarts(t *testing.T, dataDir string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		started := sessionNewStarts(t, dataDir)
		if started >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reads at session/new = %d, want %d at the same time", started, count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// releaseHarnessReads lets each read of the hold script continue.
func releaseHarnessReads(t *testing.T, dataDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, "harnesses", "session_new_release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHarnessChoicesKeepASuccessfulReadUntilTheNextStart(t *testing.T) {
	t.Parallel()
	server, dataDir := choicesServer(t, choicesScript)
	now := time.Now()
	server.Engine.SetClock(func() time.Time { return now })

	server.Engine.ReadHarnessChoices(t.Context())
	testkit.WaitFor(t, func() bool { return agentStarts(t, dataDir) == harnessCount })
	first := server.Engine.HarnessChoices(t.Context())
	now = now.Add(24 * time.Hour)
	second := server.Engine.HarnessChoices(t.Context())

	if len(first) != harnessCount || !reflect.DeepEqual(first, second) {
		t.Errorf("first = %+v, second = %+v", first, second)
	}
	if starts := agentStarts(t, dataDir); starts != harnessCount {
		t.Errorf("starts = %d, want %d", starts, harnessCount)
	}
	claude := first[0]
	want := []runner.ModelChoice{{Model: "sonnet", Efforts: []string{"low", "medium"}}, {Model: "opus", Efforts: []string{"low", "high", "max"}}}
	if claude.Harness != config.ClaudeCode || claude.Error != "" || !reflect.DeepEqual(claude.Models, want) {
		t.Errorf("claude-code = %+v", claude)
	}
}

func TestHarnessChoicesReadTheHarnessesAtTheSameTime(t *testing.T) {
	t.Parallel()
	server, dataDir := choicesServer(t, choicesScript)
	testkit.InstallFakeAgent(t, dataDir, holdChoicesScript)
	defer releaseHarnessReads(t, dataDir)

	result := make(chan []engine.HarnessChoice, 1)
	go func() { result <- server.Engine.HarnessChoices(t.Context()) }()

	waitForSessionNewStarts(t, dataDir, harnessCount)
	releaseHarnessReads(t, dataDir)
	choices := <-result

	if len(choices) != harnessCount {
		t.Fatalf("choices = %+v", choices)
	}
	for _, choice := range choices {
		if choice.Error != "" {
			t.Errorf("%s = %+v", choice.Harness, choice)
		}
	}
}

func TestARequestWhileTheStartReadRunsWaitsAndStartsNoSecondRead(t *testing.T) {
	t.Parallel()
	server, dataDir := choicesServer(t, choicesScript)
	testkit.InstallFakeAgent(t, dataDir, holdChoicesScript)
	defer releaseHarnessReads(t, dataDir)

	started := make(chan struct{})
	go func() {
		server.Engine.ReadHarnessChoices(t.Context())
		close(started)
	}()
	waitForSessionNewStarts(t, dataDir, harnessCount)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("ReadHarnessChoices did not return before the reads ended")
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(dataDir, "harnesses", "session_new_release"), nil, 0o600)
	}()

	choices := server.Engine.HarnessChoices(t.Context())

	if got := sessionNewStarts(t, dataDir); got != harnessCount {
		t.Errorf("reads at session/new = %d, want %d", got, harnessCount)
	}
	if len(choices) != harnessCount {
		t.Fatalf("choices = %+v", choices)
	}
	for _, choice := range choices {
		if choice.Error != "" {
			t.Errorf("%s = %+v", choice.Harness, choice)
		}
	}
}

func TestAFailedHarnessReadGivesTheErrorTextAndTheNextCallReadsAgain(t *testing.T) {
	t.Parallel()
	server, dataDir := choicesServer(t, noModelScript)

	server.Engine.ReadHarnessChoices(t.Context())
	first := server.Engine.HarnessChoices(t.Context())

	if len(first) != harnessCount {
		t.Fatalf("choices = %+v", first)
	}
	for _, choice := range first {
		if want := runner.Program(choice.Harness) + " gave no model option"; choice.Error != want || len(choice.Models) != 0 {
			t.Errorf("%s = %+v, want the error %q", choice.Harness, choice, want)
		}
	}
	if starts := agentStarts(t, dataDir); starts != harnessCount {
		t.Errorf("starts = %d, want %d", starts, harnessCount)
	}
	server.Engine.HarnessChoices(t.Context())
	if starts := agentStarts(t, dataDir); starts != 2*harnessCount {
		t.Errorf("starts = %d, want %d", starts, 2*harnessCount)
	}
}

func TestAHarnessWhoseProgramIsNotOnThePathHasNoChoice(t *testing.T) {
	t.Parallel()
	server, dataDir := choicesServer(t, choicesScript)
	if err := os.Remove(filepath.Join(dataDir, "harnesses", "agy_acp_server")); err != nil {
		t.Fatal(err)
	}

	var harnesses []config.Harness
	for _, choice := range server.Engine.HarnessChoices(t.Context()) {
		harnesses = append(harnesses, choice.Harness)
	}

	if want := []config.Harness{config.ClaudeCode, config.Devin, config.DSH}; !reflect.DeepEqual(harnesses, want) {
		t.Errorf("harnesses = %v, want %v", harnesses, want)
	}
}

func TestTheAPIGivesTheModelsAndTheEffortsOfEachHarness(t *testing.T) {
	t.Parallel()
	server, _ := choicesServer(t, choicesScript)

	response, err := server.Client.Get(server.URL + "/api/harnesses")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var body struct {
		Data []struct {
			Harness string `json:"harness"`
			Models  []struct {
				Model   string   `json:"model"`
				Efforts []string `json:"efforts"`
			} `json:"models"`
			Error string `json:"error"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != harnessCount {
		t.Fatalf("data = %+v", body.Data)
	}
	claude, antigravity := body.Data[0], body.Data[1]
	if claude.Harness != "claude-code" || claude.Error != "" || len(claude.Models) != 2 ||
		claude.Models[0].Model != "sonnet" || !reflect.DeepEqual(claude.Models[0].Efforts, []string{"low", "medium"}) ||
		claude.Models[1].Model != "opus" || !reflect.DeepEqual(claude.Models[1].Efforts, []string{"low", "high", "max"}) {
		t.Errorf("claude-code = %+v", claude)
	}
	if antigravity.Harness != "antigravity" || len(antigravity.Models) != 2 || len(antigravity.Models[0].Efforts) != 0 {
		t.Errorf("antigravity = %+v", antigravity)
	}
}
