package engine

import "context"

// The tests make these waits short.
var (
	ToolsTimeout  = &toolsTimeout
	RestartDelays = &restartDelays
	HangTimeout   = &hangTimeout
)

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
