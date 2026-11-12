package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	gh "github.com/google/go-github/v92/github"
)

const (
	commentField           = "comment"
	replyField             = "reply"
	replyResolveField      = "reply_resolve"
	reviewCommentField     = "review_comment"
	reviewCommentEditField = "review_comment_edit"
	approvalCommentField   = "approval_comment"
)

// writeMarkerPrefix starts the hidden marker that ends each comment of an outgoing change. The marker holds the id of
// the row, so a send again finds a comment that GitHub already has.
const writeMarkerPrefix = "<!-- mobius:write "

// markerReserve is the number of characters that a caller leaves free in a comment for the marker.
const markerReserve = 64

// finishedChangeLife is the time that a sent or refused outgoing change stays in the database.
const finishedChangeLife = 24 * time.Hour

func writeMarker(id int64) string {
	return fmt.Sprintf("%s%d -->", writeMarkerPrefix, id)
}

// isWriteComment tells if the body is the body of a comment that an outgoing change sent. Such a comment is no question.
func isWriteComment(body string) bool {
	return strings.Contains(body, writeMarkerPrefix)
}

// permanentRefusal tells whether err is a 4xx answer of GitHub that is not a rate limit, or a GraphQL error of the type
// FORBIDDEN or NOT_FOUND. A call that got such an answer fails again. A GraphQL rate limit, a timeout and an error with
// no type can pass, so they are no permanent refusal.
func permanentRefusal(err error) bool {
	var graphQL *github.GraphQLError
	if errors.As(err, &graphQL) {
		return graphQL.Type == "FORBIDDEN" || graphQL.Type == "NOT_FOUND"
	}
	var refused *gh.ErrorResponse
	return errors.As(err, &refused) && refused.Response.StatusCode < http.StatusInternalServerError && refused.Response.StatusCode != http.StatusTooManyRequests
}

// addOutgoingChange adds a pending outgoing change for the issue or the pull request number, with q, so the caller can
// add it in the transaction of its own change. replyTo is the first comment of the review thread of a reply, and 0
// for each other field.
func (e *Engine) addOutgoingChange(ctx context.Context, q *store.Queries, repository string, number, replyTo int64, field, text string) (int64, error) {
	return q.AddOutgoingChange(ctx, store.AddOutgoingChangeParams{
		Repository: repository,
		Number:     number,
		ReplyTo:    sql.NullInt64{Int64: replyTo, Valid: replyTo != 0},
		Field:      field,
		Text:       sql.NullString{String: text, Valid: true},
		CreatedAt:  now(),
	})
}

// postComment adds a comment on the issue or the pull request number as an outgoing change, and sends it at once.
func (e *Engine) postComment(ctx context.Context, repository github.Repository, number int64, text string) error {
	id, err := e.addOutgoingChange(ctx, e.queries, repository.FullName, number, 0, commentField, text)
	if err != nil {
		return err
	}
	return e.sendNow(ctx, repository, id)
}

// sendNow sends the outgoing change id after its caller added it. It reads no comments, because no earlier send of the
// row exists. The row stays pending when an older pending row of the same item exists or when GitHub fails. The next
// poll sends it then. Only a failed read or write of the database is an error.
func (e *Engine) sendNow(ctx context.Context, repository github.Repository, id int64) error {
	e.outgoingMu.Lock()
	defer e.outgoingMu.Unlock()
	change, err := e.queries.GetOutgoingChange(ctx, id)
	if err != nil || change.Status != "pending" {
		return err
	}
	pending, err := e.pendingChanges(ctx, repository.FullName, change.Number)
	if err != nil {
		return err
	}
	if pending[0].ID != change.ID {
		return nil
	}
	_, err = e.send(ctx, repository, change, false)
	return err
}

// sentOrLater gives the text sent when the outgoing change id is sent, and the text later when it stays pending. A
// refused change is a refusal of the tool.
func (e *Engine) sentOrLater(ctx context.Context, id int64, sent, later string) (string, error) {
	change, err := e.queries.GetOutgoingChange(ctx, id)
	if err != nil {
		return "", err
	}
	switch change.Status {
	case "sent":
		return sent, nil
	case "pending":
		return later, nil
	}
	return "", refuse("GitHub refused the write.")
}

// pendingChanges gives the pending outgoing changes of the issue or the pull request number, in the order of the send.
func (e *Engine) pendingChanges(ctx context.Context, repository string, number int64) ([]store.OutgoingChange, error) {
	changes, err := e.queries.ListPendingOutgoingChanges(ctx, repository)
	return slices.DeleteFunc(changes, func(change store.OutgoingChange) bool { return change.Number != number }), err
}

