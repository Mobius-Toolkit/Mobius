package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

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
// with the same text. A failure of the comment does not skip the Inbox item. Call tellStop after the labels change,
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
	text := fmt.Sprintf("%s\n\nNext step: %s", notice.text, notice.next)
	_, commentErr := repository.AddComment(ctx, task.Issue, text)
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
	return errors.Join(commentErr, itemErr)
}
