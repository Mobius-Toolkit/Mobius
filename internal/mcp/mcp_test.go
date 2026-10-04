package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// echo gives its text argument, or the text as the error when fail is true.
var echo = Tool{
	Name:        "echo",
	Description: "Give the text.",
	Properties: map[string]any{
		"text": map[string]any{"type": "string"},
		"fail": map[string]any{"type": "boolean"},
	},
	Run: func(_ context.Context, arguments json.RawMessage) (string, error) {
		var input struct {
			Text string `json:"text"`
			Fail bool   `json:"fail"`
		}
		if err := json.Unmarshal(arguments, &input); err != nil {
			return "", err
		}
		if input.Fail {
			return "", errors.New(input.Text)
		}
		return input.Text, nil
	},
}

var none = Tool{
	Name:        "none",
	Description: "Take no arguments.",
	Properties:  map[string]any{},
	Run: func(_ context.Context, arguments json.RawMessage) (string, error) {
		return string(arguments), nil
	},
}

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

func TestTheToolListHasTheToolsOfTheKeyAndTellsTheProtocolVersion(t *testing.T) {
	server, addr := start(t)
	key := server.Open(Caller{Tools: []Tool{echo, none}})
	server.Open(Caller{Tools: []Tool{none}})
	session := connect(t, URL(addr, key))

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Tools) != 2 {
		t.Fatalf("tools = %d", len(result.Tools))
	}
	tool := result.Tools[0]
	if tool.Name != "echo" || tool.Description != "Give the text." {
		t.Errorf("tool = %s: %s", tool.Name, tool.Description)
	}
	wantSchema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"fail": map[string]any{"type": "boolean"}, "text": map[string]any{"type": "string"}},
		"required":             []any{"fail", "text"},
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(tool.InputSchema, wantSchema) {
		t.Errorf("schema = %v", tool.InputSchema)
	}
	wantSchema = map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}, "additionalProperties": false}
	if !reflect.DeepEqual(result.Tools[1].InputSchema, wantSchema) {
		t.Errorf("schema = %v", result.Tools[1].InputSchema)
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

func call(t *testing.T, session *sdk.ClientSession, name string, arguments any) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestACallGivesTheTextOrTheErrorOfTheTool(t *testing.T) {
	server, addr := start(t)
	session := connect(t, URL(addr, server.Open(Caller{Tools: []Tool{echo, none}})))

	result := call(t, session, "echo", map[string]any{"text": "Hello."})
	failed := call(t, session, "echo", map[string]any{"text": "No such issue.", "fail": true})
	empty := call(t, session, "none", nil)

	if result.IsError || result.Content[0].(*sdk.TextContent).Text != "Hello." {
		t.Errorf("result = %+v", result.Content[0])
	}
	if !failed.IsError || failed.Content[0].(*sdk.TextContent).Text != "No such issue." {
		t.Errorf("result = %+v", failed.Content[0])
	}
	if empty.IsError || empty.Content[0].(*sdk.TextContent).Text != "{}" {
		t.Errorf("result = %+v", empty.Content[0])
	}
}

func send(t *testing.T, method, url, version, body string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
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
	key := server.Open(Caller{Tools: []Tool{none}})

	_, text := send(t, http.MethodPost, URL(addr, key), "2025-11-25", `{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}`)

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
	key := server.Open(Caller{Tools: []Tool{none}, GHToken: func(context.Context) (string, error) { return "ghu_1", nil }})
	server.Close(key)

	for _, url := range []string{URL(addr, "unknown"), URL(addr, key)} {
		status, text := send(t, http.MethodPost, url, "2026-07-28", `{}`)
		if status != http.StatusNotFound {
			t.Errorf("%s: %d %s", url, status, text)
		}
	}
	for _, url := range []string{GHTokenURL(addr, "unknown"), GHTokenURL(addr, key)} {
		status, text := send(t, http.MethodGet, url, "", "")
		if status != http.StatusNotFound {
			t.Errorf("%s: %d %s", url, status, text)
		}
	}
}

func TestTheGHTokenRouteGivesTheTokenOfACallerWithAGHToken(t *testing.T) {
	server, addr := start(t)
	lead := server.Open(Caller{GHToken: func(context.Context) (string, error) { return "ghu_1", nil }})
	failing := server.Open(Caller{GHToken: func(context.Context) (string, error) { return "", errors.New("authorize the App") }})
	other := server.Open(Caller{})

	for url, want := range map[string]string{
		GHTokenURL(addr, lead):    "200 ghu_1",
		GHTokenURL(addr, failing): "500 authorize the App\n",
		GHTokenURL(addr, other):   "404 404 page not found\n",
	} {
		status, text := send(t, http.MethodGet, url, "", "")
		if got := fmt.Sprintf("%d %s", status, text); got != want {
			t.Errorf("%s: %q, want %q", url, got, want)
		}
	}
}
