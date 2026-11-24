package engine

import (
	"context"
	"database/sql"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// Wait is the kind of the wait of a task or of a session. The empty Wait means no wait.
type Wait string

const (
	waitPaused           Wait = "paused"
	waitStartOff         Wait = "start_off"
	waitDrain            Wait = "drain"
	waitNoAccess         Wait = "no_access"
	waitSlot             Wait = "slot"
	waitCheckSlot        Wait = "check_slot"
	waitLowLoad          Wait = "low_load"
	waitLocalCheck       Wait = "local_check"
	waitInit             Wait = "init"
	waitHarnessStart     Wait = "harness_start"
	waitCI               Wait = "ci"
	waitLead             Wait = "lead"
	waitOwner            Wait = "owner"
	waitStartImplementer Wait = "start_implementer"
	waitOwnerAction      Wait = "owner_action"
)

// queueWait is the reason that a session waits, with the kind of the wait. The zero value is no wait.
type queueWait struct {
	kind   Wait
	reason string
}

func (w queueWait) none() bool {
	return w == queueWait{}
}

// setQueueWait writes w to the row of the open session id. A zero w clears the queue reason and the wait kind.
func (e *Engine) setQueueWait(ctx context.Context, id int64, w queueWait) (store.Session, error) {
	if w.none() {
		return e.queries.ClearQueueReason(ctx, id)
	}
	return e.queries.SetQueueReason(ctx, store.SetQueueReasonParams{
		QueueReason: sql.NullString{String: w.reason, Valid: true},
		QueueWait:   sql.NullString{String: string(w.kind), Valid: true},
		ID:          id,
	})
}

// waitOf gives the wait of a task in state, or of a session when state is empty. heldEvent tells that the task has a
// held Lead event. session is the wait of the live session of the task, or "". A wait of the session wins over a wait
// of the state. A task in queued waits for a slot until its session shows a wait.
func waitOf(state string, heldEvent bool, session Wait) Wait {
	if session != "" {
		return session
	}
	switch state {
	case "queued":
		return waitSlot
	case "checks":
		return waitCI
	case "approval":
		if heldEvent {
			return waitOwner
		}
		return waitLead
	case "dispatched":
		return waitStartImplementer
	case "owner_action":
		return waitOwnerAction
	}
	return ""
}

// sessionWait is the wait of the live session of a task, and the Harness of that session.
type sessionWait struct {
	harness string
	wait    Wait
}

// sessionWaits gives the wait of the live session of each task in queued or working, by the issue of the task.
func (e *Engine) sessionWaits(ctx context.Context) (map[issueRef]sessionWait, error) {
	rows, err := e.queries.ListTaskWaits(ctx)
	if err != nil {
		return nil, err
	}
	waits := make(map[issueRef]sessionWait, len(rows))
	for _, row := range rows {
		waits[issueRef{row.Repository, row.Issue}] = sessionWait{row.Harness, Wait(row.QueueWait)}
	}
	return waits, nil
}

// taskWait gives the wait of a task in state, and the end of its pause when the wait is waitPaused. session is the wait
// of the live session of the task, or the zero sessionWait.
func (e *Engine) taskWait(ctx context.Context, state string, heldEvent bool, session sessionWait) (Wait, *time.Time, error) {
	wait := waitOf(state, heldEvent, session.wait)
	until, err := e.pausedUntil(ctx, session.harness, wait)
	return wait, until, err
}

// pausedUntil gives the end of the pause of harness when wait is waitPaused, or nil.
func (e *Engine) pausedUntil(ctx context.Context, harness string, wait Wait) (*time.Time, error) {
	if wait != waitPaused {
		return nil, nil
	}
	pause, err := e.harnessPause(ctx, config.Harness(harness))
	if err != nil || pause == nil {
		return nil, err
	}
	return pauseEnd(*pause)
}
