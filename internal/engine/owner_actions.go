package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	gh "github.com/google/go-github/v92/github"
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
func (e *Engine) pauseForOwnerAction(ctx context.Context, repository github.Repository, j *reviewJob, task store.Task, open []github.ReviewThread, action string) error {
	if err := setStep(ctx, repository, j.head, "Wait for the Owner", "Mobius waits for the Owner to mark an action as done."); err != nil {
		return err
	}
	result := fmt.Sprintf("The task waits for an action of the Owner: %s. Mobius reviews the head again after the Owner marks the action as done.", action)
	if err := e.endRound(ctx, repository, j, task, result, open); err != nil {
		return err
	}
	number := int64(j.pullRequest.GetNumber())
	_, moved, err := e.pauseTask(ctx, repository, task, "working", ReviewerRole, "review", action, fmt.Sprintf("%s\n%s", action, j.pullRequest.GetHTMLURL()), number)
	if err != nil || !moved {
		return err
	}
	text := fmt.Sprintf("%s %s \"%s\" waits for an action of the Owner: %s. Pull request %s %s.",
		time.Now().UTC().Format(timeFormat), taskEventRef(task, task.Issue), j.title, action, taskEventRef(task, number), j.pullRequest.GetHTMLURL())
	if err := e.addLeadEvent(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, "owner_action", text); err != nil {
		log.Printf("add the owner action event of %s#%d: %v", task.Repository, task.Issue, err)
	}
	return nil
}

// pauseTask moves the task from the state from to owner_action. The same transaction adds the owner action Inbox item
// with the text and the owner_actions row. A task with no pull request has the pull request number 0. It tells if the
// task moved: a task that left from before the transaction gets no item.
func (e *Engine) pauseTask(ctx context.Context, repository github.Repository, task store.Task, from, role, next, action, text string, pullRequest int64) (store.InboxItem, bool, error) {
	workstream, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: task.WorkstreamRepository, Number: task.Workstream})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return store.InboxItem{}, false, err
	}
	var item store.InboxItem
	var change int64
	moved := false
	err = e.inTx(ctx, func(q *store.Queries) error {
		changed, err := q.SetTaskState(ctx, store.SetTaskStateParams{State: "owner_action", ID: task.ID, FromState: from})
		if err != nil || changed == 0 {
			return err
		}
		moved = true
		if change, err = addStateLabelsChange(ctx, q, task.Repository, task.Issue); err != nil {
			return err
		}
		item, err = q.AddInboxItem(ctx, store.AddInboxItemParams{
			Kind:                 ownerActionKind,
			Organization:         repository.Owner(),
			Repository:           task.Repository,
			WorkstreamRepository: task.WorkstreamRepository,
			Workstream:           task.Workstream,
			Issue:                task.Issue,
			Title:                action,
			Reason:               workstream.Title,
			Text:                 text,
			Link:                 issueLink(task.Repository, task.Issue),
			PullRequest:          pullRequest,
			Time:                 now(),
		})
		if err != nil {
			return err
		}
		return q.AddOwnerAction(ctx, store.AddOwnerActionParams{Task: task.ID, InboxItem: item.ID, Role: role, Action: action, Next: next, CreatedAt: now()})
	})
	if err != nil || !moved {
		return store.InboxItem{}, false, err
	}
	e.publish(Change{Workstreams: true})
	e.publish(Change{Inbox: &item})
	return item, true, e.sendLabelsNow(ctx, task.Repository, change)
}

