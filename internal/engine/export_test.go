package engine

// The tests make these waits short.
var (
	ToolsTimeout  = &toolsTimeout
	RestartDelays = &restartDelays
)

// Seal seals the drain for the restart.
func (e *Engine) Seal() DrainEnd {
	return e.seal()
}
