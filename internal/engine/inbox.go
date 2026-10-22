package engine

import (
	"context"
	"crypto/rand"
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
	approvalKind       = "approval"
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
		if item.Kind == questionKind && issue != nil && issue.GetState() != "closed" && hasLabel(issue, needsHumanLabel) {
			if err := e.reopenStoppedItem(ctx, item); err != nil {
				return err
			}
		}
		if err := e.Dismiss(ctx, item.ID); err != nil {
			return err
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
// issue is in needs_human and a question item of the issue was open when the stopped item closed.
func (e *Engine) reopenStoppedItem(ctx context.Context, question store.InboxItem) error {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: question.Repository, Issue: question.Issue})
	if errors.Is(err, sql.ErrNoRows) || err == nil && task.State != "needs_human" {
		return nil
	}
	if err != nil {
		return err
	}
	stopped, err := e.queries.GetNewestInboxItem(ctx, store.GetNewestInboxItemParams{Repository: question.Repository, Issue: question.Issue, Kind: stoppedKind})
	if errors.Is(err, sql.ErrNoRows) || err == nil && !stopped.DismissedAt.Valid {
		return nil
	}
	if err != nil {
		return err
	}
	dismissed, err := time.Parse(time.RFC3339Nano, stopped.DismissedAt.String)
	if err != nil {
		return err
	}
	questions, err := e.queries.ListIssueInboxItems(ctx, store.ListIssueInboxItemsParams{Repository: question.Repository, Issue: question.Issue, Kind: questionKind})
	if err != nil {
		return err
	}
	replaced := false
	for _, item := range questions {
		asked, err := time.Parse(time.RFC3339Nano, item.Time)
		if err != nil {
			return err
		}
		closed := time.Time{}
		if item.DismissedAt.Valid {
			if closed, err = time.Parse(time.RFC3339Nano, item.DismissedAt.String); err != nil {
				return err
			}
		}
		if !asked.After(dismissed) && (!item.DismissedAt.Valid || !closed.Before(dismissed)) {
			replaced = true
			break
		}
	}
	if !replaced {
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

// SendAgain adds a new Owner message to the chat of the open Lead failed item id, with the text, the images and the
// Inbox items of the message that the Lead did not take. The new message has a browser id, so a restart of Mobius gives it again. Then
// it removes the item from the Inbox.
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
	inboxItems, err := inboxItemsOf(message)
	if err != nil {
		return err
	}
	key := ChatKey{Organization: message.Organization, Repository: message.Repository, Workstream: message.Workstream}
	if err := e.postChat(ctx, key, ownerAuthor, rand.Text(), message.Text, images, inboxItems); err != nil {
		return err
	}
	return e.Dismiss(ctx, id)
}

// ErrNoInboxItem is the error of an Inbox item that does not exist.
var ErrNoInboxItem = errors.New("the Inbox has no such item")

// ErrNoInboxIssue is the error of an Inbox item with no issue.
var ErrNoInboxIssue = errors.New("the Inbox item has no issue")

// AnswerInboxItem posts text as a comment on the issue of the Inbox item id, with the user token of the Owner, and
// gives the GitHub URL of the comment. For a question of the Coordinator, it adds text to the Coordinator chat as a
// message of the Owner, dismisses the item, and gives an empty URL. It stores the id of the comment in owner_answers, so the poll treats the comment
// as a comment of a trusted user. It returns a refusal when the Owner did not authorize the Mobius App.
func (e *Engine) AnswerInboxItem(ctx context.Context, id int64, text string) (string, error) {
	item, err := e.queries.GetInboxItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoInboxItem
	}
	if err != nil {
		return "", err
	}
	if isCoordinatorQuestion(item) {
		if item.DismissedAt.Valid {
			return "", refuse("The Coordinator withdrew this question, or the Owner answered it.")
		}
		key := ChatKey{Organization: item.Organization}
		if err := e.SendChat(ctx, key, rand.Text(), text, nil, nil); err != nil {
			return "", err
		}
		return "", e.Dismiss(ctx, id)
	}
	if item.Issue == 0 {
		return "", ErrNoInboxIssue
	}
	repository, err := e.repository(item.Repository)
	if err != nil {
		return "", err
	}
	asOwner, err := e.github.AsOwner(ctx, repository)
	if err != nil {
		// The text tells the Owner how to authorize the Mobius App.
		return "", refusal(err.Error())
	}
	e.ownerAnswerMu.Lock()
	defer e.ownerAnswerMu.Unlock()
	ctx = context.WithoutCancel(ctx)
	comment, err := asOwner.AddComment(ctx, item.Issue, text)
	if err != nil {
		return "", err
	}
	if err := e.queries.AddOwnerAnswer(ctx, store.AddOwnerAnswerParams{Repository: repository.FullName, CommentID: comment.GetID()}); err != nil {
		return "", err
	}
	return comment.GetHTMLURL(), nil
}

