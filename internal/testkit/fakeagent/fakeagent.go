// Package fakeagent is an ACP agent that plays a Harness from a TOML script.
//
// The script has these keys:
//
//	login_required = true   # session/new fails until an authenticate request logs in
//	login_works = true      # authenticate logs in; with no login_works it fails
//
//	[options]               # one select option for each id, in id order; the first value is current
//	model = ["sonnet", "opus"]
//
//	[[prompts]]
//	when = "text"           # answers each prompt that contains text; the prompts with no when answer in order
//	updates = ['{"sessionUpdate": "plan", "entries": []}']  # the JSON of each session/update before the reply
//	reply = ["text"]        # one agent message chunk for each text
//	shell = "gh issue list" # runs in /bin/sh; the reply gets its stdout, its stderr and "exit <code>"
//	call = { tool = "create_workstream", arguments = { title = "{shell}" } }
//	                        # calls the tool of the Mobius MCP server; the reply gets the text of the result,
//	                        # after "error: " for an error result; {shell} becomes the trimmed stdout of shell
//	list_tools = true       # the reply gets the JSON of the tool list of the Mobius MCP server
//	error = { code = -32000, message = "Usage limit", data = "..." }  # the turn ends with this error
//	hang = true             # the turn ends only at session/cancel
//
// The reply has the texts of reply, then the text of call or list_tools, then the text of shell.
package fakeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pelletier/go-toml/v2"
)

type script struct {
	LoginRequired bool                `toml:"login_required"`
	LoginWorks    bool                `toml:"login_works"`
	Options       map[string][]string `toml:"options"`
	Prompts       []prompt            `toml:"prompts"`
}

type prompt struct {
	When      string       `toml:"when"`
	Reply     []string     `toml:"reply"`
	Updates   []string     `toml:"updates"`
	Hang      bool         `toml:"hang"`
	ListTools bool         `toml:"list_tools"`
	Call      *call        `toml:"call"`
	Shell     string       `toml:"shell"`
	Error     *scriptError `toml:"error"`
}

type call struct {
	Tool      string         `toml:"tool"`
	Arguments map[string]any `toml:"arguments"`
}

type scriptError struct {
	Code    int    `toml:"code"`
	Message string `toml:"message"`
	Data    any    `toml:"data"`
}

