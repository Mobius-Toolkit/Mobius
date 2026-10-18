package engine

import (
	"context"
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
	text := fmt.Sprintf("%s stale pull request #%d of #%d \"%s\": it %s and is older than %s. %s",
		time.Now().UTC().Format(timeFormat), number, task.Issue, issue.GetTitle(), reason, age, pullRequest.GetHTMLURL())
	handed, err := e.handFromStates(ctx, []string{task.State}, task, issue.GetTitle(), text, "", stopNotice{
		reason:       fmt.Sprintf("PR #%d %s", number, reason),
		text:         fmt.Sprintf("Pull request #%d of #%d \"%s\" %s and is older than %s.", number, task.Issue, issue.GetTitle(), reason, age),
		next:         "Close the pull request, or Resume for a conflict round.",
		checkTitle:   "Stale pull request",
		checkSummary: "The pull request is old. It has a merge conflict or is behind its base branch. A human decides the next step.",
		leadKind:     "stale_pull_request",
	})
	if err != nil || !handed {
		return err
	}
	return e.sendStopNotices(ctx, task.Repository, task.ID)
}
