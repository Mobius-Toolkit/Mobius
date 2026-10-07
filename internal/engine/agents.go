package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// toolsTimeout is the time that a Claude Code session has for its first tools/list.
var toolsTimeout = 30 * time.Second

// listenerBuffer is the number of changes that the channel of a listener holds.
const listenerBuffer = 256

// cancelResend is the time between two cancels for the same new details.
const cancelResend = time.Second

// errNoTools is the error of a Claude Code session that gets no tools/list (Mobius-rust#254).
var errNoTools = errors.New("the session sent no tools/list, so it has no Mobius tools")

// Spec is the session of an agent.
type Spec struct {
	// Role is a Role of the sessions table, for example LeadRole. The session runs on the Role binding of its Role.
	Role string
	// Organization is the owner of Repository. The Triager chat has an organization and an empty Repository.
	Organization string
	Repository   string
	// Workstream is the number of the Workstream issue. The Triager has 0.
	Workstream int64
	// Issue is the one issue that the session works on.
	Issue sql.NullInt64
	// Parent is the session of the agent that started this session.
	Parent sql.NullInt64
	// Task is the id of the queued task of the session, or 0. The session waits for its slot at the place of the task in the queue.
	Task int64
	// PullRequest is the pull request that the Implementer of a fix round or a conflict round, or the Reviewer, works
	// on, or 0.
	PullRequest int64
	// Tracked tells that the drain counts the session from a tryTrack of the caller.
	Tracked bool
	// Dir is the working directory of the agent. With an empty Dir, Start makes scratch/<session id> below the data
	// directory, the agent works there, and the end of the session removes that directory.
	Dir string
}

// Agent is a live agent session. It records its Transcript.
type Agent struct {
	engine  *Engine
	id      int64
	spec    Spec
	harness config.Harness
	key     string
	session *runner.Session
	// slot is the Role of the slot that the session holds, or "".
	slot string
	// tracked tells that the drain counts the session.
	tracked bool
	// uncounted tells that the session waits for the end of a pause and the drain does not count it. It and tracked
	// are never both true.
	uncounted bool

	// scratch is the directory of the session below scratch/, or "".
	scratch string

	mu       sync.Mutex
	prompted bool
	// turn tells that a turn runs: Prompt sent its text and the agent did not end the turn.
	turn bool
	// activity is the time of the last activity of the agent, or of the last prompt.
	activity time.Time
	// subagent tells that the current prompt started a background subagent.
	subagent bool
	// autonomous tells that a turn runs that no prompt of Mobius started.
	autonomous bool
	// autonomousEnd is the time of the end of an autonomous turn while a prompt runs, until the next work update.
	// Otherwise it is zero.
	autonomousEnd time.Time
	// ended gets a value at each autonomousEnd.
	ended chan struct{}
	// stopRequested tells that a stop came while no turn ran. Prompt takes it before it sends its text.
	stopRequested bool
	// retries is the number of retry prompts that Prompt sent in the session.
	retries int
	// details are the new details from the Owner that the next prompt of the Implementer or the Researcher carries.
	details []string
	// wake tells the running Prompt that new details wait. Only the goroutine of that Prompt sends the cancel, so the
	// cancel never reaches a later turn.
	wake chan struct{}
	// chunk is the JSON of the last Transcript row while that row is a message chunk or a thought chunk, and chunkID is its id.
	chunk   map[string]any
	chunkID int64
	// resetHint is the last _claude/rateLimit.resetsAt of a usage update of the session, or zero.
	resetHint time.Time
	// author is the author of the chat messages that the reply text adds to the chat of the session, or "" when the
	// reply text goes only to the Transcript.
	author string
	// message is the id of the chat message that the reply text grows until the next prompt or tool call, or 0.
	message int64
	// reply is the reply text of the turn after its last tool call.
	reply strings.Builder
	// cannotDo is the reason of the cannot_do of the Implementer in the last turn, or "".
	cannotDo string
	// replies are the replies of the reply_thread calls of the Implementer that wait for the push.
	replies []heldReply
	// head is the head commit that the Reviewer reviews.
	head string
	// items are the items of the batch of the Judge.
	items []judgeItem
	// verdicts are the verdicts of the last valid submit_verdicts call of the Judge, or nil.
	verdicts []itemVerdicts
}

