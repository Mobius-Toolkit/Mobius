package engine

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// keepApproval keeps the id of the newest APPROVED review of a trusted user that comes after the kept approval in the
// list of reviews. A dismissal of the kept approval cancels it: the task keeps the id and the approval counts as
// refused, so a dismissal never brings back an older approval. A dismissal of another review does not change the kept
// approval. It gives the task with the new approval.
func (e *Engine) keepApproval(ctx context.Context, repository github.Repository, task store.Task) (store.Task, error) {
	reviews := e.pull(repository, task.PullRequest.Int64).Reviews
	kept := slices.IndexFunc(reviews, func(review github.Review) bool { return review.ID == task.ApprovedReview.String })
	approved := task.ApprovedReview
	for _, review := range slices.Backward(reviews[kept+1:]) {
		if review.State == "APPROVED" && e.trustedUser(review.Author) {
			approved = sql.NullString{String: review.ID, Valid: true}
			break
		}
	}
	if approved != task.ApprovedReview {
		task.ApprovedReview = approved
		return task, e.queries.SetTaskApprovedReview(ctx, store.SetTaskApprovedReviewParams{ApprovedReview: approved, ID: task.ID})
	}
	if kept >= 0 && reviews[kept].State == "DISMISSED" && task.RefusedReview != approved {
		task.RefusedReview = approved
		return task, e.queries.SetTaskRefusedReview(ctx, store.SetTaskRefusedReviewParams{RefusedReview: approved, ID: task.ID})
	}
	return task, nil
}

// awaitsMerge tells if the task has an approval that GitHub did not refuse and that nobody dismissed.
func awaitsMerge(task store.Task) bool {
	return task.ApprovedReview.Valid && task.ApprovedReview != task.RefusedReview
}

// mergeApproved squash merges the pull request of the task with an approval, when no Worker works on the task and its
// head commit passes these conditions: the pull request is not a draft and has no merge conflict, it has no open review
// thread, and each check run of the head completed and passed, with the Mobius check among them. It reads the check
// runs only when the other conditions are true, and it reads the review threads again before the merge. It gives true
// when GitHub merged the pull request. When GitHub refuses the merge, the Lead gets an event, and the poll does not
// try again until a new APPROVED review.
func (e *Engine) mergeApproved(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	number := int64(pullRequest.GetNumber())
	if !awaitsMerge(task) || task.State == "queued" || task.State == "working" || pullRequest.GetDraft() || !pullRequest.GetMergeable() || hasOpenThread(e.pull(repository, number)) {
		return false, nil
	}
	head := pullRequest.GetHead().GetSHA()
	runs, err := repository.CheckRuns(ctx, head)
	if err != nil {
		return false, err
	}
	if !slices.ContainsFunc(runs, func(run *gh.CheckRun) bool { return run.GetName() == checkRunName }) || slices.ContainsFunc(runs, func(run *gh.CheckRun) bool { return !passed(run) }) {
		return false, nil
	}
	if err := e.readPullRequests(ctx, repository, []int64{number}); err != nil {
		return false, err
	}
	if task, err = e.keepApproval(ctx, repository, task); err != nil || !awaitsMerge(task) || hasOpenThread(e.pull(repository, number)) {
		return false, err
	}
	merged, reason, err := repository.MergePullRequest(ctx, number, head)
	if err != nil || merged || reason == "" {
		return merged, err
	}
	if err := e.queries.SetTaskRefusedReview(ctx, store.SetTaskRefusedReviewParams{RefusedReview: task.ApprovedReview, ID: task.ID}); err != nil {
		return false, err
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return false, err
	}
	text := fmt.Sprintf("%s merge refused for #%d \"%s\": GitHub refused to merge pull request #%d. A trusted user approved the pull request. GitHub gave this reason: \"%s\". Mobius does not try again until a trusted user approves the pull request again.",
		time.Now().UTC().Format(timeFormat), task.Issue, issue.GetTitle(), number, reason)
	return false, e.addLeadEvent(ctx, task.Repository, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "merge_refused", text)
}

// hasOpenThread tells if the pull request has a review thread that is not resolved.
func hasOpenThread(state *pullState) bool {
	return slices.ContainsFunc(state.Threads, func(thread github.ReviewThread) bool { return !thread.Resolved })
}

// passed tells if the check run completed with the conclusion success, neutral or skipped.
func passed(run *gh.CheckRun) bool {
	return run.GetStatus() == "completed" && slices.Contains([]string{"success", "neutral", "skipped"}, run.GetConclusion())
}
