package fakeagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type client struct {
	dir  string
	conn *acp.Connection

	mu      sync.Mutex
	updates []map[string]any
}

// start serves script to a new client in the test process.
func start(t *testing.T, script string) *client {
	t.Helper()
	c := &client{dir: t.TempDir()}
	path := filepath.Join(c.dir, "claude-agent-acp.toml")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	go func() { _ = Serve(path, agentIn, agentOut) }()
	c.conn = acp.NewConnection(c.handle, clientOut, clientIn)
	t.Cleanup(func() { _ = clientOut.Close() })
	return c
}

func (c *client) handle(_ context.Context, method string, params json.RawMessage) (any, *acp.RequestError) {
	if method != acp.ClientMethodSessionUpdate {
		return nil, acp.NewMethodNotFound(method)
	}
	var notification struct {
		Update map[string]any `json:"update"`
	}
	if err := json.Unmarshal(params, &notification); err != nil {
		return nil, acp.NewInvalidParams(err.Error())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, notification.Update)
	return nil, nil
}

// takeUpdates gives the updates since the last call.
func (c *client) takeUpdates() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	updates := c.updates
	c.updates = nil
	return updates
}

func (c *client) call(t *testing.T, method string, params, result any) error {
	t.Helper()
	raw, err := acp.SendRequest[json.RawMessage](c.conn, context.Background(), method, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, result); err != nil {
		t.Fatal(err)
	}
	return nil
}

func (c *client) newSession(t *testing.T, mcpURL string) error {
	t.Helper()
	var response map[string]any
	return c.call(t, acp.AgentMethodSessionNew, map[string]any{
		"cwd":        c.dir,
		"mcpServers": []any{map[string]any{"type": "http", "name": "mobius", "url": mcpURL, "headers": []any{}}},
	}, &response)
}

func (c *client) send(text string) (string, error) {
	response, err := acp.SendRequest[acp.PromptResponse](c.conn, context.Background(), acp.AgentMethodSessionPrompt, map[string]any{
		"sessionId": "fake-session",
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	})
	return string(response.StopReason), err
}

// prompt gives the text of the agent message chunks of the turn and the stop reason.
func (c *client) prompt(t *testing.T, text string) (string, string) {
	t.Helper()
	stopReason, err := c.send(text)
	if err != nil {
		t.Fatal(err)
	}
	return messages(c.takeUpdates()), stopReason
}

func messages(updates []map[string]any) string {
	var texts []string
	for _, update := range updates {
		if update["sessionUpdate"] == "agent_message_chunk" {
			texts = append(texts, update["content"].(map[string]any)["text"].(string))
		}
	}
	return strings.Join(texts, "|")
}

func TestTheOptionsComeInIDOrderAndASetChangesTheCurrentValue(t *testing.T) {
	c := start(t, `
[options]
model = ["sonnet", "opus"]
mode = ["default", "bypassPermissions"]
`)
	var session struct {
		SessionID     string           `json:"sessionId"`
		ConfigOptions []map[string]any `json:"configOptions"`
	}
	if err := c.call(t, acp.AgentMethodSessionNew, map[string]any{"cwd": c.dir, "mcpServers": []any{}}, &session); err != nil {
		t.Fatal(err)
	}
	if session.SessionID != "fake-session" || len(session.ConfigOptions) != 2 ||
		session.ConfigOptions[0]["id"] != "mode" || session.ConfigOptions[0]["category"] != "mode" ||
		session.ConfigOptions[1]["id"] != "model" || session.ConfigOptions[1]["currentValue"] != "sonnet" {
		t.Fatalf("session = %+v", session)
	}

	var set struct {
		ConfigOptions []map[string]any `json:"configOptions"`
	}
	if err := c.call(t, acp.AgentMethodSessionSetConfigOption, map[string]any{"sessionId": "fake-session", "configId": "model", "value": "opus"}, &set); err != nil {
		t.Fatal(err)
	}

	if set.ConfigOptions[1]["currentValue"] != "opus" {
		t.Errorf("options = %+v", set.ConfigOptions)
	}
	updates := c.takeUpdates()
	if len(updates) != 1 || updates[0]["sessionUpdate"] != "config_option_update" {
		t.Errorf("updates = %+v", updates)
	}
	err := c.call(t, acp.AgentMethodSessionSetConfigOption, map[string]any{"sessionId": "fake-session", "configId": "effort", "value": "low"}, &set)
	var refused *acp.RequestError
	if !errors.As(err, &refused) || refused.Code != -32602 {
		t.Errorf("error = %v", err)
	}
}

