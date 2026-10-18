package engine

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
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
	// checkTitle and checkSummary complete the open Mobius check of the head as failure. An empty checkTitle leaves the
	// check as it is.
	checkTitle, checkSummary string
}

// tellStop posts the reason text and the next action of notice on the issue of the task, and adds a stopped Inbox item
// with the same text. When the issue has an open question item, the stopped item is dismissed at once, so the issue has
// one open item. A comment that GitHub refuses goes to comment_writes, and the next poll sends it again.
// Call tellStop after the labels change, because closeInboxItems dismisses a stopped item of an issue with no
// mobius:needs-human.
func (e *Engine) tellStop(ctx context.Context, task store.Task, title string, notice stopNotice) error {
	repository, err := e.repository(task.Repository)
	if err != nil {
		return err
	}
	current, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if err != nil {
		return err
	}
	commentErr := e.addOrKeepComment(ctx, repository, task.Issue, notice.comment())
	itemErr := e.addInboxItem(ctx, stoppedItem(current, title, notice))
	if itemErr == nil {
		itemErr = e.dismissStoppedItemOfQuestion(ctx, task)
	}
	return errors.Join(commentErr, itemErr)
}

// comment gives the text of the stop comment.
func (n stopNotice) comment() string {
	return n.text + nextStepMarker + n.next
}

// stoppedItem gives the stopped Inbox item of notice for the issue of the task.
func stoppedItem(task store.Task, title string, notice stopNotice) store.AddInboxItemParams {
	text := notice.comment()
	if notice.output != "" {
		text += "\n\n" + notice.output
	}
	return store.AddInboxItemParams{
		Kind:         stoppedKind,
		Organization: owner(task.Repository),
		Repository:   task.Repository,
		Workstream:   task.Workstream,
		Issue:        task.Issue,
		Title:        title,
		Reason:       notice.reason,
		Text:         text,
		Link:         issueLink(task.Repository, task.Issue),
		PullRequest:  task.PullRequest.Int64,
	}
}

// storeStopNotice gives the record of a stop notice that is not delivered yet. leadText is the text of the stop event of
// the Lead. A notice with no text gives the Lead an event only.
func storeStopNotice(task store.Task, title, leadText string, notice stopNotice) store.AddStopNoticeParams {
	return store.AddStopNoticeParams{
		Task:         task.ID,
		Title:        title,
		Reason:       notice.reason,
		Text:         notice.text,
		Output:       notice.output,
		Next:         notice.next,
		LeadText:     leadText,
		CheckTitle:   notice.checkTitle,
		CheckSummary: notice.checkSummary,
		CreatedAt:    now(),
	}
}

// sendStopNotices delivers the stop notices of the repository that an earlier step stored: the stopped Inbox item, the
// stop event of the Lead and the comment. A notice is deleted after all of them succeed, or when its task left
// needs_human, or when GitHub answers 404 or 410. A write that fails stays for the next poll.
// The caller of handToHuman gives the id of its task. Then sendStopNotices sends only the notice of that task. The poll
// gives 0, and sendStopNotices sends each notice of the repository whose issue has mobius:needs-human. It writes no
// label, because a person can have removed the label to resume the task. checkTask heals the label or resumes the task.
func (e *Engine) sendStopNotices(ctx context.Context, name string, task int64) error {
	e.stopNoticeMu.Lock()
	defer e.stopNoticeMu.Unlock()
	rows, err := e.queries.ListStopNotices(ctx, name)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if task != 0 && row.Task.ID != task {
			continue
		}
		if row.Task.State == "needs_human" {
			if task == 0 {
				if labelled, err := e.hasNeedsHuman(ctx, row.Task); err != nil || !labelled {
					if err != nil {
						log.Printf("read the labels of %s#%d: %v", name, row.Task.Issue, err)
					}
					continue
				}
			}
			if err := e.deliverStopNotice(ctx, row.StopNotice, row.Task); err != nil && !github.NotFound(err) {
				log.Printf("send the stop notice of %s#%d: %v", name, row.Task.Issue, err)
				continue
			}
		}
		if err := e.queries.DeleteStopNotice(ctx, row.StopNotice.ID); err != nil {
			return err
		}
	}
	return nil
}

