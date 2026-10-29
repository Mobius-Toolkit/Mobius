package engine

import (
	"context"
	"os"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// harnessChoicesTTL is the time that the engine keeps a successful read of the models of a Harness.
const harnessChoicesTTL = 10 * time.Minute

// HarnessChoice is a Harness with its models. Error is the reason of a failed read, and then Models is empty.
type HarnessChoice struct {
	Harness config.Harness
	Models  []runner.ModelChoice
	Error   string
}

// harnessRead is one read of the models of a Harness. done closes when the read ends, and then choice and at are final.
type harnessRead struct {
	done   chan struct{}
	choice HarnessChoice
	at     time.Time
}

// HarnessChoices gives the models and the effort levels of each Harness of config.Harnesses whose program is on the
// PATH of the agents. It keeps a successful read for 10 minutes, and runs one read at a time for each Harness.
func (e *Engine) HarnessChoices(ctx context.Context) []HarnessChoice {
	path := runner.AgentPath(e.config.DataDir, e.agents.Path)
	var choices []HarnessChoice
	for _, harness := range config.Harnesses {
		if len(runner.Missing(path, runner.Program(harness))) == 0 {
			choices = append(choices, e.harnessChoice(ctx, harness, path))
		}
	}
	return choices
}

func (e *Engine) harnessChoice(ctx context.Context, harness config.Harness, path string) HarnessChoice {
	e.harnessReadsMu.Lock()
	read := e.harnessReads[harness]
	if read == nil || !e.fresh(read) {
		read = &harnessRead{done: make(chan struct{})}
		e.harnessReads[harness] = read
		e.harnessReadsMu.Unlock()
		read.choice = e.readHarness(context.WithoutCancel(ctx), harness, path)
		read.at = e.timeNow()
		close(read.done)
	} else {
		e.harnessReadsMu.Unlock()
		<-read.done
	}
	return read.choice
}

// fresh tells if read still runs, or ended with a success that is younger than harnessChoicesTTL.
func (e *Engine) fresh(read *harnessRead) bool {
	select {
	case <-read.done:
		return read.choice.Error == "" && e.timeNow().Sub(read.at) < harnessChoicesTTL
	default:
		return true
	}
}

func (e *Engine) readHarness(ctx context.Context, harness config.Harness, path string) HarnessChoice {
	choice := HarnessChoice{Harness: harness}
	dir, err := os.MkdirTemp("", "mobius-harness-")
	if err != nil {
		choice.Error = err.Error()
		return choice
	}
	defer func() { _ = os.RemoveAll(dir) }()
	models, err := runner.ReadChoices(ctx, harness, dir, dir, path)
	if err != nil {
		choice.Error = err.Error()
		return choice
	}
	choice.Models = models
	return choice
}
