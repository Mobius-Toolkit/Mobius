// Package runner starts a Claude Code session through ACP.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/coder/acp-go-sdk"
)

const program = "claude-agent-acp"

// The mode of Claude Code that runs each tool with no permission request.
const fullAutoMode = "bypassPermissions"

func agentEnv(dataDir string) string {
	return filepath.Join(dataDir, "agent-env")
}

// Prepare writes the agent environment below dataDir.
func Prepare(dataDir string) error {
	agentEnv := agentEnv(dataDir)
	if err := os.MkdirAll(filepath.Join(agentEnv, "gh-config"), 0o750); err != nil {
		return err
	}
	// An empty helper clears the credential helpers of the system git config.
	return os.WriteFile(filepath.Join(agentEnv, "gitconfig"), []byte("[credential]\n\thelper =\n"), 0o600)
}

func find(program, path string) string {
	for _, dir := range filepath.SplitList(path) {
		file := filepath.Join(dir, program)
		info, err := os.Stat(file)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return file
		}
	}
	return ""
}

func command(ctx context.Context, file, cwd, dataDir, path string) *exec.Cmd {
	agentEnv := agentEnv(dataDir)
	cmd := exec.CommandContext(ctx, file)
	cmd.Dir = cwd
	cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "GH_TOKEN=") || strings.HasPrefix(entry, "GITHUB_TOKEN=")
	})
	cmd.Env = append(cmd.Env,
		"GH_CONFIG_DIR="+filepath.Join(agentEnv, "gh-config"),
		"GIT_CONFIG_GLOBAL="+filepath.Join(agentEnv, "gitconfig"),
		"GIT_TERMINAL_PROMPT=0",
		"PATH="+path,
	)
	return cmd
}

// Session is one ACP session of Claude Code.
type Session struct {
	conn    *acp.ClientSideConnection
	id      acp.SessionId
	options []acp.SessionConfigOption
	cmd     *exec.Cmd
	stop    context.CancelFunc
}

// Start starts the agent in cwd with the Mobius MCP server at mcpURL, and opens a session.
// It gives each session/update notification of the session to updates.
func Start(ctx context.Context, cwd, dataDir, path, mcpURL string, updates func(acp.SessionNotification)) (*Session, error) {
	file := find(program, path)
	if file == "" {
		return nil, fmt.Errorf("%s is not on PATH", program)
	}
	processCtx, stop := context.WithCancel(context.Background())
	cmd := command(processCtx, file, cwd, dataDir, path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stop()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stop()
		return nil, fmt.Errorf("%s: %w", program, err)
	}
	session := &Session{
		conn: acp.NewClientSideConnection(client{updates: updates}, stdin, stdout),
		cmd:  cmd,
		stop: stop,
	}
	if err := session.open(ctx, cwd, mcpURL); err != nil {
		session.Close()
		return nil, fmt.Errorf("%s: %w", program, err)
	}
	return session, nil
}

func (s *Session) open(ctx context.Context, cwd, mcpURL string) error {
	if _, err := s.conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		return err
	}
	response, err := s.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd: cwd,
		McpServers: []acp.McpServer{{Http: &acp.McpServerHttpInline{
			Name:    "mobius",
			Url:     mcpURL,
			Headers: []acp.HttpHeader{},
		}}},
	})
	if err != nil {
		return err
	}
	s.id = response.SessionId
	s.options = response.ConfigOptions
	return nil
}

// ID gives the ACP session id.
func (s *Session) ID() string {
	return string(s.id)
}

// Configure sets the model, the effort when it is not empty, and the full auto mode.
func (s *Session) Configure(ctx context.Context, model, effort string) error {
	if err := s.set(ctx, acp.SessionConfigOptionCategoryModel, "model", model); err != nil {
		return err
	}
	if effort != "" {
		if err := s.set(ctx, acp.SessionConfigOptionCategoryThoughtLevel, "thought_level", effort); err != nil {
			return err
		}
	}
	return s.set(ctx, acp.SessionConfigOptionCategoryMode, "mode", fullAutoMode)
}

func (s *Session) set(ctx context.Context, category acp.SessionConfigOptionCategory, name, value string) error {
	var option *acp.SessionConfigOptionSelect
	for _, candidate := range s.options {
		if candidate.Select != nil && candidate.Select.Category != nil && *candidate.Select.Category == category {
			option = candidate.Select
			break
		}
	}
	if option == nil {
		return fmt.Errorf("%s has no %s option", program, name)
	}
	values := values(option)
	refused := fmt.Sprintf("%s refuses %s %q, it has: %s", program, name, value, strings.Join(values, ", "))
	if !slices.Contains(values, value) {
		return errors.New(refused)
	}
	response, err := s.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
		SessionId: s.id,
		ConfigId:  option.Id,
		Value:     acp.SessionConfigValueId(value),
	}})
	if err != nil {
		return fmt.Errorf("%s: %w", refused, err)
	}
	s.options = response.ConfigOptions
	return nil
}

func values(option *acp.SessionConfigOptionSelect) []string {
	var values []string
	if option.Options.Ungrouped != nil {
		for _, choice := range *option.Options.Ungrouped {
			values = append(values, string(choice.Value))
		}
	}
	if option.Options.Grouped != nil {
		for _, group := range *option.Options.Grouped {
			for _, choice := range group.Options {
				values = append(values, string(choice.Value))
			}
		}
	}
	return values
}

// Prompt sends text and holds until the turn ends.
func (s *Session) Prompt(ctx context.Context, text string) (acp.StopReason, error) {
	response, err := s.conn.Prompt(ctx, acp.PromptRequest{SessionId: s.id, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
	return response.StopReason, err
}

// Close ends the agent process.
func (s *Session) Close() {
	s.stop()
	// The kill ends the agent, so Wait gives an error that tells nothing new.
	_ = s.cmd.Wait()
}

type client struct {
	updates func(acp.SessionNotification)
}

func (c client) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	c.updates(notification)
	return nil
}

func (client) RequestPermission(_ context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{Outcome: permission(request.Options)}, nil
}

func permission(options []acp.PermissionOption) acp.RequestPermissionOutcome {
	for _, option := range options {
		if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: option.OptionId}}
		}
	}
	return acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}
}

// The session gives the agent no file system and no terminal, so the agent must not call these methods.

func (client) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

func (client) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

func (client) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (client) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (client) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (client) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (client) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}
