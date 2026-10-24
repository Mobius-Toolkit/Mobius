package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const mandatoryTodosReason = "Mobius merges after the mandatory todos are done."

// addDeploymentTodos adds an Inbox todo item and a deployment_todos row for each stored deployment need of the task that
// has no todo yet. It gives the names of the new todos. A todo exists for each (kind, name) of a task, so a later
// approval adds a todo only for a new need.
func (e *Engine) addDeploymentTodos(ctx context.Context, repository github.Repository, task store.Task, pullRequest int64) ([]string, error) {
	needs, err := e.queries.ListDeploymentNeeds(ctx, task.ID)
	if err != nil || len(needs) == 0 {
		return nil, err
	}
	todos, err := e.queries.ListDeploymentTodos(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	workstream, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: task.WorkstreamRepository, Number: task.Workstream})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var names []string
	for _, need := range needs {
		if slices.ContainsFunc(todos, func(todo store.DeploymentTodo) bool { return todo.Kind == need.Kind && todo.Name == need.Name }) {
			continue
		}
		importance := "optional"
		if need.Mandatory == 1 {
			importance = "mandatory"
		}
		var item store.InboxItem
		err := e.inTx(ctx, func(queries *store.Queries) error {
			var err error
			item, err = queries.AddInboxItem(ctx, store.AddInboxItemParams{
				Kind:                 todoKind,
				Organization:         repository.Owner(),
				Repository:           task.Repository,
				WorkstreamRepository: task.WorkstreamRepository,
				Workstream:           task.Workstream,
				Issue:                task.Issue,
				Title:                need.Name,
				Reason:               importance + " · " + workstream.Title,
				Text:                 fmt.Sprintf("%s: %s\n%s\n%s:%d", need.Kind, need.Name, need.Reason, need.Path, need.Line),
				Link:                 issueLink(task.Repository, task.Issue),
				PullRequest:          pullRequest,
				Time:                 now(),
			})
			if err != nil {
				return err
			}
			return queries.AddDeploymentTodo(ctx, store.AddDeploymentTodoParams{
				Task: task.ID, InboxItem: item.ID, Kind: need.Kind, Name: need.Name, Path: need.Path,
				Line: need.Line, Reason: need.Reason, Mandatory: need.Mandatory,
			})
		})
		if err != nil {
			return names, err
		}
		e.publish(Change{Inbox: &item})
		todos = append(todos, store.DeploymentTodo{Kind: need.Kind, Name: need.Name})
		names = append(names, need.Name)
	}
	return names, nil
}

// blockedByTodos tells if a mandatory deployment need of the task has a todo that is not done.
func (e *Engine) blockedByTodos(ctx context.Context, task store.Task) (bool, error) {
	needs, err := e.queries.ListDeploymentNeeds(ctx, task.ID)
	if err != nil || len(needs) == 0 {
		return false, err
	}
	todos, err := e.queries.ListDeploymentTodos(ctx, task.ID)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(needs, func(need store.DeploymentNeed) bool {
		return need.Mandatory == 1 && slices.ContainsFunc(todos, func(todo store.DeploymentTodo) bool {
			return todo.Kind == need.Kind && todo.Name == need.Name && !todo.DoneAt.Valid
		})
	}), nil
}

// clearBlockReason removes the reason mandatoryTodosReason from the open ready for review Inbox items of the task.
func (e *Engine) clearBlockReason(ctx context.Context, task store.Task) error {
	items, err := e.openReadyItems(ctx, task)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(items, func(item store.InboxItem) bool { return item.MergeRefusal == mandatoryTodosReason }) {
		return nil
	}
	return e.refuseMergeItem(ctx, task, "")
}

// DoneInboxItem stores the note and the time on the todo of the open todo Inbox item id, and dismisses the item. It
// gives the changed item. When the todo is for a mandatory need of the current list and no mandatory todo of its task is open, the task gets the
// automatic merge as after a change of the Autopilot level to a merge level. It dismisses the item only after that
// step succeeds, so the Owner can mark the todo as done again after an error. It returns a refusal when the item is
// not an open todo.
func (e *Engine) DoneInboxItem(ctx context.Context, id int64, note string) (store.InboxItem, error) {
	item, err := e.queries.GetInboxItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.InboxItem{}, ErrNoInboxItem
	}
	if err != nil {
		return store.InboxItem{}, err
	}
	if item.Kind != todoKind || item.DismissedAt.Valid {
		return store.InboxItem{}, refuse("This item is not an open todo.")
	}
	todo, err := e.queries.GetDeploymentTodoOfItem(ctx, id)
	if err != nil {
		return store.InboxItem{}, err
	}
	ctx = context.WithoutCancel(ctx)
	if err := e.queries.SetDeploymentTodoDone(ctx, store.SetDeploymentTodoDoneParams{Note: note, DoneAt: sql.NullString{String: now(), Valid: true}, ID: todo.ID}); err != nil {
		return store.InboxItem{}, err
	}
	if err := e.mergeAfterTodos(ctx, item, todo); err != nil {
		return store.InboxItem{}, err
	}
	if err := e.Dismiss(ctx, id); err != nil {
		return store.InboxItem{}, err
	}
	e.addRecord(ctx, inboxChat(item), fmt.Sprintf("You marked the todo \"%s\" of PR %s as done.", item.Title, eventRef(item.WorkstreamRepository, item.Repository, item.PullRequest)))
	return e.queries.GetInboxItem(ctx, id)
}

// mergeAfterTodos gives the task of the done todo the automatic merge, when the todo is for a mandatory need of the
// current list, its Workstream has a merge level and no mandatory todo of the task is open.
func (e *Engine) mergeAfterTodos(ctx context.Context, item store.InboxItem, todo store.DeploymentTodo) error {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
	if errors.Is(err, sql.ErrNoRows) || err == nil && task.ID != todo.Task {
		return nil
	}
	if err != nil {
		return err
	}
	needs, err := e.queries.ListDeploymentNeeds(ctx, task.ID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(needs, func(need store.DeploymentNeed) bool {
		return need.Mandatory == 1 && need.Kind == todo.Kind && need.Name == todo.Name
	}) {
		return nil
	}
	level, err := e.workstreamLevel(ctx, task.WorkstreamRepository, task.Workstream)
	if err != nil || !mergesPullRequests(level) {
		return err
	}
	blocked, err := e.blockedByTodos(ctx, task)
	if err != nil || blocked {
		return err
	}
	return e.approveWaitingTask(ctx, task)
}
