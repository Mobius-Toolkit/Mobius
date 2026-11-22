package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	gh "github.com/google/go-github/v92/github"
)

const (
	commentField           = "comment"
	questionField          = "question"
	replyField             = "reply"
	replyResolveField      = "reply_resolve"
	reviewCommentField     = "review_comment"
	reviewCommentEditField = "review_comment_edit"
	autopilotField         = "autopilot"
	autoMergeField         = "auto_merge"
	evergreenField         = "evergreen"
	maintenanceField       = "maintenance"
	skillsField            = "skills"
	bodyField              = "body"
	stateField             = "state"
	workstreamField        = "workstream"
	titleField             = "title"
	approvalCommentField   = "approval_comment"
	createField            = "create"
	blockersField          = "blockers"
	parentField            = "parent"
	kindField              = "kind"
	triageField            = "triage"
	startField             = "start"
	stateLabelsField       = "state_labels"
	// bucketField is the field of the own writes of fillBuckets. No outgoing change has it.
	bucketField = "bucket"
)

// settingLabels gives the label of each field of an outgoing change that writes a setting of a Workstream.
var settingLabels = map[string]string{
	autopilotField:   autopilotLabel,
	autoMergeField:   autoMergeLabel,
	evergreenField:   evergreenLabel,
	maintenanceField: maintenanceLabel,
	skillsField:      skillsLabel,
}

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

// withoutWriteMarker gives the body of a comment with no hidden marker of an outgoing change.
func withoutWriteMarker(body string) string {
	before, _, _ := strings.Cut(body, "\n\n"+writeMarkerPrefix)
	return before
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
		Number:     sql.NullInt64{Int64: number, Valid: true},
		ReplyTo:    sql.NullInt64{Int64: replyTo, Valid: replyTo != 0},
		Field:      field,
		Text:       sql.NullString{String: text, Valid: true},
		CreatedAt:  now(),
	})
}

// addFieldChange adds a pending outgoing change with no text that writes field of the Workstream number from the row of
// workstreams.
func addFieldChange(ctx context.Context, q *store.Queries, repository string, number int64, field string) (int64, error) {
	return q.AddOutgoingChange(ctx, store.AddOutgoingChangeParams{Repository: repository, Number: sql.NullInt64{Int64: number, Valid: true}, Field: field, CreatedAt: now()})
}

// addIssueChange adds a pending outgoing change with no text that writes field of the tree issue row issue from issues.
func addIssueChange(ctx context.Context, q *store.Queries, repository string, number, issue int64, field string) (int64, error) {
	return q.AddOutgoingChange(ctx, store.AddOutgoingChangeParams{
		Repository: repository,
		Number:     sql.NullInt64{Int64: number, Valid: true},
		Issue:      sql.NullInt64{Int64: issue, Valid: true},
		Field:      field,
		CreatedAt:  now(),
	})
}

// addNewIssueChange adds a pending outgoing change with optional text for the unnumbered tree issue row issue.
func addNewIssueChange(ctx context.Context, q *store.Queries, repository string, issue int64, field, text string) (int64, error) {
	return q.AddOutgoingChange(ctx, store.AddOutgoingChangeParams{
		Repository: repository,
		Issue:      sql.NullInt64{Int64: issue, Valid: true},
		Field:      field,
		Text:       sql.NullString{String: text, Valid: text != ""},
		CreatedAt:  now(),
	})
}

// addCreateChange adds a pending outgoing change that creates the issue of the row workstream of workstreams.
func addCreateChange(ctx context.Context, q *store.Queries, repository string, workstream int64) (int64, error) {
	return q.AddOutgoingChange(ctx, store.AddOutgoingChangeParams{Repository: repository, Workstream: sql.NullInt64{Int64: workstream, Valid: true}, Field: createField, CreatedAt: now()})
}

// addSettingChange adds a pending outgoing change that writes the label of the setting field of the Workstream number.
func addSettingChange(ctx context.Context, q *store.Queries, repository string, number int64, field string) (int64, error) {
	return addFieldChange(ctx, q, repository, number, field)
}

