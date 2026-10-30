package engine

import (
	"context"
	"os"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// HarnessChoice is a Harness with its models. Error is the reason of a failed read, and then Models is empty.
type HarnessChoice struct {
	Harness config.Harness
	Models  []runner.ModelChoice
	Error   string
}

// harnessRead is one read of the models of a Harness. done closes when the read ends, and then choice is final.
// seen tells that a request gave the error of this ended read. The next request reads the Harness again.
type harnessRead struct {
	done   chan struct{}
	choice HarnessChoice
	seen   bool
}

// ReadHarnessChoices starts a read of the models of each Harness of config.Harnesses whose program is on the PATH of
// the agents. The reads run at the same time, and the method returns before they end.
func (e *Engine) ReadHarnessChoices(ctx context.Context) {
	path := runner.AgentPath(e.config.DataDir, e.agents.Path)
	for _, harness := range config.Harnesses {
		if len(runner.Missing(path, runner.Program(harness))) == 0 {
			e.harnessReadChoice(ctx, harness, path)
		}
	}
}

// HarnessChoices gives the models and the effort levels of each Harness of config.Harnesses whose program is on the
// PATH of the agents, in the order of config.Harnesses. It reads the Harnesses at the same time and keeps a successful
// read until the next start of Mobius. A failed read gives its error one time, and the next request reads it again.
func (e *Engine) HarnessChoices(ctx context.Context) []HarnessChoice {
	path := runner.AgentPath(e.config.DataDir, e.agents.Path)
	var reads []*harnessRead
	for _, harness := range config.Harnesses {
		if len(runner.Missing(path, runner.Program(harness))) == 0 {
			reads = append(reads, e.harnessReadChoice(ctx, harness, path))
		}
	}
	choices := make([]HarnessChoice, 0, len(reads))
	for _, read := range reads {
		<-read.done
		choices = append(choices, read.choice)
		if read.choice.Error != "" {
			e.markHarnessReadSeen(read)
		}
	}
	return choices
}

// harnessReadChoice gives the read of harness. It starts a read when there is none, when the last read failed, or when
// a request already gave the error of that failed read. The caller waits for done.
func (e *Engine) harnessReadChoice(ctx context.Context, harness config.Harness, path string) *harnessRead {
	e.harnessReadsMu.Lock()
	defer e.harnessReadsMu.Unlock()
	if read := e.harnessReads[harness]; read != nil {
		select {
		case <-read.done:
			if read.choice.Error == "" || !read.seen {
				read.seen = true
				return read
			}
		default:
			return read
		}
	}
	read := &harnessRead{done: make(chan struct{})}
	e.harnessReads[harness] = read
	go func() {
		read.choice = e.readHarness(context.WithoutCancel(ctx), harness, path)
		close(read.done)
	}()
	return read
}

// markHarnessReadSeen marks that a request gave the error of read.
func (e *Engine) markHarnessReadSeen(read *harnessRead) {
	e.harnessReadsMu.Lock()
	read.seen = true
	e.harnessReadsMu.Unlock()
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
