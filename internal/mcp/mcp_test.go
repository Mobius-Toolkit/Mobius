package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const wantTasks = `#2 Spike: Open a copy of mobius.db and serve the Workstream list: dispatched
#3 Spike: Start one Claude Code session through ACP: dispatched, blocked by #2
#4 Spike: Measure the build, the tests and the disk use: open, blocked by #3, #40 (Workstream "Release")
`

func start(t *testing.T) (*Server, string) {
	t.Helper()
	server := New()
	mux := http.NewServeMux()
	server.Register(mux)
	listener := httptest.NewServer(mux)
	t.Cleanup(listener.Close)
	return server, strings.TrimPrefix(listener.URL, "http://")
}

func connect(t *testing.T, url string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0.1.0"}, nil)
	session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestToolListHasListTasksAndTellsTheProtocolVersion(t *testing.T) {
	server, addr := start(t)
	key := server.Open()
	session := connect(t, URL(addr, key))

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Tools) != 1 {
		t.Fatalf("tools = %d", len(result.Tools))
	}
	tool := result.Tools[0]
	if tool.Name != "list_tasks" || tool.Description != "Give the task list of the Workstream: one line for each open issue." {
		t.Errorf("tool = %s: %s", tool.Name, tool.Description)
	}
	wantSchema := map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}, "additionalProperties": false}
	if !reflect.DeepEqual(tool.InputSchema, wantSchema) {
		t.Errorf("schema = %v", tool.InputSchema)
	}
	if tool.Meta["anthropic/alwaysLoad"] != true {
		t.Errorf("meta = %v", tool.Meta)
	}
	if result.TTLMs != 0 || result.CacheScope != "private" {
		t.Errorf("ttlMs = %d, cacheScope = %q", result.TTLMs, result.CacheScope)
	}
	if version := <-server.Listed(key); version != "2026-07-28" {
		t.Errorf("version = %q", version)
	}
}

func TestListTasksGivesTheTaskLines(t *testing.T) {
	server, addr := start(t)
	session := connect(t, URL(addr, server.Open()))

	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_tasks", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}

	if result.IsError || len(result.Content) != 1 || result.Content[0].(*sdk.TextContent).Text != wantTasks {
		t.Errorf("result = %+v", result)
	}
}

func TestListTasksRefusesAnUnknownArgument(t *testing.T) {
	server, addr := start(t)
	session := connect(t, URL(addr, server.Open()))

	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_tasks", Arguments: map[string]any{"n": 1}})
	if err != nil {
		t.Fatal(err)
	}

	want := `Invalid arguments for list_tasks: json: unknown field "n".`
	if !result.IsError || result.Content[0].(*sdk.TextContent).Text != want {
		t.Errorf("result = %+v", result.Content[0])
	}
}

func post(t *testing.T, url, version, body string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", version)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	text, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, text
}

func TestAnOlderClientGetsTheCacheFieldsOfTheToolList(t *testing.T) {
	server, addr := start(t)
	key := server.Open()

	_, text := post(t, URL(addr, key), "2025-11-25", `{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}`)

	var body struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(text, &body); err != nil {
		t.Fatal(err)
	}
	if string(body.Result["ttlMs"]) != "0" || string(body.Result["cacheScope"]) != `"private"` {
		t.Errorf("result = %v", body.Result)
	}
	if version := <-server.Listed(key); version != "2025-11-25" {
		t.Errorf("version = %q", version)
	}
}

func TestAnUnknownOrClosedKeyIsNotFound(t *testing.T) {
	server, addr := start(t)
	key := server.Open()
	server.Close(key)

	for _, url := range []string{URL(addr, "unknown"), URL(addr, key)} {
		status, text := post(t, url, "2026-07-28", `{}`)
		if status != http.StatusNotFound {
			t.Errorf("%s: %d %s", url, status, text)
		}
	}
}
