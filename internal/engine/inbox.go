package engine

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The kinds of the Inbox items.
const (
	questionKind       = "question"
	stoppedKind        = "stopped"
	lostKind           = "lost"
	readyForReviewKind = "ready for review"
	fullDiskKind       = "full disk"
)

// addInboxItem adds the Inbox item at the time now, and sends it to the listeners.
func (e *Engine) addInboxItem(ctx context.Context, params store.AddInboxItemParams) error {
	params.Time = now()
	item, err := e.queries.AddInboxItem(ctx, params)
	if err != nil {
		return err
	}
	e.publish(Change{Inbox: &item})
	return nil
}

// Inbox gives the Inbox items that the Owner did not dismiss, the oldest first.
func (e *Engine) Inbox(ctx context.Context) ([]store.InboxItem, error) {
	return e.queries.ListOpenInboxItems(ctx)
}

// closeInboxItems dismisses each Inbox item of repository whose action of the Owner is done. It reads the issue of the
// item from GitHub, because an item can be newer than the labels of the local copy.
//   - A question is done when the issue has no mobius:question.
//   - A stopped task is done when the issue has no mobius:needs-human.
//   - A lost task is done when the issue has a live task.
//   - Each item is done when its issue is closed.
func (e *Engine) closeInboxItems(ctx context.Context, repository github.Repository) error {
	items, err := e.Inbox(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Repository != repository.FullName || !slices.Contains([]string{questionKind, stoppedKind, lostKind, readyForReviewKind}, item.Kind) {
			continue
		}
		done, err := e.actionDone(ctx, repository, item)
		if err != nil {
			return err
		}
		if done {
			if err := e.Dismiss(ctx, item.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) actionDone(ctx context.Context, repository github.Repository, item store.InboxItem) (bool, error) {
	issue, err := repository.Issue(ctx, item.Issue)
	if err != nil {
		return false, err
	}
	if issue == nil || issue.GetState() == "closed" {
		return true, nil
	}
	switch item.Kind {
	case questionKind:
		return !hasLabel(issue, questionLabel), nil
	case stoppedKind:
		return !hasLabel(issue, needsHumanLabel), nil
	case lostKind:
		_, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}
	return false, nil
}

// dismissIssueItems dismisses each Inbox item of the kinds that is open on the issue number of repository.
func (e *Engine) dismissIssueItems(ctx context.Context, repository string, number int64, kinds ...string) error {
	items, err := e.Inbox(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Repository == repository && item.Issue == number && slices.Contains(kinds, item.Kind) {
			if err := e.Dismiss(ctx, item.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Dismiss removes the Inbox item id from the Inbox.
func (e *Engine) Dismiss(ctx context.Context, id int64) error {
	item, err := e.queries.DismissInboxItem(ctx, store.DismissInboxItemParams{DismissedAt: sql.NullString{String: now(), Valid: true}, ID: id})
	if err != nil {
		return err
	}
	e.publish(Change{Inbox: &item})
	return nil
}
