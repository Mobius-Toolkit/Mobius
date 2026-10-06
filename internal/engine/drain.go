package engine

import (
	"context"
	"log"
	"slices"
	"sync"
)

// drainReason is the queue reason of a Worker that the drain holds.
const drainReason = "Mobius prepares an upgrade"

// DrainEnd tells how a drain ended.
type DrainEnd string

const (
	// Drained tells that no agent of Mobius runs.
	Drained DrainEnd = "drained"
	// Cancelled tells that the Owner cancelled the drain.
	Cancelled DrainEnd = "cancelled"
)

// DrainState is the state of the drain for an upgrade.
type DrainState struct {
	On bool
	// Waiting is the number of sessions that the drain waits for.
	Waiting int
}

// drain is the drain for an upgrade. While on, no Worker takes a slot. running counts each session that the drain
// waits for: a Worker from its slot, and each other session from its start.
type drain struct {
	mu      sync.Mutex
	on      bool
	running int
	// uncounted are the sessions that wait for the end of a pause and that the drain does not count.
	uncounted []*Agent
	// sealed is set by seal: a sealed drain accepts no new session and ignores a cancel.
	sealed bool
	// changed wakes Drain at each change.
	changed signal
}

func (e *Engine) draining() bool {
	d := &e.drain
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.on
}

// sealed tells if the drain is sealed for the restart.
func (e *Engine) sealed() bool {
	d := &e.drain
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sealed
}

// trackWorker counts a Worker that takes a slot. It gives false while the drain is on, so the Worker stays in the queue.
func (e *Engine) trackWorker() bool {
	d := &e.drain
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.on {
		return false
	}
	d.running++
	return true
}

// tryTrack counts a session that is not a Worker. It gives false while the drain is on, so the drain holds the
// session.
func (e *Engine) tryTrack() bool {
	d := &e.drain
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.on {
		return false
	}
	d.running++
	return true
}

// track counts a session that is not a Worker, also while the drain is on. It gives false after seal.
func (e *Engine) track() bool {
	d := &e.drain
	d.mu.Lock()
	if d.sealed {
		d.mu.Unlock()
		return false
	}
	d.running++
	state := DrainState{d.on, d.running}
	d.mu.Unlock()
	if state.On {
		e.publish(Change{Drain: &state})
	}
	return true
}

func (e *Engine) untrack() {
	d := &e.drain
	d.mu.Lock()
	d.running--
	state := DrainState{d.on, d.running}
	d.mu.Unlock()
	if state.On {
		e.publish(Change{Drain: &state})
	}
	d.changed.notify()
}

// setUncounted stops the count of a session of a in the drain, and starts it again with uncounted false. A session that
// the drain does not count has uncounted true. It gives false when the session must count again after seal.
func (a *Agent) setUncounted(uncounted bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case uncounted && a.tracked:
		a.tracked, a.uncounted = false, true
		a.engine.untrack()
		a.engine.holdUncounted(a, true)
	case !uncounted && a.uncounted:
		if !a.engine.track() {
			return false
		}
		a.tracked, a.uncounted = true, false
		a.engine.holdUncounted(a, false)
	}
	return true
}

func (e *Engine) holdUncounted(a *Agent, held bool) {
	d := &e.drain
	d.mu.Lock()
	defer d.mu.Unlock()
	if held {
		d.uncounted = append(d.uncounted, a)
	} else {
		d.uncounted = slices.DeleteFunc(d.uncounted, func(other *Agent) bool { return other == a })
	}
}

// Draining gives the state of the drain.
func (e *Engine) Draining() DrainState {
	d := &e.drain
	d.mu.Lock()
	defer d.mu.Unlock()
	return DrainState{d.on, d.running}
}

// Drain holds each new Worker in the queue, asks each chat to close, and waits until no session runs. A session that
// waits for the end of a pause does not count. A cancel ends the wait. A completed drain stays on, so a cancel still
// releases the held Workers when the restart does not come.
func (e *Engine) Drain(ctx context.Context) (DrainEnd, error) {
	d := &e.drain
	d.mu.Lock()
	d.on = true
	state := DrainState{d.on, d.running}
	d.mu.Unlock()
	// The Workers that wait for a slot show the drain reason.
	e.workers.changed.notify()
	e.pausesChanged.notify()
	e.closeChats()
	e.publish(Change{Drain: &state})
	for {
		changed := d.changed.wait()
		state := e.Draining()
		if !state.On {
			return Cancelled, nil
		}
		if state.Waiting == 0 {
			return Drained, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-changed:
		}
	}
}

// CancelDrain ends the drain: the held Workers take their slots, and the waiting events go to the Leads. It does
// nothing to a sealed drain.
func (e *Engine) CancelDrain() {
	d := &e.drain
	d.mu.Lock()
	sealed := d.sealed
	d.mu.Unlock()
	if !sealed {
		e.releaseDrain()
	}
}

// seal closes a completed drain for the restart: no session starts and a cancel does nothing, until abortDrain.
// It gives Drained when it sealed the drain, Cancelled after a cancel, and "" while a session runs. A sealed drain
// closes the Harness of each session that waits for the end of a pause, because the Harness process goes on after the
// exec of the restart.
func (e *Engine) seal() DrainEnd {
	d := &e.drain
	d.mu.Lock()
	switch {
	case !d.on:
		d.mu.Unlock()
		return Cancelled
	case d.running > 0:
		d.mu.Unlock()
		return ""
	}
	d.sealed = true
	waiting := slices.Clone(d.uncounted)
	d.mu.Unlock()
	for _, a := range waiting {
		a.closeHarness()
	}
	return Drained
}

// abortDrain ends a sealed drain when the restart fails.
func (e *Engine) abortDrain() {
	d := &e.drain
	d.mu.Lock()
	d.sealed = false
	d.mu.Unlock()
	e.releaseDrain()
}

func (e *Engine) releaseDrain() {
	d := &e.drain
	d.mu.Lock()
	if !d.on {
		d.mu.Unlock()
		return
	}
	d.on = false
	d.mu.Unlock()
	d.changed.notify()
	e.publish(Change{Drain: &DrainState{}})
	e.workers.changed.notify()
	e.pausesChanged.notify()
	if err := e.wakeAllEvents(context.Background()); err != nil {
		log.Printf("give the waiting events to the Leads after the drain: %v", err)
	}
}