type option struct {
	Type         string   `json:"type"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	CurrentValue string   `json:"currentValue"`
	Options      []choice `json:"options"`
}

type choice struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

type agent struct {
	path   string
	script script

	mu          sync.Mutex
	conn        *acp.Connection
	options     []option
	promptsDone int
	mcpURL      string
	// cancel closes at a session/cancel. It is nil when no turn runs.
	cancel chan struct{}
}

// Run is the main function of the fake agent program, with the script at path.
// It writes its environment to the file "env" and its working directory to the file "pwd" next to the script.
func Run(path string) error {
	dir := filepath.Dir(path)
	if err := os.WriteFile(filepath.Join(dir, "env"), []byte(strings.Join(os.Environ(), "\n")+"\n"), 0o600); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "pwd"), []byte(cwd+"\n"), 0o600); err != nil {
		return err
	}
	return Serve(path, os.Stdin, os.Stdout)
}

// Serve plays the script at path to the ACP client that writes to r and reads from w. It returns when r ends.
func Serve(path string, r io.Reader, w io.Writer) error {
	text, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	a := &agent{path: path}
	decoder := toml.NewDecoder(bytes.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&a.script); err != nil {
		var unknown *toml.StrictMissingError
		if errors.As(err, &unknown) {
			return fmt.Errorf("%s: %s", path, unknown.String())
		}
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, id := range slices.Sorted(maps.Keys(a.script.Options)) {
		values := a.script.Options[id]
		choices := []choice{}
		for _, value := range values {
			choices = append(choices, choice{Value: value, Name: value})
		}
		a.options = append(a.options, option{Type: "select", ID: id, Name: id, Category: id, CurrentValue: values[0], Options: choices})
	}
	a.mu.Lock()
	a.conn = acp.NewConnection(a.handle, w, r)
	a.mu.Unlock()
	<-a.conn.Done()
	return nil
}

func (a *agent) handle(ctx context.Context, method string, params json.RawMessage) (any, *acp.RequestError) {
	switch method {
	case acp.AgentMethodInitialize:
		var request acp.InitializeRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		return acp.InitializeResponse{ProtocolVersion: request.ProtocolVersion}, nil
	case acp.AgentMethodAuthenticate:
		return a.authenticate()
	case acp.AgentMethodSessionNew:
		return a.newSession(params)
	case acp.AgentMethodSessionSetConfigOption:
		return a.setOption(ctx, params)
	case acp.AgentMethodSessionPrompt:
		return a.prompt(ctx, params)
	case acp.AgentMethodSessionCancel:
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.cancel != nil {
			close(a.cancel)
			a.cancel = nil
		}
		return nil, nil
	}
	return nil, acp.NewMethodNotFound(method)
}

func (a *agent) loggedInPath() string {
	return strings.TrimSuffix(a.path, filepath.Ext(a.path)) + ".logged_in"
}

func (a *agent) authenticate() (any, *acp.RequestError) {
	fmt.Fprintln(os.Stderr, "Open the following link to authenticate: https://example.com/login")
	if !a.script.LoginWorks {
		return nil, acp.NewInternalError("Onboarding failed: Timed out waiting for the authentication flow to complete.")
	}
	if err := os.WriteFile(a.loggedInPath(), nil, 0o600); err != nil {
		return nil, acp.NewInternalError(err.Error())
	}
	return acp.AuthenticateResponse{}, nil
}

// newSession writes the URL of the Mobius MCP server to the file "mcp_url" next to the script.
func (a *agent) newSession(params json.RawMessage) (any, *acp.RequestError) {
	var request struct {
		McpServers []struct {
			Type string `json:"type"`
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, acp.NewInvalidParams(err.Error())
	}
	if _, err := os.Stat(a.loggedInPath()); a.script.LoginRequired && err != nil {
		return nil, acp.NewAuthRequired(nil)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mcpURL = ""
	for _, server := range request.McpServers {
		if server.Type == "http" && server.Name == "mobius" {
			a.mcpURL = server.URL
		}
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(a.path), "mcp_url"), []byte(a.mcpURL), 0o600); err != nil {
		return nil, acp.NewInternalError(err.Error())
	}
	return map[string]any{"sessionId": "fake-session", "configOptions": a.options}, nil
}

func (a *agent) send(ctx context.Context, sessionID string, update any) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	return conn.SendNotification(ctx, acp.ClientMethodSessionUpdate, map[string]any{"sessionId": sessionID, "update": update})
}

func (a *agent) setOption(ctx context.Context, params json.RawMessage) (any, *acp.RequestError) {
	var request struct {
		SessionID string `json:"sessionId"`
		ConfigID  string `json:"configId"`
		Value     string `json:"value"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, acp.NewInvalidParams(err.Error())
	}
	a.mu.Lock()
	index := slices.IndexFunc(a.options, func(option option) bool { return option.ID == request.ConfigID })
	if index < 0 {
		a.mu.Unlock()
		return nil, acp.NewInvalidParams("no option " + request.ConfigID)
	}
	a.options[index].CurrentValue = request.Value
	options := slices.Clone(a.options)
	a.mu.Unlock()
	update := map[string]any{"sessionUpdate": "config_option_update", "configOptions": options}
	if err := a.send(ctx, request.SessionID, update); err != nil {
		return nil, acp.NewInternalError(err.Error())
	}
	return map[string]any{"configOptions": options}, nil
}