// Node is a session in the agent tree of a Workstream.
type Node struct {
	Session store.Session
	// Name is the name of the Role, for example Lead.
	Name string
	// Title tells what the session works on, for example "chat session". It can be empty.
	Title string
}

// Change is a change for the live event stream: a new, changed or ended session, a new or changed Transcript line,
// a change of the drain, a change of the error of the last upgrade, a change of the Workstream list, a change of the
// repositories of the Apps, a new or longer chat message, a new unread count of a chat, a new state of a chat, a new or
// dismissed Inbox item, or a Workstream that the Triager chat created. It has one field that is not empty.
type Change struct {
	Node  *Node
	Line  *Line
	Drain *DrainState
	// Upgrade is the error of the last upgrade, or "" when the last upgrade has no error.
	Upgrade      *string
	Workstreams  bool
	Repositories bool
	Message      *store.ChatMessage
	Unread       *Unread
	Chat         *ChatState
	Inbox        *store.InboxItem
	Created      *Created
}

// Created is a Workstream that the Triager chat created.
type Created struct {
	Repository string
	Number     int64
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func endParams(id int64, reason string) store.EndSessionParams {
	return store.EndSessionParams{
		EndedAt:   sql.NullString{String: now(), Valid: true},
		EndReason: sql.NullString{String: reason, Valid: true},
		ID:        id,
	}
}

// Start adds the session of spec, waits for a slot of its Role, starts its agent on the Harness of the Role binding,
// and configures the agent. The Lead gets a gh with the user token of the Owner.
//
// When the task of the session leaves the queue, the session ends with the reason "declined" and Start gives
// ErrLeftQueue. When ctx ends before the agent starts, the session ends with the reason "stopped". When the start
// fails, the session ends with the reason "failed" and the error in its Transcript.
func (e *Engine) Start(ctx context.Context, spec Spec) (*Agent, error) {
	a, err := e.addAgent(ctx, spec)
	if err != nil {
		return nil, err
	}
	// The end of a session must also work after the end of ctx.
	ended := context.WithoutCancel(ctx)
	if spec.Dir == "" {
		a.scratch = filepath.Join(e.config.DataDir, "scratch", strconv.FormatInt(a.id, 10))
		a.spec.Dir = a.scratch
		if err := os.MkdirAll(a.scratch, 0o750); err != nil {
			return nil, a.Fail(ended, err)
		}
	}
	if err := a.open(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, errors.Join(err, a.End(ended, "stopped"))
		}
		return nil, a.Fail(ended, err)
	}
	return a, nil
}

