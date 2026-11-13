package engine

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
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

// loadWait is a check that waits for a low load. now is the channel that StartCheckNow sends to. ciFix tells that the
// check is the check of a CI fix task. since is the time at which the wait started.
type loadWait struct {
	now   chan struct{}
	ciFix bool
	since time.Time
}

// before tells if the check of the session of w comes before the check of the session other of o. A CI fix check
// comes before each other check. Two CI fix checks come in the order of their wait start, then by session id. The
// other checks have no order.
func (w loadWait) before(session int64, o loadWait, other int64) bool {
	if !w.ciFix || !o.ciFix {
		return w.ciFix
	}
	if !w.since.Equal(o.since) {
		return w.since.Before(o.since)
	}
	return session < other
}

// startCheck tells if the check of the session of own can start now, and gives the load average. A check can start
// when the load average is lower than cores, checkGap passed since the start of the last check, and no other waiting
// check comes before it. A check with no wait yet also counts the waiting CI fix checks. When the check can start,
// startCheck sets the start time of the last check.
func (e *Engine) startCheck(session int64, own loadWait) (float64, bool, error) {
	load, err := loadAverage()
	if err != nil {
		return 0, false, err
	}
	e.loadWaitsMu.Lock()
	defer e.loadWaitsMu.Unlock()
	if load >= float64(cores) {
		return load, false, nil
	}
	for other, w := range e.loadWaits {
		if other != session && w.before(other, own, session) {
			return load, false, nil
		}
	}
	e.checkStartMu.Lock()
	defer e.checkStartMu.Unlock()
	if time.Since(e.checkStart) < checkGap {
		return load, false, nil
	}
	e.checkStart = time.Now()
	delete(e.loadWaits, session)
	return load, true, nil
}

// StartCheckNow ends the wait for a low load of the check of the session, so the check starts at once with no regard
// for the load and for checkGap. The check keeps its check slot.
func (e *Engine) StartCheckNow(_ context.Context, session int64) error {
	e.loadWaitsMu.Lock()
	defer e.loadWaitsMu.Unlock()
	wait, ok := e.loadWaits[session]
	if !ok {
		return refuse("The check of session %d does not wait for a low load.", session)
	}
	select {
	case wait.now <- struct{}{}:
	default:
	}
	return nil
}

// waitForLowLoad holds until startCheck lets the check of a start, or until StartCheckNow. The check of a CI fix task
// starts before each other check. It shows the wait in the queue reason and in the Transcript of a. After
// loadWaitEvent of one wait, the Lead gets one event.
func (e *Engine) waitForLowLoad(ctx context.Context, a *Agent, j *job) (err error) {
	ciFix, err := e.queries.IsCIFixIssue(ctx, store.IsCIFixIssueParams{Repository: j.task.Repository, Issue: j.task.Issue})
	if err != nil {
		return err
	}
	wait := loadWait{now: make(chan struct{}, 1), ciFix: ciFix, since: time.Now()}
	load, ready, err := e.startCheck(a.id, wait)
	if err != nil || ready {
		return err
	}
	startedAt := now()
	defer func() { a.endStep(ctx, "low_load", startedAt, sql.NullInt64{}, err) }()
	e.loadWaitsMu.Lock()
	e.loadWaits[a.id] = wait
	e.loadWaitsMu.Unlock()
	defer func() {
		e.loadWaitsMu.Lock()
		delete(e.loadWaits, a.id)
		e.loadWaitsMu.Unlock()
	}()
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
		case <-wait.now:
			e.checkStartMu.Lock()
			e.checkStart = time.Now()
			e.checkStartMu.Unlock()
			return a.checkPhase(ctx, lowLoadReason, "The Owner started the check at once.")
		case <-ticker.C:
		}
		load, ready, err = e.startCheck(a.id, wait)
		if err != nil || ready {
			return err
		}
		if !reported && time.Since(began) >= loadWaitEvent {
			reported = true
			text := fmt.Sprintf("%s long wait for a low load of %s \"%s\": the .mobius/check waited %d minutes. The 1-minute load average is %.2f, and the machine has %d cores.",
				time.Now().UTC().Format(timeFormat), taskEventRef(j.task, j.task.Issue), j.title, int(loadWaitEvent.Minutes()), load, cores)
			if err := e.addLeadEvent(ctx, j.task.WorkstreamRepository, j.task.Workstream, j.task.Repository, sql.NullInt64{Int64: j.task.Issue, Valid: true}, "load_wait", text); err != nil {
				return err
			}
		}
	}
}
