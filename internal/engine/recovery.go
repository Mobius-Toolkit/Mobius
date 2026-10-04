package engine

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

const (
	workingLabel    = "mobius:working"
	needsHumanLabel = "mobius:needs-human"
	// stoppedKind is the kind of the Inbox item of a task that stopped.
	stoppedKind = "stopped"
	lostText    = "Mobius lost the state of this task. Add mobius:ready to start again."
)

// handLostTasks hands to a human each issue of repository that has mobius:working and no live task: the task
// is lost with the store. The Inbox item comes before the label change, so a later poll finds the issue again
// after a failure.
func (e *Engine) handLostTasks(ctx context.Context, repository github.Repository) error {
	issues, err := repository.OpenIssuesWithLabel(ctx, workingLabel)
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
		if _, err := e.queries.AddInboxItem(ctx, store.AddInboxItemParams{
			Kind:         stoppedKind,
			Organization: repository.Owner(),
			Repository:   repository.FullName,
			Workstream:   workstream,
			Issue:        number,
			Text:         lostText,
			Link:         issue.GetHTMLURL(),
			Time:         now(),
		}); err != nil {
			return err
		}
		if err := repository.AddLabel(ctx, number, needsHumanLabel); err != nil {
			return err
		}
		if err := repository.RemoveLabel(ctx, number, workingLabel); err != nil {
			return err
		}
	}
	return nil
}
