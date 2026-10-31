package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "gh" {
		os.Exit(GH(os.Args[1:], os.Getenv))
	}
	if filepath.Base(os.Args[0]) == "check" {
		os.Exit(printNiceValue())
	}
	if dataDir := os.Getenv("MOBIUS_FETCH_HELPER"); dataDir != "" {
		err := Fetch(context.Background(), dataDir, "acme/shop", sshURL, "")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if os.Getenv("MOBIUS_FAKE_AGENT") == "1" {
		runFakeAgent()
		return
	}
	testkit.Main(m)
}

func TestFindGivesTheFileOfAnExecutableProgram(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "gh")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := Find("gh", dir); got != filepath.Join(dir, "gh") {
		t.Errorf("Find gh = %q", got)
	}
	if got := Find("curl", dir); got != "" {
		t.Errorf("Find curl = %q", got)
	}
	if got := Find("git", dir); got != "" {
		t.Errorf("Find git = %q", got)
	}
}

func TestCommandHasTheAgentEnvironmentAndNoGitHubToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghs_secret")
	t.Setenv("GITHUB_TOKEN", "ghs_secret")
	t.Setenv("MOBIUS_GH_SESSION_URL", "http://127.0.0.1:1/gh-token/parent")
	t.Setenv("MOBIUS_GH_WRITES_URL", "http://127.0.0.1:1/gh-writes/parent")
	t.Setenv("MOBIUS_GH_MARK", "— Lead of #1")
	dataDir := t.TempDir()

	cmd := command(context.Background(), "/bin/agent", "/work", dataDir, "/usr/bin:/bin", "", "", "")

	if cmd.Dir != "/work" {
		t.Errorf("dir = %q", cmd.Dir)
	}
	for _, entry := range []string{
		"GH_CONFIG_DIR=" + filepath.Join(dataDir, "agent-env", "gh-config"),
		"GIT_CONFIG_GLOBAL=" + filepath.Join(dataDir, "agent-env", "gitconfig"),
		"GIT_TERMINAL_PROMPT=0",
		"PATH=" + filepath.Join(dataDir, "agent-env", "bin") + ":/usr/bin:/bin",
	} {
		if !slices.Contains(cmd.Env, entry) {
			t.Errorf("env has no %s", entry)
		}
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "GH_TOKEN=") || strings.HasPrefix(entry, "GITHUB_TOKEN=") || strings.HasPrefix(entry, "MOBIUS_GH_SESSION_URL=") || strings.HasPrefix(entry, "MOBIUS_GH_WRITES_URL=") || strings.HasPrefix(entry, "MOBIUS_GH_MARK=") {
			t.Errorf("env has %s", entry)
		}
	}
}

func TestACommandWithAGHTokenURLHasTheURL(t *testing.T) {
	cmd := command(context.Background(), "/bin/agent", "/work", t.TempDir(), "/usr/bin:/bin", "http://127.0.0.1:6363/gh-token/key", "", "")

	if !slices.Contains(cmd.Env, "MOBIUS_GH_SESSION_URL=http://127.0.0.1:6363/gh-token/key") {
		t.Errorf("env = %v", cmd.Env)
	}
}

func TestACommandWithAGHWritesURLHasTheURL(t *testing.T) {
	cmd := command(context.Background(), "/bin/agent", "/work", t.TempDir(), "/usr/bin:/bin", "", "http://127.0.0.1:6363/gh-writes/key", "")

	if !slices.Contains(cmd.Env, "MOBIUS_GH_WRITES_URL=http://127.0.0.1:6363/gh-writes/key") {
		t.Errorf("env = %v", cmd.Env)
	}
}

func TestACommandWithAGHMarkHasTheMark(t *testing.T) {
	cmd := command(context.Background(), "/bin/agent", "/work", t.TempDir(), "/usr/bin:/bin", "", "", "— Lead of #1")

	if !slices.Contains(cmd.Env, "MOBIUS_GH_MARK=— Lead of #1") {
		t.Errorf("env = %v", cmd.Env)
	}
}

