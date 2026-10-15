package engine

import (
	"context"
	"errors"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// nextStepMarker starts the next action in the comment of a stop notice.
const nextStepMarker = "\n\nNext step: "

// isStopComment tells if the body is a comment of tellStop or the lost comment of handLostTasks. Such a comment is no
// question.
func isStopComment(body string) bool {
	return body == lostText || strings.Contains(body, nextStepMarker)
}

// stopNotice tells the Owner why a task stopped and what to do next.
type stopNotice struct {
	// reason is the short reason of the Inbox item.
	reason string
	// text is the reason text. The comment and the Inbox item both hold it, so the repository can be public: it has no
	// local path and no error output.
	text string
	// output is the error output and the local path. Only the Inbox item holds it.
	output string
	// next is the next action of the Owner.
	next string
}

// tellStop posts the reason text and the next action of notice on the issue of the task, and adds a stopped Inbox item
// with the same text. When the issue has an open question item, the stopped item is dismissed at once, so the issue has
// one open item. A comment that GitHub refuses goes to comment_writes, and the next poll sends it again. Call tellStop after the labels change,
// because closeInboxItems dismisses a stopped item of an issue with no mobius:needs-human.
func (e *Engine) tellStop(ctx context.Context, task store.Task, title string, notice stopNotice) error {
	repository, err := e.repository(task.Repository)
	if err != nil {
		return err
	}
	current, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if err != nil {
		return err
	}
	text := notice.text + nextStepMarker + notice.next
	commentErr := e.addOrKeepComment(ctx, repository, task.Issue, text)
	if notice.output != "" {
		text += "\n\n" + notice.output
	}
	itemErr := e.addInboxItem(ctx, store.AddInboxItemParams{
		Kind:         stoppedKind,
		Organization: repository.Owner(),
		Repository:   task.Repository,
		Workstream:   task.Workstream,
		Issue:        task.Issue,
		Title:        title,
		Reason:       notice.reason,
		Text:         text,
		Link:         issueLink(task.Repository, task.Issue),
		PullRequest:  current.PullRequest.Int64,
	})
	if itemErr == nil {
		itemErr = e.dismissStoppedItemOfQuestion(ctx, task)
	}
	return errors.Join(commentErr, itemErr)
}

// dismissStoppedItemOfQuestion dismisses the stopped items of the issue of the task when the issue has an open question
// item.
func (e *Engine) dismissStoppedItemOfQuestion(ctx context.Context, task store.Task) error {
	items, err := e.Inbox(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Repository == task.Repository && item.Issue == task.Issue && item.Kind == questionKind {
			return e.dismissIssueItems(ctx, task.Repository, task.Issue, stoppedKind)
		}
	}
	return nil
}