// addAgent adds the session of spec and waits for a slot of its Role, with the ends of Start. A Worker then
// prepares its directory and opens the agent itself.
func (e *Engine) addAgent(ctx context.Context, spec Spec) (*Agent, error) {
	a, err := e.newAgent(ctx, spec)
	if err != nil {
		return nil, err
	}
	if err := a.waitForSlot(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

// newAgent adds the session of spec. The agent holds no slot yet.
func (e *Engine) newAgent(ctx context.Context, spec Spec) (*Agent, error) {
	binding, ok := roleBinding(e.config, spec.Role)
	if !ok {
		return nil, fmt.Errorf("the Role %s has no Role binding", spec.Role)
	}
	worker := workerRoles[spec.Role]
	if !worker && !spec.Tracked && !e.track() {
		return nil, refuse("Mobius restarts for an upgrade.")
	}
	session, err := e.queries.AddSession(ctx, store.AddSessionParams{
		Role:         spec.Role,
		Harness:      string(binding.Harness),
		Model:        binding.Model,
		Organization: spec.Organization,
		Repository:   spec.Repository,
		Workstream:   spec.Workstream,
		Issue:        spec.Issue,
		Parent:       spec.Parent,
		StartedAt:    now(),
	})
	if err != nil {
		if !worker {
			e.untrack()
		}
		return nil, err
	}
	e.publish(Change{Node: new(node(session))})
	return &Agent{engine: e, id: session.ID, spec: spec, harness: binding.Harness, tracked: !worker, wake: make(chan struct{}, 1), ended: make(chan struct{}, 1)}, nil
}

// waitForSlot waits for a slot of the Role of a, and starts the session. A failed wait ends the session, like Start.
func (a *Agent) waitForSlot(ctx context.Context) error {
	e := a.engine
	ended := context.WithoutCancel(ctx)
	if err := e.takeSlot(ctx, a); err != nil {
		switch {
		case errors.Is(err, ErrLeftQueue):
			return errors.Join(err, a.End(ended, "declined"))
		case ctx.Err() != nil:
			return errors.Join(err, a.End(ended, "stopped"))
		}
		return a.Fail(ended, err)
	}
	started, err := e.queries.StartSession(ctx, store.StartSessionParams{StartedAt: now(), ID: a.id})
	if err != nil {
		return a.Fail(ended, err)
	}
	e.publish(Change{Node: new(node(started))})
	return nil
}

// open starts the Harness in the directory of the session and configures the session. A Claude Code session with no
// tools/list in toolsTimeout has no Mobius tools (Mobius-rust#254), so open starts its Harness again, at most
// max_worker_restarts times.
func (a *Agent) open(ctx context.Context) error {
	binding, _ := roleBinding(a.engine.config, a.spec.Role)
	for restart := 0; ; restart++ {
		if err := a.startHarness(ctx); err != nil {
			return err
		}
		if binding.Harness != config.ClaudeCode || a.listed() {
			return a.session.Configure(ctx, binding.Model, binding.Effort)
		}
		if restart == a.engine.config.MaxWorkerRestarts {
			return errNoTools
		}
		a.closeHarness()
		if err := a.addError(ctx, "The session sent no tools/list, so it has no Mobius tools. Mobius starts the session again."); err != nil {
			return err
		}
	}
}

// listed tells if the session sent its first tools/list in toolsTimeout.
func (a *Agent) listed() bool {
	select {
	case <-a.engine.agents.MCP.Listed(a.key):
		return true
	case <-time.After(toolsTimeout):
		return false
	}
}

// startHarness opens a new session key with the tools of the Role, and starts the Harness with it.
func (a *Agent) startHarness(ctx context.Context) error {
	e := a.engine
	spec := a.spec
	mcpCaller := mcp.Caller{Tools: e.tools(caller{
		session:      a.id,
		role:         spec.Role,
		organization: spec.Organization,
		repository:   spec.Repository,
		workstream:   spec.Workstream,
		agent:        a,
	})}
	ghTokenURL := ""
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
	if spec.Role == LeadRole {
		ghTokenURL = mcp.GHTokenURL(e.agents.Addr, a.key)
	}
	session, err := runner.Start(ctx, a.harness, spec.Dir, e.config.DataDir, e.agents.Path, mcp.URL(e.agents.Addr, a.key), ghTokenURL, a.update)
	if err != nil {
		return err
	}
	a.session = session
	return e.queries.SetACPSessionID(ctx, store.SetACPSessionIDParams{AcpSessionID: sql.NullString{String: session.ID(), Valid: true}, ID: a.id})
}

// closeHarness ends the Harness process and the session key.
func (a *Agent) closeHarness() {
	// The session is nil when the Harness did not start.
	if a.session != nil {
		a.session.Close()
	}
	a.engine.agents.MCP.Close(a.key)
}

// ID gives the id of the session.
func (a *Agent) ID() int64 {
	return a.id
}

// Prompt adds text to the Transcript, sends it with images, and holds until the turn ends. The Transcript row has
// only the type and the size of each image. When the agent cannot read images, Prompt sends a note in place of them.
// After a usage limit, Prompt waits for the end of the pause of the Harness and sends text again.
func (a *Agent) Prompt(ctx context.Context, text string, images []Image) error {
	if len(images) > 0 && !a.session.ImagesSupported() {
		text += imagesNote(text, len(images))
		images = nil
	}
	retrying := false
	for {
		if !retrying {
			err := a.waitQuiet(ctx)
			if errors.Is(err, errStopped) {
				a.takeStop()
				return nil
			}
			if errors.Is(err, errHung) {
				var retry string
				retry, err = a.retryHang(ctx)
				text = retry + "\n\n" + text
			}
			if err != nil {
				return err
			}
		}
		row, err := compact(promptRow(text, images))
		if err != nil {
			return err
		}
		a.mu.Lock()
		err = a.engine.addRow(ctx, a.id, "prompt", row, !a.prompted)
		a.prompted = true
		a.chunk = nil
		a.message = 0
		a.reply.Reset()
		a.cannotDo = ""
		a.mu.Unlock()
		if err != nil {
			return err
		}
		paused, err := a.engine.harnessPause(ctx, a.harness)
		if err != nil {
			return err
		}
		a.mu.Lock()
		if a.stopRequested {
			a.stopRequested = false
			a.mu.Unlock()
			return a.engine.endPauseSince(ctx, paused)
		}
		a.turn = true
		a.subagent = false
		a.autonomousEnd = time.Time{}
		a.activity = time.Now()
		a.mu.Unlock()
		resendCtx, stopResend := context.WithCancel(ctx)
		resent := make(chan struct{})
		go func() {
			defer close(resent)
			a.resendCancel(resendCtx)
		}()
		err = a.sendPrompt(ctx, text, images)
		stopResend()
		<-resent
		// While a retry runs, turn stays true, so a late update of the hung turn is no autonomous turn.
		retrying = errors.Is(err, errHung) && a.retries < maxRetries
		if !retrying {
			a.mu.Lock()
			a.turn = false
			a.mu.Unlock()
		}
		if err == nil {
			return a.engine.endPauseSince(ctx, paused)
		}
		if errors.Is(err, errHung) {
			if text, err = a.retryHang(ctx); err != nil {
				return err
			}
			images = nil
			continue
		}
		limited, waitErr := a.waitOutLimit(ctx, err)
		if waitErr != nil {
			return errors.Join(err, waitErr)
		}
		if !limited {
			return err
		}
	}
}

// stop asks the agent to end the turn that runs. While no turn runs, stop only sets stopRequested, so Prompt sends
// no prompt and no cancel reaches an autonomous turn.
func (a *Agent) stop(ctx context.Context) error {
	a.mu.Lock()
	if !a.turn {
		a.stopRequested = true
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	return a.cancel(ctx)
}

// takeStop clears stopRequested.
func (a *Agent) takeStop() {
	a.mu.Lock()
	a.stopRequested = false
	a.mu.Unlock()
}

// cancel asks the agent to end the turn that runs.
func (a *Agent) cancel(ctx context.Context) error {
	return a.session.Cancel(ctx)
}

// resendCancel sends the cancel at each wake and each cancelResend while new details wait, until ctx ends. A cancel
// that the agent gets before it reads the prompt request does not end the turn.
func (a *Agent) resendCancel(ctx context.Context) {
	ticker := time.NewTicker(cancelResend)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.wake:
		case <-ticker.C:
		}
		a.mu.Lock()
		waiting := len(a.details) > 0
		a.mu.Unlock()
		if waiting {
			_ = a.cancel(ctx)
		}
	}
}

// replyText gives the reply text of the last turn after its last tool call.
func (a *Agent) replyText() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reply.String()
}

