// Package fakeagent is an ACP agent that plays a Harness from a TOML script.
//
// The script has these keys:
//
//	login_required = true   # session/new fails until an authenticate request logs in
//	login_works = true      # authenticate logs in; with no login_works it fails
//	image_support = true    # the agent declares that it reads the images of a prompt
//	skip_tools_list = 1     # the first 1 agent processes of the script do not list the tools after session/new;
//	                        # the file "starts" next to the script counts the processes
//	hang_start = 1          # the first 1 agent processes of the script never answer initialize;
//	                        # the file "hung_starts" next to the script counts the processes
//	session_new_hold = true # session/new appends the program name to the file "session_new_started", and then waits
//	                        # for the file "session_new_release"
//	steering_outcome = "injected"  # initialize tells _meta.steering.supported, and _session/steering answers with this
//	                        # outcome. With the outcome injected, the text takes a prompt by the rules of a prompt, and
//	                        # the agent plays it in the running turn. The turn then ends with end_turn, unless the
//	                        # played prompt has hang. With no running turn, the outcome is promptRequired
//	steering_hang = true    # initialize tells _meta.steering.supported, and _session/steering gets no response
//	steering_hold = true    # with steering_outcome, _session/steering appends the program name to the file "steerings",
//	                        # and then waits for the file "steering_release" before it answers
//
// Each session/cancel appends the program name to the file "cancels" next to the script. A session/cancel ends only a
// turn whose session/prompt the agent read before that session/cancel, as an agent that handles its input in order does.
//
//	[options]               # one select option for each id, in id order; the first value is current
//	model = ["sonnet", "opus"]
//
//	[efforts]               # the thought_level values for each model; a set of the model option replaces the values
//	opus = ["low", "high"]  # of the thought_level option, and keeps its current value when the new list has it
//
//	[[prompts]]
//	when = "text"           # answers each prompt that contains text; the prompts with no when answer in order
//	updates = ['{"sessionUpdate": "plan", "entries": []}']  # the JSON of each session/update before the reply
//	reply = ["text"]        # one agent message chunk for each text
//	shell = "gh issue list" # runs in /bin/sh; the reply gets its stdout, its stderr and "exit <code>"
//	call = { tool = "create_workstream", arguments = { title = "{shell}" } }
//	                        # calls the tool of the Mobius MCP server; the reply gets the text of the result,
//	                        # after "error: " for an error result; {shell} becomes the trimmed stdout of shell, and an
//	                        # argument that is only {shell} becomes a number when the stdout is a number
//	list_tools = true       # the reply gets the JSON of the tool list of the Mobius MCP server
//	usage = '{"inputTokens": 10, "outputTokens": 2}'  # the usage field of the response
//	meta = '{"quota": {}}'  # the _meta field of the response
//	error = { code = -32000, message = "Usage limit", data = "..." }  # the turn ends with this error
//	hang = true             # the turn ends only at session/cancel
//	stop_reason = "max_tokens"  # the stop reason of the response, with no hang and no cancel; the default is end_turn
//	busy = "300ms"          # before the reply, the agent sends a tool_call_update every 10 ms for this long
//	linger = "1s"           # after the reply, the turn waits this long or until session/cancel
//	later = { after = "100ms", updates = ['{"sessionUpdate": "plan", "entries": []}'] }
//	                        # after the response, the agent waits for after and then sends the updates, as Claude Code
//	                        # does for a turn that it starts alone
//	later = { wait_for = "/tmp/go", updates = ['{"sessionUpdate": "plan", "entries": []}'] }
//	                        # with wait_for, the agent waits for the file instead of the time after
//	later = { after = "100ms", call = { tool = "cannot_do", arguments = { reason = "No." } } }
//	                        # with call, the agent calls the tool of the Mobius MCP server before it sends the updates
//	later = { updates = ['{"sessionUpdate": "plan", "entries": []}'], absorb = true }
//	                        # with absorb, the agent sends the updates when the next session/prompt arrives, and that
//	                        # prompt gets no response before session/cancel, which ends it as cancelled, as Claude
//	                        # Code does when it takes a prompt into a turn that it started alone
//
// The prompts with no matching when answer in order. Two prompts are the exception. The prompt that asks a Reviewer for
// its deployment needs calls report_deployment_needs with an empty list. The prompt of the access check of Mobius,
// "Reply with the word OK.", ends the turn with no reply, no tool call and no error. Mobius sends it at its start and at
// a save of a Role, to each Harness with an empty MCP server list. With no matching when, neither prompt takes an
// ordered prompt. A when that is a part of the access check prompt, for example "OK", answers it too, so use a longer
// text. A prompt with a when text that the access check prompt contains and an error makes the check fail.
//
// The agent writes the files env, pwd, mcp_url, session_meta and mcp_servers only for a session with the Mobius MCP
// server, so an access check leaves them as they are. session_meta holds the raw _meta of session/new, and is empty with
// no _meta. mcp_servers holds the raw mcpServers of session/new. The agent also writes them to the file
// mcp_servers.<key>, where key is the last path segment of the URL of the Mobius MCP server, so that each session keeps
// its own file.
//
// The reply has the texts of reply, then one text "image <MIME type> <base64 data>" for each image block of the
// prompt, then the text of call or list_tools, then the text of shell.
//
// After session/new, the agent lists the tools of the Mobius MCP server in the background, as Claude Code does.
package fakeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/acp-go-sdk"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pelletier/go-toml/v2"
)