func TestABusyPromptSendsToolCallUpdatesBeforeItsReply(t *testing.T) {
	c := start(t, `
[[prompts]]
busy = "100ms"
reply = ["done"]
`)
	if err := c.newSession(t, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := c.send("go"); err != nil {
		t.Fatal(err)
	}

	updates := c.takeUpdates()
	if len(updates) < 3 || updates[0]["sessionUpdate"] != "tool_call_update" || updates[len(updates)-1]["sessionUpdate"] != "agent_message_chunk" {
		t.Errorf("updates = %+v", updates)
	}
}

func TestAPromptWithWhenAnswersByTextAndTheOtherPromptsAnswerInOrder(t *testing.T) {
	c := start(t, `
[[prompts]]
reply = ["first"]

[[prompts]]
when = "plan"
reply = ["a plan", "in two chunks"]

[[prompts]]
reply = ["second"]
`)

	var got []string
	for _, text := range []string{"Hello.", "Make a plan.", "Continue.", "And now?"} {
		reply, stopReason := c.prompt(t, text)
		got = append(got, reply+" "+stopReason)
	}

	want := []string{"first end_turn", "a plan|in two chunks end_turn", "second end_turn", " end_turn"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("replies = %q", got)
	}
}

func TestATurnSendsTheUpdatesThenTheReplyThenTheShellOutput(t *testing.T) {
	c := start(t, `
[[prompts]]
updates = ['{"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "Hmm."}}']
reply = ["Done."]
shell = "echo out; echo err >&2; exit 3"
`)

	if _, err := c.send("Run it."); err != nil {
		t.Fatal(err)
	}

	updates := c.takeUpdates()
	if len(updates) != 3 || updates[0]["sessionUpdate"] != "agent_thought_chunk" {
		t.Fatalf("updates = %+v", updates)
	}
	if reply := messages(updates[1:]); reply != "Done.|out\nerr\nexit 3" {
		t.Errorf("reply = %q", reply)
	}
}

type echoInput struct {
	Text string `json:"text"`
	Fail bool   `json:"fail,omitempty"`
}

// mcpServer serves an MCP server with the tool echo, and gives its URL.
func mcpServer(t *testing.T) string {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "0.1.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "echo"}, func(_ context.Context, _ *sdk.CallToolRequest, input echoInput) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: input.Text}}, IsError: input.Fail}, nil, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	listener := httptest.NewServer(handler)
	t.Cleanup(listener.Close)
	return listener.URL
}

func TestACallGivesTheToolResultWithTheShellOutputInItsArguments(t *testing.T) {
	c := start(t, `
[[prompts]]
shell = "echo seeds"
call = { tool = "echo", arguments = { text = "Sell {shell}." } }

[[prompts]]
call = { tool = "echo", arguments = { text = "No such issue.", fail = true } }

[[prompts]]
list_tools = true
`)
	url := mcpServer(t)
	if err := c.newSession(t, url); err != nil {
		t.Fatal(err)
	}

	sell, _ := c.prompt(t, "Sell.")
	failed, _ := c.prompt(t, "Fail.")
	list, _ := c.prompt(t, "List.")

	if sell != "Sell seeds.|seeds\nexit 0" {
		t.Errorf("reply = %q", sell)
	}
	if failed != "error: No such issue." {
		t.Errorf("reply = %q", failed)
	}
	var tools sdk.ListToolsResult
	if err := json.Unmarshal([]byte(list), &tools); err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Errorf("reply = %q", list)
	}
	written, err := os.ReadFile(filepath.Join(c.dir, "mcp_url"))
	if err != nil || string(written) != url {
		t.Errorf("mcp_url = %q, %v", written, err)
	}
}

func TestTheAgentListsTheToolsAfterTheNewSession(t *testing.T) {
	c := start(t, "")
	listed := make(chan struct{}, 1)
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "0.1.0"}, nil)
	server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if method == "tools/list" {
				listed <- struct{}{}
			}
			return next(ctx, method, req)
		}
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	listener := httptest.NewServer(handler)
	t.Cleanup(listener.Close)

	if err := c.newSession(t, listener.URL); err != nil {
		t.Fatal(err)
	}

	select {
	case <-listed:
	case <-time.After(time.Minute):
		t.Error("the agent sent no tools/list")
	}
}

func TestAnErrorEndsTheTurnWithTheScriptError(t *testing.T) {
	c := start(t, `
[[prompts]]
reply = ["Partial work."]
error = { code = -32000, message = "Usage limit", data = { resets = "18:00" } }
`)

	_, err := c.send("Work.")

	var scripted *acp.RequestError
	if !errors.As(err, &scripted) || scripted.Error() != `{"code":-32000,"message":"Usage limit","data":{"resets":"18:00"}}` {
		t.Errorf("error = %v", err)
	}
	if reply := messages(c.takeUpdates()); reply != "Partial work." {
		t.Errorf("reply = %q", reply)
	}
}

func TestAHangEndsAtTheCancel(t *testing.T) {
	c := start(t, `
[[prompts]]
reply = ["Waiting."]
hang = true
`)
	done := make(chan string)
	go func() {
		stopReason, _ := c.send("Wait.")
		done <- stopReason
	}()
	deadline := time.Now().Add(time.Minute)
	for len(c.updatesNow()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if err := c.conn.SendNotification(context.Background(), acp.AgentMethodSessionCancel, map[string]any{"sessionId": "fake-session"}); err != nil {
		t.Fatal(err)
	}

	if stopReason := <-done; stopReason != "cancelled" {
		t.Errorf("stop reason = %s", stopReason)
	}
}

func (c *client) updatesNow() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updates
}

func TestALoginRequiredRefusesTheSessionUntilALoginWorks(t *testing.T) {
	refusing := start(t, "login_required = true\n")
	var response map[string]any

	err := refusing.newSession(t, "")
	var refused *acp.RequestError
	if !errors.As(err, &refused) || refused.Code != -32000 {
		t.Errorf("session error = %v", err)
	}
	err = refusing.call(t, acp.AgentMethodAuthenticate, map[string]any{"methodId": "login"}, &response)
	if !errors.As(err, &refused) || refused.Error() != `{"code":-32603,"message":"Internal error","data":"Onboarding failed: Timed out waiting for the authentication flow to complete."}` {
		t.Errorf("login error = %v", err)
	}

	working := start(t, "login_required = true\nlogin_works = true\n")
	if err := working.call(t, acp.AgentMethodAuthenticate, map[string]any{"methodId": "login"}, &response); err != nil {
		t.Fatal(err)
	}
	if err := working.newSession(t, ""); err != nil {
		t.Errorf("session error = %v", err)
	}
}

func TestAScriptWithAnUnknownKeyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	if err := os.WriteFile(path, []byte("[[prompts]]\nanswer = \"Hi.\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Serve(path, strings.NewReader(""), io.Discard)

	if err == nil || !strings.Contains(err.Error(), "answer") {
		t.Errorf("error = %v", err)
	}
}
