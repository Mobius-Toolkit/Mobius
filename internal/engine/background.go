package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
)

// quietPoll is the time between two checks of waitQuiet.
const quietPoll = 20 * time.Millisecond

// absorbTimeout is the time with no work update after an autonomous end, while a prompt runs, after which Mobius
// takes the prompt as absorbed into the autonomous turn.
var absorbTimeout = 30 * time.Second

// backgroundCheckInterval is the time between the end of a turn of waitBackground that left a background task running
// and the next wait prompt.
var backgroundCheckInterval = 5 * time.Minute

// backgroundTimeout is the total time after which waitBackground stops to wait for the background tasks.
var backgroundTimeout = time.Hour

// backgroundPrompt is the prompt of waitBackground.
const backgroundPrompt = "Before your final report, check each background task that you started. Wait for each task that still runs, for example with `TaskOutput` and `block`. Then call `background_state` with `done` and give your full final report. If a task still runs at the end of this turn, call `background_state` with `running`."

// implementerBackgroundPrompt is the prompt of waitBackground for the Implementer. The Implementer gives no final report.
const implementerBackgroundPrompt = "Before the local check, check each background task that you started. Wait for each task that still runs, for example with `TaskOutput` and `block`. Commit all work. Then call `background_state` with `done` and end the turn. If a task still runs at the end of this turn, call `background_state` with `running`."

// chatBackgroundPrompt is the prompt of waitChatBackground. The reply text of the turn with done goes to the chat.
const chatBackgroundPrompt = "Check each background task that you started. Wait for each task that still runs, for example with `TaskOutput` and `block`. Then call `background_state` with `done`, and write in your reply only what the Owner must know from their results. If a task still runs at the end of this turn, call `background_state` with `running`."

// backgroundRoles are the Roles that wait for the background tasks of their session before the work of the session
// ends. They get the tool background_state.
var backgroundRoles = []string{ResearcherRole, JudgeRole, DiagnoserRole, CuratorRole, TriagerRole, ReviewerRole, ImplementerRole, LeadRole, CoordinatorRole}

// errStopped is the error of waitQuiet when a stop ends the wait.
var errStopped = errors.New("a stop ended the wait for an autonomous turn")

// errCannotDone is the error of waitQuiet when the agent called cannot_do in the autonomous turn.
var errCannotDone = errors.New("the agent called cannot_do in an autonomous turn")

// autonomousOrigins are the origin kinds of the end of an autonomous turn. Any other kind ends a turn of a user.
var autonomousOrigins = []string{"task-notification", "peer", "coordinator", "observer", "observer-activity"}

// track follows the autonomous turns of a Claude Code agent. An autonomous turn is a turn that the CLI starts alone,
// for example when a background task ends. The caller holds a.mu.
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
			a.autonomous = true
		}
		if kind == "tool_call" || kind == "tool_call_update" {
			a.markBackground(update)
		}
		if kind == "tool_call_update" && running && field(update, "_meta", "claudeCode", "toolResponse", "isAsync") == true {
			a.subagent = true
		}
	case "usage_update":
		origin := stringField(update, "_meta", "_claude/origin", "kind")
		if !slices.Contains(autonomousOrigins, origin) {
			return
		}
		a.autonomous = false
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

// markBackground sets a.background when the tool call update starts a background task of the session: an async result
// of the Agent tool, or a tool call with run_in_background. The mark parentToolUseId can be only on the first update
// of a tool call, so the session keeps the calls of subagents. A task of a subagent is no start: the CLI sends its end
// to the subagent. The caller holds a.mu.
func (a *Agent) markBackground(update any) {
	id := stringField(update, "toolCallId")
	if field(update, "_meta", "claudeCode", "parentToolUseId") != nil {
		if a.subagentCalls == nil {
			a.subagentCalls = map[string]struct{}{}
		}
		a.subagentCalls[id] = struct{}{}
	}
	if _, subagent := a.subagentCalls[id]; subagent {
		return
	}
	if field(update, "_meta", "claudeCode", "toolResponse", "isAsync") == true || field(update, "rawInput", "run_in_background") == true {
		a.background = true
	}
}

type backgroundStateInput struct {
	State string `json:"state"`
}

func (e *Engine) backgroundStateTool(c caller) mcp.Tool {
	return tool(e, c, "background_state",
		"Tell Mobius if a background task of this session still runs. Call it only when Mobius asks. Mobius continues to wait while you give the state running.",
		map[string]any{
			"state": map[string]any{"type": "string", "enum": []string{"running", "done"}, "description": "running: a background task still runs. done: each background task ended."},
		},
		e.backgroundState)
}

// backgroundState records the state of the background tasks on the session of c.
func (e *Engine) backgroundState(_ context.Context, c caller, _ github.Repository, input backgroundStateInput) (string, error) {
	if input.State != "running" && input.State != "done" {
		return "", refuse("state must be running or done.")
	}
	c.agent.setBackground(input.State == "running")
	return "Mobius has the state " + input.State + ".", nil
}

// setBackground records the state that the agent gave with background_state.
func (a *Agent) setBackground(running bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.background = running
}

// waitBackground is the step of a Role that waits for the background tasks of its session after a Prompt. When the
// session started a background task, it sends prompt, and again each backgroundCheckInterval, until the agent calls
// background_state with done. Prompt holds while an autonomous turn runs. After backgroundTimeout, waitBackground adds
// a note and ends. The reply text of the session is the reply text of the last turn. New details end the wait at once,
// also during the sleep between two prompts, so that the caller sends them. A stop ends the wait with no error: the
// caller reads ctx.
func (a *Agent) waitBackground(ctx context.Context, prompt string) error {
	started := time.Now()
	for {
		a.mu.Lock()
		waiting := a.background && len(a.details) == 0
		a.mu.Unlock()
		if !waiting {
			return nil
		}
		if err := a.Prompt(ctx, prompt, nil); err != nil {
			return err
		}
		a.mu.Lock()
		waiting = a.background && len(a.details) == 0
		a.mu.Unlock()
		if !waiting || ctx.Err() != nil {
			return nil
		}
		if time.Since(started) >= backgroundTimeout {
			return a.addNote(ctx, backgroundTimeoutNote())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-a.wake:
		case <-time.After(backgroundCheckInterval):
		}
	}
}

func backgroundTimeoutNote() string {
	return fmt.Sprintf("A background task still ran after %s. Mobius ends the wait for the background tasks.", backgroundTimeout)
}

// waitQuiet holds until no autonomous turn runs. When the agent has no activity for hangTimeout, the agent is stuck in
// the autonomous turn: waitQuiet sends the cancel, forgets the turn, and gives errHung. A stop gives errStopped. A
// cannot_do of the agent in the autonomous turn gives errCannotDone, because the cancel of cannot_do may end that
// turn with no autonomous end.
func (a *Agent) waitQuiet(ctx context.Context) error {
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
		busy := a.autonomous
		if a.activity.After(last) {
			last = a.activity
		}
		hung := busy && time.Since(last) >= hangTimeout
		if hung {
			a.autonomous = false
		}
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
