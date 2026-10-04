package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/mcp"
	"github.com/Mobius-Toolkit/mobius-go/internal/runner"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// The Roles of the sessions table that have Mobius tools.
const (
	LeadRole    = "lead_chat"
	TriagerRole = "triager"
)

// toolsTimeout is the time that a Claude Code session has for its first tools/list.
const toolsTimeout = 30 * time.Second

// listenerBuffer is the number of changes that the channel of a listener holds.
const listenerBuffer = 256

// Spec is the session of an agent.
type Spec struct {
	Role    string
	Binding config.RoleBinding
	// Organization is the owner of Repository. The Triager chat has an organization and an empty Repository.
	Organization string
	Repository   string
	// Workstream is the number of the Workstream issue. The Triager has 0.
	Workstream int64
	// Issue is the one issue that the session works on.
	Issue sql.NullInt64
	// Parent is the session of the agent that started this session.
	Parent sql.NullInt64
	// Dir is the working directory of the agent.
	Dir string
}

// Agent is a live agent session. It records its Transcript.
type Agent struct {
	engine  *Engine
	id      int64
	key     string
	session *runner.Session

	mu       sync.Mutex
	prompted bool
	// chunk is the JSON of the last Transcript row while that row is a message chunk or a thought chunk, and chunkID is its id.
	chunk   map[string]any
	chunkID int64
}

// Node is a session in the agent tree of a Workstream.
type Node struct {
	Session store.Session
	// Name is the name of the Role, for example Lead.
	Name string
	// Title tells what the session works on, for example "chat session". It can be empty.
	Title string
}

