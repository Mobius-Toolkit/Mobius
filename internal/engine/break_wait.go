package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// branchUpdateWait is the time that the break wait gives GitHub to add the merge commit after the answer 202 of the
// update of a branch.
const branchUpdateWait = 5 * time.Minute

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
// no stop. When the CI of the waiting head shows no failure any more, for example after a run of its failed jobs again,
// the wait ends and the usual flow reads the CI. It decides from state, the CI of the head that the caller read.
func (e *Engine) waitsForBreak(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest, state ci) (bool, error) {
	if !afterReview(task.State) {
		return false, nil
	}
	open, err := e.breakOpenFor(ctx, repository.FullName, task)
	if err != nil || !open {
		return false, err
	}
	head := pullRequest.GetHead().GetSHA()
	waiting := task.BreakWaitHead.String == head
	if state.failedCheck || state.failedWorkflow {
		if waiting {
			return true, nil
		}
		text, err := e.breakWaitText(ctx, repository.FullName, task, pullRequest)
		if err != nil {
			return false, err
		}
		err = e.addLeadEventWith(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, "break_wait", text, func(q *store.Queries) error {
			return q.SetTaskBreakWaitHead(ctx, store.SetTaskBreakWaitHeadParams{BreakWaitHead: sql.NullString{String: head, Valid: true}, ID: task.ID})
		})
		return err == nil, err
	}
	if waiting {
		return false, e.queries.SetTaskBreakWaitHead(ctx, store.SetTaskBreakWaitHeadParams{ID: task.ID})
	}
	return false, nil
}

// breakWaitText gives the text of the Lead event of a head that starts to wait: the task, the pull request, the head and
// each open break of the repository.
func (e *Engine) breakWaitText(ctx context.Context, repository string, task store.Task, pullRequest *gh.PullRequest) (string, error) {
	breaks, err := e.queries.ListOpenCIBreaks(ctx, repository)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s wait of %s with the pull request %s for the end of a break of the CI of the default branch. The CI of the head %s failed. Open breaks of %s:",
		e.timeNow().UTC().Format(timeFormat), taskEventRef(task, task.Issue), taskEventRef(task, int64(pullRequest.GetNumber())), pullRequest.GetHead().GetSHA(), repository)
	for _, ciBreak := range breaks {
		issue := "no task issue"
		if ciBreak.Issue != 0 {
			issue = "task issue " + eventRef(task.WorkstreamRepository, ciBreak.Repository, max(ciBreak.Issue, -ciBreak.Issue))
		}
		started, err := time.Parse(time.RFC3339Nano, ciBreak.StartedAt)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&text, "\n- workflow %s, run %s, started %s, %s", ciBreak.WorkflowName, ciBreak.RunUrl, started.UTC().Format(timeFormat), issue)
	}
	return text.String(), nil
}

// endBreakWait ends the wait of the task when the repository has no open break. It updates the branch of the pull
// request when its head is still the waiting head, also when GitHub does not report the pull request as behind its base. GitHub adds the merge commit
// after it answers, so the wait goes on, with a new read of the pull request at each poll, until the head is not the
// waiting head or branchUpdateWait has passed. It gives true while the wait goes on, so the usual flow does not act on
// the old head. In each other case, and when GitHub refuses the update with a 4xx status, it clears break_wait_head, so
// that the usual flow reads the CI of the head. A 5xx answer or another error returns, and the next poll tries again.
func (e *Engine) endBreakWait(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	if !task.BreakWaitHead.Valid {
		return false, nil
	}
	open, err := e.breakOpenFor(ctx, repository.FullName, task)
	if err != nil || open {
		return false, err
	}
	head := pullRequest.GetHead().GetSHA()
	state := e.pull(repository, task.PullRequest.Int64)
	if head == task.BreakWaitHead.String && state.updatedHead == head && e.timeNow().Sub(state.updatedAt) < branchUpdateWait {
		state.changed = true
		return true, nil
	}
	if head == task.BreakWaitHead.String && state.updatedHead == head {
		state.updatedHead = ""
		return false, e.queries.SetTaskBreakWaitHead(ctx, store.SetTaskBreakWaitHeadParams{ID: task.ID})
	}
	if afterReview(task.State) && head == task.BreakWaitHead.String {
		var refused *gh.ErrorResponse
		err := repository.UpdatePullRequestBranch(ctx, task.PullRequest.Int64, head)
		if err == nil {
			state.updatedHead, state.updatedAt, state.changed = head, e.timeNow(), true
			return true, nil
		}
		if !errors.As(err, &refused) || refused.Response.StatusCode >= 500 {
			return false, err
		}
	}
	return false, e.queries.SetTaskBreakWaitHead(ctx, store.SetTaskBreakWaitHeadParams{ID: task.ID})
}
