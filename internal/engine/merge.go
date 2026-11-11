package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// keepApproval keeps the id of the newest APPROVED review of a trusted user that GitHub submitted after the kept
// approval. The list of reviews is in the order of the start of each review, so the submit time decides which review is
// newer. A dismissal of the kept approval cancels it: the task keeps the id and the approval counts as
// refused, so a dismissal never brings back an older approval. It also gives each open ready for review Inbox item of
// the issue the reason, and ends its merging state. A dismissal of another review does not change the kept approval.
// It gives the task with the new approval.
func (e *Engine) keepApproval(ctx context.Context, repository github.Repository, task store.Task) (store.Task, error) {
	reviews := e.pull(repository, task.PullRequest.Int64).Reviews
	kept := slices.IndexFunc(reviews, func(review github.Review) bool { return review.ID == task.ApprovedReview.String })
	var keptAt time.Time
	if kept >= 0 {
		keptAt = reviews[kept].SubmittedAt
	}
	approved := task.ApprovedReview
	var newest time.Time
	for _, review := range reviews {
		if review.State == "APPROVED" && e.trustedUser(review.Author) && review.SubmittedAt.After(keptAt) && !review.SubmittedAt.Before(newest) {
			approved = sql.NullString{String: review.ID, Valid: true}
			newest = review.SubmittedAt
		}
	}
	if approved != task.ApprovedReview {
		task.ApprovedReview = approved
		return task, e.queries.SetTaskApprovedReview(ctx, store.SetTaskApprovedReviewParams{ApprovedReview: approved, ID: task.ID})
	}
	if kept >= 0 && reviews[kept].State == "DISMISSED" && task.RefusedReview != approved {
		reason := fmt.Sprintf("A person dismissed the approval of pull request #%d on GitHub.", task.PullRequest.Int64)
		if err := e.refuseMergeItem(ctx, task, reason); err != nil {
			return task, err
		}
		if task.MergeQueuedAt.Valid {
			if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
				return task, err
			}
			task.MergeQueuedAt = sql.NullString{}
		}
		task.RefusedReview = approved
		return task, e.queries.SetTaskRefusedReview(ctx, store.SetTaskRefusedReviewParams{RefusedReview: approved, ID: task.ID})
	}
	return task, nil
}

// awaitsMerge tells if the task has an approval that GitHub did not refuse and that nobody dismissed.
func awaitsMerge(task store.Task) bool {
	return task.ApprovedReview.Valid && task.ApprovedReview != task.RefusedReview
}

