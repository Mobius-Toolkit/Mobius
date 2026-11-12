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

// mergeQueueHoldLimit is the time that the head of the merge queue can stay in checks or in approval before Mobius
// removes it from the queue, when another task waits in the queue.
const mergeQueueHoldLimit = 2 * time.Hour

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
		if err := e.clearMergeQueue(ctx, &task); err != nil {
			return task, err
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

// mergeApprovedAfterApproval calls mergeApproved with the task row read again under approvalMu. Thus the poll sees the
// task before the approval of its head (finishApproval) or after it, never between its steps. It does nothing while an
// approval runs, because the next poll sees the task after the approval. It also does nothing when the task row is gone.
// The approval of the Owner that finishApproval wrote on GitHub is not in the task row until keepApproval reads it, and the
// poll read the reviews before the approval. Thus a queued task with auto_merge in ready_for_review that has no approval
// reads the reviews again.
func (e *Engine) mergeApprovedAfterApproval(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	if !e.approvalMu.TryLock() {
		return false, nil
	}
	defer e.approvalMu.Unlock()
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if task.State == "ready_for_review" && task.AutoMerge == 1 && task.MergeQueuedAt.Valid && !awaitsMerge(task) {
		if err := e.readPullRequests(ctx, repository, []int64{task.PullRequest.Int64}); err != nil {
			return false, err
		}
		if task, err = e.keepApproval(ctx, repository, task); err != nil {
			return false, err
		}
	}
	return e.mergeApproved(ctx, repository, task, pullRequest)
}

// mergeApproved places an approved task in the merge queue of its repository when no Worker works on the task and its
// head commit passes these conditions: the pull request is not a draft and has no merge conflict, has no open review
// thread, and each check run of the head completed and passed, with the Mobius check among them. It reads the check
// runs only when the other conditions are true, and it reads the review threads and the task again before the merge. A task with
// auto_merge also needs a Workstream with a merge level, else its automatic merge ends (endAutoMerge), and the Merge
// switch on, else its automatic merge pauses (pauseAutoMerge).
//
// Only the head of the merge queue updates its branch and merges: when its head does not contain the default branch and
// the pull request and the new commits of the default branch change a common file, it updates the branch
// (updateForMerge). A head that is behind with no common file merges with its approval. A head with no Mobius check
// run, for example after the update of the branch, sends the task in ready_for_review back to checks
// (returnForApproval). When the head has no wait, it squash merges the pull request. It gives true when GitHub merged the pull request, and then it dismisses the open
// ready for review Inbox items of the issue and leaves the queue. It does not end the task: checkTask does that. When GitHub refuses the merge, the open ready for
// review Inbox item of the issue gets the reason and the Lead gets an event, and the poll does not try again until a
// new APPROVED review. A refusal for a pull request that a person merged in the meantime is no refusal: the next read
// of the pull request goes to GitHub, and the poll ends the task. The caller holds approvalMu, so no approval of the
// head runs at the same time (mergeApprovedAfterApproval).
func (e *Engine) mergeApproved(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	number := int64(pullRequest.GetNumber())
	conflict := pullRequest.Mergeable != nil && !pullRequest.GetMergeable()
	if pullRequest.GetDraft() || hasOpenThread(e.pull(repository, number)) || conflict {
		if err := e.clearMergeQueue(ctx, &task); err != nil {
			return false, err
		}
		return false, nil
	}
	if blocked, err := e.blockedByTodos(ctx, task); err != nil || blocked {
		if blocked {
			if err := e.clearMergeQueue(ctx, &task); err != nil {
				return false, err
			}
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
		if err := e.clearMergeQueue(ctx, &task); err != nil {
			return false, err
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
		if err := e.clearMergeQueue(ctx, &task); err != nil {
			return false, err
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
		if err := e.publishMergeQueue(ctx, task.Repository); err != nil {
			return false, err
		}
	}
	queueHead, err := e.queries.GetMergeQueueHead(ctx, task.Repository)
	if err != nil {
		return false, err
	}
	if queueHead.ID != task.ID {
		return false, nil
	}
	if task.State == "checks" || task.State == "approval" {
		if removed, err := e.removeHeldHead(ctx, repository, task, pullRequest); err != nil || removed {
			return false, err
		}
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
		if err := e.clearMergeQueue(ctx, &task); err != nil {
			return false, err
		}
		return false, nil
	}
	if !awaitsMerge(task) || slices.ContainsFunc(runs, func(run *gh.CheckRun) bool { return !passed(run) }) {
		if !awaitsMerge(task) && task.State != "checks" && task.State != "approval" {
			if err := e.clearMergeQueue(ctx, &task); err != nil {
				return false, err
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
		if hasOpenThread(e.pull(repository, number)) || !awaitsMerge(task) {
			if clearErr := e.clearMergeQueue(ctx, &task); clearErr != nil {
				return false, errors.Join(err, clearErr)
			}
		}
		return false, err
	}
	task, err = e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !awaitsMerge(task) {
		if !awaitsMerge(task) {
			if clearErr := e.clearMergeQueue(ctx, &task); clearErr != nil {
				return false, errors.Join(err, clearErr)
			}
		}
		return false, err
	}
	merged, reason, err := repository.MergePullRequest(ctx, number, head)
	if err != nil {
		return false, err
	}
	if merged {
		if err := e.clearMergeQueue(ctx, &task); err != nil {
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
		if err := e.clearMergeQueue(ctx, &task); err != nil {
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
	if err := e.clearMergeQueue(ctx, &task); err != nil {
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

// removeHeldHead removes the head of the merge queue from the queue when it stayed in its state for more than
// mergeQueueHoldLimit since the later of the state time and the queue time, and another task waits in the queue. The
// Lead gets the event merge_queue_held. The task keeps its state, and it enters the queue again at the end by the
// rules of mergeApproved. It tells if it removed the task.
func (e *Engine) removeHeldHead(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	enteredState, err := time.Parse(time.RFC3339Nano, task.StateAt)
	if err != nil {
		return false, err
	}
	queued, err := time.Parse(time.RFC3339Nano, task.MergeQueuedAt.String)
	if err != nil {
		return false, err
	}
	now := e.timeNow()
	if now.Sub(laterTime(enteredState, queued)) <= mergeQueueHoldLimit {
		return false, nil
	}
	tasks, err := e.queries.ListMergeQueueTasks(ctx, task.Repository)
	if err != nil {
		return false, err
	}
	if len(tasks) < 2 {
		return false, nil
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return false, err
	}
	text := fmt.Sprintf("%s %s \"%s\" held the head of the merge queue in %s for more than %s: pull request %s %s, head %s. Mobius removed it from the queue, so the next task can merge. %d other tasks wait in the queue. The task enters the queue again at the end after each check of its head passes and its head has the Mobius check.",
		now.UTC().Format(timeFormat), taskEventRef(task, task.Issue), issue.GetTitle(), task.State, mergeQueueHoldLimit, taskEventRef(task, int64(pullRequest.GetNumber())), pullRequest.GetHTMLURL(), pullRequest.GetHead().GetSHA(), len(tasks)-1)
	err = e.addLeadEventWith(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, "merge_queue_held", text, func(q *store.Queries) error {
		_, err := q.ClearTaskMergeQueuedAt(ctx, task.ID)
		return err
	})
	if err != nil {
		return false, err
	}
	task.MergeQueuedAt = sql.NullString{}
	return true, e.publishMergeQueue(ctx, task.Repository, task)
}

// laterTime gives the later of two times.
func laterTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// hasOpenThread tells if the pull request has a review thread that is not resolved.
func hasOpenThread(state *pullState) bool {
	return slices.ContainsFunc(state.Threads, func(thread github.ReviewThread) bool { return !thread.Resolved })
}

// passed tells if the check run completed with the conclusion success, neutral or skipped.
func passed(run *gh.CheckRun) bool {
	return run.GetStatus() == "completed" && slices.Contains([]string{"success", "neutral", "skipped"}, run.GetConclusion())
}

// publishMergeQueue publishes a Workstream change and publishes again the open ready for review Inbox items of each
// task in the merge queue of repository, with their new place and size. It also publishes the open ready for review
// Inbox items of extraTasks.
func (e *Engine) publishMergeQueue(ctx context.Context, repository string, extraTasks ...store.Task) error {
	e.publish(Change{Workstreams: true})
	tasks, err := e.queries.ListMergeQueueTasks(ctx, repository)
	if err != nil {
		return err
	}
	all := append([]store.Task{}, tasks...)
	for _, extra := range extraTasks {
		if !slices.ContainsFunc(all, func(t store.Task) bool { return t.ID == extra.ID }) {
			all = append(all, extra)
		}
	}
	for _, task := range all {
		items, err := e.openReadyItems(ctx, task)
		if err != nil {
			return err
		}
		for _, item := range items {
			e.publish(Change{Inbox: &item})
		}
	}
	return nil
}

// clearMergeQueue removes the task from the merge queue of its repository. It publishes a Workstream change and
// publishes again the open ready for review Inbox items of the tasks of the queue and of the task when the task was in
// the queue.
func (e *Engine) clearMergeQueue(ctx context.Context, task *store.Task) error {
	if !task.MergeQueuedAt.Valid {
		return nil
	}
	if _, err := e.queries.ClearTaskMergeQueuedAt(ctx, task.ID); err != nil {
		return err
	}
	task.MergeQueuedAt = sql.NullString{}
	return e.publishMergeQueue(ctx, task.Repository, *task)
}

// MergeQueuePlace gives the place and size of the task in the merge queue of its repository. It gives nil, 0 when the
// task is not in the queue.
func (e *Engine) MergeQueuePlace(ctx context.Context, task store.Task) (*int64, int64, error) {
	if !task.MergeQueuedAt.Valid {
		return nil, 0, nil
	}
	tasks, err := e.queries.ListMergeQueueTasks(ctx, task.Repository)
	if err != nil {
		return nil, 0, err
	}
	idx := slices.IndexFunc(tasks, func(t store.Task) bool { return t.ID == task.ID })
	if idx < 0 {
		return nil, 0, nil
	}
	place := int64(idx + 1)
	return &place, int64(len(tasks)), nil
}

// MergeQueuePlaceOfItem gives the place and size of the task of the ready for review Inbox item in the merge queue of
// its repository. It gives nil, 0 when the task is not in the queue or the item is dismissed.
func (e *Engine) MergeQueuePlaceOfItem(ctx context.Context, item store.InboxItem) (*int64, int64, error) {
	if item.Kind != readyForReviewKind || item.DismissedAt.Valid {
		return nil, 0, nil
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && task.PullRequest.Int64 != item.PullRequest) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return e.MergeQueuePlace(ctx, task)
}