// SENSITIVE_ENV_PATTERN of scrubbedParentEnv in @deepseek-ai/dsh-subprocess drops each environment name that it
// matches, and each name with the prefix DSH_.
var dshSensitiveEnv = regexp.MustCompile(`(?i)KEY|PASSWORD|SECRET|TOKEN`)

func dshDropsEnv(name string) bool {
	return dshSensitiveEnv.MatchString(name) || strings.HasPrefix(strings.ToUpper(name), "DSH_")
}

func TestTheGHEnvironmentOfAnAgentPassesTheDSHEnvironmentFilter(t *testing.T) {
	cmd := command(context.Background(), "/bin/agent", "/work", t.TempDir(), "/usr/bin:/bin", "http://127.0.0.1:6363/gh-token/key", "http://127.0.0.1:6363/gh-writes/key", "— Lead of #1")

	kept := map[string]bool{}
	for _, entry := range cmd.Env {
		name, _, _ := strings.Cut(entry, "=")
		if !dshDropsEnv(name) {
			kept[name] = true
		}
	}
	for _, name := range []string{ghURLEnv, ghWritesURLEnv, ghMarkEnv} {
		if !kept[name] {
			t.Errorf("dsh drops %s from the environment of a shell command", name)
		}
	}
}

// lastEnv gives the last value of name in env, which is the value that exec.Cmd gives the process.
func lastEnv(env []string, name string) string {
	value := ""
	for _, entry := range env {
		if got, ok := strings.CutPrefix(entry, name+"="); ok {
			value = got
		}
	}
	return value
}

