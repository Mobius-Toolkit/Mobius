package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	recurringKind       = "recurring"
	recurringChangeKind = "recurring_change"
)

const (
	recurringEvent   = "event"
	recurringSkipped = "skipped"
	recurringManual  = "manual"
)

const overlapReason = "the earlier event is not delivered yet"

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ErrNoRecurringTask is the error of a recurring task id that is not a task of the Workstream.
var ErrNoRecurringTask = errors.New("the Workstream has no such recurring task")

// ErrRecurringEventPending is the error of a run now of a recurring task whose earlier event is not delivered yet.
var ErrRecurringEventPending = errors.New("the earlier event of the recurring task is not delivered yet")

// ErrNoRecurringWorkstream is the error of a Workstream that is not an open Workstream of the local copy.
var ErrNoRecurringWorkstream = errors.New("the Workstream is not an open Workstream")

// ErrEmptyRecurringText is the error of a recurring task with no text.
var ErrEmptyRecurringText = errors.New("the text of the recurring task is empty")

// CronError is the error of a wrong cron expression.
type CronError struct {
	Cron string
	Err  error
}

func (c *CronError) Error() string {
	return fmt.Sprintf("the cron expression %q is wrong: %v", c.Cron, c.Err)
}

func (c *CronError) Unwrap() error { return c.Err }

// RecurringTask is a recurring task of a Workstream. Next is the zero time for a paused task. Runs are the newest
// first.
type RecurringTask struct {
	ID     int64
	Cron   string
	Text   string
	Paused bool
	Next   time.Time
	Runs   []RecurringTaskRun
}

// RecurringTaskRun is a time of a recurring task that gave an event or was skipped.
type RecurringTaskRun struct {
	Time   time.Time
	Result string
	Reason string
}

func parseCron(expression string) (cron.Schedule, error) {
	if strings.HasPrefix(expression, "TZ=") || strings.HasPrefix(expression, "CRON_TZ=") {
		return nil, &CronError{Cron: expression, Err: errors.New("the time zone is the local time zone of the server")}
	}
	schedule, err := cronParser.Parse(expression)
	if err != nil {
		return nil, &CronError{Cron: expression, Err: err}
	}
	return schedule, nil
}

// nextCronTime gives the first time of the cron expression after t, in the local time zone. It gives the zero time
// for an expression that has no time in the next years.
func nextCronTime(expression string, t time.Time) (time.Time, error) {
	schedule, err := parseCron(expression)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(t.In(time.Local)), nil
}

// NextTimes gives the next n times of the cron expression after the engine time.
func (e *Engine) NextTimes(expression string, n int) ([]time.Time, error) {
	var times []time.Time
	t := e.timeNow()
	for range n {
		next, err := nextCronTime(expression, t)
		if err != nil {
			return nil, err
		}
		if next.IsZero() {
			break
		}
		times = append(times, next)
		t = next
	}
	return times, nil
}

