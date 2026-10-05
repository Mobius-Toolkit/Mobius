package setup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// The test binary plays a fake Harness when it runs with this variable.
// The variable holds the script of each program name as JSON.
const scriptsVariable = "MOBIUS_FAKE_HARNESS_SCRIPTS"

func TestMain(m *testing.M) {
	if scripts := os.Getenv(scriptsVariable); scripts != "" {
		runFakeHarness(scripts)
		return
	}
	os.Exit(m.Run())
}

// harnessScript tells a fake Harness what to do.
type harnessScript struct {
	// Options holds the values of each session option, by category. The first value is the current value.
	Options map[string][]string
	// With LoginRequired, session/new fails until an authenticate request logs in.
	// Only with LoginWorks does the login succeed. The login writes a file in the working directory.
	LoginRequired bool
	LoginWorks    bool
}

var (
	claude = harnessScript{Options: map[string][]string{
		"model":         {"sonnet", "opus"},
		"thought_level": {"low", "medium", "high"},
		"mode":          {"default", "bypassPermissions"},
	}}
	antigravity = harnessScript{LoginRequired: true, Options: map[string][]string{
		"model": {"gemini-3-pro"},
		"mode":  {"default", "yolo"},
	}}
	devin = harnessScript{Options: map[string][]string{
		"model":         {"swe-1.5"},
		"thought_level": {"high"},
	}}
)

// installFakeHarnesses makes the test binary the program of each Harness of scripts,
// and gives the PATH with the programs.
func installFakeHarnesses(t *testing.T, scripts map[config.Harness]harnessScript) string {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	programs := map[string]harnessScript{}
	for harness, script := range scripts {
		program := runner.Program(harness)
		programs[program] = script
		if err := os.Symlink(exe, filepath.Join(dir, program)); err != nil {
			t.Fatal(err)
		}
	}
	text, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(scriptsVariable, string(text))
	return dir
}

func allHarnesses(t *testing.T) string {
	t.Helper()
	return installFakeHarnesses(t, map[config.Harness]harnessScript{
		config.ClaudeCode:  claude,
		config.Antigravity: antigravity,
		config.Devin:       devin,
	})
}

func runInit(t *testing.T, answers, configPath, path string) (string, error) {
	t.Helper()
	var out strings.Builder
	err := Run(context.Background(), strings.NewReader(answers), &out, configPath, path)
	return out.String(), err
}

// Lead, Triager, Implementer, Researcher, a refused Reviewer, the Reviewer, and the Judge: each has a Harness, a model, and an effort.
const answers = `short
short
correct horse
correct horse
owner teammate

2
3
1

2
2


1
1
1
2


1
2
3
1
1
1
`

func TestRunWritesAConfigFileThatLoads(t *testing.T) {
	path := allHarnesses(t)
	configPath := filepath.Join(t.TempDir(), "mobius", "config.toml")

	out, err := runInit(t, answers, configPath, path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, want := range []string{
		"The password has less than 8 characters.",
		"Antigravity: Internal error \"Onboarding failed: Timed out waiting for the authentication flow to complete.\"\n",
		"  1) claude-code\n  2) devin\nHarness [1]: ",
		"The Reviewer needs another Harness or another model than the Implementer.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output has no %q:\n%s", want, out)
		}
	}
	if want := "Mobius wrote " + configPath + ".\nStart Mobius with `mobius`.\n"; !strings.HasSuffix(out, want) {
		t.Errorf("output does not end with %q:\n%s", want, out)
	}
	text, err := os.ReadFile(filepath.Clean(configPath))
	if err != nil {
		t.Fatal(err)
	}
	want := `access_password = 'correct horse'
trusted_users = ['owner', 'teammate']

[roles]
lead = {harness = 'claude-code', model = 'opus', effort = 'high'}
triager = {harness = 'claude-code', model = 'sonnet', effort = 'medium'}
implementer = {harness = 'devin', model = 'swe-1.5', effort = 'high'}
researcher = {harness = 'claude-code', model = 'sonnet', effort = 'low'}
reviewer = {harness = 'claude-code', model = 'opus', effort = 'high'}
judge = {harness = 'claude-code', model = 'sonnet', effort = 'low'}
`
	if string(text) != want {
		t.Errorf("config file:\n%s\nwant:\n%s", text, want)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %v, want 0600", mode)
	}
	if _, err := config.Load(configPath); err != nil {
		t.Error(err)
	}
}