// hasNeedsHuman tells if the issue of the task has mobius:needs-human. closeInboxItems dismisses a stopped item of an
// issue with no such label.
func (e *Engine) hasNeedsHuman(ctx context.Context, task store.Task) (bool, error) {
	repository, err := e.repository(task.Repository)
	if err != nil {
		return false, err
	}
	issue, err := repository.Issue(ctx, task.Issue)
	return issue != nil && hasLabel(issue, needsHumanLabel), err
}

// deliverStopNotice does each write of the stop notice once. The comment is the last write, so that a retry after a
// failed write does not post it twice.
func (e *Engine) deliverStopNotice(ctx context.Context, record store.StopNotice, task store.Task) error {
	repository, err := e.repository(task.Repository)
	if err != nil {
		return err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, record.CreatedAt)
	if err != nil {
		return err
	}
	notice := stopNotice{reason: record.Reason, text: record.Text, output: record.Output, next: record.Next}
	if record.CheckTitle != "" {
		if err := e.failStoppedCheck(ctx, repository, task, record.CheckTitle, record.CheckSummary); err != nil && !github.NotFound(err) {
			return err
		}
	}
	if record.Text != "" {
		if err := e.addStoppedItemOnce(ctx, task, record.Title, notice, createdAt); err != nil {
			return err
		}
	}
	stopped, err := e.queries.ListStopEvents(ctx, store.ListStopEventsParams{Repository: task.Repository, Issue: sql.NullInt64{Int64: task.Issue, Valid: true}, Payload: record.LeadText})
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(stopped, func(event store.LeadEvent) bool { return !timeBefore(event.Time, createdAt) }) {
		if err := e.addLeadEvent(ctx, task.Repository, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "stop", record.LeadText); err != nil {
			return err
		}
	}
	if record.Text == "" {
		return nil
	}
	return e.addOrKeepComment(ctx, repository, task.Issue, notice.comment())
}

// failStoppedCheck completes the open Mobius check of the head as failure. The head is the failed head of a CI stop, or
// else the head of the pull request of the task. A head whose Mobius checks are all complete keeps them, so that a retry
// adds no second check. A head with no Mobius check gets a failed one. A task with no pull request has no check.
func (e *Engine) failStoppedCheck(ctx context.Context, repository github.Repository, task store.Task, title, summary string) error {
	head := task.CiFailedHead.String
	if !task.CiFailedHead.Valid {
		if !task.PullRequest.Valid {
			return nil
		}
		pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
		if err != nil {
			return err
		}
		head = pullRequest.GetHead().GetSHA()
	}
	runs, err := repository.CheckRuns(ctx, head)
	if err != nil {
		return err
	}
	var complete bool
	for _, run := range runs {
		if run.GetName() != checkRunName {
			continue
		}
		if run.GetStatus() != "completed" {
			return repository.UpdateCheckRun(ctx, run.GetID(), checkRunName, "completed", "failure", title, summary)
		}
		complete = true
	}
	if complete {
		return nil
	}
	return repository.CreateCheckRun(ctx, checkRunName, head, "completed", "failure", title, summary)
}

// addStoppedItemOnce adds the stopped Inbox item of notice unless the issue has an item with the same text from createdAt
// or later. Then it dismisses the item when the issue has an open question item.
func (e *Engine) addStoppedItemOnce(ctx context.Context, task store.Task, title string, notice stopNotice, createdAt time.Time) error {
	params := stoppedItem(task, title, notice)
	items, err := e.queries.ListIssueInboxItems(ctx, store.ListIssueInboxItemsParams{Repository: task.Repository, Issue: task.Issue, Kind: stoppedKind})
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(items, func(item store.InboxItem) bool { return item.Text == params.Text && !timeBefore(item.Time, createdAt) }) {
		if err := e.addInboxItem(ctx, params); err != nil {
			return err
		}
	}
	return e.dismissStoppedItemOfQuestion(ctx, task)
}

// timeBefore tells if the RFC3339Nano text value is before limit. A text that is no time is before it.
func timeBefore(value string, limit time.Time) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err != nil || parsed.Before(limit)
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
