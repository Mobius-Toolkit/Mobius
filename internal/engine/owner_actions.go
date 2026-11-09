package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const ownerActionKind = "owner action"

// reportOwnerAction stores the action of the Owner of input on the session of the Reviewer of c. A later call replaces
// the action. endReview pauses the task with it.
func (e *Engine) reportOwnerAction(_ context.Context, c caller, _ github.Repository, input ownerActionInput) (string, error) {
	if empty(input.Action) {
		return "", refuse("action must not be empty.")
	}
	c.agent.mu.Lock()
	defer c.agent.mu.Unlock()
	c.agent.ownerAction = strings.TrimSpace(input.Action)
	return "Stored the action of the Owner. Mobius pauses the task after the review.", nil
}

// pauseForOwnerAction ends the review round of the working task with the action of the Owner that the Reviewer
// reported, and moves the task to owner_action. The same transaction adds the Inbox item and the owner_actions row.
// The open threads stay on the pull request, so the next review sees them. A task that left working during the review
// gets no Inbox item.
func (e *Engine) pauseForOwnerAction(ctx context.Context, repository github.Repository, j *reviewJob, task store.Task, comment int64, open []github.ReviewThread, action string) error {
	if err := setStep(ctx, repository, j.head, "Wait for the Owner", "Mobius waits for the Owner to mark an action as done."); err != nil {
		return err
	}
	result := fmt.Sprintf("The task waits for an action of the Owner: %s. Mobius reviews the head again after the Owner marks the action as done.", action)
	if err := e.endRound(ctx, repository, j, task, comment, result, open); err != nil {
		return err
	}
	workstream, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: task.WorkstreamRepository, Number: task.Workstream})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	number := int64(j.pullRequest.GetNumber())
	var item store.InboxItem
	moved := false
	err = e.inTx(ctx, func(q *store.Queries) error {
		changed, err := q.SetTaskState(ctx, store.SetTaskStateParams{State: "owner_action", ID: task.ID, FromState: "working"})
		if err != nil || changed == 0 {
			return err
		}
		moved = true
		item, err = q.AddInboxItem(ctx, store.AddInboxItemParams{
			Kind:                 ownerActionKind,
			Organization:         repository.Owner(),
			Repository:           task.Repository,
			WorkstreamRepository: task.WorkstreamRepository,
			Workstream:           task.Workstream,
			Issue:                task.Issue,
			Title:                action,
			Reason:               workstream.Title,
			Text:                 fmt.Sprintf("%s\n%s", action, j.pullRequest.GetHTMLURL()),
			Link:                 issueLink(task.Repository, task.Issue),
			PullRequest:          number,
			Time:                 now(),
		})
		if err != nil {
			return err
		}
		return q.AddOwnerAction(ctx, store.AddOwnerActionParams{Task: task.ID, InboxItem: item.ID, Role: ReviewerRole, Action: action, Next: "review", CreatedAt: now()})
	})
	if err != nil || !moved {
		return err
	}
	e.publish(Change{Workstreams: true})
	e.publish(Change{Inbox: &item})
	text := fmt.Sprintf("%s %s \"%s\" waits for an action of the Owner: %s. Pull request %s %s.",
		time.Now().UTC().Format(timeFormat), taskEventRef(task, task.Issue), j.title, action, taskEventRef(task, number), j.pullRequest.GetHTMLURL())
	if err := e.addLeadEvent(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, "owner_action", text); err != nil {
		log.Printf("add the owner action event of %s#%d: %v", task.Repository, task.Issue, err)
	}
	return nil
}

// doneOwnerAction starts the next step of the task of the open owner action Inbox item: the review of the current head
// of its pull request. The task gets zero round counters. Only after the review is queued does it close the action,
// dismiss the item and add a record with the note. A failed step leaves the task in owner_action and the item open.
func (e *Engine) doneOwnerAction(ctx context.Context, item store.InboxItem, note string) (store.InboxItem, error) {
	action, err := e.queries.GetOwnerActionOfItem(ctx, item.ID)
	if err != nil {
		return store.InboxItem{}, err
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
	if errors.Is(err, sql.ErrNoRows) || err == nil && (task.ID != action.Task || task.State != "owner_action" || !task.PullRequest.Valid) {
		return store.InboxItem{}, refuse("The task does not wait for this action.")
	}
	if err != nil {
		return store.InboxItem{}, err
	}
	ctx = context.WithoutCancel(ctx)
	repository, err := e.repository(task.Repository)
	if err != nil {
		return store.InboxItem{}, err
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return store.InboxItem{}, err
	}
	pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
	if err != nil {
		return store.InboxItem{}, err
	}
	parent, err := e.restartParent(ctx, task, ReviewerRole)
	if err != nil {
		return store.InboxItem{}, err
	}
	if err := e.queries.ResetTaskCounters(ctx, task.ID); err != nil {
		return store.InboxItem{}, err
	}
	task.ReviewRounds, task.FixRounds, task.WorkerRestarts = 0, 0, 0
	head := pullRequest.GetHead().GetSHA()
	if err := e.setReviewStep(ctx, repository, head, task, false); err != nil {
		return store.InboxItem{}, err
	}
	queued, err := e.queueTask(ctx, store.QueueTaskParams{QueuedAt: sql.NullString{String: now(), Valid: true}, ID: task.ID, FromState: "owner_action"})
	if err != nil {
		return store.InboxItem{}, err
	}
	if queued == 0 {
		return store.InboxItem{}, refuse("The task does not wait for this action.")
	}
	if err := e.queries.SetTaskWorker(ctx, store.SetTaskWorkerParams{Worker: sql.NullString{String: ReviewerRole, Valid: true}, ID: task.ID}); err != nil {
		return store.InboxItem{}, err
	}
	e.runReviewer(reviewJob{task: task, title: issue.GetTitle(), pullRequest: pullRequest, head: head, parent: parent})
	if err := e.queries.SetOwnerActionDone(ctx, store.SetOwnerActionDoneParams{DoneAt: sql.NullString{String: now(), Valid: true}, ID: action.ID}); err != nil {
		return store.InboxItem{}, err
	}
	if err := e.Dismiss(ctx, item.ID); err != nil {
		return store.InboxItem{}, err
	}
	record := fmt.Sprintf("You marked the action \"%s\" of PR %s as done.", action.Action, eventRef(item.WorkstreamRepository, item.Repository, item.PullRequest))
	if !empty(note) {
		record += " Note: " + strings.TrimSpace(note)
	}
	e.addRecord(ctx, inboxChat(item), record)
	return e.queries.GetInboxItem(ctx, item.ID)
}

// closeOwnerActions sets done_at on each open owner action of the task, and dismisses its Inbox item. The task ended
// or stopped, so no action is left to do.
func (e *Engine) closeOwnerActions(ctx context.Context, task store.Task) error {
	actions, err := e.queries.ListOpenOwnerActionsOfTask(ctx, task.ID)
	if err != nil {
		return err
	}
	for _, action := range actions {
		if err := e.queries.SetOwnerActionDone(ctx, store.SetOwnerActionDoneParams{DoneAt: sql.NullString{String: now(), Valid: true}, ID: action.ID}); err != nil {
			return err
		}
		if err := e.Dismiss(ctx, action.InboxItem); err != nil {
			return err
		}
	}
	return nil
}
