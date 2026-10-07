package engine

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// quietPoll is the time between two checks of waitQuiet.
const quietPoll = 20 * time.Millisecond

// absorbTimeout is the time with no work update after an autonomous end, while a prompt runs, after which Mobius
// takes the prompt as absorbed into the autonomous turn.
var absorbTimeout = 30 * time.Second

// track follows the background tasks and the autonomous turns of a Claude Code agent. An autonomous turn is a turn
// that the CLI starts alone, for example when a background task ends. The caller holds a.mu.
//
// A turn that Mobius started sends all its updates before the response of its prompt, and Mobius handles them in
// that order. Thus a work update while no prompt runs belongs to an autonomous turn.
func (a *Agent) track(notification map[string]any, kind string) {
	update := notification["update"]
	switch kind {
	case "agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update", "plan":
		a.activity = time.Now()
		a.autonomousEnd = time.Time{}
		if !a.turn {
			a.autonomous = true
		}
	case "usage_update":
		origin := stringField(update, "_meta", "_claude/origin", "kind")
		if origin == "" || origin == "human" {
			return
		}
		a.autonomous = false
		if a.turn {
			a.autonomousEnd = time.Now()
			select {
			case a.ended <- struct{}{}:
			default:
			}
		}
		if origin == "task-notification" && len(a.tasks) > 0 {
			a.tasks = a.tasks[1:]
		}
	}
	if kind == "tool_call_update" {
		a.trackTasks(update)
	}
}

// trackTasks adds the background task that a tool call update starts, and removes the task that a TaskStop or a
// KillShell tool call stops. A Monitor that is persistent has no end that Mobius can wait for, so it does not count.
func (a *Agent) trackTasks(update any) {
	response := field(update, "_meta", "claudeCode", "toolResponse")
	id := stringField(response, "backgroundTaskId")
	if taskID := stringField(response, "taskId"); taskID != "" && field(response, "persistent") != true {
		id = taskID
	}
	if field(response, "isAsync") == true {
		id = stringField(response, "agentId")
	}
	if id != "" && !slices.Contains(a.tasks, id) {
		a.tasks = append(a.tasks, id)
	}
	stopped := ""
	switch stringField(update, "_meta", "claudeCode", "toolName") {
	case "TaskStop":
		stopped = stringField(response, "task_id")
	case "KillShell":
		stopped = stringField(response, "shell_id")
	}
	if index := slices.Index(a.tasks, stopped); stopped != "" && index >= 0 {
		a.tasks = slices.Delete(a.tasks, index, index+1)
	}
}

// waitQuiet holds until no autonomous turn runs and no background task is live. When the agent has no activity for
// hangTimeout, no Mobius prompt runs, so the agent is not stuck in a turn: waitQuiet adds a note, forgets the tasks
// and the turn, and gives nil.
func (a *Agent) waitQuiet(ctx context.Context) error {
	ticker := time.NewTicker(quietPoll)
	defer ticker.Stop()
	last := time.Now()
	for {
		a.mu.Lock()
		busy := a.autonomous || len(a.tasks) > 0
		if a.activity.After(last) {
			last = a.activity
		}
		if busy && time.Since(last) >= hangTimeout {
			a.autonomous = false
			a.tasks = nil
			a.mu.Unlock()
			return a.addNote(ctx, fmt.Sprintf("The agent had no activity for %s while Mobius waited for its background tasks. Mobius continues.", hangTimeout))
		}
		a.mu.Unlock()
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