// deploymentNeedsRequest is the start of the prompt that asks a Reviewer for its deployment needs. A turn with this
// prompt and no matching when calls report_deployment_needs with an empty list.
const deploymentNeedsRequest = "Call `report_deployment_needs` one time"

// accessCheckRequest is the prompt of the access check of Mobius (runner.AccessPrompt).
const accessCheckRequest = "Reply with the word OK."

// busyInterval is the time between two updates of a busy turn.
const busyInterval = 10 * time.Millisecond

// steeringMethod is the request of claude-agent-acp that puts a message into a running turn.
const steeringMethod = "_session/steering"

type script struct {
	LoginRequired   bool                `toml:"login_required"`
	LoginWorks      bool                `toml:"login_works"`
	ImageSupport    bool                `toml:"image_support"`
	SkipToolsList   int                 `toml:"skip_tools_list"`
	HangStart       int                 `toml:"hang_start"`
	SessionNewHold  bool                `toml:"session_new_hold"`
	SteeringOutcome string              `toml:"steering_outcome"`
	SteeringHang    bool                `toml:"steering_hang"`
	SteeringHold    bool                `toml:"steering_hold"`
	Options         map[string][]string `toml:"options"`
	Efforts         map[string][]string `toml:"efforts"`
	Prompts         []prompt            `toml:"prompts"`
}

type prompt struct {
	When       string       `toml:"when"`
	Reply      []string     `toml:"reply"`
	Updates    []string     `toml:"updates"`
	Hang       bool         `toml:"hang"`
	StopReason string       `toml:"stop_reason"`
	Busy       string       `toml:"busy"`
	Linger     string       `toml:"linger"`
	ListTools  bool         `toml:"list_tools"`
	Call       *call        `toml:"call"`
	Shell      string       `toml:"shell"`
	Error      *scriptError `toml:"error"`
	Usage      string       `toml:"usage"`
	Meta       string       `toml:"meta"`
	Later      *later       `toml:"later"`
}

