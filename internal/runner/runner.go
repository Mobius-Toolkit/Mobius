// Package runner starts an agent session of a Harness through ACP.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
)

// Program gives the ACP program of harness.
func Program(harness config.Harness) string {
	switch harness {
	case config.Antigravity:
		return "agy_acp_server"
	case config.Devin:
		return "devin"
	}
	return "claude-agent-acp"
}

// The modes that run each tool with no permission request. Devin has no mode option.
var fullAutoModes = map[config.Harness]string{
	config.ClaudeCode:  "bypassPermissions",
	config.Antigravity: "yolo",
}

// The ACP error code of session/new when the agent needs a login.
const authRequired = -32000

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

// Missing gives each of programs that is not on path.
func Missing(path string, programs ...string) []string {
	var missing []string
	for _, program := range programs {
		if find(program, path) == "" {
			missing = append(missing, program)
		}
	}
	return missing
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

// Session is one ACP session of a Harness.
type Session struct {
	harness config.Harness
	conn    *acp.ClientSideConnection
	id      acp.SessionId
	options []acp.SessionConfigOption
	cmd     *exec.Cmd
	stop    context.CancelFunc
}

func harnessCommand(ctx context.Context, harness config.Harness, cwd, dataDir, path string) (*exec.Cmd, error) {
	program := Program(harness)
	file := find(program, path)
	if file == "" {
		return nil, fmt.Errorf("%s is not on PATH", program)
	}
	cmd := command(ctx, file, cwd, dataDir, path)
	if harness == config.Devin {
		cmd.Args = append(cmd.Args, "acp")
	}
	return cmd, nil
}

// Start starts the agent of harness in cwd with the Mobius MCP server at mcpURL, and opens a session.
// It gives each session/update notification of the session to updates.
func Start(ctx context.Context, harness config.Harness, cwd, dataDir, path, mcpURL string, updates func(acp.SessionNotification)) (*Session, error) {
	program := Program(harness)
	processCtx, stop := context.WithCancel(context.Background())
	cmd, err := harnessCommand(processCtx, harness, cwd, dataDir, path)
	if err != nil {
		stop()
		return nil, err
	}
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
		harness: harness,
		conn:    acp.NewClientSideConnection(client{updates: updates}, stdin, stdout),
		cmd:     cmd,
		stop:    stop,
	}
	if err := session.open(ctx, cwd, mcpURL); err != nil {
		session.Close()
		return nil, fmt.Errorf("%s: %w", program, describe(err))
	}
	return session, nil
}

// LogInAntigravity starts the Google login of Antigravity when Antigravity is not logged in.
// Antigravity has no login command. Its login is the ACP method authenticate, which writes
// a Google link to stderr and waits 300 s for the browser. LogInAntigravity gives false
// when Antigravity is already logged in.
func LogInAntigravity(ctx context.Context, cwd, dataDir, path string) (bool, error) {
	processCtx, stop := context.WithCancel(context.Background())
	defer stop()
	cmd, err := harnessCommand(processCtx, config.Antigravity, cwd, dataDir, path)
	if err != nil {
		return false, err
	}
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return false, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	defer func() {
		stop()
		// The kill ends the agent, so Wait gives an error that tells nothing new.
		_ = cmd.Wait()
	}()
	conn := acp.NewClientSideConnection(client{updates: func(acp.SessionNotification) {}}, stdin, stdout)
	if _, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		return false, describe(err)
	}
	_, err = conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	var requestErr *acp.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != authRequired {
		return false, describe(err)
	}
	if _, err := conn.Authenticate(ctx, acp.AuthenticateRequest{MethodId: "oauth-personal"}); err != nil {
		return false, describe(err)
	}
	return true, nil
}

// describe gives the message and the data of an ACP error.
func describe(err error) error {
	var requestErr *acp.RequestError
	if !errors.As(err, &requestErr) {
		return err
	}
	if requestErr.Data == nil {
		return errors.New(requestErr.Message)
	}
	data, marshalErr := json.Marshal(requestErr.Data)
	if marshalErr != nil {
		return err
	}
	return fmt.Errorf("%s %s", requestErr.Message, data)
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

// Choices are the values of a session option and its current value.
type Choices struct {
	Values  []string
	Current string
}

// Models gives the model choices. They have no values when the session has no model option.
func (s *Session) Models() Choices {
	return s.choices(acp.SessionConfigOptionCategoryModel)
}

// Efforts gives the effort choices. They have no values when the session has no thought_level option.
func (s *Session) Efforts() Choices {
	return s.choices(acp.SessionConfigOptionCategoryThoughtLevel)
}

func (s *Session) choices(category acp.SessionConfigOptionCategory) Choices {
	option := s.option(category)
	if option == nil {
		return Choices{}
	}
	return Choices{Values: values(option), Current: string(option.CurrentValue)}
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
	mode, ok := fullAutoModes[s.harness]
	if !ok {
		return nil
	}
	return s.set(ctx, acp.SessionConfigOptionCategoryMode, "mode", mode)
}

func (s *Session) option(category acp.SessionConfigOptionCategory) *acp.SessionConfigOptionSelect {
	for _, candidate := range s.options {
		if candidate.Select != nil && candidate.Select.Category != nil && *candidate.Select.Category == category {
			return candidate.Select
		}
	}
	return nil
}

func (s *Session) set(ctx context.Context, category acp.SessionConfigOptionCategory, name, value string) error {
	program := Program(s.harness)
	option := s.option(category)
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
