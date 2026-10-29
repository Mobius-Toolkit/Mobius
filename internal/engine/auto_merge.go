package engine

import (
	"context"
	"database/sql"
	"errors"
	"log"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const mergeOffReason = "The merge is off for this Workstream."

const mergePausedReason = "Autopilot paused: no automatic merge."

// autoApprove does what MergeInboxItem does, for a Workstream with a merge level: it adds an APPROVED review of the
// Owner on the head, sets auto_merge of the task, and marks the open ready for review Inbox items as merging. The poll
// then merges the pull request (mergeApproved). When it fails, the items get the error text as their reason, so the
// "Merge" button shows, and the task has no auto_merge. When a mandatory deployment need of the task has a todo that is
// not done, it does not approve. The items get the reason mandatoryTodosReason. While the Merge switch is off, it does
// not approve either. The items get the reason mergePausedReason, and the issue goes to the held work of the switch.
// In both cases it gives the reason. An approval that the Owner gave with "Merge" or on GitHub stays, and the task
// does not count as blocked. While the Merge switch is off, that approval merges the pull request as it is.
func (e *Engine) autoApprove(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (string, error) {
	blocked, err := e.blockedByTodos(ctx, task)
	if err != nil {
		return "", err
	}
	if blocked && (task.AutoMerge == 1 || !awaitsMerge(task)) {
		return mandatoryTodosReason, e.refuseMergeItem(ctx, task, mandatoryTodosReason)
	}
	off, err := e.mergeOff(ctx)
	if err != nil {
		return "", err
	}
	if off {
		if task.AutoMerge == 0 && awaitsMerge(task) {
			return "", e.clearBlockReason(ctx, task)
		}
		return mergePausedReason, e.holdMerge(ctx, task)
	}
	err = e.approveAsOwner(ctx, repository, task, pullRequest)
	if err == nil {
		return "", nil
	}
	return "", errors.Join(err, e.refuseMergeItem(ctx, task, err.Error()))
}

// holdMerge records the issue of the task in the held work of the Merge switch, and gives the open ready for review
// Inbox items of the issue the reason mergePausedReason.
func (e *Engine) holdMerge(ctx context.Context, task store.Task) error {
	if err := e.holdWork(ctx, "merge", task.Repository, task.Issue); err != nil {
		return err
	}
	return e.refuseMergeItem(ctx, task, mergePausedReason)
}

// continuePausedMerges gives approveWaitingTask to each task of repository whose open ready for review Inbox item has
// the reason mergePausedReason, while the Merge switch is on. When the Workstream of the task has no merge level, the
// item loses the reason. The item also loses the reason when the task has an approval of a person, also while the
// switch is off, because that approval merges the pull request.
func (e *Engine) continuePausedMerges(ctx context.Context, repository github.Repository) error {
	off, err := e.mergeOff(ctx)
	if err != nil {
		return err
	}
	items, err := e.queries.ListOpenInboxItems(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Repository != repository.FullName || item.Kind != readyForReviewKind || item.MergeRefusal != mergePausedReason {
			continue
		}
		task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if task.AutoMerge == 0 && awaitsMerge(task) {
			if err := e.clearBlockReason(ctx, task); err != nil {
				return err
			}
			continue
		}
		if off {
			continue
		}
		level, err := e.workstreamLevel(ctx, task.WorkstreamRepository, task.Workstream)
		if err != nil {
			return err
		}
		if mergesPullRequests(level) {
			err = e.approveWaitingTask(ctx, task)
		} else {
			err = e.clearBlockReason(ctx, task)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) approveAsOwner(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) error {
	e.mergeMu.Lock()
	defer e.mergeMu.Unlock()
	asOwner, err := e.github.AsOwner(ctx, repository)
	if err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if err := e.queries.SetTaskAutoMerge(ctx, store.SetTaskAutoMergeParams{AutoMerge: 1, ID: task.ID}); err != nil {
		return err
	}
	if err := asOwner.Approve(ctx, int64(pullRequest.GetNumber()), pullRequest.GetHead().GetSHA()); err != nil {
		return errors.Join(err, e.queries.SetTaskAutoMerge(ctx, store.SetTaskAutoMergeParams{ID: task.ID}))
	}
	if err := e.setMergeItems(ctx, task, 1, ""); err != nil {
		return err
	}
	select {
	case e.pollWake <- struct{}{}:
	default:
	}
	return nil
}

// applyLevel acts on the live tasks of the Workstream after a person changed its Autopilot level to level. With a merge
// level, a task in ready_for_review with an open ready for review Inbox item and no approval gets autoApprove. Without
// a merge level, a task that Mobius approved for the merge and that waits for the merge gets the reason
// mergeOffReason, and the "Merge" button shows again. A task whose items have mandatoryTodosReason or mergePausedReason
// loses that reason.
// A task that already has the result stays as it is, so a poll that runs the step again changes nothing.
func (e *Engine) applyLevel(ctx context.Context, workstreamRepository string, workstream int64, level string) error {
	tasks, err := e.queries.ListWorkstreamLiveTasks(ctx, store.ListWorkstreamLiveTasksParams{WorkstreamRepository: workstreamRepository, Workstream: workstream})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if mergesPullRequests(level) {
			if err := e.approveWaitingTask(ctx, task); err != nil {
				return err
			}
			continue
		}
		if task.AutoMerge == 1 && awaitsMerge(task) {
			err = e.endAutoMerge(ctx, task)
		} else {
			err = e.clearBlockReason(ctx, task)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// approveWaitingTask gives autoApprove to a task in ready_for_review with an open ready for review Inbox item, an open
// pull request and no approval. A failure of autoApprove goes to the log, because it is on the items.
func (e *Engine) approveWaitingTask(ctx context.Context, task store.Task) error {
	if task.State != "ready_for_review" || !task.PullRequest.Valid || task.AutoMerge != 0 || awaitsMerge(task) {
		return nil
	}
	items, err := e.openReadyItems(ctx, task)
	if err != nil || len(items) == 0 {
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
	if _, err := e.autoApprove(ctx, repository, task, pullRequest); err != nil {
		log.Printf("approve pull request %d of %s for the merge: %v", task.PullRequest.Int64, task.Repository, err)
	}
	return nil
}

// endAutoMerge ends the automatic merge of the task, because its Workstream has no merge level any more. The Inbox
// items get the reason, so the "Merge" button shows again, and the approval of Mobius counts as refused.
func (e *Engine) endAutoMerge(ctx context.Context, task store.Task) error {
	if err := e.refuseMergeItem(ctx, task, mergeOffReason); err != nil {
		return err
	}
	return e.queries.SetTaskRefusedReview(ctx, store.SetTaskRefusedReviewParams{RefusedReview: task.ApprovedReview, ID: task.ID})
}

// pauseAutoMerge ends the automatic merge of the task, because the Merge switch is off. It works as endAutoMerge, with
// the reason mergePausedReason, and the issue goes to the held work of the switch.
func (e *Engine) pauseAutoMerge(ctx context.Context, task store.Task) error {
	if err := e.holdMerge(ctx, task); err != nil {
		return err
	}
	return e.queries.SetTaskRefusedReview(ctx, store.SetTaskRefusedReviewParams{RefusedReview: task.ApprovedReview, ID: task.ID})
}

// updateForMerge tells if the merge of the pull request of a task with auto_merge must wait, because the head does not
// contain the default branch. Then it updates the branch of the pull request. GitHub adds the merge commit after it answers, so the wait goes on until the head changes or branchUpdateWait
// has passed. A 4xx answer of the update waits too, and the next poll tries again after branchUpdateWait. A 5xx answer
// or another error returns, and the next poll tries again.
func (e *Engine) updateForMerge(ctx context.Context, repository github.Repository, pullRequest *gh.PullRequest) (bool, error) {
	number := int64(pullRequest.GetNumber())
	head := pullRequest.GetHead().GetSHA()
	state := e.pull(repository, number)
	if state.updatedHead == head && e.timeNow().Sub(state.updatedAt) < branchUpdateWait {
		state.changed = true
		return true, nil
	}
	behind, err := repository.CommitsBehind(ctx, repository.DefaultBranch, head)
	if err != nil || behind == 0 {
		return false, err
	}
	err = repository.UpdatePullRequestBranch(ctx, number, head)
	var refused *gh.ErrorResponse
	if err != nil && (!errors.As(err, &refused) || refused.Response.StatusCode >= 500) {
		return false, err
	}
	if err != nil {
		log.Printf("update the branch of pull request %d of %s: %v", number, repository.FullName, err)
	}
	state.updatedHead, state.updatedAt, state.changed = head, e.timeNow(), true
	return true, nil
}

// returnForApproval sends the task in ready_for_review back to checks, because its head has no Mobius check run, so the
// Lead did not approve it. This is the case after the update of the branch. The old approval ends, and the head goes
// through the CI and the approval of the Lead again.
func (e *Engine) returnForApproval(ctx context.Context, task store.Task) error {
	if err := e.queries.SetTaskRefusedReview(ctx, store.SetTaskRefusedReviewParams{RefusedReview: task.ApprovedReview, ID: task.ID}); err != nil {
		return err
	}
	if err := e.dismissReadyForReview(ctx, task, task.State); err != nil {
		return err
	}
	_, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "checks", ID: task.ID, FromState: "ready_for_review"})
	return err
}