type promptResult struct {
	StopReason acp.StopReason  `json:"stopReason"`
	Usage      json.RawMessage `json:"usage,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type later struct {
	After   string   `toml:"after"`
	WaitFor string   `toml:"wait_for"`
	Updates []string `toml:"updates"`
	Absorb  bool     `toml:"absorb"`
	Call    *call    `toml:"call"`
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
	// record writes the files env and pwd. It is nil when the agent does not write them.
	record func() error

	mu          sync.Mutex
	conn        *acp.Connection
	options     []option
	promptsDone int
	mcpURL      string
	// order records the position of each session/cancel among the session/prompt lines of the input.
	order *inputOrder
	// promptsStarted counts the session/prompt requests that the agent started to handle.
	promptsStarted int
	// cancels has the channel of each running turn, by the number of its session/prompt. The channel closes at a
	// session/cancel that the agent read after that session/prompt.
	cancels map[int]chan struct{}
	// cancelledUpTo is the highest number of a session/prompt that a session/cancel ended or will end.
	cancelledUpTo int
	// steered closes when a steering request with the outcome injected finished its prompt. It is nil when no turn runs.
	steered chan struct{}
	// absorbed is the later with absorb that waits for the next session/prompt, or nil.
	absorbed *later
}

// Run is the main function of the fake agent program, with the script at path.
// At the first session with the Mobius MCP server, it writes its environment to the file "env" and its working
// directory to the file "pwd" next to the script.
func Run(path string) error {
	dir := filepath.Dir(path)
	record := func() error {
		if err := os.WriteFile(filepath.Join(dir, "env"), []byte(strings.Join(os.Environ(), "\n")+"\n"), 0o600); err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "pwd"), []byte(cwd+"\n"), 0o600)
	}
	return serve(path, os.Stdin, os.Stdout, record)
}

// Serve plays the script at path to the ACP client that writes to r and reads from w. It returns when r ends.
func Serve(path string, r io.Reader, w io.Writer) error {
	return serve(path, r, w, nil)
}

func serve(path string, r io.Reader, w io.Writer, record func() error) error {
	text, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	a := &agent{path: path, record: record}
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
	a.order = &inputOrder{r: r}
	a.cancels = map[int]chan struct{}{}
	a.mu.Lock()
	a.conn = acp.NewConnection(a.handle, w, a.order)
	a.mu.Unlock()
	<-a.conn.Done()
	return nil
}

func (a *agent) handle(ctx context.Context, method string, params json.RawMessage) (any, *acp.RequestError) {
	switch method {
	case acp.AgentMethodInitialize:
		hang, err := a.within("hung_starts", a.script.HangStart)
		if err != nil {
			return nil, acp.NewInternalError(err.Error())
		}
		if hang {
			<-ctx.Done()
			return nil, acp.NewInternalError("the start ended")
		}
		var request acp.InitializeRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		response := acp.InitializeResponse{
			ProtocolVersion:   request.ProtocolVersion,
			AgentCapabilities: acp.AgentCapabilities{PromptCapabilities: acp.PromptCapabilities{Image: a.script.ImageSupport}},
		}
		if a.script.SteeringOutcome != "" || a.script.SteeringHang {
			response.Meta = map[string]any{"steering": map[string]any{"supported": true}}
		}
		return response, nil
	case acp.AgentMethodAuthenticate:
		return a.authenticate()
	case acp.AgentMethodSessionNew:
		return a.newSession(ctx, params)
	case acp.AgentMethodSessionSetConfigOption:
		return a.setOption(ctx, params)
	case acp.AgentMethodSessionPrompt:
		return a.prompt(ctx, params)
	case steeringMethod:
		return a.steer(ctx, params)
	case acp.AgentMethodSessionCancel:
		if err := a.appendName("cancels"); err != nil {
			return nil, acp.NewInternalError(err.Error())
		}
		promptsRead := a.order.takeCancel()
		a.mu.Lock()
		defer a.mu.Unlock()
		a.cancelledUpTo = max(a.cancelledUpTo, promptsRead)
		for number, cancel := range a.cancels {
			if number <= promptsRead {
				close(cancel)
				delete(a.cancels, number)
			}
		}
		return nil, nil
	}
	return nil, acp.NewMethodNotFound(method)
}

// inputOrder reads the input of the agent and keeps the order of its session/prompt and session/cancel lines. The
// connection runs each request in a new goroutine and each notification in a queue, so a handler cannot see this order.
type inputOrder struct {
	r io.Reader

	mu      sync.Mutex
	partial []byte
	prompts int
	cancels []int
}

func (o *inputOrder) Read(p []byte) (int, error) {
	n, err := o.r.Read(p)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.partial = append(o.partial, p[:n]...)
	for {
		end := bytes.IndexByte(o.partial, '\n')
		if end < 0 {
			return n, err
		}
		var line struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(o.partial[:end], &line) == nil {
			switch line.Method {
			case acp.AgentMethodSessionPrompt:
				o.prompts++
			case acp.AgentMethodSessionCancel:
				o.cancels = append(o.cancels, o.prompts)
			}
		}
		o.partial = o.partial[end+1:]
	}
}

// takeCancel gives the number of session/prompt lines that the agent read before the oldest session/cancel that no
// handler took yet. The connection handles the notifications in the order of the input.
func (o *inputOrder) takeCancel() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	promptsRead := o.cancels[0]
	o.cancels = o.cancels[1:]
	return promptsRead
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

// newSession writes the URL of the Mobius MCP server to the file "mcp_url", the _meta of the request to the file
// "session_meta" and the mcpServers of the request to the file "mcp_servers" next to the script.
func (a *agent) newSession(ctx context.Context, params json.RawMessage) (any, *acp.RequestError) {
	var request struct {
		McpServers json.RawMessage `json:"mcpServers"`
		Meta       json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, acp.NewInvalidParams(err.Error())
	}
	var servers []struct {
		Type string `json:"type"`
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(request.McpServers, &servers); err != nil {
		return nil, acp.NewInvalidParams(err.Error())
	}
	if a.script.SessionNewHold {
		if err := a.holdSessionNew(ctx); err != nil {
			return nil, acp.NewInternalError(err.Error())
		}
	}
	if _, err := os.Stat(a.loggedInPath()); a.script.LoginRequired && err != nil {
		return nil, acp.NewAuthRequired(nil)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mcpURL = ""
	for _, server := range servers {
		if server.Type == "http" && server.Name == "mobius" {
			a.mcpURL = server.URL
		}
	}
	if a.mcpURL != "" {
		if err := os.WriteFile(filepath.Join(filepath.Dir(a.path), "mcp_url"), []byte(a.mcpURL), 0o600); err != nil {
			return nil, acp.NewInternalError(err.Error())
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(a.path), "session_meta"), request.Meta, 0o600); err != nil {
			return nil, acp.NewInternalError(err.Error())
		}
		for _, name := range []string{"mcp_servers", "mcp_servers." + path.Base(a.mcpURL)} {
			if err := os.WriteFile(filepath.Join(filepath.Dir(a.path), name), request.McpServers, 0o600); err != nil {
				return nil, acp.NewInternalError(err.Error())
			}
		}
		if a.record != nil {
			if err := a.record(); err != nil {
				return nil, acp.NewInternalError(err.Error())
			}
		}
	}
	skip, err := a.within("starts", a.script.SkipToolsList)
	if err != nil {
		return nil, acp.NewInternalError(err.Error())
	}
	if a.mcpURL != "" && !skip {
		go listTools(ctx, a.mcpURL)
	}
	return map[string]any{"sessionId": "fake-session-" + strconv.Itoa(os.Getpid()), "configOptions": a.options}, nil
}

// appendName appends the program name to the file name next to the script.
func (a *agent) appendName(name string) error {
	file, err := os.OpenFile(filepath.Clean(filepath.Join(filepath.Dir(a.path), name)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(file, strings.TrimSuffix(filepath.Base(a.path), filepath.Ext(a.path)))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// holdSessionNew appends the program name to session_new_started, and waits for session_new_release.
func (a *agent) holdSessionNew(ctx context.Context) error {
	if err := a.appendName("session_new_started"); err != nil {
		return err
	}
	return a.waitForFile(ctx, "session_new_release")
}

// waitForFile waits until the file name next to the script exists.
func (a *agent) waitForFile(ctx context.Context, name string) error {
	path := filepath.Join(filepath.Dir(a.path), name)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// within counts this agent process in the file name next to the script, and tells if the process is one of the
// first limit processes. A lock makes the count correct when agent processes start at the same time.
func (a *agent) within(name string, limit int) (bool, error) {
	if limit == 0 {
		return false, nil
	}
	dir := filepath.Dir(a.path)
	lock, err := os.OpenFile(filepath.Clean(filepath.Join(dir, name+".lock")), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return false, err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	path := filepath.Join(dir, name)
	text, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	starts, _ := strconv.Atoi(string(text))
	starts++
	tmp := path + ".count"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(starts)), 0o600); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, err
	}
	return starts <= limit, nil
}

// listTools lists the tools of the MCP server at url. As in Claude Code, a failed list only leaves the session with no tools.
func listTools(ctx context.Context, url string) {
	ctx = context.WithoutCancel(ctx)
	client := sdk.NewClient(&sdk.Implementation{Name: "fake-agent", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		return
	}
	defer func() { _ = session.Close() }()
	_, _ = session.ListTools(ctx, nil)
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
	if efforts, ok := a.script.Efforts[request.Value]; ok && request.ConfigID == "model" {
		a.setEfforts(efforts)
	}
	options := slices.Clone(a.options)
	a.mu.Unlock()
	update := map[string]any{"sessionUpdate": "config_option_update", "configOptions": options}
	if err := a.send(ctx, request.SessionID, update); err != nil {
		return nil, acp.NewInternalError(err.Error())
	}
	return map[string]any{"configOptions": options}, nil
}

// setEfforts replaces the values of the thought_level option. The caller holds a.mu.
func (a *agent) setEfforts(efforts []string) {
	index := slices.IndexFunc(a.options, func(option option) bool { return option.ID == "thought_level" })
	if index < 0 {
		return
	}
	choices := []choice{}
	for _, value := range efforts {
		choices = append(choices, choice{Value: value, Name: value})
	}
	a.options[index].Options = choices
	if !slices.Contains(efforts, a.options[index].CurrentValue) {
		a.options[index].CurrentValue = efforts[0]
	}
}

// next gives the script prompt for text, the URL of the Mobius MCP server, the channel that closes at a session/cancel
// of the turn, and the channel that closes when a steering request finished its prompt. Number is the number of the
// session/prompt of the turn.
func (a *agent) next(text string, number int) (prompt, string, chan struct{}, chan struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cancel := a.newCancel(number)
	a.steered = make(chan struct{})
	return a.choose(text), a.mcpURL, cancel, a.steered
}

// choose gives the script prompt for text. The caller holds a.mu.
func (a *agent) choose(text string) prompt {
	for _, candidate := range a.script.Prompts {
		if candidate.When != "" && strings.Contains(text, candidate.When) {
			return candidate
		}
	}
	if text == accessCheckRequest {
		return prompt{}
	}
	if strings.Contains(text, deploymentNeedsRequest) {
		return prompt{Call: &call{Tool: "report_deployment_needs", Arguments: map[string]any{"needs": []any{}}}}
	}
	var ordered []prompt
	for _, candidate := range a.script.Prompts {
		if candidate.When == "" {
			ordered = append(ordered, candidate)
		}
	}
	a.promptsDone++
	if a.promptsDone > len(ordered) {
		return prompt{}
	}
	return ordered[a.promptsDone-1]
}

// steer answers a steering request. With the outcome injected and a running turn, it plays the prompt of the text in
// the background, and the running turn ends when that prompt ends.
func (a *agent) steer(ctx context.Context, params json.RawMessage) (any, *acp.RequestError) {
	if a.script.SteeringHang {
		<-ctx.Done()
		return nil, acp.NewRequestCancelled(nil)
	}
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
	if a.script.SteeringHold {
		if err := a.appendName("steerings"); err != nil {
			return nil, acp.NewInternalError(err.Error())
		}
		if err := a.waitForFile(ctx, "steering_release"); err != nil {
			return nil, acp.NewRequestCancelled(nil)
		}
	}
	if a.script.SteeringOutcome != "injected" {
		return map[string]any{"outcome": a.script.SteeringOutcome}, nil
	}
	var text strings.Builder
	for _, block := range request.Prompt {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	a.mu.Lock()
	steered := a.steered
	if steered == nil {
		a.mu.Unlock()
		return map[string]any{"outcome": "promptRequired", "reason": "noRunningTurn"}, nil
	}
	turn, mcpURL, cancel := a.choose(text.String()), a.mcpURL, a.cancels[a.promptsStarted]
	a.mu.Unlock()
	played := context.WithoutCancel(ctx)
	go func() {
		_, _ = a.play(played, request.SessionID, turn, mcpURL, cancel, nil)
		if turn.Hang {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.steered == steered {
			close(steered)
			a.steered = nil
		}
	}()
	return map[string]any{"outcome": "injected"}, nil
}

func (a *agent) prompt(ctx context.Context, params json.RawMessage) (any, *acp.RequestError) {
	var request struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MIMEType string `json:"mimeType"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, acp.NewInvalidParams(err.Error())
	}
	var text strings.Builder
	var images []string
	for _, block := range request.Prompt {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "image":
			images = append(images, "image "+block.MIMEType+" "+block.Data)
		}
	}
	number := a.startPrompt()
	if cancel, absorbed := a.absorb(number); absorbed != nil {
		for _, update := range absorbed.Updates {
			if err := a.send(ctx, request.SessionID, json.RawMessage(update)); err != nil {
				return nil, acp.NewInternalError(err.Error())
			}
		}
		defer a.endTurn(number)
		select {
		case <-cancel:
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		case <-ctx.Done():
			return nil, acp.NewRequestCancelled(nil)
		}
	}
	turn, mcpURL, cancel, steered := a.next(text.String(), number)
	turn.Reply = append(slices.Clone(turn.Reply), images...)
	stopReason, err := a.play(ctx, request.SessionID, turn, mcpURL, cancel, steered)
	a.mu.Lock()
	delete(a.cancels, number)
	if a.steered == steered {
		a.steered = nil
	}
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if turn.Later != nil {
		if err := a.startLater(request.SessionID, mcpURL, *turn.Later); err != nil {
			return nil, acp.NewInternalError(err.Error())
		}
	}
	result := promptResult{StopReason: stopReason}
	if turn.Usage != "" {
		result.Usage = json.RawMessage(turn.Usage)
	}
	if turn.Meta != "" {
		result.Meta = json.RawMessage(turn.Meta)
	}
	return result, nil
}

