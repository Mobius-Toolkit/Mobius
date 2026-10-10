package engine

import (
	"context"
	"encoding/json"
	"time"
)

// The tests make these waits short.
var (
	ToolsTimeout  = &toolsTimeout
	RestartDelays = &restartDelays
	HangTimeout   = &hangTimeout
	ErrHung       = errHung
	AbsorbTimeout = &absorbTimeout
	LoadAverage   = &loadAverage
	CheckGap      = &checkGap
	LoadPoll      = &loadPoll
	LoadWaitEvent = &loadWaitEvent
	Cores         = cores
)

// SetClock makes the long waits read the time from clock.
func (e *Engine) SetClock(clock func() time.Time) {
	e.clock.Store(&clock)
}

// ListenerCount gives the number of listeners of the changes.
func (e *Engine) ListenerCount() int {
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
	a.update(params)
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
