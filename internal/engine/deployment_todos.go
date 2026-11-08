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

const openTodosReason = "Mobius merges after you close the open todos."

// addDeploymentTodos adds an Inbox todo item and a deployment_todos row for each stored deployment need of the task that
// has no todo yet. A todo exists for each (kind, name) of a task, so a later approval adds a todo only for a new need.
// For a todo with another mandatory value than its need, it sets the value of the need on the row and on the reason of
// its open Inbox item. A dismissed todo whose need is mandatory opens again with a new Inbox item. A done todo stays
// done. It gives the names of the new and the opened again todos.
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
		i := slices.IndexFunc(todos, func(todo store.DeploymentTodo) bool { return todo.Kind == need.Kind && todo.Name == need.Name })
		var todo store.DeploymentTodo
		if i >= 0 {
			todo = todos[i]
		}
		reopen := todo.DismissedAt.Valid && need.Mandatory == 1
		if i >= 0 && (todo.DoneAt.Valid || !reopen && todo.Mandatory == need.Mandatory) {
			continue
		}
		var item store.InboxItem
		err := e.inTx(ctx, func(queries *store.Queries) error {
			var err error
			switch {
			case i < 0 || reopen:
				item, err = queries.AddInboxItem(ctx, store.AddInboxItemParams{
					Kind:                 todoKind,
					Organization:         repository.Owner(),
					Repository:           task.Repository,
					WorkstreamRepository: task.WorkstreamRepository,
					Workstream:           task.Workstream,
					Issue:                task.Issue,
					Title:                need.Name,
					Reason:               todoReason(need, workstream.Title),
					Text:                 fmt.Sprintf("%s: %s\n%s\n%s:%d", need.Kind, need.Name, need.Reason, need.Path, need.Line),
					Link:                 issueLink(task.Repository, task.Issue),
					PullRequest:          pullRequest,
					Time:                 now(),
				})
				if err != nil {
					return err
				}
				if i < 0 {
					return queries.AddDeploymentTodo(ctx, store.AddDeploymentTodoParams{
						Task: task.ID, InboxItem: item.ID, Kind: need.Kind, Name: need.Name, Path: need.Path,
						Line: need.Line, Reason: need.Reason, Mandatory: need.Mandatory, AfterMerge: need.AfterMerge,
					})
				}
				return queries.ReopenDeploymentTodo(ctx, store.ReopenDeploymentTodoParams{Mandatory: need.Mandatory, InboxItem: item.ID, ID: todo.ID})
			default:
				item, err = queries.GetInboxItem(ctx, todo.InboxItem)
				if err != nil {
					return err
				}
				if !item.DismissedAt.Valid {
					item, err = queries.SetInboxItemReason(ctx, store.SetInboxItemReasonParams{Reason: todoReason(need, workstream.Title), ID: item.ID})
					if err != nil {
						return err
					}
				}
				return queries.SetDeploymentTodoMandatory(ctx, store.SetDeploymentTodoMandatoryParams{Mandatory: need.Mandatory, ID: todo.ID})
			}
		})
		if err != nil {
			return names, err
		}
		if !item.DismissedAt.Valid {
			e.publish(Change{Inbox: &item})
		}
		if i < 0 {
			todos = append(todos, store.DeploymentTodo{Kind: need.Kind, Name: need.Name})
		}
		if i < 0 || reopen {
			names = append(names, need.Name)
		}
	}
	return names, nil
}

func todoReason(need store.DeploymentNeed, workstream string) string {
	importance := "optional"
	if need.Mandatory == 1 {
		importance = "mandatory"
	}
	return importance + " · " + workstream
}

// blockedByTodos tells if a deployment need of the task that is not an after-merge need has no closed todo. A mandatory
// todo is closed when it is done. An optional todo is closed when it is done or dismissed. A need with no todo counts
// as open.
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
		if need.AfterMerge == 1 {
			return false
		}
		return !slices.ContainsFunc(todos, func(todo store.DeploymentTodo) bool {
			return todo.Kind == need.Kind && todo.Name == need.Name && (todo.DoneAt.Valid || need.Mandatory == 0 && todo.DismissedAt.Valid)
		})
	}), nil
}

// clearBlockReason removes the reason openTodosReason, mergePausedReason or mergeRetryReason from the open ready
// for review Inbox items of the task.
func (e *Engine) clearBlockReason(ctx context.Context, task store.Task) error {
	items, err := e.openReadyItems(ctx, task)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(items, func(item store.InboxItem) bool {
		return item.MergeRefusal == openTodosReason || item.MergeRefusal == mergePausedReason || item.MergeRefusal == mergeRetryReason
	}) {
		return nil
	}
	return e.refuseMergeItem(ctx, task, "")
}

