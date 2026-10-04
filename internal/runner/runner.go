// Package runner starts an agent session of a Harness through ACP, and fetches the bare clones of the repositories.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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

// ghURLEnv is the environment variable of an agent process that gives the URL of the token of its gh.
const ghURLEnv = "MOBIUS_GH_TOKEN_URL"

func agentEnv(dataDir string) string {
	return filepath.Join(dataDir, "agent-env")
}

// Prepare writes the agent environment below dataDir. The gh of the agent environment is a link to
// this program, and this program runs GH when its name is gh.
func Prepare(dataDir string) error {
	agentEnv := agentEnv(dataDir)
	for _, dir := range []string{"gh-config", "bin"} {
		if err := os.MkdirAll(filepath.Join(agentEnv, dir), 0o750); err != nil {
			return err
		}
	}
	// An empty helper clears the credential helpers of the system git config.
	if err := os.WriteFile(filepath.Join(agentEnv, "gitconfig"), []byte("[credential]\n\thelper =\n"), 0o600); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	gh := filepath.Join(agentEnv, "bin", "gh")
	if err := os.Remove(gh); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Symlink(self, gh)
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
		if info, err := os.Stat(file); err == nil && executable(info) {
			return file
		}
	}
	return ""
}

func executable(info fs.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// FindGH gives the first gh on path that is not this program, or "" when path has no such gh.
// The gh of the agent environment is this program, so FindGH never gives it.
func FindGH(path string) string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	selfInfo, err := os.Stat(self)
	if err != nil {
		return ""
	}
	for _, dir := range filepath.SplitList(path) {
		file := filepath.Join(dir, "gh")
		if info, err := os.Stat(file); err == nil && executable(info) && !os.SameFile(info, selfInfo) {
			return file
		}
	}
	return ""
}

func command(ctx context.Context, file, cwd, dataDir, path, ghTokenURL string) *exec.Cmd {
	agentEnv := agentEnv(dataDir)
	cmd := exec.CommandContext(ctx, file)
	cmd.Dir = cwd
	cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "GH_TOKEN=") || strings.HasPrefix(entry, "GITHUB_TOKEN=") || strings.HasPrefix(entry, ghURLEnv+"=")
	})
	cmd.Env = append(cmd.Env,
		"GH_CONFIG_DIR="+filepath.Join(agentEnv, "gh-config"),
		"GIT_CONFIG_GLOBAL="+filepath.Join(agentEnv, "gitconfig"),
		"GIT_TERMINAL_PROMPT=0",
		"PATH="+filepath.Join(agentEnv, "bin")+string(filepath.ListSeparator)+path,
	)
	if ghTokenURL != "" {
		cmd.Env = append(cmd.Env, ghURLEnv+"="+ghTokenURL)
	}
	return cmd
}

// Session is one ACP session of a Harness.
type Session struct {
	harness config.Harness
	conn    *acp.Connection
	id      acp.SessionId
	options []acp.SessionConfigOption
	cmd     *exec.Cmd
	stop    context.CancelFunc
}

func harnessCommand(ctx context.Context, harness config.Harness, cwd, dataDir, path, ghTokenURL string) (*exec.Cmd, error) {
	program := Program(harness)
	file := find(program, path)
	if file == "" {
		return nil, fmt.Errorf("%s is not on PATH", program)
	}
	cmd := command(ctx, file, cwd, dataDir, path, ghTokenURL)
	if harness == config.Devin {
		cmd.Args = append(cmd.Args, "acp")
	}
	return cmd, nil
}

// connect starts cmd and gives the ACP connection to its stdin and stdout.
func connect(cmd *exec.Cmd, updates func(json.RawMessage)) (*acp.Connection, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return acp.NewConnection(handler(updates), stdin, stdout), nil
}