// newCancel gives the cancel channel of the session/prompt number. The channel is closed when a session/cancel that came
// after that session/prompt in the input was handled before the turn started. The caller holds a.mu.
func (a *agent) newCancel(number int) chan struct{} {
	cancel := make(chan struct{})
	if number <= a.cancelledUpTo {
		close(cancel)
		return cancel
	}
	a.cancels[number] = cancel
	return cancel
}

// endTurn removes the cancel channel of the session/prompt number.
func (a *agent) endTurn(number int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.cancels, number)
}

// startPrompt gives the number of the session/prompt that the agent starts to handle.
func (a *agent) startPrompt() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.promptsStarted++
	return a.promptsStarted
}

// absorb gives the channel that closes at a session/cancel, and the later with absorb that waited for this prompt, or nil.
// Number is the number of the session/prompt.
func (a *agent) absorb(number int) (chan struct{}, *later) {
	a.mu.Lock()
	defer a.mu.Unlock()
	absorbed := a.absorbed
	if absorbed == nil {
		return nil, nil
	}
	a.absorbed = nil
	return a.newCancel(number), absorbed
}

// startLater sends the updates of l in the background after the response of the prompt, or holds a later with
// absorb for the next prompt.
func (a *agent) startLater(sessionID, mcpURL string, l later) error {
	if l.Absorb {
		a.mu.Lock()
		a.absorbed = &l
		a.mu.Unlock()
		return nil
	}
	var after time.Duration
	if l.WaitFor == "" {
		var err error
		if after, err = time.ParseDuration(l.After); err != nil {
			return err
		}
	}
	go func() {
		time.Sleep(after)
		for l.WaitFor != "" {
			if _, err := os.Stat(l.WaitFor); err == nil {
				break
			}
			time.Sleep(busyInterval)
		}
		if l.Call != nil {
			if _, err := mobiusReply(context.Background(), mcpURL, prompt{Call: l.Call}, ""); err != nil {
				return
			}
		}
		for _, update := range l.Updates {
			if err := a.send(context.Background(), sessionID, json.RawMessage(update)); err != nil {
				break
			}
		}
	}()
	return nil
}

