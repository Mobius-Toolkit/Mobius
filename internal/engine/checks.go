package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	// githubActions is the slug of the App of GitHub Actions.
	githubActions = "github-actions"
	// jobLogLines is the number of lines of the end of a job log of GitHub Actions in a fix round.
	jobLogLines = 200
)

// ciWait is the head that the poll saw first at a time, for a task in checks.
type ciWait struct {
	head  string
	since time.Time
}

// failedCheckRuns gives the completed check runs of other Apps that failed on the commit sha.
func failedCheckRuns(ctx context.Context, repository github.Repository, sha string) ([]*gh.CheckRun, error) {
	runs, err := repository.CheckRuns(ctx, sha)
	if err != nil {
		return nil, err
	}
	var failed []*gh.CheckRun
	for _, run := range runs {
		if run.GetName() != checkRunName && run.GetStatus() == "completed" && failedConclusion(run.GetConclusion()) {
			failed = append(failed, run)
		}
	}
	return failed, nil
}

// unhandledFailure tells if the head of the pull request of the task has a failed check run of another App and no fix
// round yet.
func (e *Engine) unhandledFailure(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	head := pullRequest.GetHead().GetSHA()
	if task.CheckHead.String == head {
		return false, nil
	}
	runs, err := failedCheckRuns(ctx, repository, head)
	return len(runs) > 0, err
}

// onFailure starts a fix round when check runs of other Apps failed on the head of the pull request of the task in
// ready_for_review, checks or approval. Each failed check run is an item with its annotations, and a check run of
// GitHub Actions also has the end of its job log. A head gets one round. It gives true when a round started, or when
// the task left its state.
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
	moved, err := e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: "working", ID: task.ID, FromState: task.State})
	if err != nil || moved == 0 {
		return true, err
	}
	if err := e.fixRound(ctx, repository, round{task: task, title: issue.GetTitle(), pullRequest: pullRequest, counts: true, items: items.String(), parent: parent, failedCheck: true}); err != nil {
		_, stateErr := e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: task.State, ID: task.ID, FromState: "working"})
		return false, errors.Join(err, stateErr)
	}
	return true, e.queries.SetTaskCheckHead(ctx, store.SetTaskCheckHeadParams{CheckHead: sql.NullString{String: head, Valid: true}, ID: task.ID})
}

// onChecks moves the task in checks to approval when the CI of the head of its pull request passed, and gives the Lead
// the event ready_for_approval one time for the head. A task that is not in checks, for example after a decline,
// stays as it is.
func (e *Engine) onChecks(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) error {
	passed, err := e.ciPassed(ctx, repository, task.ID, pullRequest.GetHead().GetSHA())
	if err != nil || !passed {
		return err
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return err
	}
	moved, err := e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: "approval", ID: task.ID, FromState: "checks"})
	if err != nil || moved == 0 {
		return err
	}
	delete(e.ciWait, task.ID)
	text := fmt.Sprintf("%s ready for Lead approval of #%d \"%s\": pull request #%d %s.", time.Now().UTC().Format(timeFormat), task.Issue, issue.GetTitle(), pullRequest.GetNumber(), pullRequest.GetHTMLURL())
	return e.addLeadEvent(ctx, task.Repository, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "ready_for_approval", text)
}

// ciPassed tells if the CI of the head passed. Each check run of another App on the head is completed and did not
// fail, and each workflow run of GitHub Actions on the head is completed. A workflow run can exist before its jobs have
// check runs. A head with no check run of another App and no workflow run has no CI yet. It counts as passed after
// review_quiet_period from the first poll that saw it, so a repository with no CI does not wait forever.
func (e *Engine) ciPassed(ctx context.Context, repository github.Repository, taskID int64, head string) (bool, error) {
	runs, err := repository.CheckRuns(ctx, head)
	if err != nil {
		return false, err
	}
	workflows, err := repository.WorkflowRuns(ctx, head)
	if err != nil {
		return false, err
	}
	others := 0
	for _, run := range runs {
		if run.GetName() == checkRunName {
			continue
		}
		if run.GetStatus() != "completed" || failedConclusion(run.GetConclusion()) {
			return false, nil
		}
		others++
	}
	if slices.ContainsFunc(workflows, func(workflow *gh.WorkflowRun) bool { return workflow.GetStatus() != "completed" }) {
		return false, nil
	}
	if others+len(workflows) > 0 {
		return true, nil
	}
	seen, ok := e.ciWait[taskID]
	if !ok || seen.head != head {
		seen = ciWait{head, time.Now()}
		e.ciWait[taskID] = seen
	}
	return time.Since(seen.since) >= e.config.ReviewQuietPeriod, nil
}

// failedConclusion tells if the conclusion of a completed check run is a failure.
func failedConclusion(conclusion string) bool {
	return conclusion == "failure" || conclusion == "timed_out" || conclusion == "cancelled"
}
