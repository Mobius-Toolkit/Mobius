package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

const (
	// githubActions is the slug of the App of GitHub Actions.
	githubActions = "github-actions"
	// jobLogLines is the number of lines of the end of a job log of GitHub Actions in a fix round.
	jobLogLines = 200
)

// failedCheckRuns gives the completed check runs of other Apps that failed on the commit sha.
func failedCheckRuns(ctx context.Context, repository github.Repository, sha string) ([]*gh.CheckRun, error) {
	runs, err := repository.CheckRuns(ctx, sha)
	if err != nil {
		return nil, err
	}
	var failed []*gh.CheckRun
	for _, run := range runs {
		switch conclusion := run.GetConclusion(); {
		case run.GetName() == checkRunName || run.GetStatus() != "completed":
		case conclusion == "failure" || conclusion == "timed_out" || conclusion == "cancelled":
			failed = append(failed, run)
		}
	}
	return failed, nil
}

// onFailure starts a fix round when check runs of other Apps failed on the head of the pull request of the task in
// ready_for_review. Each failed check run is an item with its annotations, and a check run of GitHub Actions also has
// the end of its job log. A head gets one round. It gives true when a round started, or when the task left
// ready_for_review.
func (e *Engine) onFailure(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	head := pullRequest.GetHead().GetSHA()
	if task.CheckHead.String == head {
		return false, nil
	}
	runs, err := failedCheckRuns(ctx, repository, head)
	if err != nil {
		return false, err
	}
	var items strings.Builder
	for _, run := range runs {
		output := run.GetOutput()
		fmt.Fprintf(&items, "\nCheck run \"%s\", %s:\n%s\n\n%s\n", run.GetName(), run.GetHTMLURL(), output.GetTitle(), output.GetSummary())
		annotations, err := repository.CheckRunAnnotations(ctx, run.GetID())
		if err != nil {
			return false, err
		}
		for _, annotation := range annotations {
			fmt.Fprintf(&items, "- %s line %d: %s\n", annotation.GetPath(), annotation.GetStartLine(), annotation.GetMessage())
		}
		if run.GetApp().GetSlug() == githubActions {
			// A job log that cannot download leaves the round with the annotations.
			if log, err := repository.JobLog(ctx, run.GetID()); err == nil {
				lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
				fmt.Fprintf(&items, "\nEnd of the job log:\n%s\n", strings.Join(lines[max(0, len(lines)-jobLogLines):], "\n"))
			}
		}
		items.WriteString("\nAction: fix\n")
	}
	if items.Len() == 0 {
		return false, nil
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return false, err
	}
	parent, err := e.newestSession(ctx, task)
	if err != nil {
		return false, err
	}
	moved, err := e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: "working", ID: task.ID, FromState: "ready_for_review"})
	if err != nil || moved == 0 {
		return true, err
	}
	if err := e.fixRound(ctx, repository, round{task: task, title: issue.GetTitle(), pullRequest: pullRequest, items: items.String(), parent: parent}); err != nil {
		_, stateErr := e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: "ready_for_review", ID: task.ID, FromState: "working"})
		return false, errors.Join(err, stateErr)
	}
	return true, e.queries.SetTaskCheckHead(ctx, store.SetTaskCheckHeadParams{CheckHead: sql.NullString{String: head, Valid: true}, ID: task.ID})
}