// sendOutgoingChanges sends each pending outgoing change of the repository in the order of the rows. Before a post, it
// reads the comments of the item, because an earlier send can have made the comment and failed to tell. A change that
// stays pending holds the later changes of its item. The changes of other items continue.
func (e *Engine) sendOutgoingChanges(ctx context.Context, repository github.Repository) error {
	e.outgoingMu.Lock()
	defer e.outgoingMu.Unlock()
	changes, err := e.queries.ListPendingOutgoingChanges(ctx, repository.FullName)
	if err != nil {
		return err
	}
	waiting := map[int64]bool{}
	for _, change := range changes {
		if waiting[change.Number] {
			continue
		}
		if waiting[change.Number], err = e.send(ctx, repository, change, true); err != nil {
			return err
		}
	}
	return nil
}

// send sends the pending outgoing change, and marks it sent or refused. It tells if the change stays pending. Only a
// failed read or write of the database is an error. The caller holds outgoingMu.
func (e *Engine) send(ctx context.Context, repository github.Repository, change store.OutgoingChange, read bool) (bool, error) {
	if change.Field == reviewCommentEditField {
		return e.sendEdit(ctx, repository, change)
	}
	if read {
		comment, url, err := findWrittenComment(ctx, repository, change)
		if err != nil {
			return e.failed(ctx, repository, change, err)
		}
		if comment != 0 {
			return e.complete(ctx, repository, change, comment, url)
		}
	}
	text := change.Text.String
	marker := "\n\n" + writeMarker(change.ID)
	body := tail(text, commentLimit-utf8.RuneCountInString(marker)) + marker
	var comment int64
	var url string
	var err error
	if isReply(change) {
		err = repository.ReplyToReviewComment(ctx, change.Number, change.ReplyTo.Int64, body)
	} else {
		var created *gh.IssueComment
		created, err = repository.AddComment(ctx, change.Number, body)
		comment, url = created.GetID(), created.GetHTMLURL()
	}
	if err != nil {
		return e.failed(ctx, repository, change, err)
	}
	return e.complete(ctx, repository, change, comment, url)
}

func isReply(change store.OutgoingChange) bool {
	return change.Field == replyField || change.Field == replyResolveField
}

// complete does the steps after the comment of the change is on GitHub, and marks the change sent. A reply_resolve
// resolves its thread first. The reply is on GitHub, so a resolve that GitHub refuses for all time is only logged, and it
// does not hold the later changes of the item. It tells if the change stays pending.
func (e *Engine) complete(ctx context.Context, repository github.Repository, change store.OutgoingChange, comment int64, url string) (bool, error) {
	if change.Field == replyResolveField {
		if err := resolveThread(ctx, repository, change.Number, change.ReplyTo.Int64); err != nil {
			if !permanentRefusal(err) {
				return e.failed(ctx, repository, change, err)
			}
			log.Printf("the resolve of the thread of %s#%d failed: %v", repository.FullName, change.Number, err)
		}
	}
	return false, e.finish(ctx, change, comment, url)
}

// resolveThread resolves the review thread of the pull request number that starts with the comment root. It reads the
// node id of the thread from GitHub. A thread that is gone or resolved needs no resolve.
func resolveThread(ctx context.Context, repository github.Repository, number, root int64) error {
	threads, err := repository.ReviewThreads(ctx, number)
	if err != nil {
		return err
	}
	for _, thread := range threads {
		if thread.Comment == root && !thread.Resolved {
			return repository.ResolveReviewThread(ctx, thread.ID)
		}
	}
	return nil
}

// findWrittenComment gives the id and the URL of the comment of the Mobius App that holds the marker of the change, or
// the id 0.
func findWrittenComment(ctx context.Context, repository github.Repository, change store.OutgoingChange) (int64, string, error) {
	marker := writeMarker(change.ID)
	app := appLogin(repository.AppSlug)
	written := func(login, body string) bool {
		return strings.EqualFold(login, app) && strings.Contains(body, marker)
	}
	if isReply(change) {
		comments, err := repository.ReviewComments(ctx, change.Number)
		for _, comment := range comments {
			if written(comment.GetUser().GetLogin(), comment.GetBody()) {
				return comment.GetID(), comment.GetHTMLURL(), nil
			}
		}
		return 0, "", err
	}
	comments, err := repository.Comments(ctx, change.Number)
	for _, comment := range comments {
		if written(comment.GetUser().GetLogin(), comment.GetBody()) {
			return comment.GetID(), comment.GetHTMLURL(), nil
		}
	}
	return 0, "", err
}

// finish marks the change sent. In the same transaction, a review_comment stores the id of its comment, and an
// approval_comment stores its URL (linkApproval).
func (e *Engine) finish(ctx context.Context, change store.OutgoingChange, comment int64, url string) error {
	var item *store.InboxItem
	err := e.inTx(ctx, func(q *store.Queries) error {
		switch change.Field {
		case reviewCommentField:
			err := q.SetReviewCommentOfPullRequest(ctx, store.SetReviewCommentOfPullRequestParams{
				ReviewComment: sql.NullInt64{Int64: comment, Valid: true},
				Repository:    change.Repository,
				PullRequest:   sql.NullInt64{Int64: change.Number, Valid: true},
			})
			if err != nil {
				return err
			}
		case approvalCommentField:
			var err error
			if item, err = linkApproval(ctx, q, change, url); err != nil {
				return err
			}
		}
		return q.SetOutgoingChangeStatus(ctx, store.SetOutgoingChangeStatusParams{Status: "sent", ID: change.ID})
	})
	if err == nil && item != nil {
		e.publish(Change{Inbox: item})
	}
	return err
}