// markOwnerAction pauses the dispatched task, or the task with a pull request that waits for CI, for the Lead or for the
// Owner, or that needs a human, until the Owner marks the action as done or the Lead clears it. Then Mobius starts
// the step next. It dismisses the ready for review item or the stopped items of the old state. The state label of the
// old state goes away with the send of the state. No review round runs, so it adds no Lead event.
func (e *Engine) markOwnerAction(ctx context.Context, c caller, repository github.Repository, input markOwnerActionInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	action := strings.TrimSpace(input.Action)
	if action == "" {
		return "", refuse("action must not be empty.")
	}
	if !slices.Contains([]string{"review", "fix_round", "implementer"}, input.Next) {
		return "", refuse("next must be review, fix_round or implementer.")
	}
	task, err := e.workstreamTask(ctx, repository, c.repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	switch {
	case task.State == "asked":
		return "", refuse("The task of #%d waits for an answer to a question.", input.N)
	case !slices.Contains([]string{"dispatched", "checks", "approval", "ready_for_review", "needs_human"}, task.State):
		return "", refuse("The task of #%d is %s, not dispatched, checks, approval, ready_for_review or needs_human.", input.N, task.State)
	case input.Next == "implementer" && task.State != "dispatched":
		return "", refuse("The task of #%d is %s, not dispatched.", input.N, task.State)
	case input.Next != "implementer" && !task.PullRequest.Valid:
		return "", refuse("The task of #%d has no pull request.", input.N)
	case input.Next == "fix_round" && task.State == "dispatched":
		return "", refuse("The task of #%d is dispatched, not checks, approval, ready_for_review or needs_human.", input.N)
	}
	text, number := action, int64(0)
	if task.PullRequest.Valid {
		pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
		if err != nil {
			return "", err
		}
		if task.State == "dispatched" && input.Next == "review" && pullRequest.GetState() != "open" {
			return "", refuse("The pull request of #%d is not open.", input.N)
		}
		text, number = fmt.Sprintf("%s\n%s", action, pullRequest.GetHTMLURL()), task.PullRequest.Int64
	}
	item, moved, err := e.pauseTask(ctx, repository, task, task.State, LeadRole, input.Next, action, text, number)
	if err != nil {
		return "", err
	}
	if !moved {
		return "", refuse("The task of #%d is not %s any more.", input.N, task.State)
	}
	if err := e.cleanAfterPause(ctx, task); err != nil {
		return "", fmt.Errorf("the task of #%d waits for the action now, but the clean up failed: %w", input.N, err)
	}
	return fmt.Sprintf("Paused the task of #%d. Inbox item %d has the action. After the Owner marks it as done, or after clear_owner_action, Mobius starts the step %s.", input.N, item.ID, input.Next), nil
}

// cleanAfterPause dismisses the Inbox items of the state that the task left for owner_action.
func (e *Engine) cleanAfterPause(ctx context.Context, task store.Task) error {
	switch task.State {
	case "needs_human":
		return e.dismissIssueItems(ctx, task.Repository, task.Issue, stoppedKind)
	case "ready_for_review":
		return e.dismissReadyForReview(ctx, task, task.State)
	}
	return nil
}

// doneOwnerAction starts the next step of the task of the open owner action Inbox item. The task gets zero round
// counters. Only after the step started does it close the action, dismiss the item and add the note. A
// failed step leaves the task in owner_action and the item open.
func (e *Engine) doneOwnerAction(ctx context.Context, item store.InboxItem, note string, attachments Attachments) (store.InboxItem, Record, error) {
	action, err := e.queries.GetOwnerActionOfItem(ctx, item.ID)
	if err != nil {
		return store.InboxItem{}, Record{}, err
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
	if errors.Is(err, sql.ErrNoRows) || err == nil && (task.ID != action.Task || task.State != "owner_action") {
		return store.InboxItem{}, Record{}, refuse("The task does not wait for this action.")
	}
	if err != nil {
		return store.InboxItem{}, Record{}, err
	}
	ctx = context.WithoutCancel(ctx)
	if err := e.startAfterOwnerAction(ctx, task, action, note); err != nil {
		return store.InboxItem{}, Record{}, err
	}
	done, err := e.closeOwnerAction(ctx, item, action, note, attachments)
	if err != nil {
		return store.InboxItem{}, Record{}, err
	}
	return done, recordIn(fmt.Sprintf("You marked the action \"%s\" of %s as done.", action.Action, ownerActionSubject(item)), inboxChat(item)), nil
}

// clearOwnerAction starts the next step of the open action of the task, from the Lead or from the Reviewer, as a Done
// of the Owner does.
func (e *Engine) clearOwnerAction(ctx context.Context, c caller, repository github.Repository, input taskInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	task, err := e.workstreamTask(ctx, repository, c.repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	actions, err := e.queries.ListOpenOwnerActionsOfTask(ctx, task.ID)
	if err != nil {
		return "", err
	}
	if len(actions) == 0 || task.State != "owner_action" {
		return "", refuse("The task of #%d has no open action of the Owner.", input.N)
	}
	action := actions[0]
	item, err := e.queries.GetInboxItem(ctx, action.InboxItem)
	if err != nil {
		return "", err
	}
	if err := e.startAfterOwnerAction(ctx, task, action, ""); err != nil {
		return "", err
	}
	if _, err := e.closeOwnerAction(ctx, item, action, "", Attachments{}); err != nil {
		return "", err
	}
	e.AddRecord(ctx, recordIn(fmt.Sprintf("The Lead cleared the action \"%s\" of %s.", action.Action, ownerActionSubject(item)), inboxChat(item)))
	return fmt.Sprintf("Cleared the action of #%d. Mobius started the step %s.", input.N, action.Next), nil
}

// ownerActionSubject names the pull request of the item, or the issue of an item with no pull request.
func ownerActionSubject(item store.InboxItem) string {
	if item.PullRequest == 0 {
		return eventRef(item.WorkstreamRepository, item.Repository, item.Issue)
	}
	return "PR " + eventRef(item.WorkstreamRepository, item.Repository, item.PullRequest)
}

// closeOwnerAction sets done_at on the action after its step started, dismisses its item and adds the Owner message.
func (e *Engine) closeOwnerAction(ctx context.Context, item store.InboxItem, action store.OwnerAction, note string, attachments Attachments) (store.InboxItem, error) {
	if err := e.queries.SetOwnerActionDone(ctx, store.SetOwnerActionDoneParams{DoneAt: sql.NullString{String: now(), Valid: true}, ID: action.ID}); err != nil {
		return store.InboxItem{}, err
	}
	if err := e.Dismiss(ctx, item.ID); err != nil {
		return store.InboxItem{}, err
	}
	e.addInboxOwnerNote(ctx, item, note, attachments)
	return e.queries.GetInboxItem(ctx, item.ID)
}

// startAfterOwnerAction gives the task of the action zero round counters and starts the step of action.Next:
//   - review: a Reviewer of the current head of the pull request.
//   - fix_round: continueTask from owner_action.
//   - implementer: the task moves to dispatched, and the Lead gets a dispatch event with the note of the Owner.
//
// A step that does not start leaves the task in owner_action.
func (e *Engine) startAfterOwnerAction(ctx context.Context, task store.Task, action store.OwnerAction, note string) error {
	repository, err := e.repository(task.Repository)
	if err != nil {
		return err
	}
	issue, err := e.existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return err
	}
	if err := e.queries.ResetTaskCounters(ctx, task.ID); err != nil {
		return err
	}
	task.ReviewRounds, task.FixRounds, task.WorkerRestarts = 0, 0, 0
	if action.Next == "implementer" {
		text := fmt.Sprintf("%s action of the Owner done of %s \"%s\": %s", time.Now().UTC().Format(timeFormat), taskEventRef(task, task.Issue), issue.GetTitle(), action.Action)
		if !empty(note) {
			text += "\nNote of the Owner: " + strings.TrimSpace(note)
		}
		moved, err := e.redispatch(ctx, issue, task, text, commentRef{})
		if err == nil && !moved {
			return refuse("The task does not wait for this action.")
		}
		return err
	}
	if !task.PullRequest.Valid {
		return refuse("The task has no pull request.")
	}
	pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
	if err != nil {
		return err
	}
	if action.Next == "fix_round" {
		moved, err := e.continueTask(ctx, repository, issue.GetTitle(), task, pullRequest, "owner_action")
		if err == nil && !moved {
			return refuse("The task does not wait for this action.")
		}
		return err
	}
	return e.reviewAgain(ctx, repository, issue, task, pullRequest)
}

// reviewAgain queues a review of the current head of the pull request of the task in owner_action.
func (e *Engine) reviewAgain(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task, pullRequest *gh.PullRequest) error {
	parent, err := e.restartParent(ctx, task, ReviewerRole)
	if err != nil {
		return err
	}
	head := pullRequest.GetHead().GetSHA()
	if err := e.setReviewStep(ctx, repository, head, task, false); err != nil {
		return err
	}
	defer e.holdWorker(task.ID)()
	queued, err := e.queueTask(ctx, store.QueueTaskParams{QueuedAt: sql.NullString{String: now(), Valid: true}, ID: task.ID, FromState: "owner_action"})
	if err != nil {
		return err
	}
	if queued == 0 {
		return refuse("The task does not wait for this action.")
	}
	if err := e.setTaskWorker(ctx, store.SetTaskWorkerParams{Worker: sql.NullString{String: ReviewerRole, Valid: true}, ID: task.ID}); err != nil {
		return err
	}
	e.runReviewer(reviewJob{task: task, title: issue.GetTitle(), pullRequest: pullRequest, head: head, parent: parent})
	return nil
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
