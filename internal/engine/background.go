package engine

import (
	"context"
	"errors"
	"slices"
	"time"
)

// quietPoll is the time between two checks of waitQuiet.
const quietPoll = 20 * time.Millisecond

// absorbTimeout is the time with no work update after an autonomous end, while a prompt runs, after which Mobius
// takes the prompt as absorbed into the autonomous turn.
var absorbTimeout = 30 * time.Second

// errStopped is the error of waitQuiet when a stop ends the wait.
var errStopped = errors.New("a stop ended the wait for an autonomous turn")

// errCannotDone is the error of waitQuiet when the agent called cannot_do in the autonomous turn.
var errCannotDone = errors.New("the agent called cannot_do in an autonomous turn")

// autonomousOrigins are the origin kinds of the end of an autonomous turn. Any other kind ends a turn of a user.
var autonomousOrigins = []string{"task-notification", "peer", "coordinator", "observer", "observer-activity"}

// track follows the autonomous turns of a Claude Code agent. An autonomous turn is a turn that the CLI starts alone,
// for example when a background task ends. The caller holds a.mu.
//
// An async result of the Agent tool (isAsync) starts a background agent. A tool call with run_in_background in its input
// starts a background command. The adapter sends no signal for the end of one task to a client with no subagent or
// async task capability: the end of an autonomous turn with the origin task-notification is the only sign, and it does
// not tell which task ended. Thus the count takes one from the tasks for each such end, whatever the task is, and it
// counts the commands with the agents, so the end of a command is not the end of an agent. The reply text of the
// session then belongs to the last turn, so the first update of an autonomous turn clears the reply text.
//
// A turn that Mobius started sends all its updates before the response of its prompt. Thus a work update while no
// prompt runs, or after the response of the prompt (late), belongs to an autonomous turn.
func (a *Agent) track(notification map[string]any, kind string, late bool) {
	update := notification["update"]
	running := a.turn && !late
	switch kind {
	case "agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update", "plan":
		a.activity = time.Now()
		a.autonomousEnd = time.Time{}
		if !running {
			if !a.autonomous {
				a.reply.Reset()
			}
			a.autonomous = true
		}
		async := kind == "tool_call_update" && field(update, "_meta", "claudeCode", "toolResponse", "isAsync") == true
		if id := stringField(update, "toolCallId"); async || field(update, "rawInput", "run_in_background") == true {
			if _, started := a.backgroundCalls[id]; !started {
				if a.backgroundCalls == nil {
					a.backgroundCalls = map[string]struct{}{}
				}
				a.backgroundCalls[id] = struct{}{}
				a.background++
			}
		}
		if async {
			a.subagent = a.subagent || running
		}
	case "usage_update":
		origin := stringField(update, "_meta", "_claude/origin", "kind")
		if !slices.Contains(autonomousOrigins, origin) {
			return
		}
		a.autonomous = false
		a.background = max(a.background-1, 0)
		// The adapter holds the turn open for a background subagent, so the prompt is not absorbed.
		if running && !a.subagent {
			a.autonomousEnd = time.Now()
			select {
			case a.ended <- struct{}{}:
			default:
			}
		}
	}
}

// waitQuiet holds until no autonomous turn runs. When the agent has no activity for hangTimeout, the agent is stuck in
// the autonomous turn: waitQuiet sends the cancel, forgets the turn, and gives errHung. A stop gives errStopped. A
// cannot_do of the agent in the autonomous turn gives errCannotDone, because the cancel of cannot_do may end that
// turn with no autonomous end.
func (a *Agent) waitQuiet(ctx context.Context) error {
	return a.hold(ctx, false)
}

// waitBackground is the wait of a Role that ends its session after its last Prompt. It holds until no autonomous turn
// runs and no background task of the session runs, with the stop, cannot_do and hang rules of waitQuiet. A stop or a
// cannot_do ends the wait with no error. The end of the wait gives the reply text of the last turn.
//
// The count of the tasks can stay above zero when the CLI folds the ends of two tasks into one autonomous turn. Thus,
// when no autonomous turn runs and the agent has no activity for hangTimeout, the wait ends with no error and the
// session keeps its reply text.
func (a *Agent) waitBackground(ctx context.Context) error {
	err := a.hold(ctx, true)
	switch {
	case errors.Is(err, errStopped):
		a.takeStop()
		return nil
	case errors.Is(err, errCannotDone):
		return nil
	}
	return err
}

// hold is waitQuiet. With background, it also holds while a background task runs.
func (a *Agent) hold(ctx context.Context, background bool) error {
	ticker := time.NewTicker(quietPoll)
	defer ticker.Stop()
	last := time.Now()
	for {
		a.mu.Lock()
		if a.stopRequested {
			a.mu.Unlock()
			return errStopped
		}
		if a.cannotDo != "" {
			a.autonomous = false
			a.mu.Unlock()
			return errCannotDone
		}
		waiting := background && a.background > 0
		if a.activity.After(last) {
			last = a.activity
		}
		idle := time.Since(last) >= hangTimeout
		hung := a.autonomous && idle
		if hung {
			a.autonomous = false
		}
		if hung || waiting && idle {
			a.background = 0
		}
		busy := a.autonomous || waiting && !idle
		a.mu.Unlock()
		if hung {
			_ = a.cancel(ctx)
			return errHung
		}
		if !busy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// watchAbsorbed holds until ctx ends, or until an autonomous turn ended while the prompt runs and no work update came
// for absorbTimeout. In the second case, the CLI took the prompt into the autonomous turn and the prompt gets no
// response. watchAbsorbed then sends the cancel and gives true.
func (a *Agent) watchAbsorbed(ctx context.Context) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-a.ended:
		}
		for {
			a.mu.Lock()
			ended := a.autonomousEnd
			a.mu.Unlock()
			if ended.IsZero() {
				break
			}
			remaining := absorbTimeout - time.Since(ended)
			if remaining <= 0 {
				_ = a.cancel(ctx)
				return true
			}
			select {
			case <-ctx.Done():
				return false
			case <-time.After(remaining):
			}
		}
	}
}
