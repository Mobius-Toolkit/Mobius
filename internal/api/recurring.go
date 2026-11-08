package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// RecurringTaskRun is a time of a recurring task that gave an event or was skipped.
type RecurringTaskRun struct {
	// Time is the time of the run
	Time time.Time `gork:"time"`
	// Result is event when the Lead got an event, or skipped
	Result string `gork:"result" validate:"oneof=event skipped"`
	// Reason is the reason of a skipped time, or empty
	Reason string `gork:"reason"`
}

// RecurringTask is a recurring task of a Workstream.
type RecurringTask struct {
	// ID is the id of the recurring task
	ID int64 `gork:"id"`
	// Cron is the cron expression with five fields, in the local time zone of the Mobius server
	Cron string `gork:"cron"`
	// Text is the text that the Lead gets at each time
	Text string `gork:"text"`
	// Paused is true when the task gives no event
	Paused bool `gork:"paused"`
	// Next is the next time of the task, or null for a paused task
	Next *time.Time `gork:"next"`
	// Runs are the last 20 times of the task, newest first
	Runs []RecurringTaskRun `gork:"runs"`
}

func recurringTaskOf(task engine.RecurringTask) RecurringTask {
	runs := make([]RecurringTaskRun, 0, len(task.Runs))
	for _, run := range task.Runs {
		runs = append(runs, RecurringTaskRun{Time: run.Time, Result: run.Result, Reason: run.Reason})
	}
	found := RecurringTask{ID: task.ID, Cron: task.Cron, Text: task.Text, Paused: task.Paused, Runs: runs}
	if !task.Next.IsZero() {
		found.Next = &task.Next
	}
	return found
}

// ListRecurringTasksRequest is the request of ListRecurringTasks.
type ListRecurringTasksRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the Workstream issue
		Number int64 `gork:"number"`
	}
}

// RecurringTasksList is the tasks of a Workstream.
type RecurringTasksList struct {
	// Tasks are the recurring tasks in id order
	Tasks []RecurringTask `gork:"tasks"`
}

// ListRecurringTasksResponse is the response of ListRecurringTasks.
type ListRecurringTasksResponse struct {
	Body Envelope[RecurringTasksList]
}

// ListRecurringTasks returns the recurring tasks of the Workstream with their last 20 times, in id order.
func (h *handlers) ListRecurringTasks(ctx context.Context, req ListRecurringTasksRequest) (*ListRecurringTasksResponse, error) {
	tasks, err := h.engine.RecurringTasks(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number)
	if err != nil {
		return nil, err
	}
	found := make([]RecurringTask, 0, len(tasks))
	for _, task := range tasks {
		found = append(found, recurringTaskOf(task))
	}
	return &ListRecurringTasksResponse{Body: Envelope[RecurringTasksList]{Data: RecurringTasksList{Tasks: found}}}, nil
}

// SetRecurringTaskPausedRequest is the request of SetRecurringTaskPaused.
type SetRecurringTaskPausedRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the Workstream issue
		Number int64 `gork:"number"`
		// ID is the id of the recurring task
		ID int64 `gork:"id"`
	}
	Body struct {
		// Paused is true to pause the task and false to resume it
		Paused bool `gork:"paused"`
	}
}

// SetRecurringTaskPausedResponse is the response of SetRecurringTaskPaused.
type SetRecurringTaskPausedResponse struct {
	Body Envelope[RecurringTask]
}

// SetRecurringTaskPaused pauses or resumes a recurring task and gives the Lead the quiet event recurring_change. A
// task that already has the state stays as it is and gives no event. A resume gives no event for the times of the
// pause. It returns 404 for an id that is not a task of the Workstream.
func (h *handlers) SetRecurringTaskPaused(ctx context.Context, req SetRecurringTaskPausedRequest) (*SetRecurringTaskPausedResponse, error) {
	task, err := h.engine.OwnerSetRecurringTaskPaused(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number, req.Path.ID, req.Body.Paused)
	if errors.Is(err, engine.ErrNoRecurringTask) {
		return nil, api.NewHTTPError(http.StatusNotFound, "The Workstream has no such recurring task.")
	}
	if err != nil {
		return nil, err
	}
	return &SetRecurringTaskPausedResponse{Body: Envelope[RecurringTask]{Data: recurringTaskOf(task)}}, nil
}

// DeleteRecurringTaskRequest is the request of DeleteRecurringTask.
type DeleteRecurringTaskRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the Workstream issue
		Number int64 `gork:"number"`
		// ID is the id of the recurring task
		ID int64 `gork:"id"`
	}
}

// DeleteRecurringTask deletes a recurring task with its history and gives the Lead the quiet event recurring_change.
// It returns 404 for an id that is not a task of the Workstream.
func (h *handlers) DeleteRecurringTask(ctx context.Context, req DeleteRecurringTaskRequest) error {
	err := h.engine.OwnerDeleteRecurringTask(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number, req.Path.ID)
	if errors.Is(err, engine.ErrNoRecurringTask) {
		return api.NewHTTPError(http.StatusNotFound, "The Workstream has no such recurring task.")
	}
	return err
}