func TestDSHGetsTheDangerFullAccessModeAndNoOtherHarnessGetsIt(t *testing.T) {
	t.Setenv("DSH_PERMISSION_MODE", "workspace-write")
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range config.Harnesses {
		if err := os.Symlink(exe, filepath.Join(dir, Program(harness))); err != nil {
			t.Fatal(err)
		}
	}

	cmd, err := harnessCommand(context.Background(), config.DSH, 0, "/work", t.TempDir(), dir, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--profile", "acp"}; !slices.Equal(cmd.Args[1:], want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
	if got := lastEnv(cmd.Env, "DSH_PERMISSION_MODE"); got != "danger-full-access" {
		t.Errorf("dsh DSH_PERMISSION_MODE = %q, want danger-full-access", got)
	}

	for _, harness := range config.Harnesses {
		if harness == config.DSH {
			continue
		}
		cmd, err := harnessCommand(context.Background(), harness, 0, "/work", t.TempDir(), dir, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := lastEnv(cmd.Env, "DSH_PERMISSION_MODE"); got != "workspace-write" {
			t.Errorf("%s DSH_PERMISSION_MODE = %q, want the inherited workspace-write", harness, got)
		}
	}
}

func TestOnlyClaudeCodeWithAContextGetsTheContextWindow(t *testing.T) {
	t.Setenv(contextWindowEnv, "inherited")
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range config.Harnesses {
		if err := os.Symlink(exe, filepath.Join(dir, Program(harness))); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		name    string
		harness config.Harness
		context int
		want    string
	}{
		{"claude-code with a context", config.ClaudeCode, 200000, "200000"},
		{"claude-code with no context", config.ClaudeCode, 0, ""},
		{"dsh with a context", config.DSH, 200000, ""},
	} {
		cmd, err := harnessCommand(context.Background(), c.harness, c.context, "/work", t.TempDir(), dir, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := lastEnv(cmd.Env, contextWindowEnv); got != c.want {
			t.Errorf("%s: %s = %q, want %q", c.name, contextWindowEnv, got, c.want)
		}
	}
}

func TestPermissionSelectsTheFirstAllowOption(t *testing.T) {
	outcome := permission([]acp.PermissionOption{
		{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce},
		{OptionId: "always", Kind: acp.PermissionOptionKindAllowAlways},
		{OptionId: "once", Kind: acp.PermissionOptionKindAllowOnce},
	})

	if outcome.Selected == nil || outcome.Selected.OptionId != "always" {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestPermissionWithNoAllowOptionIsCancelled(t *testing.T) {
	outcome := permission([]acp.PermissionOption{{OptionId: "reject", Kind: acp.PermissionOptionKindRejectAlways}})

	if outcome.Cancelled == nil || outcome.Selected != nil {
		t.Errorf("outcome = %+v", outcome)
	}
}

// fakeAgentDir gives a directory with the fake agent as the program of Claude Code.
func fakeAgentDir(t *testing.T) string {
	t.Setenv("MOBIUS_FAKE_AGENT", "1")
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, Program(config.ClaudeCode))); err != nil {
		t.Fatal(err)
	}
	return dir
}

func startFakeAgent(t *testing.T, updates func(json.RawMessage, bool)) *Session {
	return startFakeAgentAt(t, "http://127.0.0.1:1/mcp/key", updates)
}

func startFakeAgentAt(t *testing.T, mcpURL string, updates func(json.RawMessage, bool)) *Session {
	dir := fakeAgentDir(t)
	session, err := Start(context.Background(), config.ClaudeCode, 0, dir, dir, dir, mcpURL, "", "", "", updates)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session
}

func TestSessionGivesTheMCPServerSetsTheOptionsAndAllowsTools(t *testing.T) {
	var message strings.Builder
	session := startFakeAgent(t, func(params json.RawMessage, _ bool) {
		var notification struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if err := json.Unmarshal(params, &notification); err != nil {
			t.Error(err)
		}
		if notification.Update.SessionUpdate == "agent_message_chunk" {
			message.WriteString(notification.Update.Content.Text)
		}
	})
	ctx := context.Background()

	if err := session.Configure(ctx, "sonnet", "low"); err != nil {
		t.Fatal(err)
	}
	result, err := session.Prompt(ctx, "Call list_tasks.", nil)
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %s", result.StopReason)
	}
	want := `mcp [{"headers":[],"name":"mobius","type":"http","url":"http://127.0.0.1:1/mcp/key"}]; set model=sonnet effort=low mode=bypassPermissions; permission always`
	if message.String() != want {
		t.Errorf("message = %s", message.String())
	}
}

func TestStartWithNoMCPURLGivesNoMCPServer(t *testing.T) {
	var message strings.Builder
	session := startFakeAgentAt(t, "", func(params json.RawMessage, _ bool) {
		var notification struct {
			Update struct {
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if err := json.Unmarshal(params, &notification); err != nil {
			t.Error(err)
		}
		message.WriteString(notification.Update.Content.Text)
	})

	if _, err := session.Prompt(context.Background(), "Say the servers.", nil); err != nil {
		t.Fatal(err)
	}

	if want := "mcp []; set ; permission always"; message.String() != want {
		t.Errorf("message = %s, want %s", message.String(), want)
	}
}

func startDSH(t *testing.T, updates func(json.RawMessage, bool)) *Session {
	t.Setenv("MOBIUS_FAKE_AGENT", "1")
	t.Setenv("MOBIUS_FAKE_DSH", "1")
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "dsh")); err != nil {
		t.Fatal(err)
	}
	session, err := Start(context.Background(), config.DSH, 0, dir, dir, dir, "http://127.0.0.1:1/mcp/key", "", "", "", updates)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session
}

func TestDSHRunsWithTheACPProfileAndGetsTheModelAndTheEffortByTheirCategories(t *testing.T) {
	var message strings.Builder
	session := startDSH(t, func(params json.RawMessage, _ bool) {
		var notification struct {
			Update struct {
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if err := json.Unmarshal(params, &notification); err != nil {
			t.Error(err)
		}
		message.WriteString(notification.Update.Content.Text)
	})
	ctx := context.Background()

	if err := session.Configure(ctx, "deepseek-account/deepseek-v4-pro", "high"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Prompt(ctx, "Say the options.", nil); err != nil {
		t.Fatal(err)
	}

	want := `mcp [{"headers":[],"name":"mobius","type":"http","url":"http://127.0.0.1:1/mcp/key"}]; set model=["deepseek-account","deepseek-v4-pro"] reasoning_effort=high; permission allow-once; args --profile acp`
	if message.String() != want {
		t.Errorf("message = %s", message.String())
	}
}

func TestDSHModelsAreRouteAndModel(t *testing.T) {
	session := startDSH(t, func(json.RawMessage, bool) {})

	models := session.Models()

	if want := []string{"deepseek-account/deepseek-flash", "deepseek-account/deepseek-v4-pro"}; !slices.Equal(models.Values, want) {
		t.Errorf("values = %v, want %v", models.Values, want)
	}
	if want := "deepseek-official/deepseek-v4-flash"; models.Current != want {
		t.Errorf("current = %s, want %s", models.Current, want)
	}
}

func TestConfigureRefusesADSHModelWithNoRouteAndListsRouteAndModel(t *testing.T) {
	session := startDSH(t, func(json.RawMessage, bool) {})

	err := session.Configure(context.Background(), "deepseek-v4-pro", "")

	want := `dsh refuses model "deepseek-v4-pro", it has: deepseek-account/deepseek-flash, deepseek-account/deepseek-v4-pro`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v", err)
	}
}

func TestModelsOfAnotherHarnessStayAsTheAgentGivesThem(t *testing.T) {
	session := startFakeAgent(t, func(json.RawMessage, bool) {})

	models := session.Models()

	if want := []string{"default", "sonnet"}; !slices.Equal(models.Values, want) || models.Current != "default" {
		t.Errorf("models = %+v", models)
	}
}

func TestAStartThatDoesNotAnswerFailsAfterStartTimeoutAndKillsTheAgent(t *testing.T) {
	dir := fakeAgentDir(t)
	t.Setenv("MOBIUS_FAKE_HANG", "1")
	defer func(limit time.Duration) { StartTimeout = limit }(StartTimeout)
	StartTimeout = 200 * time.Millisecond

	_, err := Start(context.Background(), config.ClaudeCode, 0, dir, dir, dir, "http://127.0.0.1:1/mcp/key", "", "", "", func(json.RawMessage, bool) {})

	if want := "claude-agent-acp: the start took longer than 200ms"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
	text, readErr := os.ReadFile(filepath.Clean(filepath.Join(dir, "pid")))
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, _ := strconv.Atoi(string(text))
	if killErr := syscall.Kill(pid, 0); !errors.Is(killErr, syscall.ESRCH) {
		t.Errorf("the agent runs: %v", killErr)
	}
}

func TestConfigureRefusesAnUnknownModel(t *testing.T) {
	session := startFakeAgent(t, func(json.RawMessage, bool) {})

	err := session.Configure(context.Background(), "opus", "")

	want := `claude-agent-acp refuses model "opus", it has: default, sonnet`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v", err)
	}
}

const fakeOptions = `[
	{"type": "select", "id": "model", "name": "Model", "category": "model", "currentValue": "default",
		"options": [{"value": "default", "name": "Default"}, {"value": "sonnet", "name": "Sonnet"}]},
	{"type": "select", "id": "effort", "name": "Effort", "category": "thought_level", "currentValue": "high",
		"options": [{"group": "all", "name": "All", "options": [{"value": "low", "name": "Low"}, {"value": "high", "name": "High"}]}]},
	{"type": "select", "id": "mode", "name": "Mode", "category": "mode", "currentValue": "default",
		"options": [{"value": "default", "name": "Default"}, {"value": "bypassPermissions", "name": "Bypass"}]}
]`

// dshOptions are the options of dsh: the ids differ from the categories.
const dshOptions = `[
	{"type": "select", "id": "model", "name": "Model", "category": "model", "currentValue": "[\"deepseek-official\",\"deepseek-v4-flash\"]",
		"options": [{"group": "deepseek-account", "name": "DeepSeek Account", "options": [
			{"value": "[\"deepseek-account\",\"deepseek-flash\"]", "name": "DeepSeek-V4.1-Flash"},
			{"value": "[\"deepseek-account\",\"deepseek-v4-pro\"]", "name": "DeepSeek-V4-Pro"}]}]},
	{"type": "select", "id": "reasoning_effort", "name": "Reasoning effort", "category": "thought_level", "currentValue": "high",
		"options": [{"value": "off", "name": "Off"}, {"value": "low", "name": "Low"}, {"value": "high", "name": "High"}, {"value": "max", "name": "Max"}]}
]`

func fakeOptionsText() string {
	if os.Getenv("MOBIUS_FAKE_DSH") == "1" {
		return dshOptions
	}
	return fakeOptions
}

// fakeAgent tells the received MCP servers and options in its reply to a prompt.
type fakeAgent struct {
	conn    *acp.AgentSideConnection
	servers []acp.McpServer
	sets    []string
}

func runFakeAgent() {
	agent := &fakeAgent{}
	agent.conn = acp.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
	<-agent.conn.Done()
}

func (a *fakeAgent) Initialize(ctx context.Context, _ acp.InitializeRequest) (acp.InitializeResponse, error) {
	if os.Getenv("MOBIUS_FAKE_HANG") == "1" {
		if err := os.WriteFile("pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return acp.InitializeResponse{}, err
		}
		<-ctx.Done()
		return acp.InitializeResponse{}, ctx.Err()
	}
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber}, nil
}

func (a *fakeAgent) NewSession(_ context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	a.servers = request.McpServers
	servers, err := json.Marshal(request.McpServers)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	if err := os.WriteFile("servers.json", servers, 0o600); err != nil {
		return acp.NewSessionResponse{}, err
	}
	var options []acp.SessionConfigOption
	if err := json.Unmarshal([]byte(fakeOptionsText()), &options); err != nil {
		return acp.NewSessionResponse{}, err
	}
	return acp.NewSessionResponse{SessionId: "session-1", ConfigOptions: options}, nil
}

func (a *fakeAgent) SetSessionConfigOption(_ context.Context, request acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	a.sets = append(a.sets, fmt.Sprintf("%s=%s", request.ValueId.ConfigId, request.ValueId.Value))
	var options []acp.SessionConfigOption
	err := json.Unmarshal([]byte(fakeOptionsText()), &options)
	return acp.SetSessionConfigOptionResponse{ConfigOptions: options}, err
}

func (a *fakeAgent) Prompt(ctx context.Context, request acp.PromptRequest) (acp.PromptResponse, error) {
	options := []acp.PermissionOption{
		{OptionId: "reject", Name: "Reject", Kind: acp.PermissionOptionKindRejectOnce},
		{OptionId: "always", Name: "Always", Kind: acp.PermissionOptionKindAllowAlways},
	}
	if os.Getenv("MOBIUS_FAKE_DSH") == "1" {
		options = []acp.PermissionOption{
			{OptionId: "reject-once", Name: "Reject once", Kind: acp.PermissionOptionKindRejectOnce},
			{OptionId: "allow-once", Name: "Allow once", Kind: acp.PermissionOptionKindAllowOnce},
		}
	}
	permission, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: request.SessionId,
		ToolCall:  acp.ToolCallUpdate{ToolCallId: "call-1"},
		Options:   options,
	})
	if err != nil {
		return acp.PromptResponse{}, err
	}
	servers, err := json.Marshal(a.servers)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	text := fmt.Sprintf("mcp %s; set %s; permission %s", servers, strings.Join(a.sets, " "), permission.Outcome.Selected.OptionId)
	if os.Getenv("MOBIUS_FAKE_DSH") == "1" {
		text += "; args " + strings.Join(os.Args[1:], " ")
	}
	prompt, err := json.Marshal(request.Prompt)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	if err := os.WriteFile("prompt.json", prompt, 0o600); err != nil {
		return acp.PromptResponse{}, err
	}
	sets, err := json.Marshal(a.sets)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	if err := os.WriteFile("sets.json", sets, 0o600); err != nil {
		return acp.PromptResponse{}, err
	}
	if err := a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: request.SessionId, Update: acp.UpdateAgentMessageText(text)}); err != nil {
		return acp.PromptResponse{}, err
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

var errFake = errors.New("the fake agent has no such method")

func (a *fakeAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, errFake
}

func (a *fakeAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, errFake
}

func (a *fakeAgent) Cancel(context.Context, acp.CancelNotification) error {
	return errFake
}

func (a *fakeAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, errFake
}

func (a *fakeAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, errFake
}

func (a *fakeAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, errFake
}

func (a *fakeAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, errFake
}