// Change is a change for the live event stream: a new or ended session, or a new or changed Transcript line.
// It has a Node or a Line.
type Change struct {
	Node *Node
	Line *Line
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// Start adds the session of spec, starts its agent, and configures the agent. The Lead gets a gh with the user token of the Owner.
// When the start fails, the session ends with the reason "failed" and the error in its Transcript.
func (e *Engine) Start(ctx context.Context, spec Spec) (*Agent, error) {
	session, err := e.queries.AddSession(ctx, store.AddSessionParams{
		Role:         spec.Role,
		Harness:      string(spec.Binding.Harness),
		Model:        spec.Binding.Model,
		Organization: spec.Organization,
		Repository:   spec.Repository,
		Workstream:   spec.Workstream,
		Issue:        spec.Issue,
		Parent:       spec.Parent,
		StartedAt:    now(),
	})
	if err != nil {
		return nil, err
	}
	e.publish(Change{Node: new(node(session))})
	a := &Agent{engine: e, id: session.ID}
	tools := e.tools(caller{
		session:      session.ID,
		role:         spec.Role,
		organization: spec.Organization,
		repository:   spec.Repository,
		workstream:   spec.Workstream,
	})
	mcpCaller := mcp.Caller{Tools: tools}
	if spec.Role == LeadRole {
		mcpCaller.GHToken = func(ctx context.Context) (string, error) {
			repository, err := e.repository(spec.Repository)
			if err != nil {
				return "", err
			}
			return e.github.UserToken(ctx, repository.AppID)
		}
	}
	a.key = e.agents.MCP.Open(mcpCaller)
	if err := a.open(ctx, spec); err != nil {
		return nil, a.Fail(ctx, err)
	}
	return a, nil
}

func (a *Agent) open(ctx context.Context, spec Spec) error {
	e := a.engine
	ghTokenURL := ""
	if spec.Role == LeadRole {
		ghTokenURL = mcp.GHTokenURL(e.agents.Addr, a.key)
	}
	session, err := runner.Start(ctx, spec.Binding.Harness, spec.Dir, e.agents.DataDir, e.agents.Path, mcp.URL(e.agents.Addr, a.key), ghTokenURL, a.update)
	if err != nil {
		return err
	}
	a.session = session
	if err := e.queries.SetACPSessionID(ctx, store.SetACPSessionIDParams{AcpSessionID: sql.NullString{String: session.ID(), Valid: true}, ID: a.id}); err != nil {
		return err
	}
	// A Claude Code session that gets no tools/list has no Mobius tools (Mobius#254).
	if spec.Binding.Harness == config.ClaudeCode {
		select {
		case <-e.agents.MCP.Listed(a.key):
		case <-time.After(toolsTimeout):
			return errors.New("the session sent no tools/list, so it has no Mobius tools")
		}
	}
	return session.Configure(ctx, spec.Binding.Model, spec.Binding.Effort)
}

// ID gives the id of the session.
func (a *Agent) ID() int64 {
	return a.id
}

// Prompt adds text to the Transcript, sends it, and holds until the turn ends.
func (a *Agent) Prompt(ctx context.Context, text string) error {
	row, err := compact(map[string]string{"text": text})
	if err != nil {
		return err
	}
	a.mu.Lock()
	err = a.engine.addRow(ctx, a.id, "prompt", row, !a.prompted)
	a.prompted = true
	a.chunk = nil
	a.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = a.session.Prompt(ctx, text)
	return err
}

// End ends the agent and the session with reason, for example "done".
func (a *Agent) End(ctx context.Context, reason string) error {
	// The session is nil when the agent did not start.
	if a.session != nil {
		a.session.Close()
	}
	a.engine.agents.MCP.Close(a.key)
	session, err := a.engine.queries.EndSession(ctx, store.EndSessionParams{
		EndedAt:   sql.NullString{String: now(), Valid: true},
		EndReason: sql.NullString{String: reason, Valid: true},
		ID:        a.id,
	})
	if err != nil {
		return err
	}
	a.engine.publish(Change{Node: new(node(session))})
	return nil
}

// Fail adds err to the Transcript, and ends the agent and the session with the reason "failed". It gives err.
func (a *Agent) Fail(ctx context.Context, err error) error {
	row, marshalErr := compact(map[string]string{"message": err.Error()})
	if marshalErr != nil {
		return errors.Join(err, marshalErr)
	}
	return errors.Join(err, a.engine.addRow(ctx, a.id, "error", row, false), a.End(ctx, "failed"))
}

func (a *Agent) update(params json.RawMessage) {
	if err := a.record(params); err != nil {
		log.Printf("record an update of the session %d: %v", a.id, err)
	}
}

// record adds the update to the Transcript. A message chunk or a thought chunk that follows a chunk of the same kind
// joins the row of that chunk.
func (a *Agent) record(params json.RawMessage) error {
	notification, err := decodeObject(params)
	if err != nil {
		return err
	}
	kind := stringField(notification, "update", "sessionUpdate")
	content, ok := field(notification, "update", "content", "text").(string)
	isChunk := ok && (kind == "agent_message_chunk" || kind == "agent_thought_chunk")
	ctx := context.Background()
	a.mu.Lock()
	defer a.mu.Unlock()
	if isChunk && a.chunk != nil && stringField(a.chunk, "update", "sessionUpdate") == kind {
		update := a.chunk["update"].(map[string]any)
		update["content"].(map[string]any)["text"] = stringField(a.chunk, "update", "content", "text") + content
		merged, err := compact(a.chunk)
		if err != nil {
			return err
		}
		row, err := a.engine.queries.SetTranscriptJSON(ctx, store.SetTranscriptJSONParams{Json: merged, ID: a.chunkID})
		if err != nil {
			return err
		}
		return a.engine.publishRow(row, false)
	}
	row, err := a.engine.queries.AddTranscriptRow(ctx, store.AddTranscriptRowParams{Session: a.id, Time: now(), Kind: "update", Json: string(params)})
	if err != nil {
		return err
	}
	a.chunk = nil
	if isChunk {
		a.chunk, a.chunkID = notification, row.ID
	}
	return a.engine.publishRow(row, false)
}

// addRow adds a Transcript row of the session and sends its line to the listeners. folded tells that the row is the
// first prompt of the session.
func (e *Engine) addRow(ctx context.Context, session int64, kind, data string, folded bool) error {
	row, err := e.queries.AddTranscriptRow(ctx, store.AddTranscriptRowParams{Session: session, Time: now(), Kind: kind, Json: data})
	if err != nil {
		return err
	}
	return e.publishRow(row, folded)
}

func (e *Engine) publishRow(row store.Transcript, folded bool) error {
	line, err := line(row, folded)
	if err != nil {
		return err
	}
	e.publish(Change{Line: &line})
	return nil
}

func node(session store.Session) Node {
	switch {
	case session.Role == LeadRole:
		return Node{Session: session, Name: "Lead", Title: "chat session"}
	case session.Role == TriagerRole && session.Repository == "":
		return Node{Session: session, Name: "Triager", Title: "chat session"}
	case session.Role == TriagerRole:
		return Node{Session: session, Name: "Triager", Title: session.Repository}
	}
	return Node{Session: session, Name: session.Role}
}

// Tree gives the sessions of the Workstream of repository, the oldest first.
func (e *Engine) Tree(ctx context.Context, repository string, workstream int64) ([]Node, error) {
	organization, _, _ := strings.Cut(repository, "/")
	sessions, err := e.queries.ListSessions(ctx, store.ListSessionsParams{Organization: organization, Repository: repository, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(sessions))
	for _, session := range sessions {
		nodes = append(nodes, node(session))
	}
	return nodes, nil
}

// Listen gives a new channel that gets each change, and the function that removes the channel.
// When the channel is full, the engine removes the channel and closes it.
func (e *Engine) Listen() (<-chan Change, func()) {
	listener := make(chan Change, listenerBuffer)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners[listener] = true
	return listener, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.listeners[listener] {
			delete(e.listeners, listener)
			close(listener)
		}
	}
}

func (e *Engine) publish(change Change) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for listener := range e.listeners {
		select {
		case listener <- change:
		default:
			delete(e.listeners, listener)
			close(listener)
		}
	}
}