// linkApproval stores the URL of the approval comment of the change on the task that waits for it, and as the report
// link of the ready_for_review Inbox item that approve added after it added the change. It gives the changed item, or
// nil when the change is not the newest approval comment of the task, or when the item is gone.
func linkApproval(ctx context.Context, q *store.Queries, change store.OutgoingChange, url string) (*store.InboxItem, error) {
	task, err := q.GetNewestTaskOfPullRequest(ctx, store.GetNewestTaskOfPullRequestParams{Repository: change.Repository, PullRequest: sql.NullInt64{Int64: change.Number, Valid: true}})
	if errors.Is(err, sql.ErrNoRows) || err == nil && task.ApprovedAgainChange.Int64 != change.ID {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = q.SetTaskApprovedAgainComment(ctx, store.SetTaskApprovedAgainCommentParams{ApprovedAgainComment: sql.NullString{String: url, Valid: true}, ID: task.ID})
	if err != nil {
		return nil, err
	}
	created, err := time.Parse(time.RFC3339Nano, change.CreatedAt)
	if err != nil {
		return nil, err
	}
	return linkReport(ctx, q, task, url, created)
}

// linkReport stores url as the report link of the newest ready_for_review Inbox item of the task, when the item is not
// older than notBefore. It gives the changed item, or nil when the task has no such item or the item has the link.
func linkReport(ctx context.Context, q *store.Queries, task store.Task, url string, notBefore time.Time) (*store.InboxItem, error) {
	item, err := q.GetNewestInboxItem(ctx, store.GetNewestInboxItemParams{Repository: task.Repository, Issue: task.Issue, Kind: readyForReviewKind})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	added, err := time.Parse(time.RFC3339Nano, item.Time)
	if err != nil {
		return nil, err
	}
	if added.Before(notBefore) || item.ReportLink == url {
		return nil, nil
	}
	item, err = q.SetInboxItemReportLink(ctx, store.SetInboxItemReportLinkParams{ReportLink: url, ID: item.ID})
	return &item, err
}

// failed handles the error err of the read or the send of the change. A permanent refusal marks the change refused.
// Any other error leaves it pending for the next poll.
func (e *Engine) failed(ctx context.Context, repository github.Repository, change store.OutgoingChange, err error) (bool, error) {
	log.Printf("the %s of %s#%d failed: %v", change.Field, repository.FullName, change.Number, err)
	if !permanentRefusal(err) {
		return true, nil
	}
	return false, e.refuse(ctx, change)
}

func (e *Engine) refuse(ctx context.Context, change store.OutgoingChange) error {
	return e.queries.SetOutgoingChangeStatus(ctx, store.SetOutgoingChangeStatusParams{Status: "refused", ID: change.ID})
}

// sendEdit replaces the body of the comment of the newest task of the pull request. It waits while an older
// review_comment change of the pull request is pending, because that change stores the comment. A task with no comment
// has none to edit, so the change is refused. The edit ends the round, so it clears the comment of the task.
func (e *Engine) sendEdit(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	pending, err := e.pendingChanges(ctx, repository.FullName, change.Number)
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(pending, func(other store.OutgoingChange) bool {
		return other.Field == reviewCommentField && other.ID < change.ID
	}) {
		return true, nil
	}
	task, err := e.queries.GetNewestTaskOfPullRequest(ctx, store.GetNewestTaskOfPullRequestParams{Repository: change.Repository, PullRequest: sql.NullInt64{Int64: change.Number, Valid: true}})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if !task.ReviewComment.Valid {
		log.Printf("edit the review comment of %s#%d: the task has no review comment", repository.FullName, change.Number)
		return false, e.refuse(ctx, change)
	}
	if err := repository.UpdateComment(ctx, task.ReviewComment.Int64, change.Text.String); err != nil {
		return e.failed(ctx, repository, change, err)
	}
	return false, e.inTx(ctx, func(q *store.Queries) error {
		if err := q.SetReviewComment(ctx, store.SetReviewCommentParams{ID: task.ID}); err != nil {
			return err
		}
		return q.SetOutgoingChangeStatus(ctx, store.SetOutgoingChangeStatusParams{Status: "sent", ID: change.ID})
	})
}

// deleteFinishedOutgoingChanges deletes each sent or refused outgoing change that is older than finishedChangeLife.
func (e *Engine) deleteFinishedOutgoingChanges(ctx context.Context) error {
	return e.queries.DeleteFinishedOutgoingChanges(ctx, time.Now().Add(-finishedChangeLife).UTC().Format(time.RFC3339Nano))
}
