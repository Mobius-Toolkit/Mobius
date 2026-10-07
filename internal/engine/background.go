package engine

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

// quietPoll is the time between two checks of waitQuiet.
const quietPoll = 20 * time.Millisecond

// absorbTimeout is the time with no work update after an autonomous end, while a prompt runs, after which Mobius
// takes the prompt as absorbed into the autonomous turn.
var absorbTimeout = 30 * time.Second

// autonomousOrigins are the origin kinds of the end of an autonomous turn. Any other kind ends a turn of a user.
var autonomousOrigins = []string{"task-notification", "peer", "coordinator", "observer", "observer-activity"}

// monitor is a live Monitor of the agent. A Monitor sends each event and its end as a task-notification, so Mobius
// cannot tell the end from an event.
type monitor struct {
	id         string
	persistent bool
	// deadline is the time of the latest end of a Monitor that is not persistent, or zero when it has no timeout.
	deadline time.Time
}

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
		a.expireMonitors()
		origin := stringField(update, "_meta", "_claude/origin", "kind")
		if !slices.Contains(autonomousOrigins, origin) {
			return
		}
		a.autonomous = false
		// The origin has no task id, so the oldest task counts as the one that ended. A Monitor sends one
		// task-notification for each event, so while a Monitor lives, no end belongs to a task for sure.
		if origin == "task-notification" && len(a.tasks) > 0 && len(a.monitors) == 0 {
			a.tasks = a.tasks[1:]
		}
		// A live task at the end means that the turn started it, and the adapter can hold the turn open for it. Then
		// the prompt is not absorbed.
		if a.turn && len(a.tasks) == 0 {
			a.autonomousEnd = time.Now()
			select {
			case a.ended <- struct{}{}:
			default:
			}
		}
	}
	if kind == "tool_call_update" {
		a.trackTasks(update)
	}
}

// trackTasks adds the background task that a tool call update starts, and removes the task that a TaskStop or a
// KillShell tool call stops, or that a BashOutput or a TaskOutput tool call shows with a final status. A turn that
// reads the final status of a task takes the task notification into itself, so no autonomous end follows. A final
// status of a task that is not in the list takes the oldest task: an autonomous end can have taken the id of
// that task for another id. A Monitor that is persistent has no end that Mobius can wait for, so it does not count
// as a task, but it still counts in monitors. A Monitor that is not persistent ends at the latest after its timeout.
func (a *Agent) trackTasks(update any) {
	response := field(update, "_meta", "claudeCode", "toolResponse")
	id := stringField(response, "backgroundTaskId")
	toolName := stringField(update, "_meta", "claudeCode", "toolName")
	if taskID := stringField(response, "taskId"); toolName == "Monitor" && taskID != "" {
		persistent := field(response, "persistent") == true
		if !slices.ContainsFunc(a.monitors, func(m monitor) bool { return m.id == taskID }) {
			started := monitor{id: taskID, persistent: persistent}
			timeout, _ := field(response, "timeoutMs").(json.Number)
			if ms, _ := timeout.Int64(); ms > 0 && !persistent {
				started.deadline = time.Now().Add(time.Duration(ms) * time.Millisecond)
			}
			a.monitors = append(a.monitors, started)
		}
		if !persistent {
			id = taskID
		}
	}
	if field(response, "isAsync") == true {
		id = stringField(response, "agentId")
	}
	if id != "" && !slices.Contains(a.tasks, id) {
		a.tasks = append(a.tasks, id)
	}
	ended := ""
	shown := false
	switch toolName {
	case "TaskStop":
		ended = stringField(response, "task_id")
	case "KillShell":
		ended = stringField(response, "shell_id")
	case "BashOutput":
		if isFinalStatus(stringField(response, "status")) {
			ended = stringField(response, "shellId")
			shown = true
		}
	case "TaskOutput":
		if isFinalStatus(stringField(response, "task", "status")) {
			ended = stringField(response, "task", "task_id")
			shown = true
		}
	}
	if ended == "" {
		return
	}
	if !a.forget(ended) && shown && len(a.tasks) > 0 {
		a.tasks = a.tasks[1:]
	}
}

// forget removes the task and the Monitor with id, and tells if a task was in the list.
func (a *Agent) forget(id string) bool {
	a.monitors = slices.DeleteFunc(a.monitors, func(m monitor) bool { return m.id == id })
	index := slices.Index(a.tasks, id)
	if index >= 0 {
		a.tasks = slices.Delete(a.tasks, index, index+1)
	}
	return index >= 0
}

// expireMonitors removes each Monitor that is not persistent and passed its timeout. The caller holds a.mu.
func (a *Agent) expireMonitors() {
	now := time.Now()
	for _, m := range slices.Clone(a.monitors) {
		if !m.deadline.IsZero() && now.After(m.deadline) {
			a.forget(m.id)
		}
	}
}

func isFinalStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "killed"
}

// waitQuiet holds until no autonomous turn runs and no background task is live. When the agent has no activity for
// hangTimeout, no Mobius prompt runs and the agent is stuck in an autonomous turn or has lost the end of a task:
// waitQuiet sends the cancel, forgets the tasks and the turn, and gives errHung. While a Monitor lives, the end of a
// task is not sure, so the wait can end only because the end of the Monitor has no signal. Then waitQuiet forgets the
// tasks and the Monitors that are not persistent, and gives nil.
func (a *Agent) waitQuiet(ctx context.Context) error {
	ticker := time.NewTicker(quietPoll)
	defer ticker.Stop()
	last := time.Now()
	for {
		a.mu.Lock()
		a.expireMonitors()
		busy := a.autonomous || len(a.tasks) > 0
		if a.activity.After(last) {
			last = a.activity
		}
		hung := busy && time.Since(last) >= hangTimeout
		unsure := hung && !a.autonomous && len(a.monitors) > 0
		if hung {
			a.autonomous = false
			a.tasks = nil
			a.monitors = slices.DeleteFunc(a.monitors, func(m monitor) bool { return !m.persistent })
		}
		a.mu.Unlock()
		if unsure {
			return nil
		}
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