// Start starts the agent of harness in cwd with the Mobius MCP server at mcpURL, and opens a session.
// It gives the params of each session/update notification to updates, as the agent sent them.
// The gh of the agent gets its token from ghTokenURL. With an empty ghTokenURL, the gh of the agent refuses to run.
func Start(ctx context.Context, harness config.Harness, cwd, dataDir, path, mcpURL, ghTokenURL string, updates func(json.RawMessage)) (*Session, error) {
	program := Program(harness)
	processCtx, stop := context.WithCancel(context.Background())
	cmd, err := harnessCommand(processCtx, harness, cwd, dataDir, path, ghTokenURL)
	if err != nil {
		stop()
		return nil, err
	}
	conn, err := connect(cmd, updates)
	if err != nil {
		stop()
		return nil, fmt.Errorf("%s: %w", program, err)
	}
	session := &Session{harness: harness, conn: conn, cmd: cmd, stop: stop}
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
	cmd, err := harnessCommand(processCtx, config.Antigravity, cwd, dataDir, path, "")
	if err != nil {
		return false, err
	}
	cmd.Stderr = os.Stderr
	conn, err := connect(cmd, func(json.RawMessage) {})
	if err != nil {
		return false, err
	}
	defer func() {
		stop()
		// The kill ends the agent, so Wait gives an error that tells nothing new.
		_ = cmd.Wait()
	}()
	if _, err := acp.SendRequest[acp.InitializeResponse](conn, ctx, acp.AgentMethodInitialize, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		return false, describe(err)
	}
	_, err = acp.SendRequest[acp.NewSessionResponse](conn, ctx, acp.AgentMethodSessionNew, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	var requestErr *acp.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != authRequired {
		return false, describe(err)
	}
	if _, err := acp.SendRequest[acp.AuthenticateResponse](conn, ctx, acp.AgentMethodAuthenticate, acp.AuthenticateRequest{MethodId: "oauth-personal"}); err != nil {
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
	if _, err := acp.SendRequest[acp.InitializeResponse](s.conn, ctx, acp.AgentMethodInitialize, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		return err
	}
	response, err := acp.SendRequest[acp.NewSessionResponse](s.conn, ctx, acp.AgentMethodSessionNew, acp.NewSessionRequest{
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
	response, err := acp.SendRequest[acp.SetSessionConfigOptionResponse](s.conn, ctx, acp.AgentMethodSessionSetConfigOption, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
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

// Prompt sends text and holds until the turn ends. The updates of the turn go to the updates
// function of Start before Prompt returns.
func (s *Session) Prompt(ctx context.Context, text string) (acp.StopReason, error) {
	response, err := acp.SendRequest[acp.PromptResponse](s.conn, ctx, acp.AgentMethodSessionPrompt, acp.PromptRequest{SessionId: s.id, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
	return response.StopReason, err
}

// Cancel asks the agent to end the turn of the session that runs.
func (s *Session) Cancel(ctx context.Context) error {
	return s.conn.SendNotification(ctx, acp.AgentMethodSessionCancel, acp.CancelNotification{SessionId: s.id})
}

// Close ends the agent process.
func (s *Session) Close() {
	s.stop()
	// The kill ends the agent, so Wait gives an error that tells nothing new.
	_ = s.cmd.Wait()
}

// handler gives the params of each session/update notification to updates, and allows each permission request.
// The session gives the agent no file system and no terminal, so the agent must not call other methods.
func handler(updates func(json.RawMessage)) acp.MethodHandler {
	return func(_ context.Context, method string, params json.RawMessage) (any, *acp.RequestError) {
		switch method {
		case acp.ClientMethodSessionUpdate:
			updates(params)
			return nil, nil
		case acp.ClientMethodSessionRequestPermission:
			var request acp.RequestPermissionRequest
			if err := json.Unmarshal(params, &request); err != nil {
				return nil, acp.NewInvalidParams(err.Error())
			}
			return acp.RequestPermissionResponse{Outcome: permission(request.Options)}, nil
		}
		return nil, acp.NewMethodNotFound(method)
	}
}

func permission(options []acp.PermissionOption) acp.RequestPermissionOutcome {
	for _, option := range options {
		if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: option.OptionId}}
		}
	}
	return acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}
}