func TestRunLogsInToAntigravityAndOffersIt(t *testing.T) {
	script := antigravity
	script.LoginWorks = true
	path := installFakeHarnesses(t, map[config.Harness]harnessScript{
		config.ClaudeCode:  claude,
		config.Antigravity: script,
		config.Devin:       devin,
	})
	configPath := filepath.Join(t.TempDir(), "config.toml")
	// The Researcher uses Antigravity, and the Implementer uses Devin. The other Roles take the defaults.
	answers := "correct horse\ncorrect horse\nowner\n\n\n\n\n\n\n3\n\n\n2\n\n\n\n\n\n\n\n"

	out, err := runInit(t, answers, configPath, path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, want := range []string{
		"Antigravity: logged in\n",
		"  1) claude-code\n  2) antigravity\n  3) devin\nHarness [1]: ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output has no %q:\n%s", want, out)
		}
	}
	text, err := os.ReadFile(filepath.Clean(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if want := "researcher = {harness = 'antigravity', model = 'gemini-3-pro'}\n"; !strings.Contains(string(text), want) {
		t.Errorf("config file has no %q:\n%s", want, text)
	}
}

func TestRunSkipsAHarnessThatIsNotOnPath(t *testing.T) {
	path := installFakeHarnesses(t, map[config.Harness]harnessScript{config.ClaudeCode: claude})
	configPath := filepath.Join(t.TempDir(), "config.toml")
	answers := "correct horse\ncorrect horse\nowner\n" + strings.Repeat("\n\n\n", 4) + "\n2\n\n\n\n\n"

	out, err := runInit(t, answers, configPath, path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, want := range []string{
		"Antigravity: `agy_acp_server` is not on PATH\n",
		"Devin: `devin` is not on PATH\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output has no %q:\n%s", want, out)
		}
	}
}

func TestRunRefusesAnExistingConfigFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runInit(t, answers, configPath, "")

	if want := configPath + " exists: `mobius init` writes only a new file"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
}

func TestRunRefusesAnInputThatEnds(t *testing.T) {
	_, err := runInit(t, "correct horse\n", filepath.Join(t.TempDir(), "config.toml"), "")

	if want := "the input ended before the last answer"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
}

// fakeHarness plays a Harness from the script of its program name.
type fakeHarness struct {
	script harnessScript
}

// The fake Harness runs in the working directory that mobius init gives to each Harness.
const loginFile = "fake-harness-logged-in"

func runFakeHarness(scripts string) {
	var programs map[string]harnessScript
	if err := json.Unmarshal([]byte(scripts), &programs); err != nil {
		panic(err)
	}
	h := &fakeHarness{script: programs[filepath.Base(os.Args[0])]}
	conn := acp.NewAgentSideConnection(h, os.Stdout, os.Stdin)
	<-conn.Done()
}

var errFakeHarness = errors.New("the fake Harness has no such method")

func (h *fakeHarness) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber}, nil
}

func (h *fakeHarness) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if _, err := os.Stat(loginFile); h.script.LoginRequired && err != nil {
		return acp.NewSessionResponse{}, acp.NewAuthRequired(nil)
	}
	var options []acp.SessionConfigOption
	for _, category := range []acp.SessionConfigOptionCategory{
		acp.SessionConfigOptionCategoryModel,
		acp.SessionConfigOptionCategoryThoughtLevel,
		acp.SessionConfigOptionCategoryMode,
	} {
		values := h.script.Options[string(category)]
		if len(values) == 0 {
			continue
		}
		choices := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(values))
		for _, value := range values {
			choices = append(choices, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(value), Name: value})
		}
		options = append(options, acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{
			Type:         "select",
			Id:           acp.SessionConfigId(category),
			Name:         string(category),
			Category:     &category,
			CurrentValue: acp.SessionConfigValueId(values[0]),
			Options:      acp.SessionConfigSelectOptions{Ungrouped: &choices},
		}})
	}
	return acp.NewSessionResponse{SessionId: "session-1", ConfigOptions: options}, nil
}

func (h *fakeHarness) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	if !h.script.LoginWorks {
		return acp.AuthenticateResponse{}, &acp.RequestError{
			Code:    -32603,
			Message: "Internal error",
			Data:    "Onboarding failed: Timed out waiting for the authentication flow to complete.",
		}
	}
	return acp.AuthenticateResponse{}, os.WriteFile(loginFile, nil, 0o600)
}

func (h *fakeHarness) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, errFakeHarness
}

func (h *fakeHarness) Prompt(context.Context, acp.PromptRequest) (acp.PromptResponse, error) {
	return acp.PromptResponse{}, errFakeHarness
}

func (h *fakeHarness) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, errFakeHarness
}

func (h *fakeHarness) Cancel(context.Context, acp.CancelNotification) error {
	return errFakeHarness
}

func (h *fakeHarness) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, errFakeHarness
}

func (h *fakeHarness) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, errFakeHarness
}

func (h *fakeHarness) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, errFakeHarness
}

func (h *fakeHarness) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, errFakeHarness
}
