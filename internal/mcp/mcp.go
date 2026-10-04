// Package mcp serves the Mobius MCP server to the agent sessions.
package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server serves the Mobius MCP server at /mcp/{key}, with one key for each session.
type Server struct {
	mu      sync.Mutex
	callers map[string]*caller
	http    *sdk.StreamableHTTPHandler
}

type caller struct {
	// listed gets the protocol version of the first tools/list of the key.
	listed chan string
	sent   bool
}

// New gives a Server with no keys.
func New() *Server {
	s := &Server{callers: map[string]*caller{}}
	// Protocol 2026-07-28 works over streamable HTTP only in stateless mode.
	s.http = sdk.NewStreamableHTTPHandler(s.server, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return s
}

// Register adds the route /mcp/{key} to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/mcp/{key}", s.serve)
}

// Open gives a new key. The key is valid until Close.
func (s *Server) Open() string {
	key := rand.Text()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callers[key] = &caller{listed: make(chan string, 1)}
	return key
}

// Close makes the key invalid.
func (s *Server) Close(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.callers, key)
}

// Listed gives the protocol version of the first tools/list of the key, when it arrives.
func (s *Server) Listed(key string) <-chan string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.callers[key].listed
}

// URL gives the URL of the key on the listener at addr.
func URL(addr, key string) string {
	return "http://" + addr + "/mcp/" + key
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	_, ok := s.callers[r.PathValue("key")]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.http.ServeHTTP(w, r)
}

func (s *Server) server(r *http.Request) *sdk.Server {
	key := r.PathValue("key")
	server := sdk.NewServer(&sdk.Implementation{Name: "mobius", Version: "0.1.0"}, &sdk.ServerOptions{
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
		SetCacheable: func(_ context.Context, _ sdk.Request, c *sdk.Cacheable) {
			c.TTLMs = 0
			c.CacheScope = "private"
		},
	})
	server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if list, ok := req.(*sdk.ListToolsRequest); ok {
				s.listed(key, list.ProtocolVersion())
			}
			return next(ctx, method, req)
		}
	})
	server.AddTool(listTasksTool, listTasks)
	return server
}

func (s *Server) listed(key, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	caller, ok := s.callers[key]
	if !ok || caller.sent {
		return
	}
	caller.listed <- version
	caller.sent = true
}

var listTasksTool = &sdk.Tool{
	Name:        "list_tasks",
	Description: "Give the task list of the Workstream: one line for each open issue.",
	InputSchema: map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"required":             []string{},
		"additionalProperties": false,
	},
	Meta: sdk.Meta{"anthropic/alwaysLoad": true},
}

type taskLine struct {
	number    int64
	title     string
	state     string
	blockedBy []blocker
}

type blocker struct {
	number int64
	// Empty for a blocker in the same Workstream.
	workstreamTitle string
}

// spikeTasks stands in for the task list of the engine and GitHub.
var spikeTasks = []taskLine{
	{number: 2, title: "Spike: Open a copy of mobius.db and serve the Workstream list", state: "dispatched"},
	{number: 3, title: "Spike: Start one Claude Code session through ACP", state: "dispatched", blockedBy: []blocker{{number: 2}}},
	{number: 4, title: "Spike: Measure the build, the tests and the disk use", state: "open", blockedBy: []blocker{{number: 3}, {number: 40, workstreamTitle: "Release"}}},
}

func listTasks(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	arguments := req.Params.Arguments
	if len(arguments) == 0 {
		arguments = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	var input struct{}
	if err := decoder.Decode(&input); err != nil {
		return toolError(fmt.Sprintf("Invalid arguments for list_tasks: %v.", err)), nil
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text(spikeTasks)}}}, nil
}

func toolError(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}, IsError: true}
}

func text(lines []taskLine) string {
	var b strings.Builder
	for _, line := range lines {
		var blockers []string
		for _, blocker := range line.blockedBy {
			if blocker.workstreamTitle == "" {
				blockers = append(blockers, fmt.Sprintf("#%d", blocker.number))
			} else {
				blockers = append(blockers, fmt.Sprintf("#%d (Workstream \"%s\")", blocker.number, blocker.workstreamTitle))
			}
		}
		blockedBy := ""
		if len(blockers) > 0 {
			blockedBy = ", blocked by " + strings.Join(blockers, ", ")
		}
		fmt.Fprintf(&b, "#%d %s: %s%s\n", line.number, line.title, line.state, blockedBy)
	}
	return b.String()
}