// RecurringTasks gives the recurring tasks of the Workstream in id order.
func (e *Engine) RecurringTasks(ctx context.Context, repository string, workstream int64) ([]RecurringTask, error) {
	rows, err := e.queries.ListRecurringTasks(ctx, store.ListRecurringTasksParams{Repository: repository, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	tasks := make([]RecurringTask, 0, len(rows))
	for _, row := range rows {
		task, err := e.recurringTask(ctx, row)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (e *Engine) recurringTask(ctx context.Context, row store.RecurringTask) (RecurringTask, error) {
	task := RecurringTask{ID: row.ID, Cron: row.Cron, Text: row.Text, Paused: row.Paused != 0}
	if !task.Paused {
		next, err := nextCronTime(row.Cron, e.timeNow())
		if err != nil {
			return RecurringTask{}, err
		}
		task.Next = next
	}
	runs, err := e.queries.ListRecurringTaskRuns(ctx, row.ID)
	if err != nil {
		return RecurringTask{}, err
	}
	for _, run := range runs {
		t, err := time.Parse(time.RFC3339Nano, run.Time)
		if err != nil {
			return RecurringTask{}, err
		}
		task.Runs = append(task.Runs, RecurringTaskRun{Time: t, Result: run.Result, Reason: run.Reason})
	}
	return task, nil
}

func checkRecurringTask(expression, text string) error {
	if _, err := parseCron(expression); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return ErrEmptyRecurringText
	}
	return nil
}

// AddRecurringTask adds a recurring task to an open Workstream of the local copy. Its first time is the first cron
// time after now.
func (e *Engine) AddRecurringTask(ctx context.Context, repository string, workstream int64, expression, text string) (RecurringTask, error) {
	if err := checkRecurringTask(expression, text); err != nil {
		return RecurringTask{}, err
	}
	open, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: repository, Number: workstream})
	if err != nil {
		return RecurringTask{}, err
	}
	if !open {
		return RecurringTask{}, ErrNoRecurringWorkstream
	}
	row, err := e.queries.AddRecurringTask(ctx, store.AddRecurringTaskParams{
		Repository: repository,
		Workstream: workstream,
		Cron:       expression,
		Text:       text,
		CheckedAt:  e.timeNow().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return RecurringTask{}, err
	}
	e.publishRecurring(repository, workstream)
	return e.recurringTask(ctx, row)
}

func (e *Engine) publishRecurring(repository string, workstream int64) {
	e.publish(Change{RecurringTasks: &RecurringChange{Repository: repository, Number: workstream}})
}

// UpdateRecurringTask changes the cron expression and the text of a recurring task. A new cron expression gives no
// event for a time before the change.
func (e *Engine) UpdateRecurringTask(ctx context.Context, repository string, workstream, id int64, expression, text string) (RecurringTask, error) {
	if err := checkRecurringTask(expression, text); err != nil {
		return RecurringTask{}, err
	}
	row, err := e.queries.UpdateRecurringTask(ctx, store.UpdateRecurringTaskParams{
		Cron:       expression,
		CheckedAt:  e.timeNow().UTC().Format(time.RFC3339Nano),
		Text:       text,
		ID:         id,
		Repository: repository,
		Workstream: workstream,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return RecurringTask{}, ErrNoRecurringTask
	}
	if err != nil {
		return RecurringTask{}, err
	}
	e.publishRecurring(repository, workstream)
	return e.recurringTask(ctx, row)
}

// DeleteRecurringTask deletes a recurring task and its history.
func (e *Engine) DeleteRecurringTask(ctx context.Context, repository string, workstream, id int64) error {
	err := e.inTx(ctx, func(q *store.Queries) error {
		deleted, err := q.DeleteRecurringTask(ctx, store.DeleteRecurringTaskParams{ID: id, Repository: repository, Workstream: workstream})
		if err != nil {
			return err
		}
		if deleted == 0 {
			return ErrNoRecurringTask
		}
		return q.DeleteRecurringTaskRuns(ctx, id)
	})
	if err == nil {
		e.publishRecurring(repository, workstream)
	}
	return err
}

// SetRecurringTaskPaused pauses or resumes a recurring task. A resume gives no event for the times of the pause.
func (e *Engine) SetRecurringTaskPaused(ctx context.Context, repository string, workstream, id int64, paused bool) (RecurringTask, error) {
	row, err := e.queries.SetRecurringTaskPaused(ctx, store.SetRecurringTaskPausedParams{
		Paused:     paused,
		CheckedAt:  e.timeNow().UTC().Format(time.RFC3339Nano),
		ID:         id,
		Repository: repository,
		Workstream: workstream,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return RecurringTask{}, ErrNoRecurringTask
	}
	if err != nil {
		return RecurringTask{}, err
	}
	e.publishRecurring(repository, workstream)
	return e.recurringTask(ctx, row)
}

// OwnerSetRecurringTaskPaused is SetRecurringTaskPaused for the Owner. A task that already has the state changes
// nothing and gives no Lead event. Else the Lead gets the quiet event recurring_change.
func (e *Engine) OwnerSetRecurringTaskPaused(ctx context.Context, repository string, workstream, id int64, paused bool) (RecurringTask, error) {
	row, err := e.queries.GetRecurringTask(ctx, store.GetRecurringTaskParams{ID: id, Repository: repository, Workstream: workstream})
	if errors.Is(err, sql.ErrNoRows) {
		return RecurringTask{}, ErrNoRecurringTask
	}
	if err != nil {
		return RecurringTask{}, err
	}
	if (row.Paused != 0) == paused {
		return e.recurringTask(ctx, row)
	}
	task, err := e.SetRecurringTaskPaused(ctx, repository, workstream, id, paused)
	if err != nil {
		return RecurringTask{}, err
	}
	verb := "resumed"
	if paused {
		verb = "paused"
	}
	return task, e.addRecurringChange(ctx, row, verb)
}

// OwnerDeleteRecurringTask is DeleteRecurringTask for the Owner. The Lead gets the quiet event recurring_change.
func (e *Engine) OwnerDeleteRecurringTask(ctx context.Context, repository string, workstream, id int64) error {
	row, err := e.queries.GetRecurringTask(ctx, store.GetRecurringTaskParams{ID: id, Repository: repository, Workstream: workstream})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRecurringTask
	}
	if err != nil {
		return err
	}
	if err := e.DeleteRecurringTask(ctx, repository, workstream, id); err != nil {
		return err
	}
	return e.addRecurringChange(ctx, row, "deleted")
}

// OwnerRunRecurringTask gives the Lead the event of a recurring task now, also for a paused task. The next scheduled
// time and the pause do not change. It returns ErrRecurringEventPending, and adds no event and no history row, when
// the earlier event of the task is not delivered yet.
func (e *Engine) OwnerRunRecurringTask(ctx context.Context, repository string, workstream, id int64) (RecurringTask, error) {
	row, err := e.queries.GetRecurringTask(ctx, store.GetRecurringTaskParams{ID: id, Repository: repository, Workstream: workstream})
	if errors.Is(err, sql.ErrNoRows) {
		return RecurringTask{}, ErrNoRecurringTask
	}
	if err != nil {
		return RecurringTask{}, err
	}
	if row.Event.Valid {
		pending, err := e.queries.HasUndeliveredLeadEvent(ctx, row.Event.Int64)
		if err != nil {
			return RecurringTask{}, err
		}
		if pending {
			return RecurringTask{}, ErrRecurringEventPending
		}
	}
	t := e.timeNow()
	at := t.UTC().Format(time.RFC3339Nano)
	text := fmt.Sprintf("%s the Owner started the recurring task %d `%s` by hand:\n\n%s", t.UTC().Format(timeFormat), row.ID, row.Cron, quote(row.Text))
	eventID, err := e.addLeadEventAt(ctx, repository, workstream, "", sql.NullInt64{}, recurringKind, text, at, commentRef{})
	if eventID == 0 {
		return RecurringTask{}, err
	}
	if err != nil {
		log.Printf("give the manual event of recurring task %d to the Lead: %v", row.ID, err)
	}
	err = e.inTx(ctx, func(q *store.Queries) error {
		set, err := q.SetRecurringTaskEvent(ctx, store.SetRecurringTaskEventParams{
			Event:      sql.NullInt64{Int64: eventID, Valid: true},
			ID:         id,
			Repository: repository,
			Workstream: workstream,
		})
		if err != nil {
			return err
		}
		if set == 0 {
			return ErrNoRecurringTask
		}
		if err := q.AddRecurringTaskRun(ctx, store.AddRecurringTaskRunParams{RecurringTask: id, Time: at, Result: recurringManual}); err != nil {
			return err
		}
		return q.TrimRecurringTaskRuns(ctx, id)
	})
	if err != nil {
		return RecurringTask{}, err
	}
	e.publishRecurring(repository, workstream)
	return e.recurringTask(ctx, row)
}

func (e *Engine) addRecurringChange(ctx context.Context, task store.RecurringTask, verb string) error {
	text := fmt.Sprintf("%s the Owner %s the recurring task %d `%s`:\n\n%s", e.timeNow().UTC().Format(timeFormat), verb, task.ID, task.Cron, quote(task.Text))
	return e.addLeadEvent(ctx, task.Repository, task.Workstream, "", sql.NullInt64{}, recurringChangeKind, text)
}

// deleteRecurringTasks deletes the recurring tasks of the Workstream and their history.
func (e *Engine) deleteRecurringTasks(ctx context.Context, repository string, workstream int64) error {
	return e.inTx(ctx, func(q *store.Queries) error { return deleteWorkstreamRecurringTasks(ctx, q, repository, workstream) })
}

func deleteWorkstreamRecurringTasks(ctx context.Context, q *store.Queries, repository string, workstream int64) error {
	if err := q.DeleteWorkstreamRecurringTaskRuns(ctx, store.DeleteWorkstreamRecurringTaskRunsParams{Repository: repository, Workstream: workstream}); err != nil {
		return err
	}
	return q.DeleteWorkstreamRecurringTasks(ctx, store.DeleteWorkstreamRecurringTasksParams{Repository: repository, Workstream: workstream})
}

// runRecurringTasks gives each recurring task that is not paused and has a cron time since its last check one Lead
// event. A task whose earlier event is not delivered gets a skipped time instead.
func (e *Engine) runRecurringTasks(ctx context.Context) {
	tasks, err := e.queries.ListAllRecurringTasks(ctx)
	if err != nil {
		log.Printf("list recurring tasks: %v", err)
		return
	}
	for _, task := range tasks {
		if err := e.runRecurringTask(ctx, task); err != nil {
			log.Printf("run recurring task %d: %v", task.ID, err)
		}
	}
}

func (e *Engine) runRecurringTask(ctx context.Context, task store.RecurringTask) error {
	t := e.timeNow()
	checked, err := time.Parse(time.RFC3339Nano, task.CheckedAt)
	if err != nil {
		return err
	}
	next, err := nextCronTime(task.Cron, checked)
	if err != nil {
		return err
	}
	if next.IsZero() || next.After(t) {
		return nil
	}
	at := t.UTC().Format(time.RFC3339Nano)
	pending := false
	if task.Event.Valid {
		pending, err = e.queries.HasUndeliveredLeadEvent(ctx, task.Event.Int64)
		if err != nil {
			return err
		}
	}
	run := store.AddRecurringTaskRunParams{RecurringTask: task.ID, Time: at, Result: recurringEvent}
	advance := store.AdvanceRecurringTaskParams{ID: task.ID, Cron: task.Cron, ReadCheckedAt: task.CheckedAt, CheckedAt: at}
	if pending {
		run.Result = recurringSkipped
		run.Reason = overlapReason
	} else {
		unchanged, err := e.queries.HasUnchangedRecurringTask(ctx, store.HasUnchangedRecurringTaskParams{ID: task.ID, Cron: task.Cron, CheckedAt: task.CheckedAt})
		if err != nil || !unchanged {
			return err
		}
		text := fmt.Sprintf("%s recurring task %d `%s`:\n\n%s", t.UTC().Format(timeFormat), task.ID, task.Cron, quote(task.Text))
		id, err := e.addLeadEventAt(ctx, task.Repository, task.Workstream, "", sql.NullInt64{}, recurringKind, text, at, commentRef{})
		if id == 0 {
			return err
		}
		if err != nil {
			log.Printf("give the event of recurring task %d to the Lead: %v", task.ID, err)
		}
		advance.Event = sql.NullInt64{Int64: id, Valid: true}
	}
	added := false
	err = e.inTx(ctx, func(q *store.Queries) error {
		advanced, err := q.AdvanceRecurringTask(ctx, advance)
		if err != nil || advanced == 0 {
			return err
		}
		if err := q.AddRecurringTaskRun(ctx, run); err != nil {
			return err
		}
		added = true
		return q.TrimRecurringTaskRuns(ctx, task.ID)
	})
	if err == nil && added {
		e.publishRecurring(task.Repository, task.Workstream)
	}
	return err
}
