package engine

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	workingLabel    = "mobius:working"
	needsHumanLabel = "mobius:needs-human"
	reviewLabel     = "mobius:review"
	// stoppedKind is the kind of the Inbox item of a task that stopped.
	stoppedKind = "stopped"
	lostText    = "Mobius lost the state of this task. Add mobius:ready to start again."
)

// handLostTasks hands to a human each issue of repository that has mobius:working or mobius:review and no live task: the
// task is lost with the store. The Inbox item comes before the label change, so a later poll finds the issue again
// after a failure.
func (e *Engine) handLostTasks(ctx context.Context, repository github.Repository) error {
	for _, label := range []string{workingLabel, reviewLabel} {
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
		workstream, err := workstreamOf(ctx, repository, number)
		if err != nil {
			return err
		}
		if err := e.addInboxItem(ctx, store.AddInboxItemParams{
			Kind:         stoppedKind,
			Organization: repository.Owner(),
			Repository:   repository.FullName,
			Workstream:   workstream,
			Issue:        number,
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
