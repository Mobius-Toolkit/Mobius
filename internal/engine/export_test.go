package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The tests make these waits short.
var (
	ToolsTimeout            = &toolsTimeout
	RestartDelays           = &restartDelays
	RateLimitGrace          = &rateLimitGrace
	HangTimeout             = &hangTimeout
	ErrHung                 = errHung
	AbsorbTimeout           = &absorbTimeout
	BackgroundTimeout       = &backgroundTimeout
	BackgroundCheckInterval = &backgroundCheckInterval
	LoadAverage             = &loadAverage
	CheckGap                = &checkGap
	LoadPoll                = &loadPoll
	LoadWaitEvent           = &loadWaitEvent
	Cores                   = cores
)

// SetClock makes the long waits read the time from clock.
func (e *Engine) SetClock(clock func() time.Time) {
	e.clock.Store(&clock)
}

// PollEnded tells if a poll ended. A poll replaces the work at its end.
func (e *Engine) PollEnded() bool {
	e.workers.mu.Lock()
	defer e.workers.mu.Unlock()
	return e.workers.work != nil
}

// MergingPullRequests gives mergingPullRequests of the repository name.
func (e *Engine) MergingPullRequests(ctx context.Context, name string) ([]int64, error) {
	repository, err := e.repository(name)
	if err != nil {
		return nil, err
	}
	return e.mergingPullRequests(ctx, repository)
}

// Listeners gives the number of channels that wait for changes.
func (e *Engine) Listeners() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.listeners)
}

// Seal seals the drain for the restart.
func (e *Engine) Seal() DrainEnd {
	return e.seal()
}

// HarnessRuns tells if the Harness process of the session takes a cancel.
func (a *Agent) HarnessRuns() bool {
	return a.session.Cancel(context.Background()) == nil
}

// ClosePaused closes the Harness of each session that waits for the end of a pause.
func (e *Engine) ClosePaused() {
	e.closePaused()
}

// AbortDrain ends a sealed drain.
func (e *Engine) AbortDrain() {
	e.abortDrain()
}

// Update gives params to the agent as a session/update of its Harness.
func (a *Agent) Update(params json.RawMessage) {
	a.update(params, false)
}

// CannotDo gives the reason of the last cannot_do of the agent, or "".
func (a *Agent) CannotDo() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cannotDo
}

// ClaudeMemoryDir gives the directory of the Claude Code memory of the Lead with the directory leadDir.
func ClaudeMemoryDir(home, leadDir string) string {
	return claudeMemoryDir(home, leadDir)
}

// StopWorker ends the Worker goroutines of the task with no state change.
func (e *Engine) StopWorker(task int64) {
	e.stop(task)
}

// DeliverMessage sets the delivery time of a message of the Owner.
func (e *Engine) DeliverMessage(ctx context.Context, id int64) error {
	return e.deliverMessage(ctx, id)
}

// AcknowledgeComment adds the reaction of an agent that gets the comment id of the repository.
func (e *Engine) AcknowledgeComment(ctx context.Context, name string, review bool, id int64) error {
	repository, err := e.repository(name)
	if err != nil {
		return err
	}
	return acknowledgeComment(ctx, repository, review, id)
}

// Poll runs one poll. The test config must have a poll interval that is longer than the test.
func (e *Engine) Poll(ctx context.Context) {
	e.poll(ctx)
}

// Closing tells if the idle timeout started the close of the chat session.
func (e *Engine) Closing(key ChatKey) bool {
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	c, ok := e.chats[key]
	return ok && c.closing
}

// BackgroundTurnRuns tells if the wait turn for the background tasks of the chat key runs.
func (e *Engine) BackgroundTurnRuns(key ChatKey) bool {
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	c, ok := e.chats[key]
	return ok && c.backgroundTurn
}

// QueueOwnerMessageWithoutWake adds a message of the Owner to the queue of the running chat key and sets writing, as
// give does, but it does not wake the chat loop. Thus the chat loop does not take the message until another step wakes it.
func (e *Engine) QueueOwnerMessageWithoutWake(ctx context.Context, key ChatKey, text string) error {
	message, err := e.addChatMessage(ctx, key, ownerAuthor, text, "", "", nil)
	if err != nil {
		return err
	}
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	c := e.chats[key]
	c.queue = append(c.queue, item{message: &message})
	c.writing = true
	return nil
}

// PostApproval adds an Approval message to the chat of key.
func (e *Engine) PostApproval(ctx context.Context, key ChatKey, text string) error {
	return e.postApproval(ctx, key, text)
}

// StartImplementer starts an Implementer for the dispatched task, as start_implementer does for a task of the
// repository of the Lead.
func (e *Engine) StartImplementer(ctx context.Context, task store.Task, title, prompt string) error {
	defer e.holdWorker(task.ID)()
	queued, err := e.queries.QueueTask(ctx, store.QueueTaskParams{QueuedAt: sql.NullString{String: now(), Valid: true}, ID: task.ID, FromState: "dispatched"})
	if err != nil || queued == 0 {
		return err
	}
	if err := e.setWorker(ctx, task.ID, ImplementerRole, prompt); err != nil {
		return err
	}
	e.runImplementer(job{task: task, title: title, prompt: prompt})
	return nil
}

// QueueTask moves the task like the engine does when it queues a task.
func (e *Engine) QueueTask(ctx context.Context, params store.QueueTaskParams) (int64, error) {
	return e.queueTask(ctx, params)
}

// TurnRunsForMessage tells if the chat key has a turn that answers a message of the Owner.
func (e *Engine) TurnRunsForMessage(key ChatKey) bool {
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	c, ok := e.chats[key]
	return ok && c.stoppable
}

// HoldWork records that the switch kind holds the work of the issue of repository.
func (e *Engine) HoldWork(ctx context.Context, kind, repository string, issue int64) error {
	return e.holdWork(ctx, kind, repository, issue)
}

// ChatAgent gives the agent of the chat key, or nil.
func (e *Engine) ChatAgent(key ChatKey) *Agent {
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	if c, ok := e.chats[key]; ok {
		return c.agent
	}
	return nil
}

// ChatQueued gives the number of items that wait in the queue of the chat key.
func (e *Engine) ChatQueued(key ChatKey) int {
	e.chatsMu.Lock()
	defer e.chatsMu.Unlock()
	if c, ok := e.chats[key]; ok {
		return len(c.queue)
	}
	return 0
}
