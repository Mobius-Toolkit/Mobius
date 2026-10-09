package engine

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

const lowLoadReason = "waits for a low load"

var (
	// loadAverage gives the 1-minute load average of the machine.
	loadAverage = runner.LoadAverage
	// cores is the number of CPU cores. A check starts only when the load average is lower than cores.
	cores = runtime.NumCPU()
	// checkGap is the least time between the starts of two checks.
	checkGap = time.Minute
	// loadPoll is the time between two reads of the load average while a check waits.
	loadPoll = 10 * time.Second
	// loadWaitEvent is the time of one wait after which the Lead gets an event.
	loadWaitEvent = 30 * time.Minute
)

// startCheck tells if a check can start now, and gives the load average. When it can, it sets the start time of the
// last check.
func (e *Engine) startCheck() (float64, bool, error) {
	load, err := loadAverage()
	if err != nil {
		return 0, false, err
	}
	e.checkStartMu.Lock()
	defer e.checkStartMu.Unlock()
	if load >= float64(cores) || time.Since(e.checkStart) < checkGap {
		return load, false, nil
	}
	e.checkStart = time.Now()
	return load, true, nil
}

// waitForLowLoad holds until the load average is lower than cores and checkGap passed since the start of the last
// check. It shows the wait in the queue reason and in the Transcript of a. After loadWaitEvent of one wait, the Lead
// gets one event.
func (e *Engine) waitForLowLoad(ctx context.Context, a *Agent, j *job) error {
	load, ready, err := e.startCheck()
	if err != nil || ready {
		return err
	}
	if err := a.checkPhase(ctx, lowLoadReason, fmt.Sprintf("The check waits for a low load. The 1-minute load average is %.2f, and the machine has %d cores.", load, cores)); err != nil {
		return err
	}
	began := time.Now()
	reported := false
	ticker := time.NewTicker(loadPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		load, ready, err = e.startCheck()
		if err != nil || ready {
			return err
		}
		if !reported && time.Since(began) >= loadWaitEvent {
			reported = true
			text := fmt.Sprintf("%s long wait for a low load of #%d \"%s\": the .mobius/check waited %d minutes. The 1-minute load average is %.2f, and the machine has %d cores.",
				time.Now().UTC().Format(timeFormat), j.task.Issue, j.title, int(loadWaitEvent.Minutes()), load, cores)
			if err := e.addLeadEvent(ctx, j.task.Repository, j.task.Workstream, sql.NullInt64{Int64: j.task.Issue, Valid: true}, "load_wait", text); err != nil {
				return err
			}
		}
	}
}
