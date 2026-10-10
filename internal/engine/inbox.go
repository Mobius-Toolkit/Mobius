package engine

import (
	"context"
	"database/sql"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The kinds of the Inbox items that the Lead adds.
const (
	questionKind = "question"
	leadKind     = "Lead"
)

// The kinds of the Inbox items of the pull requests and the checks.
const (
	readyForReviewKind   = "ready for review"
	stalePullRequestKind = "stale pull request"
	fullDiskKind         = "full disk"
	checkErrorsKind      = "check errors"
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

// Dismiss removes the Inbox item id from the Inbox.
func (e *Engine) Dismiss(ctx context.Context, id int64) error {
	item, err := e.queries.DismissInboxItem(ctx, store.DismissInboxItemParams{DismissedAt: sql.NullString{String: now(), Valid: true}, ID: id})
	if err != nil {
		return err
	}
	e.publish(Change{Inbox: &item})
	return nil
}