// postComment adds a comment on the issue or the pull request number as an outgoing change, and sends it at once.
func (e *Engine) postComment(ctx context.Context, repository github.Repository, number int64, text string) error {
	id, err := e.addOutgoingChange(ctx, e.queries, repository.FullName, number, 0, commentField, text)
	if err != nil {
		return err
	}
	return e.sendNow(ctx, repository, id)
}

// postCommentOnce is postComment, but it adds no row when the newest comment change of the item is pending or sent and
// has the same text. A step that runs again after a failure thus makes no second comment.
func (e *Engine) postCommentOnce(ctx context.Context, repository github.Repository, number int64, text string) error {
	newest, err := e.queries.GetNewestCommentChange(ctx, store.GetNewestCommentChangeParams{Repository: repository.FullName, Number: number})
	if err == nil && newest.Status != "refused" && runner.WithoutMark(newest.Text.String) == runner.WithoutMark(text) {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return e.postComment(ctx, repository, number, text)
}

// sendNow sends the outgoing change id after its caller added it. It first sends each older pending change of the same
// item in order, because a change that stays pending holds the later changes. It reads the comments of an older change,
// because an earlier send of it can have made the comment, but not of the change id, because no earlier send of that
// row exists. The row stays pending when an older change stays pending or when GitHub fails. The next poll sends it
// then. Only a failed read or write of the database is an error.
func (e *Engine) sendNow(ctx context.Context, repository github.Repository, id int64) error {
	change, err := e.queries.GetOutgoingChange(ctx, id)
	if err != nil || change.Status != "pending" {
		return err
	}
	item := itemOf(change)
	mu := e.outgoingItem(change.Repository, item)
	mu.Lock()
	defer mu.Unlock()
	change, err = e.queries.GetOutgoingChange(ctx, id)
	if err != nil || change.Status != "pending" {
		return err
	}
	pending, err := e.pendingItemChanges(ctx, change.Repository, item)
	if err != nil {
		return err
	}
	for _, next := range pending {
		own := next.ID == change.ID
		if stays, err := e.send(ctx, repository, next, !own); stays || own || err != nil {
			return err
		}
	}
	return nil
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

type changeItem struct {
	kind string
	id   int64
}

func numberItem(number int64) changeItem {
	return changeItem{kind: "number", id: number}
}

// itemOf gives the item of the change: the row of issues for a change of a tree issue, the number of its issue or pull
// request, or the row of workstreams for a create with no number.
func itemOf(change store.OutgoingChange) changeItem {
	if change.Issue.Valid {
		return changeItem{kind: "issue", id: change.Issue.Int64}
	}
	if change.Number.Valid {
		return numberItem(change.Number.Int64)
	}
	return changeItem{kind: "workstream", id: change.Workstream.Int64}
}

// pendingChanges gives the pending outgoing changes of the issue or pull request number, in the order of the send.
func (e *Engine) pendingChanges(ctx context.Context, repository string, number int64) ([]store.OutgoingChange, error) {
	return e.pendingItemChanges(ctx, repository, numberItem(number))
}

// pendingItemChanges gives the pending outgoing changes of the item, in the order of the send.
func (e *Engine) pendingItemChanges(ctx context.Context, repository string, item changeItem) ([]store.OutgoingChange, error) {
	changes, err := e.queries.ListPendingOutgoingChanges(ctx, repository)
	return slices.DeleteFunc(changes, func(change store.OutgoingChange) bool { return itemOf(change) != item }), err
}

// outgoingItem gives the mutex of the item of the repository.
func (e *Engine) outgoingItem(repository string, item changeItem) *sync.Mutex {
	mu, _ := e.outgoingItems.LoadOrStore(fmt.Sprintf("%s:%s#%d", repository, item.kind, item.id), &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// sendOutgoingChanges sends each pending outgoing change of the repository in the order of the rows. Before a post, it
// reads the comments of the item, because an earlier send can have made the comment and failed to tell. A change that
// stays pending holds the later changes of its item. An item that another send holds waits for the next poll. The
// changes of other items continue. After the send, it moves sub-issues of a Workstream that has too many into buckets.
func (e *Engine) sendOutgoingChanges(ctx context.Context, repository github.Repository) error {
	changes, err := e.queries.ListPendingOutgoingChanges(ctx, repository.FullName)
	if err != nil {
		return err
	}
	waiting := map[changeItem]bool{}
	for _, change := range changes {
		item := itemOf(change)
		if waiting[item] {
			continue
		}
		if waiting[item], err = e.sendLocked(ctx, repository, change); err != nil {
			return err
		}
	}
	e.fillBuckets(ctx, repository)
	return nil
}

// sendLocked is send for the poll. It tells that the change stays pending when another send holds the item.
func (e *Engine) sendLocked(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	mu := e.outgoingItem(repository.FullName, itemOf(change))
	if !mu.TryLock() {
		return true, nil
	}
	defer mu.Unlock()
	current, err := e.queries.GetOutgoingChange(ctx, change.ID)
	if err != nil || current.Status != "pending" {
		return false, err
	}
	return e.send(ctx, repository, current, true)
}

// send sends the pending outgoing change, and marks it sent or refused. It tells if the change stays pending. Only a
// failed read or write of the database is an error. The caller holds the mutex of the item.
func (e *Engine) send(ctx context.Context, repository github.Repository, change store.OutgoingChange, read bool) (bool, error) {
	if change.Issue.Valid {
		return e.sendIssue(ctx, repository, change)
	}
	if !change.Issue.Valid && change.Field == parentField {
		return e.sendParentRemoval(ctx, repository, change)
	}
	if change.Field == createField {
		return e.sendCreate(ctx, repository, change)
	}
	if change.Field == reviewCommentEditField {
		return e.sendEdit(ctx, repository, change)
	}
	if change.Field == triageField {
		return e.sendTriage(ctx, repository, change)
	}
	if change.Field == startField {
		return e.sendStart(ctx, repository, change)
	}
	if change.Field == stateLabelsField {
		return e.sendStateLabels(ctx, repository, change)
	}
	if _, setting := settingLabels[change.Field]; setting {
		return e.sendSetting(ctx, repository, change)
	}
	if slices.Contains([]string{workstreamField, stateField, titleField, bodyField}, change.Field) {
		return e.sendWorkstream(ctx, repository, change)
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
		err = repository.ReplyToReviewComment(ctx, change.Number.Int64, change.ReplyTo.Int64, body)
	} else {
		var created *gh.IssueComment
		created, err = repository.AddComment(ctx, change.Number.Int64, body)
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
		if err := resolveThread(ctx, repository, change.Number.Int64, change.ReplyTo.Int64); err != nil {
			if !permanentRefusal(err) {
				return e.failed(ctx, repository, change, err)
			}
			log.Printf("the resolve of the thread of %s#%d failed: %v", repository.FullName, change.Number.Int64, err)
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
		comments, err := repository.ReviewComments(ctx, change.Number.Int64)
		for _, comment := range comments {
			if written(comment.GetUser().GetLogin(), comment.GetBody()) {
				return comment.GetID(), comment.GetHTMLURL(), nil
			}
		}
		return 0, "", err
	}
	comments, err := repository.Comments(ctx, change.Number.Int64)
	for _, comment := range comments {
		if written(comment.GetUser().GetLogin(), comment.GetBody()) {
			return comment.GetID(), comment.GetHTMLURL(), nil
		}
	}
	return 0, "", err
}

// finish marks the change sent. In the same transaction, a review_comment stores the id of its comment, a question
// stores the id of its comment in its Inbox item, and an approval_comment stores its URL (linkApproval).
func (e *Engine) finish(ctx context.Context, change store.OutgoingChange, comment int64, url string) error {
	var item *store.InboxItem
	err := e.inTx(ctx, func(q *store.Queries) error {
		switch change.Field {
		case reviewCommentField:
			err := q.SetReviewCommentOfPullRequest(ctx, store.SetReviewCommentOfPullRequestParams{
				ReviewComment: sql.NullInt64{Int64: comment, Valid: true},
				Repository:    change.Repository,
				PullRequest:   change.Number,
			})
			if err != nil {
				return err
			}
		case questionField:
			err := q.SetQuestionComment(ctx, store.SetQuestionCommentParams{Comment: comment, Repository: change.Repository, Issue: change.Number.Int64, Since: change.CreatedAt})
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
	task, err := q.GetNewestTaskOfPullRequest(ctx, store.GetNewestTaskOfPullRequestParams{Repository: change.Repository, PullRequest: change.Number})
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
	log.Printf("the %s of %s#%d failed: %v", change.Field, repository.FullName, change.Number.Int64, err)
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
	pending, err := e.pendingItemChanges(ctx, repository.FullName, itemOf(change))
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(pending, func(other store.OutgoingChange) bool {
		return other.Field == reviewCommentField && other.ID < change.ID
	}) {
		return true, nil
	}
	task, err := e.queries.GetNewestTaskOfPullRequest(ctx, store.GetNewestTaskOfPullRequestParams{Repository: change.Repository, PullRequest: change.Number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if !task.ReviewComment.Valid {
		log.Printf("edit the review comment of %s#%d: the task has no review comment", repository.FullName, change.Number.Int64)
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

// sendSetting writes the label of the setting field to the Workstream issue when the issue differs from the row of
// workstreams. GitHub records no event for a label that the issue already has, so the send reads the issue first. A
// Workstream with no row or no issue has no setting to write, so the change is refused.
func (e *Engine) sendSetting(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	settings, err := e.queries.GetWorkstreamSettings(ctx, store.GetWorkstreamSettingsParams{Repository: change.Repository, Number: change.Number.Int64})
	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("write the %s of %s#%d: the Workstream has no settings", change.Field, change.Repository, change.Number.Int64)
		return false, e.refuse(ctx, change)
	}
	if err != nil {
		return false, err
	}
	issue, err := repository.Issue(ctx, change.Number.Int64)
	if err != nil {
		return e.failed(ctx, repository, change, err)
	}
	if issue == nil {
		return false, e.refuse(ctx, change)
	}
	on := map[string]bool{
		autopilotField:   settings.Autopilot,
		autoMergeField:   settings.AutoMerge,
		evergreenField:   settings.Evergreen,
		maintenanceField: settings.Maintenance,
		skillsField:      settings.Skills,
	}[change.Field]
	return e.sendLabel(ctx, repository, change, issue, settingLabels[change.Field], on)
}

// sendLabel adds the label to the issue when on is true and the issue lacks it, and removes the label when on is false
// and the issue has it.
func (e *Engine) sendLabel(ctx context.Context, repository github.Repository, change store.OutgoingChange, issue *gh.Issue, label string, on bool) (bool, error) {
	if hasLabel(issue, label) == on {
		return false, e.markSent(ctx, change)
	}
	kind := unlabeledChange
	if on {
		kind = labeledChange
	}
	return e.sendWrite(ctx, repository, change, label, kind, func() error {
		if on {
			return repository.AddLabel(ctx, change.Number.Int64, label)
		}
		return repository.RemoveLabel(ctx, change.Number.Int64, label)
	})
}

// sendWrite adds the own write of the change for the event of kind that write makes, calls write, and marks the change
// sent. A failed write deletes the own write, because GitHub made no event.
func (e *Engine) sendWrite(ctx context.Context, repository github.Repository, change store.OutgoingChange, label, kind string, write func() error) (bool, error) {
	ownWrite, err := addOwnWrite(ctx, e.queries, change.Repository, change.Number.Int64, change.Field, label, kind)
	if err != nil {
		return false, err
	}
	if err := write(); err != nil {
		if err := e.queries.DeleteOwnWrite(ctx, ownWrite); err != nil {
			return false, err
		}
		return e.failed(ctx, repository, change, err)
	}
	return false, e.markSent(ctx, change)
}

func (e *Engine) markSent(ctx context.Context, change store.OutgoingChange) error {
	return e.queries.SetOutgoingChangeStatus(ctx, store.SetOutgoingChangeStatusParams{Status: "sent", ID: change.ID})
}

// sendWorkstream writes the field of the Workstream from workstreams to the issue on GitHub when GitHub differs: the
// label mobius:workstream, the state, the title or the body. The label is on the issue only when the issue has a row.
// The removal of the label of an issue with no row copies the trees again, because the poll copied them with the label.
// The other fields are refused for an issue with no row. A Workstream with no issue is refused.
func (e *Engine) sendWorkstream(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	number := change.Number.Int64
	row, err := e.queries.GetWorkstream(ctx, store.GetWorkstreamParams{Repository: change.Repository, Number: number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	stored := err == nil
	if !stored && change.Field != workstreamField {
		log.Printf("write the %s of %s#%d: the Workstream has no row", change.Field, change.Repository, number)
		return false, e.refuse(ctx, change)
	}
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return e.failed(ctx, repository, change, err)
	}
	if issue == nil {
		return false, e.refuse(ctx, change)
	}
	update := func(request gh.UpdateIssueRequest) error {
		_, _, err := repository.Client.Issues.Update(ctx, repository.Owner(), repository.Name(), int(number), request)
		return err
	}
	switch change.Field {
	case workstreamField:
		pending, err := e.sendLabel(ctx, repository, change, issue, workstreamLabel, stored)
		if pending || err != nil || stored {
			return pending, err
		}
		if err := e.recopyAfterRemoval(ctx, repository, number); err != nil {
			log.Printf("copy the trees after the removal of the label of %s#%d: %v", change.Repository, number, err)
		}
		return false, nil
	case bodyField:
		body := e.withChatLink(repository, number, row.Body)
		if issue.GetBody() != body {
			if err := update(gh.UpdateIssueRequest{Body: &body}); err != nil {
				return e.failed(ctx, repository, change, err)
			}
		}
	case titleField:
		if issue.GetTitle() != row.Title {
			return e.sendWrite(ctx, repository, change, "", renamedChange, func() error { return update(gh.UpdateIssueRequest{Title: &row.Title}) })
		}
	case stateField:
		switch {
		case issue.GetState() == row.State:
		case row.State == "closed":
			return e.sendWrite(ctx, repository, change, "", closedChange, func() error {
				_, err := repository.CloseIssue(ctx, number, "completed")
				return err
			})
		default:
			return e.sendWrite(ctx, repository, change, "", reopenedChange, func() error { return update(gh.UpdateIssueRequest{State: new("open")}) })
		}
	}
	return false, e.markSent(ctx, change)
}

// sendIssue writes the field of the tree issue from issues to the issue on GitHub when GitHub differs: the create, the
// parent, the blockers, the title, the body, the state, the kind or the evergreen mark. An issue with no row or no Workstream is refused.
// A field other than create waits when the row has no number yet.
func (e *Engine) sendIssue(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	row, err := e.queries.GetIssueByID(ctx, change.Issue.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("write the %s of %s: the issue has no row", change.Field, change.Repository)
		return false, e.refuse(ctx, change)
	}
	if err != nil {
		return false, err
	}
	if !row.Workstream.Valid || !row.WorkstreamRepository.Valid || !row.WorkstreamNumber.Valid {
		log.Printf("write the %s of %s: the issue is not in a Workstream", change.Field, change.Repository)
		return false, e.refuse(ctx, change)
	}
	if change.Field == createField {
		return e.sendIssueCreate(ctx, repository, change, row)
	}
	if !row.Number.Valid {
		return true, nil
	}
	number := row.Number.Int64
	switch change.Field {
	case parentField:
		return e.sendIssueParent(ctx, repository, change, row)
	case blockersField:
		return e.sendIssueBlockers(ctx, repository, change, row)
	case titleField, bodyField, stateField, kindField, evergreenField:
		return e.sendIssueFields(ctx, repository, change, row, number)
	}
	log.Printf("write the %s of %s#%d: unknown issue field", change.Field, change.Repository, number)
	return false, e.refuse(ctx, change)
}

func (e *Engine) sendIssueFields(ctx context.Context, repository github.Repository, change store.OutgoingChange, row store.GetIssueByIDRow, number int64) (bool, error) {
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return e.failed(ctx, repository, change, err)
	}
	if issue == nil {
		return false, e.refuse(ctx, change)
	}
	update := func(request gh.UpdateIssueRequest) error {
		_, _, err := repository.Client.Issues.Update(ctx, repository.Owner(), repository.Name(), int(number), request)
		return err
	}
	switch change.Field {
	case kindField:
		return e.sendLabel(ctx, repository, change, issue, projectLabel, row.Kind == projectKind)
	case evergreenField:
		return e.sendLabel(ctx, repository, change, issue, evergreenLabel, row.Evergreen)
	case bodyField:
		body := e.withChatLink(github.Repository{FullName: row.WorkstreamRepository.String}, row.WorkstreamNumber.Int64, row.Body)
		if issue.GetBody() != body {
			return e.sendWrite(ctx, repository, change, "", editedChange, func() error { return update(gh.UpdateIssueRequest{Body: &body}) })
		}
	case titleField:
		if issue.GetTitle() != row.Title {
			return e.sendWrite(ctx, repository, change, "", renamedChange, func() error { return update(gh.UpdateIssueRequest{Title: &row.Title}) })
		}
	case stateField:
		switch {
		case issue.GetState() == row.State:
		case row.State == "closed":
			return e.sendWrite(ctx, repository, change, "", closedChange, func() error {
				_, err := repository.CloseIssue(ctx, number, "completed")
				return err
			})
		default:
			return e.sendWrite(ctx, repository, change, "", reopenedChange, func() error { return update(gh.UpdateIssueRequest{State: new("open")}) })
		}
	}
	return false, e.markSent(ctx, change)
}

// sendParentRemoval removes a sub-issue link on GitHub when an untrusted user added an issue with no tree row below
// a parent. change.Text holds parentMetadata.
func (e *Engine) sendParentRemoval(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	var meta parentMetadata
	if err := json.Unmarshal([]byte(change.Text.String), &meta); err != nil {
		return false, err
	}
	parentRepo, ok := e.namedRepository(meta.ParentRepository, repository)
	if !ok {
		return false, e.refuse(ctx, change)
	}
	var childID int64
	if childRepo, readable := e.namedRepository(change.Repository, repository); readable {
		child, err := childRepo.Issue(ctx, change.Number.Int64)
		if err != nil {
			return e.failed(ctx, repository, change, err)
		}
		if child == nil {
			return false, e.markSent(ctx, change)
		}
		childID = child.GetID()
	} else {
		children, err := parentRepo.SubIssues(ctx, meta.Parent)
		if github.NotFound(err) {
			return false, e.markSent(ctx, change)
		}
		if err != nil {
			return e.failed(ctx, repository, change, err)
		}
		for _, child := range children {
			if strings.EqualFold(blockerRepository(child, parentRepo.FullName), change.Repository) && int64(child.GetNumber()) == change.Number.Int64 {
				childID = child.GetID()
				break
			}
		}
		if childID == 0 {
			return false, e.markSent(ctx, change)
		}
	}
	ownWrite, err := addParentOwnWrite(ctx, e.queries, change.Repository, change.Number.Int64, parentRepo.FullName, meta.Parent, removedChange)
	if err != nil {
		return false, err
	}
	if _, _, err := parentRepo.Client.SubIssue.Remove(ctx, parentRepo.Owner(), parentRepo.Name(), meta.Parent, gh.SubIssueRequest{SubIssueID: childID}); err != nil {
		if delErr := e.queries.DeleteOwnWrite(ctx, ownWrite); delErr != nil {
			return false, delErr
		}
		if github.NotFound(err) {
			return false, e.markSent(ctx, change)
		}
		return e.failed(ctx, repository, change, err)
	}
	return false, e.markSent(ctx, change)
}

type wantedBlocker struct {
	id         int64
	repository string
	number     int64
}

// sendIssueBlockers writes the blockers of row to GitHub: it adds each blocker of row that GitHub lacks, and removes
// each open blocker on GitHub that row lacks. A closed blocker on GitHub stays.
func (e *Engine) sendIssueBlockers(ctx context.Context, repository github.Repository, change store.OutgoingChange, row store.GetIssueByIDRow) (bool, error) {
	number := row.Number.Int64
	wanted, err := e.queries.ListBlockersOfIssue(ctx, row.ID)
	if err != nil {
		return false, err
	}
	wantedIDs := map[int64]bool{}
	items := make([]wantedBlocker, 0, len(wanted))
	for _, blocker := range wanted {
		if !blocker.Number.Valid {
			return true, nil
		}
		meta, err := parseMetadata(blocker.Metadata)
		if err != nil {
			return false, err
		}
		if meta.ID == 0 {
			blockerRepo, ok := e.github.Repository(blocker.Repository)
			if !ok {
				continue
			}
			found, err := blockerRepo.Issue(ctx, blocker.Number.Int64)
			if err != nil {
				return e.failed(ctx, repository, change, err)
			}
			if found == nil {
				continue
			}
			meta.ID = found.GetID()
		}
		wantedIDs[meta.ID] = true
		items = append(items, wantedBlocker{id: meta.ID, repository: blocker.Repository, number: blocker.Number.Int64})
	}
	current, err := repository.BlockedBy(ctx, number)
	if err != nil {
		return e.failed(ctx, repository, change, err)
	}
	currentIDs := map[int64]bool{}
	for _, found := range current {
		currentIDs[found.GetID()] = true
	}
	for _, item := range items {
		if currentIDs[item.id] {
			continue
		}
		ownWrite, err := addBlockerOwnWrite(ctx, e.queries, change.Repository, number, item.repository, item.number, addedChange)
		if err != nil {
			return false, err
		}
		if _, _, err := repository.Client.Issues.AddBlockedBy(ctx, repository.Owner(), repository.Name(), number, gh.IssueDependencyRequest{IssueID: item.id}); err != nil {
			if delErr := e.queries.DeleteOwnWrite(ctx, ownWrite); delErr != nil {
				return false, delErr
			}
			return e.failed(ctx, repository, change, err)
		}
	}
	for _, found := range current {
		if found.GetState() != "open" || wantedIDs[found.GetID()] {
			continue
		}
		blockerRepo := blockerRepository(found, repository.FullName)
		ownWrite, err := addBlockerOwnWrite(ctx, e.queries, change.Repository, number, blockerRepo, int64(found.GetNumber()), removedChange)
		if err != nil {
			return false, err
		}
		if _, _, err := repository.Client.Issues.RemoveBlockedBy(ctx, repository.Owner(), repository.Name(), number, found.GetID()); err != nil {
			if delErr := e.queries.DeleteOwnWrite(ctx, ownWrite); delErr != nil {
				return false, delErr
			}
			return e.failed(ctx, repository, change, err)
		}
	}
	return false, e.markSent(ctx, change)
}

// deleteOldIncomingChanges deletes each done incoming change and each own write that is older than finishedChangeLife.
func (e *Engine) deleteOldIncomingChanges(ctx context.Context) error {
	before := time.Now().Add(-finishedChangeLife).UTC().Format(time.RFC3339Nano)
	if err := e.queries.DeleteDoneIncomingChanges(ctx, before); err != nil {
		return err
	}
	return e.queries.DeleteOldOwnWrites(ctx, before)
}

// deleteFinishedOutgoingChanges deletes each sent or refused outgoing change that is older than finishedChangeLife.
func (e *Engine) deleteFinishedOutgoingChanges(ctx context.Context) error {
	return e.queries.DeleteFinishedOutgoingChanges(ctx, time.Now().Add(-finishedChangeLife).UTC().Format(time.RFC3339Nano))
}