// setAuthor sets the author of the chat messages of the reply text, or "" for no chat message.
func (a *Agent) setAuthor(author string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.author = author
}

// End ends the agent and the session with reason, for example "done". Then the session frees its slot.
func (a *Agent) End(ctx context.Context, reason string) error {
	a.closeHarness()
	defer a.release()
	if a.scratch != "" {
		if err := os.RemoveAll(a.scratch); err != nil {
			return err
		}
	}
	session, err := a.engine.queries.EndSession(ctx, endParams(a.id, reason))
	if err != nil {
		return err
	}
	a.engine.publish(Change{Node: new(node(session))})
	return nil
}

// release frees the slot of the session and its count in the drain.
func (a *Agent) release() {
	if a.slot != "" {
		a.engine.releaseSlot(a.slot)
		a.slot = ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tracked {
		a.engine.untrack()
		a.tracked = false
	}
	if a.uncounted {
		a.engine.holdUncounted(a, false)
		a.uncounted = false
	}
}

// Fail adds err to the Transcript, and ends the agent and the session with the reason "failed". It gives err.
func (a *Agent) Fail(ctx context.Context, err error) error {
	return errors.Join(err, a.addError(ctx, err.Error()), a.End(ctx, "failed"))
}

func (a *Agent) addError(ctx context.Context, message string) error {
	row, err := compact(map[string]string{"message": message})
	if err != nil {
		return err
	}
	return a.engine.addRow(ctx, a.id, "error", row, false)
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
	a.track(notification, kind)
	// The unit of resetsAt is Unix seconds.
	if resetsAt, ok := field(notification, "update", "_meta", "_claude/rateLimit", "resetsAt").(float64); ok {
		a.resetHint = time.Unix(int64(resetsAt), 0)
	}
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
		if err := a.engine.publishRow(row, false); err != nil {
			return err
		}
		return a.addReply(ctx, kind, content)
	}
	row, err := a.engine.queries.AddTranscriptRow(ctx, store.AddTranscriptRowParams{Session: a.id, Time: now(), Kind: "update", Json: string(params)})
	if err != nil {
		return err
	}
	a.chunk = nil
	if isChunk {
		a.chunk, a.chunkID = notification, row.ID
	}
	if err := a.engine.publishRow(row, false); err != nil {
		return err
	}
	return a.addReply(ctx, kind, content)
}

