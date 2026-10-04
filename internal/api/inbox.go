package api

import (
	"context"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// InboxItem is an item of the Inbox of the Owner.
type InboxItem struct {
	// ID increases with each new item
	ID int64 `gork:"id"`
	// Kind tells what the item is about: question for a question of the Lead on a task issue, Lead for a message of the
	// Lead to the Owner, Lead failed for a message or an event that the Lead did not take, ready for review for a pull
	// request of a task, stale pull request for an old pull request with a merge conflict, usage limit for a pause of a
	// Harness, stopped for a task that Mobius lost, and full disk for a local check that waits for free disk space
	Kind string `gork:"kind" validate:"oneof=question Lead 'ready for review' 'stale pull request' 'usage limit' 'Lead failed' stopped 'full disk'"`
	// Organization is the owner of the repository
	Organization string `gork:"organization"`
	// Repository is the repository as "owner/name"
	Repository string `gork:"repository"`
	// Workstream is the number of the Workstream issue, or 0 for an issue with no Workstream
	Workstream int64 `gork:"workstream"`
	// Issue is the number of the issue of the item
	Issue int64 `gork:"issue"`
	// Text is the text of the item
	Text string `gork:"text"`
	// Link is the GitHub URL of the item, or empty
	Link string `gork:"link"`
	// Time is the time of the item
	Time time.Time `gork:"time"`
	// DismissedAt is the time when the Owner dismissed the item, or null
	DismissedAt *time.Time `gork:"dismissedAt"`
}

// ListInboxRequest is the request of ListInbox.
type ListInboxRequest struct{}

// ListInboxResponse is the response of ListInbox.
type ListInboxResponse struct {
	Body Envelope[[]InboxItem]
}

// ListInbox returns the Inbox items that the Owner did not dismiss, the oldest first.
func (h *handlers) ListInbox(ctx context.Context, _ ListInboxRequest) (*ListInboxResponse, error) {
	items, err := h.engine.Inbox(ctx)
	if err != nil {
		return nil, err
	}
	inbox := make([]InboxItem, 0, len(items))
	for _, item := range items {
		found, err := inboxItemOf(item)
		if err != nil {
			return nil, err
		}
		inbox = append(inbox, found)
	}
	return &ListInboxResponse{Body: Envelope[[]InboxItem]{Data: inbox}}, nil
}

// DismissRequest is the request of Dismiss.
type DismissRequest struct {
	Path struct {
		// ID is the id of the Inbox item
		ID int64 `gork:"id"`
	}
}

// Dismiss removes an item from the Inbox.
func (h *handlers) Dismiss(ctx context.Context, req DismissRequest) error {
	return h.engine.Dismiss(ctx, req.Path.ID)
}

func inboxItemOf(item store.InboxItem) (InboxItem, error) {
	t, err := time.Parse(time.RFC3339Nano, item.Time)
	if err != nil {
		return InboxItem{}, err
	}
	found := InboxItem{
		ID:           item.ID,
		Kind:         item.Kind,
		Organization: item.Organization,
		Repository:   item.Repository,
		Workstream:   item.Workstream,
		Issue:        item.Issue,
		Text:         item.Text,
		Link:         item.Link,
		Time:         t,
	}
	if item.DismissedAt.Valid {
		dismissedAt, err := time.Parse(time.RFC3339Nano, item.DismissedAt.String)
		if err != nil {
			return InboxItem{}, err
		}
		found.DismissedAt = &dismissedAt
	}
	return found, nil
}
