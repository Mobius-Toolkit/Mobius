package engine

import (
	"context"
	"database/sql"
	"errors"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// breakOpenFor tells if the repository has an open break of the CI of the default branch that the task does not fix.
// The task of the issue of a break fixes that break, so it never waits.
func (e *Engine) breakOpenFor(ctx context.Context, repository string, task store.Task) (bool, error) {
	breaks, err := e.queries.ListOpenCIBreaks(ctx, repository)
	if err != nil {
		return false, err
	}
	for _, ciBreak := range breaks {
		if ciBreak.Issue != 0 && max(ciBreak.Issue, -ciBreak.Issue) == task.Issue {
			return false, nil
		}
	}
	return len(breaks) > 0, nil
}

// waitsForBreak tells if the head of the pull request of the task in checks, approval or ready_for_review waits for the
// end of a break of the CI of the default branch. The head waits when the repository has an open break and the CI of the
// head failed. The head stays in break_wait_head, so the wait survives a restart. A waiting task gets no fix round and
// no stop.
func (e *Engine) waitsForBreak(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	if !afterReview(task.State) {
		return false, nil
	}
	open, err := e.breakOpenFor(ctx, repository.FullName, task)
	if err != nil || !open {
		return false, err
	}
	head := pullRequest.GetHead().GetSHA()
	if task.BreakWaitHead.String == head {
		return true, nil
	}
	state, err := ciOf(ctx, repository, head)
	if err != nil || !state.failedCheck && !state.failedWorkflow {
		return false, err
	}
	return true, e.queries.SetTaskBreakWaitHead(ctx, store.SetTaskBreakWaitHeadParams{BreakWaitHead: sql.NullString{String: head, Valid: true}, ID: task.ID})
}

// endBreakWait ends the wait of the task when the repository has no open break. It updates the branch of the pull
// request when its head is still the waiting head and the pull request is behind its base, and it gives true then,
// because the new head gets a new read at the next poll. In each other case, and when GitHub refuses the update with a
// 4xx status, it only clears break_wait_head, so that the usual flow reads the CI of the head. It clears
// break_wait_head after the update succeeds. A 5xx answer or another error returns, and the next poll tries again.
func (e *Engine) endBreakWait(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	if !task.BreakWaitHead.Valid {
		return false, nil
	}
	open, err := e.breakOpenFor(ctx, repository.FullName, task)
	if err != nil || open {
		return false, err
	}
	head := pullRequest.GetHead().GetSHA()
	updated := false
	if afterReview(task.State) && head == task.BreakWaitHead.String && behind(pullRequest) {
		var refused *gh.ErrorResponse
		err := repository.UpdatePullRequestBranch(ctx, task.PullRequest.Int64, head)
		if err != nil && (!errors.As(err, &refused) || refused.Response.StatusCode >= 500) {
			return false, err
		}
		updated = err == nil
	}
	return updated, e.queries.SetTaskBreakWaitHead(ctx, store.SetTaskBreakWaitHeadParams{ID: task.ID})
}