// addReply adds content of an agent message chunk to the reply text, and to the chat when the session has an author.
// A tool call starts a new reply text and a new chat message. The caller holds a.mu.
func (a *Agent) addReply(ctx context.Context, kind, content string) error {
	switch kind {
	case "tool_call":
		a.message = 0
		a.reply.Reset()
	case "tool_call_update":
		a.reply.Reset()
	}
	if kind != "agent_message_chunk" {
		return nil
	}
	a.reply.WriteString(content)
	if a.author == "" {
		return nil
	}
	if a.message != 0 {
		message, err := a.engine.queries.AppendChatMessage(ctx, store.AppendChatMessageParams{Text: content, ID: a.message})
		if err != nil {
			return err
		}
		a.engine.publish(Change{Message: &message})
		return nil
	}
	spec := a.spec
	message, err := a.engine.addChatMessage(ctx, ChatKey{spec.Organization, spec.Repository, spec.Workstream}, a.author, content, "")
	a.message = message.ID
	return err
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

// ActiveAgent is an open session with what the store holds about its Workstream, its issue and its task.
type ActiveAgent struct {
	Node            Node
	WorkstreamTitle sql.NullString
	IssueTitle      sql.NullString
	// PullRequest is the pull request of the newest task of the issue of the session.
	PullRequest sql.NullInt64
}

// AgentGroup is the open sessions of one Role, with the number of sessions that hold a slot and the max of the Role.
type AgentGroup struct {
	Title  string
	Count  int
	Max    int
	Agents []ActiveAgent
}

// ActiveAgents is the open sessions of all organizations in one group for each Role, in the order Lead, Triager,
// Implementer, Researcher, Reviewer, Judge. Count is the number of sessions that hold a slot and count in max_agents.
type ActiveAgents struct {
	Count  int
	Max    int
	Groups []AgentGroup
}

// ActiveAgents gives the open sessions with the limits of the config. A queued session is in its group, but it holds
// no slot. A session that holds a slot can show a queue reason too, for example a pause or the phase of its check.
func (e *Engine) ActiveAgents(ctx context.Context) (ActiveAgents, error) {
	rows, err := e.queries.ListOpenSessions(ctx)
	if err != nil {
		return ActiveAgents{}, err
	}
	e.workers.mu.Lock()
	running := maps.Clone(e.workers.running)
	e.workers.mu.Unlock()
	agents := map[string][]ActiveAgent{}
	for _, row := range rows {
		role := row.Session.Role
		if _, ok := roleBinding(e.config, role); !ok {
			continue
		}
		agents[role] = append(agents[role], ActiveAgent{node(row.Session), row.WorkstreamTitle, row.IssueTitle, row.PullRequest})
	}
	active := ActiveAgents{Max: e.config.MaxAgents}
	for _, g := range groups {
		binding, _ := roleBinding(e.config, g.role)
		if binding.CountsInMaxAgents {
			active.Count += running[g.role]
		}
		active.Groups = append(active.Groups, AgentGroup{Title: g.title, Count: running[g.role], Max: binding.Max, Agents: agents[g.role]})
	}
	return active, nil
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