// DoneInboxItem stores the note and the time on the todo of the open todo Inbox item id, and dismisses the item. It
// gives the changed item. Before it dismisses the item, it runs closeTodoStep. Thus the Owner can mark the todo as done
// again after an error. It returns a refusal when the item is not an open todo.
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
	if err := e.closeTodoStep(ctx, item, todo); err != nil {
		return store.InboxItem{}, err
	}
	if err := e.Dismiss(ctx, id); err != nil {
		return store.InboxItem{}, err
	}
	e.addRecord(ctx, inboxChat(item), fmt.Sprintf("You marked the todo \"%s\" of PR %s as done.", item.Title, eventRef(item.WorkstreamRepository, item.Repository, item.PullRequest)))
	return e.queries.GetInboxItem(ctx, id)
}

// TodoMarks tells if the todo is mandatory and if it is an after-merge todo. The need of the same kind and name in the
// current need list of its task decides. When the list has no such need, the marks of the todo decide.
func (e *Engine) TodoMarks(ctx context.Context, todo store.DeploymentTodo) (mandatory, afterMerge bool, err error) {
	needs, err := e.queries.ListDeploymentNeeds(ctx, todo.Task)
	if err != nil {
		return false, false, err
	}
	i := slices.IndexFunc(needs, func(need store.DeploymentNeed) bool { return need.Kind == todo.Kind && need.Name == todo.Name })
	if i < 0 {
		return todo.Mandatory == 1, todo.AfterMerge == 1, nil
	}
	return needs[i].Mandatory == 1, needs[i].AfterMerge == 1, nil
}

// dismissTodo stores the time on the todo of the open todo Inbox item, runs closeTodoStep, and dismisses the item. It
// returns a refusal when the item is not open or the todo is mandatory.
func (e *Engine) dismissTodo(ctx context.Context, item store.InboxItem) error {
	if item.DismissedAt.Valid {
		return refuse("This item is not an open todo.")
	}
	todo, err := e.queries.GetDeploymentTodoOfItem(ctx, item.ID)
	if err != nil {
		return err
	}
	mandatory, _, err := e.TodoMarks(ctx, todo)
	if err != nil {
		return err
	}
	if mandatory {
		return refuse("A mandatory todo cannot be dismissed. Mark it as done.")
	}
	ctx = context.WithoutCancel(ctx)
	if err := e.queries.SetDeploymentTodoDismissed(ctx, store.SetDeploymentTodoDismissedParams{DismissedAt: sql.NullString{String: now(), Valid: true}, ID: todo.ID}); err != nil {
		return err
	}
	if err := e.closeTodoStep(ctx, item, todo); err != nil {
		return err
	}
	if err := e.Dismiss(ctx, item.ID); err != nil {
		return err
	}
	e.addRecord(ctx, inboxChat(item), fmt.Sprintf("You dismissed the todo \"%s\" of PR %s.", item.Title, eventRef(item.WorkstreamRepository, item.Repository, item.PullRequest)))
	return nil
}

// closeTodoStep acts after a todo closed. It acts only when the live task is the task of the todo, the task is in
// ready_for_review, the todo has a need in the current list that is not an after-merge need, and no todo of the task
// blocks. Then, for an open pull request, it makes a draft ready for review, clears the block reason of the Inbox
// items, and with a merge level of the Workstream it gives the task to approveWaitingTask.
func (e *Engine) closeTodoStep(ctx context.Context, item store.InboxItem, todo store.DeploymentTodo) error {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
	if errors.Is(err, sql.ErrNoRows) || err == nil && (task.ID != todo.Task || task.State != "ready_for_review" || !task.PullRequest.Valid) {
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
		return need.AfterMerge == 0 && need.Kind == todo.Kind && need.Name == todo.Name
	}) {
		return nil
	}
	blocked, err := e.blockedByTodos(ctx, task)
	if err != nil || blocked {
		return err
	}
	repository, err := e.repository(task.Repository)
	if err != nil {
		return err
	}
	pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
	if err != nil || pullRequest.GetState() != "open" {
		return err
	}
	if pullRequest.GetDraft() {
		if err := repository.MarkReadyForReview(ctx, pullRequest.GetNodeID()); err != nil {
			return err
		}
	}
	if err := e.clearBlockReason(ctx, task); err != nil {
		return err
	}
	level, err := e.workstreamLevel(ctx, task.WorkstreamRepository, task.Workstream)
	if err != nil || !mergesPullRequests(level) {
		return err
	}
	return e.approveWaitingTask(ctx, task)
}
