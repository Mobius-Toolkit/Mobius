package engine

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	gh "github.com/google/go-github/v92/github"
)

// The kinds of the Inbox items.
const (
	questionKind       = "question"
	stoppedKind        = "stopped"
	lostKind           = "lost"
	readyForReviewKind = "ready for review"
	fullDiskKind       = "full disk"
)

var inboxKinds = []string{questionKind, stoppedKind, lostKind, readyForReviewKind, leadFailedKind}

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
//
// When a question is done and the task of its issue is still stopped, the newest stopped item of the stop opens again.
func (e *Engine) closeInboxItems(ctx context.Context, repository github.Repository) error {
	items, err := e.Inbox(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Repository != repository.FullName || !slices.Contains([]string{questionKind, stoppedKind, lostKind, readyForReviewKind}, item.Kind) {
			continue
		}
		issue, err := repository.Issue(ctx, item.Issue)
		if err != nil {
			return err
		}
		done, err := e.actionDone(ctx, issue, item)
		if err != nil {
			return err
		}
		if !done {
			continue
		}
		if err := e.Dismiss(ctx, item.ID); err != nil {
			return err
		}
		if item.Kind == questionKind && issue != nil && issue.GetState() != "closed" && hasLabel(issue, needsHumanLabel) {
			if err := e.reopenStoppedItem(ctx, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) actionDone(ctx context.Context, issue *gh.Issue, item store.InboxItem) (bool, error) {
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

// reopenStoppedItem opens the newest stopped item of the issue of the question item again, when the live task of the
// issue is in needs_human and the item is not older than that stop.
func (e *Engine) reopenStoppedItem(ctx context.Context, question store.InboxItem) error {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: question.Repository, Issue: question.Issue})
	if errors.Is(err, sql.ErrNoRows) || err == nil && task.State != "needs_human" {
		return nil
	}
	if err != nil {
		return err
	}
	stopped, err := e.queries.GetNewestInboxItem(ctx, store.GetNewestInboxItemParams{Repository: question.Repository, Issue: question.Issue, Kind: stoppedKind})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	itemTime, err := time.Parse(time.RFC3339Nano, stopped.Time)
	if err != nil {
		return err
	}
	stoppedAt, err := time.Parse(time.RFC3339Nano, task.NeedsHumanAt.String)
	if err != nil {
		return err
	}
	if itemTime.Before(stoppedAt) {
		return nil
	}
	item, err := e.queries.ReopenInboxItem(ctx, stopped.ID)
	if err != nil {
		return err
	}
	e.publish(Change{Inbox: &item})
	return nil
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

// SendAgain adds a new Owner message to the chat of the open Lead failed item id, with the text and the images of the
// message that the Lead did not take. Then it removes the item from the Inbox.
func (e *Engine) SendAgain(ctx context.Context, id int64) error {
	item, err := e.queries.GetInboxItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return refuse("This item has no message to send again.")
	}
	if err != nil {
		return err
	}
	if item.Kind != leadFailedKind || item.DismissedAt.Valid || !item.ChatMessage.Valid {
		return refuse("This item has no message to send again.")
	}
	message, err := e.queries.GetChatMessage(ctx, item.ChatMessage.Int64)
	if err != nil {
		return err
	}
	images, err := e.messageImages(message.ID)
	if err != nil {
		return err
	}
	key := ChatKey{Organization: message.Organization, Repository: message.Repository, Workstream: message.Workstream}
	if err := e.postChat(ctx, key, ownerAuthor, "", message.Text, images); err != nil {
		return err
	}
	return e.Dismiss(ctx, id)
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
