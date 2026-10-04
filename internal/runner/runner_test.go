package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
)

func TestMain(m *testing.M) {
	if os.Getenv("MOBIUS_FAKE_AGENT") == "1" {
		runFakeAgent()
		return
	}
	os.Exit(m.Run())
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

	if got := find("gh", dir); got != filepath.Join(dir, "gh") {
		t.Errorf("find gh = %q", got)
	}
	if got := find("curl", dir); got != "" {
		t.Errorf("find curl = %q", got)
	}
	if got := find("git", dir); got != "" {
		t.Errorf("find git = %q", got)
	}
}

func TestCommandHasTheAgentEnvironmentAndNoGitHubToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghs_secret")
	t.Setenv("GITHUB_TOKEN", "ghs_secret")
	dataDir := t.TempDir()

	cmd := command(context.Background(), "/bin/agent", "/work", dataDir, "/usr/bin:/bin")

	if cmd.Dir != "/work" {
		t.Errorf("dir = %q", cmd.Dir)
	}
	for _, entry := range []string{
		"GH_CONFIG_DIR=" + filepath.Join(dataDir, "agent-env", "gh-config"),
		"GIT_CONFIG_GLOBAL=" + filepath.Join(dataDir, "agent-env", "gitconfig"),
		"GIT_TERMINAL_PROMPT=0",
		"PATH=/usr/bin:/bin",
	} {
		if !slices.Contains(cmd.Env, entry) {
			t.Errorf("env has no %s", entry)
		}
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "GH_TOKEN=") || strings.HasPrefix(entry, "GITHUB_TOKEN=") {
			t.Errorf("env has %s", entry)
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

func startFakeAgent(t *testing.T, updates func(acp.SessionNotification)) *Session {
	t.Setenv("MOBIUS_FAKE_AGENT", "1")
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, Program(config.ClaudeCode))); err != nil {
		t.Fatal(err)
	}
	session, err := Start(context.Background(), config.ClaudeCode, dir, dir, dir, "http://127.0.0.1:1/mcp/key", updates)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session
}

func TestSessionGivesTheMCPServerSetsTheOptionsAndAllowsTools(t *testing.T) {
	var message strings.Builder
	session := startFakeAgent(t, func(notification acp.SessionNotification) {
		if chunk := notification.Update.AgentMessageChunk; chunk != nil {
			message.WriteString(chunk.Content.Text.Text)
		}
	})
	ctx := context.Background()

	if err := session.Configure(ctx, "sonnet", "low"); err != nil {
		t.Fatal(err)
	}
	stopReason, err := session.Prompt(ctx, "Call list_tasks.")
	if err != nil {
		t.Fatal(err)
	}

	if stopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %s", stopReason)
	}
	want := `mcp [{"headers":[],"name":"mobius","type":"http","url":"http://127.0.0.1:1/mcp/key"}]; set model=sonnet effort=low mode=bypassPermissions; permission always`
	if message.String() != want {
		t.Errorf("message = %s", message.String())
	}
}

func TestConfigureRefusesAnUnknownModel(t *testing.T) {
	session := startFakeAgent(t, func(acp.SessionNotification) {})

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

func (a *fakeAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber}, nil
}

func (a *fakeAgent) NewSession(_ context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	a.servers = request.McpServers
	var options []acp.SessionConfigOption
	if err := json.Unmarshal([]byte(fakeOptions), &options); err != nil {
		return acp.NewSessionResponse{}, err
	}
	return acp.NewSessionResponse{SessionId: "session-1", ConfigOptions: options}, nil
}

func (a *fakeAgent) SetSessionConfigOption(_ context.Context, request acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	a.sets = append(a.sets, fmt.Sprintf("%s=%s", request.ValueId.ConfigId, request.ValueId.Value))
	var options []acp.SessionConfigOption
	err := json.Unmarshal([]byte(fakeOptions), &options)
	return acp.SetSessionConfigOptionResponse{ConfigOptions: options}, err
}

func (a *fakeAgent) Prompt(ctx context.Context, request acp.PromptRequest) (acp.PromptResponse, error) {
	permission, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: request.SessionId,
		ToolCall:  acp.ToolCallUpdate{ToolCallId: "call-1"},
		Options: []acp.PermissionOption{
			{OptionId: "reject", Name: "Reject", Kind: acp.PermissionOptionKindRejectOnce},
			{OptionId: "always", Name: "Always", Kind: acp.PermissionOptionKindAllowAlways},
		},
	})
	if err != nil {
		return acp.PromptResponse{}, err
	}
	servers, err := json.Marshal(a.servers)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	text := fmt.Sprintf("mcp %s; set %s; permission %s", servers, strings.Join(a.sets, " "), permission.Outcome.Selected.OptionId)
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