// next gives the script prompt for text, the URL of the Mobius MCP server, and the channel that closes at a session/cancel of the turn.
func (a *agent) next(text string) (prompt, string, chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancel = make(chan struct{})
	for _, candidate := range a.script.Prompts {
		if candidate.When != "" && strings.Contains(text, candidate.When) {
			return candidate, a.mcpURL, a.cancel
		}
	}
	var ordered []prompt
	for _, candidate := range a.script.Prompts {
		if candidate.When == "" {
			ordered = append(ordered, candidate)
		}
	}
	a.promptsDone++
	if a.promptsDone > len(ordered) {
		return prompt{}, a.mcpURL, a.cancel
	}
	return ordered[a.promptsDone-1], a.mcpURL, a.cancel
}

func (a *agent) prompt(ctx context.Context, params json.RawMessage) (any, *acp.RequestError) {
	var request struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, acp.NewInvalidParams(err.Error())
	}
	var text strings.Builder
	for _, block := range request.Prompt {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	turn, mcpURL, cancel := a.next(text.String())
	stopReason, err := a.play(ctx, request.SessionID, turn, mcpURL, cancel)
	a.mu.Lock()
	if a.cancel == cancel {
		a.cancel = nil
	}
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return acp.PromptResponse{StopReason: stopReason}, nil
}

func (a *agent) play(ctx context.Context, sessionID string, turn prompt, mcpURL string, cancel chan struct{}) (acp.StopReason, *acp.RequestError) {
	for _, update := range turn.Updates {
		if err := a.send(ctx, sessionID, json.RawMessage(update)); err != nil {
			return "", acp.NewInternalError(err.Error())
		}
	}
	var stdout, shell string
	if turn.Shell != "" {
		stdout, shell = run(turn.Shell)
	}
	replies := slices.Clone(turn.Reply)
	if turn.Call != nil || turn.ListTools {
		text, err := mobiusReply(ctx, mcpURL, turn, stdout)
		if err != nil {
			return "", acp.NewInternalError(err.Error())
		}
		replies = append(replies, text)
	}
	if turn.Shell != "" {
		replies = append(replies, shell)
	}
	for _, reply := range replies {
		if err := a.send(ctx, sessionID, acp.UpdateAgentMessageText(reply)); err != nil {
			return "", acp.NewInternalError(err.Error())
		}
	}
	if turn.Error != nil {
		return "", &acp.RequestError{Code: turn.Error.Code, Message: turn.Error.Message, Data: turn.Error.Data}
	}
	if turn.Hang {
		select {
		case <-cancel:
			return acp.StopReasonCancelled, nil
		case <-ctx.Done():
			return "", acp.NewRequestCancelled(nil)
		}
	}
	return acp.StopReasonEndTurn, nil
}

// run gives the trimmed stdout of command, and the reply text: the stdout, the stderr and "exit <code>".
func run(command string) (string, string) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/bin/sh")
	cmd.Stdin = strings.NewReader(command)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	code := 0
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		return "", err.Error()
	}
	return strings.TrimSpace(stdout.String()), fmt.Sprintf("%s%sexit %d", stdout.String(), stderr.String(), code)
}

func mobiusReply(ctx context.Context, mcpURL string, turn prompt, stdout string) (string, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: "fake-agent", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: mcpURL}, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = session.Close() }()
	if turn.Call == nil {
		result, err := session.ListTools(ctx, nil)
		if err != nil {
			return "", err
		}
		text, err := json.Marshal(result)
		return string(text), err
	}
	arguments := map[string]any{}
	for name, value := range turn.Call.Arguments {
		if text, ok := value.(string); ok {
			value = strings.ReplaceAll(text, "{shell}", stdout)
		}
		arguments[name] = value
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: turn.Call.Tool, Arguments: arguments})
	if err != nil {
		return "", err
	}
	if len(result.Content) == 0 {
		return "", errors.New(turn.Call.Tool + " gave no content")
	}
	content, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		return "", errors.New(turn.Call.Tool + " gave no text")
	}
	if result.IsError {
		return "error: " + content.Text, nil
	}
	return content.Text, nil
}