// mergeApproved places an approved task in the merge queue of its repository when no Worker works on the task and its
// head commit passes these conditions: the pull request is not a draft and has no merge conflict, has no open review
// thread, and each check run of the head completed and passed, with the Mobius check among them. It reads the check
// runs only when the other conditions are true, and it reads the review threads and the task again before the merge. A task with
// auto_merge also needs a Workstream with a merge level, else its automatic merge ends (endAutoMerge), and the Merge
// switch on, else its automatic merge pauses (pauseAutoMerge).
//
// Only the head of the merge queue updates its branch and merges: when its head does not contain the default branch, it
// updates the branch (updateForMerge). Its head with no Mobius check run, for example after the update of the branch,
// sends the task in ready_for_review back to checks (returnForApproval). When the head contains the default branch, it
// squash merges the pull request. It gives true when GitHub merged the pull request, and then it dismisses the open
// ready for review Inbox items of the issue and leaves the queue. When GitHub refuses the merge, the open ready for
// review Inbox item of the issue gets the reason and the Lead gets an event, and the poll does not try again until a
// new APPROVED review. A refusal for a pull request that a person merged in the meantime is no refusal: the next read
// of the pull request goes to GitHub, and the poll ends the task.
func (e *Engine) mergeApproved(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	number := int64(pullRequest.GetNumber())
	conflict := pullRequest.Mergeable != nil && !pullRequest.GetMergeable()
	if pullRequest.GetDraft() || hasOpenThread(e.pull(repository, number)) || conflict {
		if task.MergeQueuedAt.Valid {
			if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
				return false, err
			}
			task.MergeQueuedAt = sql.NullString{}
		}
		return false, nil
	}
	if blocked, err := e.blockedByTodos(ctx, task); err != nil || blocked {
		if blocked && task.MergeQueuedAt.Valid {
			if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
				return false, err
			}
			task.MergeQueuedAt = sql.NullString{}
		}
		return false, err
	}
	if task.AutoMerge == 1 {
		level, err := e.workstreamLevel(ctx, task.WorkstreamRepository, task.Workstream)
		if err != nil {
			return false, err
		}
		if !mergesPullRequests(level) {
			return false, e.endAutoMerge(ctx, task)
		}
		off, err := e.mergeOff(ctx)
		if err != nil {
			return false, err
		}
		if off {
			return false, e.pauseAutoMerge(ctx, task)
		}
	}
	if task.State == "queued" || task.State == "working" {
		if task.MergeQueuedAt.Valid {
			if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
				return false, err
			}
			task.MergeQueuedAt = sql.NullString{}
		}
		return false, nil
	}
	if !task.MergeQueuedAt.Valid && (!awaitsMerge(task) || pullRequest.Mergeable == nil) {
		return false, nil
	}
	head := pullRequest.GetHead().GetSHA()
	runs, err := repository.CheckRuns(ctx, head)
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(runs, func(run *gh.CheckRun) bool {
		return run.GetStatus() == "completed" && failedConclusion(run.GetConclusion())
	}) {
		if task.MergeQueuedAt.Valid {
			if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
				return false, err
			}
			task.MergeQueuedAt = sql.NullString{}
		}
		return false, nil
	}
	if !task.MergeQueuedAt.Valid {
		approvedByLead := slices.ContainsFunc(runs, func(run *gh.CheckRun) bool { return run.GetName() == checkRunName })
		if !approvedByLead || slices.ContainsFunc(runs, func(run *gh.CheckRun) bool { return !passed(run) }) {
			return false, nil
		}
		now := e.timeNow().UTC().Format(time.RFC3339Nano)
		if err := e.queries.SetTaskMergeQueuedAt(ctx, store.SetTaskMergeQueuedAtParams{
			MergeQueuedAt: sql.NullString{String: now, Valid: true},
			ID:            task.ID,
		}); err != nil {
			return false, err
		}
		task.MergeQueuedAt = sql.NullString{String: now, Valid: true}
	}
	queueHead, err := e.queries.GetMergeQueueHead(ctx, task.Repository)
	if err != nil {
		return false, err
	}
	if queueHead.ID != task.ID {
		return false, nil
	}
	approvedByLead := slices.ContainsFunc(runs, func(run *gh.CheckRun) bool { return run.GetName() == checkRunName })
	if !approvedByLead {
		if task.State == "ready_for_review" {
			canApproveAgain := false
			if task.AutoMerge == 0 {
				_, again, err := e.approvedHeadText(ctx, repository, task, pullRequest)
				if err != nil {
					return false, err
				}
				canApproveAgain = again
			}
			if task.AutoMerge == 1 || canApproveAgain {
				return false, e.returnForApproval(ctx, task)
			}
		}
		if task.State == "checks" || task.State == "approval" {
			return false, nil
		}
		if task.MergeQueuedAt.Valid {
			if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
				return false, err
			}
			task.MergeQueuedAt = sql.NullString{}
		}
		return false, nil
	}
	if !awaitsMerge(task) || slices.ContainsFunc(runs, func(run *gh.CheckRun) bool { return !passed(run) }) {
		if !awaitsMerge(task) && task.State != "checks" && task.State != "approval" {
			if task.MergeQueuedAt.Valid {
				if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
					return false, err
				}
				task.MergeQueuedAt = sql.NullString{}
			}
		}
		return false, nil
	}
	if waits, err := e.updateForMerge(ctx, repository, pullRequest); err != nil || waits {
		return false, err
	}
	if err := e.readPullRequests(ctx, repository, []int64{number}); err != nil {
		return false, err
	}
	if task, err = e.keepApproval(ctx, repository, task); err != nil || !awaitsMerge(task) || hasOpenThread(e.pull(repository, number)) {
		if (hasOpenThread(e.pull(repository, number)) || !awaitsMerge(task)) && task.MergeQueuedAt.Valid {
			if clearErr := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); clearErr != nil {
				return false, errors.Join(err, clearErr)
			}
			task.MergeQueuedAt = sql.NullString{}
		}
		return false, err
	}
	task, err = e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !awaitsMerge(task) {
		if !awaitsMerge(task) && task.MergeQueuedAt.Valid {
			if clearErr := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); clearErr != nil {
				return false, errors.Join(err, clearErr)
			}
			task.MergeQueuedAt = sql.NullString{}
		}
		return false, err
	}
	merged, reason, err := repository.MergePullRequest(ctx, number, head)
	if err != nil {
		return false, err
	}
	if merged {
		if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
			return false, err
		}
		return true, e.dismissIssueItems(ctx, task.Repository, task.Issue, readyForReviewKind)
	}
	if reason == "" {
		return false, nil
	}
	current, err := repository.PullRequest(ctx, number)
	if err != nil {
		return false, err
	}
	if current.GetMerged() {
		if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
			return false, err
		}
		e.pull(repository, number).changed = true
		return false, nil
	}
	if err := e.refuseMergeItem(ctx, task, reason); err != nil {
		return false, err
	}
	if err := e.queries.SetTaskRefusedReview(ctx, store.SetTaskRefusedReviewParams{RefusedReview: task.ApprovedReview, ID: task.ID}); err != nil {
		return false, err
	}
	if err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
		return false, err
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return false, err
	}
	text := fmt.Sprintf("%s merge refused for %s \"%s\": GitHub refused to merge pull request %s. A trusted user approved the pull request. GitHub gave this reason: \"%s\". Mobius does not try again until a trusted user approves the pull request again.",
		time.Now().UTC().Format(timeFormat), taskEventRef(task, task.Issue), issue.GetTitle(), taskEventRef(task, number), reason)
	return false, e.addLeadEvent(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, "merge_refused", text)
}

// hasOpenThread tells if the pull request has a review thread that is not resolved.
func hasOpenThread(state *pullState) bool {
	return slices.ContainsFunc(state.Threads, func(thread github.ReviewThread) bool { return !thread.Resolved })
}

// passed tells if the check run completed with the conclusion success, neutral or skipped.
func passed(run *gh.CheckRun) bool {
	return run.GetStatus() == "completed" && slices.Contains([]string{"success", "neutral", "skipped"}, run.GetConclusion())
}
