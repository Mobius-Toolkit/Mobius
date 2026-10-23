// Package mcp serves the Mobius MCP server, the gh token and the gh writes to the agent sessions.
package mcp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log"
	"maps"
	"net/http"
	"slices"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool is a Mobius tool.
type Tool struct {
	Name        string
	Description string
	// Properties gives the JSON schema of each argument. Each argument is required.
	Properties map[string]any
	// Run gives the text of the result, or the error that goes back to the agent. The arguments are a JSON object.
	Run func(ctx context.Context, arguments json.RawMessage) (string, error)
}

// GHWrite is a change that the gh of a session makes on GitHub: a label change, a new issue or a comment.
type GHWrite struct {
	Repository string `json:"repository"`
	// Issue is the number of the issue or the pull request, or 0 for a new issue.
	Issue int64 `json:"issue"`
	// Kind is labeled, unlabeled, created or commented.
	Kind   string   `json:"kind"`
	Labels []string `json:"labels"`
	// Text is the title of a new issue or the body of a comment.
	Text string `json:"text"`
}

// Caller is a session that calls the Mobius MCP server with its key.
type Caller struct {
	Tools []Tool
	// GHToken gives the GitHub token of the gh of the session. A caller with no GHToken gets no token.
	GHToken func(ctx context.Context) (string, error)
	// GHWrites records the writes that the gh of the session is about to make. A caller with no GHWrites gets no route.
	GHWrites func(ctx context.Context, writes []GHWrite) error
}

// Server serves the Mobius MCP server at /mcp/{key}, the gh token at /gh-token/{key} and the gh writes at
// /gh-writes/{key}, with one key for each session.
type Server struct {
	mu      sync.Mutex
	callers map[string]*caller
	http    *sdk.StreamableHTTPHandler
}

type caller struct {
	Caller
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

// Register adds the routes /mcp/{key}, /gh-token/{key} and /gh-writes/{key} to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/mcp/{key}", s.serve)
	mux.HandleFunc("GET /gh-token/{key}", s.ghToken)
	mux.HandleFunc("POST /gh-writes/{key}", s.ghWrites)
}

// Open gives a new key of c. The key is valid until Close.
func (s *Server) Open(c Caller) string {
	key := rand.Text()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callers[key] = &caller{Caller: c, listed: make(chan string, 1)}
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

// URL gives the URL of the MCP server of the key on the listener at addr.
func URL(addr, key string) string {
	return "http://" + addr + "/mcp/" + key
}

// GHTokenURL gives the URL of the gh token of the key on the listener at addr.
func GHTokenURL(addr, key string) string {
	return "http://" + addr + "/gh-token/" + key
}

// GHWritesURL gives the URL of the gh writes of the key on the listener at addr.
func GHWritesURL(addr, key string) string {
	return "http://" + addr + "/gh-writes/" + key
}

func (s *Server) caller(key string) (*caller, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found, ok := s.callers[key]
	return found, ok
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(r.PathValue("key")); !ok {
		http.NotFound(w, r)
		return
	}
	s.http.ServeHTTP(w, r)
}

func (s *Server) ghToken(w http.ResponseWriter, r *http.Request) {
	found, ok := s.caller(r.PathValue("key"))
	if !ok || found.GHToken == nil {
		http.NotFound(w, r)
		return
	}
	token, err := found.GHToken(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := w.Write([]byte(token)); err != nil {
		log.Printf("send the gh token: %v", err)
	}
}

func (s *Server) ghWrites(w http.ResponseWriter, r *http.Request) {
	found, ok := s.caller(r.PathValue("key"))
	if !ok || found.GHWrites == nil {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Writes []GHWrite `json:"writes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := found.GHWrites(r.Context(), request.Writes); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	// The key can close between serve and this call. The server then has no tools.
	found, ok := s.caller(key)
	if !ok {
		return server
	}
	for _, tool := range found.Tools {
		server.AddTool(&sdk.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           tool.Properties,
				"required":             append([]string{}, slices.Sorted(maps.Keys(tool.Properties))...),
				"additionalProperties": false,
			},
			Meta: sdk.Meta{"anthropic/alwaysLoad": true},
		}, handler(tool))
	}
	return server
}

func handler(tool Tool) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		arguments := req.Params.Arguments
		if len(arguments) == 0 || string(arguments) == "null" {
			arguments = json.RawMessage("{}")
		}
		text, err := tool.Run(ctx, arguments)
		if err != nil {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}}, IsError: true}, nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil
	}
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