func (a *agent) play(ctx context.Context, sessionID string, turn prompt, mcpURL string, cancel, steered chan struct{}) (acp.StopReason, *acp.RequestError) {
	for _, update := range turn.Updates {
		if err := a.send(ctx, sessionID, json.RawMessage(update)); err != nil {
			return "", acp.NewInternalError(err.Error())
		}
	}
	if turn.Busy != "" {
		busy, err := time.ParseDuration(turn.Busy)
		if err != nil {
			return "", acp.NewInternalError(err.Error())
		}
		for end := time.Now().Add(busy); time.Now().Before(end); time.Sleep(busyInterval) {
			if err := a.send(ctx, sessionID, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "busy"}); err != nil {
				return "", acp.NewInternalError(err.Error())
			}
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
	if turn.Linger != "" {
		linger, err := time.ParseDuration(turn.Linger)
		if err != nil {
			return "", acp.NewInternalError(err.Error())
		}
		select {
		case <-time.After(linger):
		case <-steered:
		case <-cancel:
			return acp.StopReasonCancelled, nil
		case <-ctx.Done():
			return "", acp.NewRequestCancelled(nil)
		}
	}
	if turn.Hang {
		select {
		case <-steered:
		case <-cancel:
			return acp.StopReasonCancelled, nil
		case <-ctx.Done():
			return "", acp.NewRequestCancelled(nil)
		}
	}
	if turn.StopReason != "" {
		return acp.StopReason(turn.StopReason), nil
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
			if number, err := strconv.ParseInt(stdout, 10, 64); err == nil && text == "{shell}" {
				value = number
			}
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