// mergingPullRequests gives the numbers of the pull requests of the open ready for review Inbox items of repository in
// the merging state.
func (e *Engine) mergingPullRequests(ctx context.Context, repository github.Repository) ([]int64, error) {
	items, err := e.Inbox(ctx)
	if err != nil {
		return nil, err
	}
	var numbers []int64
	for _, item := range items {
		if item.Repository == repository.FullName && item.Kind == readyForReviewKind && item.Merging != 0 {
			numbers = append(numbers, item.PullRequest)
		}
	}
	return numbers, nil
}

// refuseMergeItem sets the reason of a refusal of GitHub on each open ready for review Inbox item of the issue of the
// task, and ends its merging state.
func (e *Engine) refuseMergeItem(ctx context.Context, task store.Task, reason string) error {
	e.mergeMu.Lock()
	defer e.mergeMu.Unlock()
	return e.setMergeItems(ctx, task, 0, reason)
}

// openReadyItems gives the open ready for review Inbox items of the issue of the task.
func (e *Engine) openReadyItems(ctx context.Context, task store.Task) ([]store.InboxItem, error) {
	items, err := e.queries.ListIssueInboxItems(ctx, store.ListIssueInboxItemsParams{Repository: task.Repository, Issue: task.Issue, Kind: readyForReviewKind})
	return slices.DeleteFunc(items, func(item store.InboxItem) bool { return item.DismissedAt.Valid }), err
}

// setMergeItems sets merging and the reason on each open ready for review Inbox item of the issue of the task. The
// caller must hold mergeMu.
func (e *Engine) setMergeItems(ctx context.Context, task store.Task, merging int64, reason string) error {
	items, err := e.openReadyItems(ctx, task)
	if err != nil {
		return err
	}
	for _, item := range items {
		item, err := e.queries.SetInboxItemMerge(ctx, store.SetInboxItemMergeParams{Merging: merging, MergeRefusal: reason, ID: item.ID})
		if err != nil {
			return err
		}
		e.publish(Change{Inbox: &item})
	}
	return nil
}

// MergeInboxItem approves the pull request of the open ready for review Inbox item id with the user token of the Owner,
// marks the item as merging, and starts a poll at once. The poll merges the pull request (mergeApproved). It gives the
// changed item. It returns a refusal when the item is not open, when the task of its issue is not in ready_for_review
// with the pull request of the item, when the pull request is not open, and when the Owner did not authorize the Mobius
// App. Requests take turns, so two requests at the same time give one review.
func (e *Engine) MergeInboxItem(ctx context.Context, id int64) (store.InboxItem, error) {
	e.mergeMu.Lock()
	defer e.mergeMu.Unlock()
	item, err := e.queries.GetInboxItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.InboxItem{}, ErrNoInboxItem
	}
	if err != nil {
		return store.InboxItem{}, err
	}
	if item.DismissedAt.Valid || item.Kind != readyForReviewKind || item.Merging != 0 {
		return store.InboxItem{}, refuse("This item is not waiting for a merge.")
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: item.Repository, Issue: item.Issue})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return store.InboxItem{}, err
	}
	if err != nil || task.State != "ready_for_review" || task.PullRequest.Int64 != item.PullRequest {
		return store.InboxItem{}, refuse("The task of #%d is not ready for review.", item.Issue)
	}
	repository, err := e.repository(item.Repository)
	if err != nil {
		return store.InboxItem{}, err
	}
	asOwner, err := e.github.AsOwner(ctx, repository)
	if err != nil {
		// The text tells the Owner how to authorize the Mobius App.
		return store.InboxItem{}, refusal(err.Error())
	}
	pullRequest, err := repository.PullRequest(ctx, item.PullRequest)
	if err != nil {
		return store.InboxItem{}, err
	}
	if pullRequest.GetState() != "open" {
		return store.InboxItem{}, refuse("Pull request #%d is already merged or closed.", item.PullRequest)
	}
	ctx = context.WithoutCancel(ctx)
	if err := asOwner.Approve(ctx, item.PullRequest, pullRequest.GetHead().GetSHA()); err != nil {
		return store.InboxItem{}, err
	}
	item, err = e.queries.SetInboxItemMerge(ctx, store.SetInboxItemMergeParams{Merging: 1, ID: id})
	if err != nil {
		return store.InboxItem{}, err
	}
	e.publish(Change{Inbox: &item})
	select {
	case e.pollWake <- struct{}{}:
	default:
	}
	return item, nil
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
