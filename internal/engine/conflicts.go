package engine

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// behind tells if the base of the pull request has commits that its head does not have.
func behind(pullRequest *gh.PullRequest) bool {
	return pullRequest.GetMergeableState() == "behind"
}

// onConflict starts a conflict round on the pull request of the task in ready_for_review, checks or approval, which has
// a merge conflict or is behind its base. A pull request older than stale_pr_age goes to a human instead, and then onConflict gives
// false.
func (e *Engine) onConflict(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	if time.Since(pullRequest.GetCreatedAt().Time) > e.config.StalePRAge {
		return false, e.stale(ctx, repository, task, pullRequest)
	}
	return true, e.conflictRound(ctx, repository, task, pullRequest)
}

// stale hands the task of the stale pull request to a human, with an Inbox item and an event for the Lead.
func (e *Engine) stale(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) error {
	moved, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "needs_human", ID: task.ID, FromState: task.State})
	if err != nil || moved == 0 {
		return err
	}
	if err := e.queries.SetTaskCiFailedHead(ctx, store.SetTaskCiFailedHeadParams{ID: task.ID}); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, workingLabel); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, reviewLabel); err != nil {
		return err
	}
	if err := addNeedsHuman(ctx, repository, task); err != nil {
		return err
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return err
	}
	reason := "has a merge conflict"
	if behind(pullRequest) {
		reason = "is behind its base branch"
	}
	age := fmt.Sprintf("%g days", e.config.StalePRAge.Hours()/24)
	number := pullRequest.GetNumber()
	err = e.addInboxItem(ctx, store.AddInboxItemParams{
		Kind:         stalePullRequestKind,
		Organization: repository.Owner(),
		Repository:   task.Repository,
		Workstream:   task.Workstream,
		Issue:        task.Issue,
		Text:         fmt.Sprintf("Pull request #%d of #%d \"%s\" %s and is older than %s.", number, task.Issue, issue.GetTitle(), reason, age),
		Link:         pullRequest.GetHTMLURL(),
	})
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s stale pull request #%d of #%d \"%s\": it %s and is older than %s. %s",
		time.Now().UTC().Format(timeFormat), number, task.Issue, issue.GetTitle(), reason, age, pullRequest.GetHTMLURL())
	return e.addLeadEvent(ctx, task.Repository, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "stale_pull_request", text)
}
