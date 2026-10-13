package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	workingLabel    = "mobius:working"
	needsHumanLabel = "mobius:needs-human"
	questionLabel   = "mobius:question"
	reviewLabel     = "mobius:review"
	lostText        = "Mobius lost the state of this task. Add mobius:ready to start again."
	findingsLost    = "The findings that the Lead kept for the next fix round are lost."
)

// handLostTasks continues the earlier task of each issue of repository that has mobius:needs-human, mobius:review or
// mobius:working and no live task, when its pull request is still open. Else it hands to a human each such issue that has
// mobius:working or mobius:review: the task is lost with the store. The Inbox item comes before the label change, so a
// later poll finds the issue again after a failure.
func (e *Engine) handLostTasks(ctx context.Context, repository github.Repository) error {
	for _, label := range []string{needsHumanLabel, reviewLabel, workingLabel} {
		if err := e.handLostTasksWith(ctx, repository, label); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) handLostTasksWith(ctx context.Context, repository github.Repository, label string) error {
	issues, err := repository.OpenIssuesWithLabel(ctx, label)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		number := int64(issue.GetNumber())
		_, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		recovered, err := e.recoverTask(ctx, repository, issue, label)
		if err != nil {
			return err
		}
		if recovered || label == needsHumanLabel {
			continue
		}
		workstream, err := workstreamOf(ctx, repository, number)
		if err != nil {
			return err
		}
		if err := e.addInboxItem(ctx, store.AddInboxItemParams{
			Kind:         lostKind,
			Organization: repository.Owner(),
			Repository:   repository.FullName,
			Workstream:   workstream,
			Issue:        number,
			Title:        issue.GetTitle(),
			Reason:       "Mobius lost the task",
			Text:         lostText,
			Link:         issue.GetHTMLURL(),
		}); err != nil {
			return err
		}
		if err := repository.AddLabel(ctx, number, needsHumanLabel); err != nil {
			return err
		}
		if err := repository.RemoveLabel(ctx, number, workingLabel); err != nil {
			return err
		}
		if err := repository.RemoveLabel(ctx, number, reviewLabel); err != nil {
			return err
		}
	}
	return nil
}

// endedTaskToContinue gives the newest ended task of the issue number that has a branch and a pull request, when
// Mobius can continue it: the pull request is open with the branch of the task as its head, and the Workstream issue is
// open. A pull request closes when its head branch is deleted.
func (e *Engine) endedTaskToContinue(ctx context.Context, repository github.Repository, number int64) (store.Task, bool, error) {
	task, err := e.queries.GetEndedTaskWithPullRequest(ctx, store.GetEndedTaskWithPullRequestParams{Repository: repository.FullName, Issue: number})
	if errors.Is(err, sql.ErrNoRows) {
		return store.Task{}, false, nil
	}
	if err != nil {
		return store.Task{}, false, err
	}
	pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
	if err != nil {
		return store.Task{}, false, err
	}
	if pullRequest.GetState() != "open" || pullRequest.GetHead().GetRef() != task.Branch.String {
		return store.Task{}, false, nil
	}
	workstream, err := workstreamOf(ctx, repository, number)
	if err != nil || workstream == 0 || workstream != task.Workstream {
		return store.Task{}, false, err
	}
	issue, err := repository.Issue(ctx, workstream)
	return task, issue.GetState() == "open", err
}

// recoverTask opens the earlier task of the issue again, in the state that the label of the issue and the state before
// the end give. It tells if the task is live again. It gives false when no task fits or when the task reached
// max_worker_restarts recoveries.
func (e *Engine) recoverTask(ctx context.Context, repository github.Repository, issue *gh.Issue, label string) (bool, error) {
	task, found, err := e.endedTaskToContinue(ctx, repository, int64(issue.GetNumber()))
	if err != nil || !found {
		return false, err
	}
	state := "ready_for_review"
	switch label {
	case needsHumanLabel:
		state = "needs_human"
	case workingLabel:
		if state, err = e.stateBeforeEnd(ctx, task.ID); err != nil {
			return false, err
		}
	}
	recovered, err := e.queries.RecoverTask(ctx, store.RecoverTaskParams{State: state, ID: task.ID, Max: int64(e.config.MaxWorkerRestarts)})
	if err != nil || recovered == 0 {
		return false, err
	}
	text := fmt.Sprintf("%s recovery of #%d \"%s\": Mobius restarted and found no live task. Mobius continued the task in %s with the branch %s and the pull request #%d (recovery %d of %d). %s",
		time.Now().UTC().Format(timeFormat), issue.GetNumber(), issue.GetTitle(), state, task.Branch.String, task.PullRequest.Int64, task.Recoveries+1, e.config.MaxWorkerRestarts, findingsLost)
	e.publish(Change{Workstreams: true})
	return true, e.addLeadEvent(ctx, repository.FullName, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "recovery", text)
}

// stateBeforeEnd gives the state that the task had before its end, when the task can go on from it. Else it gives checks.
func (e *Engine) stateBeforeEnd(ctx context.Context, task int64) (string, error) {
	state, err := e.queries.GetLastTaskState(ctx, task)
	if errors.Is(err, sql.ErrNoRows) {
		return "checks", nil
	}
	if err != nil {
		return "", err
	}
	switch state {
	case "queued", "working", "reviewed", "checks", "approval":
		return state, nil
	}
	return "checks", nil
}

// continueEndedTask opens the ended task again in the state stopped, because the Owner continued the issue, and
// continues it as resume says. The limit of recoveries does not apply.
func (e *Engine) continueEndedTask(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task, actor string) error {
	reopened, err := e.queries.ContinueEndedTask(ctx, task.ID)
	if err != nil {
		return err
	}
	if reopened > 0 {
		text := fmt.Sprintf("%s recovery of #%d \"%s\": the Owner continued the issue, and it had no live task. Mobius continued the earlier task with the branch %s and the pull request #%d. %s",
			time.Now().UTC().Format(timeFormat), issue.GetNumber(), issue.GetTitle(), task.Branch.String, task.PullRequest.Int64, findingsLost)
		if err := e.addLeadEvent(ctx, repository.FullName, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "recovery", text); err != nil {
			return err
		}
	}
	task.State, task.Recoveries = "stopped", 0
	return e.resume(ctx, repository, issue, task, actor)
}
