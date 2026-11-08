package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const openTodosReason = "Mobius merges after you close the open todos."

// addDeploymentTodos adds an Inbox todo item and a deployment_todos row for each stored deployment need of the task that
// has no todo yet. A todo exists for each (kind, name) of a task, so a later approval adds a todo only for a new need.
// For a todo with another mandatory value than its need, it sets the value of the need on the row and on the reason of
// its open Inbox item. A dismissed todo whose need is mandatory opens again with a new Inbox item. A done todo stays
// done. It gives the names of the new and the opened again todos. After a change, it sends the open ready for review
// Inbox items of the task to the listeners again.
func (e *Engine) addDeploymentTodos(ctx context.Context, repository github.Repository, task store.Task, pullRequest int64) (names []string, err error) {
	changed := false
	defer func() {
		if changed {
			err = errors.Join(err, e.publishReadyItems(context.WithoutCancel(ctx), task))
		}
	}()
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
		changed = true
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

// blockingTodoItems gives the Inbox item of the todo of each deployment need of the task that is not an after-merge need
// and has no closed todo, oldest first. A mandatory todo is closed when it is done. An optional todo is closed when it
// is done or dismissed. A need with no todo gives the ID 0.
func (e *Engine) blockingTodoItems(ctx context.Context, task store.Task) ([]int64, error) {
	needs, err := e.queries.ListDeploymentNeeds(ctx, task.ID)
	if err != nil || len(needs) == 0 {
		return nil, err
	}
	todos, err := e.queries.ListDeploymentTodos(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	var items []int64
	for _, need := range needs {
		if need.AfterMerge == 1 {
			continue
		}
		i := slices.IndexFunc(todos, func(todo store.DeploymentTodo) bool { return todo.Kind == need.Kind && todo.Name == need.Name })
		switch {
		case i < 0:
			items = append(items, 0)
		case !todos[i].DoneAt.Valid && !(need.Mandatory == 0 && todos[i].DismissedAt.Valid):
			items = append(items, todos[i].InboxItem)
		}
	}
	slices.Sort(items)
	return items, nil
}

// blockedByTodos tells if a deployment need of the task blocks the merge (blockingTodoItems).
func (e *Engine) blockedByTodos(ctx context.Context, task store.Task) (bool, error) {
	items, err := e.blockingTodoItems(ctx, task)
	return len(items) > 0, err
}

// BlockingTodos gives the IDs of the todo Inbox items that block the merge of the pull request of the ready for review
// Inbox item, oldest first. It gives none for another item, a dismissed item, and an item whose issue has no live task
// with the pull request of the item.
func (e *Engine) BlockingTodos(ctx context.Context, item store.InboxItem) ([]int64, error) {
	if item.Kind != readyForReviewKind || item.DismissedAt.Valid {
		return nil, nil
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
	if errors.Is(err, sql.ErrNoRows) || err == nil && task.PullRequest.Int64 != item.PullRequest {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items, err := e.blockingTodoItems(ctx, task)
	return slices.DeleteFunc(items, func(id int64) bool { return id == 0 }), err
}

// publishReadyItems sends each open ready for review Inbox item of the task to the listeners again, because the
// blocking todos of the task changed.
func (e *Engine) publishReadyItems(ctx context.Context, task store.Task) error {
	items, err := e.openReadyItems(ctx, task)
	for _, item := range items {
		e.publish(Change{Inbox: &item})
	}
	return err
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
// gives the changed item. Before it dismisses the item, it runs closeTodoStep and sends the open ready for review Inbox
// items of the task to the listeners again. Thus the Owner can mark the todo as done
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
	if err := e.writeTodosOfItem(ctx, item, todo); err != nil {
		return store.InboxItem{}, err
	}
	if err := e.closeTodoStep(ctx, item, todo); err != nil {
		return store.InboxItem{}, err
	}
	if err := e.publishReadyItemsOfTodo(ctx, item); err != nil {
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

// todoLine gives the checklist line of the todo with the marks of its need.
func todoLine(todo store.DeploymentTodo, need store.DeploymentNeed) string {
	marks := "optional"
	if need.Mandatory == 1 {
		marks = "mandatory"
	}
	if need.AfterMerge == 1 {
		marks += ", after merge"
	}
	switch {
	case todo.DoneAt.Valid:
		return fmt.Sprintf("- [x] %s (%s, done)", todo.Name, marks)
	case todo.DismissedAt.Valid:
		return fmt.Sprintf("- [x] %s (%s, dismissed)", todo.Name, marks)
	}
	return fmt.Sprintf("- [ ] %s (%s, open)", todo.Name, marks)
}

// withTodos gives body with section in place of the todos section of body. Without a todos section, it puts section
// before the screenshots section, or at the end of body. With an empty section, it removes the todos section.
func withTodos(body, section string) string {
	if section != "" {
		return withSection(body, todosStart, todosEnd, section, screenshotsStart)
	}
	start, end, found := sectionBounds(body, todosStart, todosEnd)
	if !found {
		return body
	}
	head := strings.TrimRight(body[:start], "\n")
	tail := strings.TrimLeft(body[end:], "\n")
	if head == "" || tail == "" {
		return head + tail
	}
	return head + "\n\n" + tail
}

// writeTodos writes the todos section of the task in the description of the pull request number. The section has a line
// for each todo whose need is in the current need list of the task. It does nothing when the task has no todo, and
// writes nothing when the description does not change.
func (e *Engine) writeTodos(ctx context.Context, repository github.Repository, taskID, pullRequest int64) error {
	todos, err := e.queries.ListDeploymentTodos(ctx, taskID)
	if err != nil || len(todos) == 0 {
		return err
	}
	needs, err := e.queries.ListDeploymentNeeds(ctx, taskID)
	if err != nil {
		return err
	}
	var lines []string
	for _, todo := range todos {
		if i := slices.IndexFunc(needs, func(need store.DeploymentNeed) bool { return need.Kind == todo.Kind && need.Name == todo.Name }); i >= 0 {
			lines = append(lines, todoLine(todo, needs[i]))
		}
	}
	section := ""
	if len(lines) > 0 {
		section = todosStart + "\n### Deployment todos\n\n" + strings.Join(lines, "\n") + "\n" + todosEnd
	}
	current, err := repository.PullRequest(ctx, pullRequest)
	if err != nil {
		return err
	}
	body := withTodos(current.GetBody(), section)
	if body == current.GetBody() {
		return nil
	}
	return repository.SetPullRequestBody(ctx, pullRequest, body)
}

// writeTodosOfItem writes the todos section of the task of todo in the pull request of the Inbox item.
func (e *Engine) writeTodosOfItem(ctx context.Context, item store.InboxItem, todo store.DeploymentTodo) error {
	repository, err := e.repository(item.Repository)
	if err != nil {
		return err
	}
	return e.writeTodos(ctx, repository, todo.Task, item.PullRequest)
}

// dismissTodo stores the time on the todo of the open todo Inbox item, runs closeTodoStep, sends the open ready for
// review Inbox items of the task to the listeners again, and dismisses the item. It
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
	if err := e.writeTodosOfItem(ctx, item, todo); err != nil {
		return err
	}
	if err := e.closeTodoStep(ctx, item, todo); err != nil {
		return err
	}
	if err := e.publishReadyItemsOfTodo(ctx, item); err != nil {
		return err
	}
	if err := e.Dismiss(ctx, item.ID); err != nil {
		return err
	}
	e.addRecord(ctx, inboxChat(item), fmt.Sprintf("You dismissed the todo \"%s\" of PR %s.", item.Title, eventRef(item.WorkstreamRepository, item.Repository, item.PullRequest)))
	return nil
}

// publishReadyItemsOfTodo runs publishReadyItems for the live task of the issue of the todo Inbox item. It does nothing
// when the issue has no live task.
func (e *Engine) publishReadyItemsOfTodo(ctx context.Context, item store.InboxItem) error {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return e.publishReadyItems(ctx, task)
}

// closeTodoStep acts after a todo closed. It acts only when the live task is the task of the todo, the task is in
// ready_for_review, the todo has a need in the current list that is not an after-merge need, and no todo of the task
// blocks. Then, for an open pull request, it makes a draft ready for review and clears the block reason of the Inbox
// items. A task with an approval gets merging items, because the poll merges it. Else, with a merge level of the
// Workstream, it gives the task to approveWaitingTask.
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
	if awaitsMerge(task) {
		e.mergeMu.Lock()
		defer e.mergeMu.Unlock()
		return e.setMergeItems(ctx, task, 1, "")
	}
	level, err := e.workstreamLevel(ctx, task.WorkstreamRepository, task.Workstream)
	if err != nil || !mergesPullRequests(level) {
		return err
	}
	return e.approveWaitingTask(ctx, task)
}
