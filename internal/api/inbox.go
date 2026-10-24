package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// InboxItem is an item of the Inbox of the Owner.
type InboxItem struct {
	// ID increases with each new item
	ID int64 `gork:"id"`
	// Kind tells what the item is about: question for a question of the Lead on an issue, Lead failed for a message
	// that the Lead did not take, ready for review for a pull request of a task, stopped for a task that a person must
	// continue (a stale pull request or a session that Mobius stopped after its hangs), lost for a task that Mobius lost,
	// and full disk for a local check that waits for free disk space, and approval for a tool call of an agent that waits
	// for the approval of the Owner, and todo for a deployment need of a pull request
	Kind string `gork:"kind" validate:"oneof=question 'Lead failed' 'ready for review' stopped lost 'full disk' approval todo"`
	// Organization is the owner of the repository
	Organization string `gork:"organization"`
	// Repository is the repository as "owner/name"
	Repository string `gork:"repository"`
	// WorkstreamRepository is the repository of the Workstream issue as "owner/name". It differs from Repository for a task whose Workstream is in another repository
	WorkstreamRepository string `gork:"workstreamRepository"`
	// Workstream is the number of the Workstream issue, or 0 for an issue with no Workstream
	Workstream int64 `gork:"workstream"`
	// Issue is the number of the issue of the item
	Issue int64 `gork:"issue"`
	// Title is the title of the issue, or empty
	Title string `gork:"title"`
	// Reason is the short reason of the item
	Reason string `gork:"reason"`
	// Text is the full text of the item
	Text string `gork:"text"`
	// Link is the GitHub URL of the issue, or empty
	Link string `gork:"link"`
	// PullRequest is the number of the pull request of the item, or 0
	PullRequest int64 `gork:"pullRequest"`
	// PullRequestURL is the GitHub URL of the pull request, or empty
	PullRequestURL string `gork:"pullRequestUrl"`
	// ReportURL is the GitHub URL of the approval report, or empty
	ReportURL string `gork:"reportUrl"`
	// Time is the time of the item
	Time time.Time `gork:"time"`
	// DismissedAt is the time when the Owner dismissed the item, or null
	DismissedAt *time.Time `gork:"dismissedAt"`
	// Merging is true after the Owner sent the approval of the pull request, until GitHub refuses the merge
	Merging bool `gork:"merging"`
	// MergeRefusal is the reason of the last refusal of GitHub to merge the pull request, or empty
	MergeRefusal string `gork:"mergeRefusal"`
	// Questions are the questions with options of a question item, or an empty list
	Questions []Question `gork:"questions"`
	// Answer is the answer of the Owner to a question item, or empty
	Answer string `gork:"answer"`
	// Withdrawn is true when the agent withdrew the question item
	Withdrawn bool `gork:"withdrawn"`
	// Action is the action of an approval item, or null for another item
	Action *PendingAction `gork:"action"`
	// Todo is the deployment need of a todo item, or null for another item
	Todo *Todo `gork:"todo"`
}

// Todo is the deployment need of a todo Inbox item.
type Todo struct {
	// Kind is the kind of the need: env_var, config, secret or manual_step
	Kind string `gork:"kind"`
	// Name is the name of the need, for example the name of the env var
	Name string `gork:"name"`
	// Path is the file that adds the need
	Path string `gork:"path"`
	// Line is the line of the file that adds the need
	Line int64 `gork:"line"`
	// Reason tells why the deployment needs it
	Reason string `gork:"reason"`
	// Mandatory is true when the deployment fails or the application does not start without it
	Mandatory bool `gork:"mandatory"`
	// Note is the note of the Owner when the todo is done, or empty
	Note string `gork:"note"`
}

// Question is a question of the Lead with its options.
type Question struct {
	// Text is the question
	Text string `gork:"text"`
	// Multiple is true when the Owner can select more than one option
	Multiple bool `gork:"multiple"`
	// Options are the options of the question
	Options []QuestionOption `gork:"options"`
}

// QuestionOption is an option of a Question.
type QuestionOption struct {
	// Title is the short answer
	Title string `gork:"title"`
	// Details is the explanation of the option, or empty
	Details string `gork:"details"`
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
		found, err := h.inboxItemOf(ctx, item)
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
	return h.engine.DismissByOwner(ctx, req.Path.ID)
}

// SendAgainRequest is the request of SendAgain.
type SendAgainRequest struct {
	Path struct {
		// ID is the id of the Inbox item
		ID int64 `gork:"id"`
	}
}

// SendAgain adds the Owner message of an open Lead failed item to its chat again, with the same text and images, and
// removes the item from the Inbox. It returns 409 for an item that is not an open Lead failed item with a message, and
// while Mobius restarts for an upgrade.
func (h *handlers) SendAgain(ctx context.Context, req SendAgainRequest) error {
	err := h.engine.SendAgain(ctx, req.Path.ID)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}

// AnswerInboxItemRequest is the request of AnswerInboxItem.
type AnswerInboxItemRequest struct {
	Path struct {
		// ID is the id of the Inbox item
		ID int64 `gork:"id"`
	}
	Body struct {
		// Text is the answer of the Owner
		Text string `gork:"text"`
	}
}

// AnswerInboxItemResponse is the response of AnswerInboxItem.
type AnswerInboxItemResponse struct {
	Body Envelope[AnsweredComment]
}

// AnsweredComment is the comment that AnswerInboxItem posted.
type AnsweredComment struct {
	// URL is the GitHub URL of the comment
	URL string `gork:"url"`
}

// AnswerInboxItem posts the text as a comment of the Owner on the issue of an Inbox item. Mobius treats the comment as
// a comment of a trusted user. Mobius posts it with the user token of the Owner, so the Owner must authorize the Mobius
// App first. For a question of the Coordinator, it instead adds the text to the Coordinator chat as a message of the
// Owner, and the URL is empty. It returns 404 for an unknown item, 400 for an item with no issue and for an empty text, and 409 with the
// steps when the Owner did not authorize the App.
func (h *handlers) AnswerInboxItem(ctx context.Context, req AnswerInboxItemRequest) (*AnswerInboxItemResponse, error) {
	if strings.TrimSpace(req.Body.Text) == "" {
		return nil, api.NewHTTPError(http.StatusBadRequest, "The text must not be empty.")
	}
	url, err := h.engine.AnswerInboxItem(ctx, req.Path.ID, req.Body.Text)
	if errors.Is(err, engine.ErrNoInboxItem) {
		return nil, api.NewHTTPError(http.StatusNotFound, "The Inbox has no such item.")
	}
	if errors.Is(err, engine.ErrNoInboxIssue) {
		return nil, api.NewHTTPError(http.StatusBadRequest, "The Inbox item has no issue.")
	}
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return nil, err
	}
	return &AnswerInboxItemResponse{Body: Envelope[AnsweredComment]{Data: AnsweredComment{URL: url}}}, nil
}

// MergeInboxItemRequest is the request of MergeInboxItem.
type MergeInboxItemRequest struct {
	Path struct {
		// ID is the id of the Inbox item
		ID int64 `gork:"id"`
	}
}

// MergeInboxItemResponse is the response of MergeInboxItem.
type MergeInboxItemResponse struct {
	Body Envelope[InboxItem]
}

// MergeInboxItem approves the pull request of a ready for review Inbox item with the user token of the Owner, so the
// Owner must authorize the Mobius App first. Mobius then merges the pull request with the checks of an approval on
// GitHub. It returns the item in the merging state. It returns 404 for an unknown item, and 409 with the reason for an
// item that is not open, for a task that is not ready for review, for a pull request that is not open, and when the
// Owner did not authorize the App.
func (h *handlers) MergeInboxItem(ctx context.Context, req MergeInboxItemRequest) (*MergeInboxItemResponse, error) {
	item, err := h.engine.MergeInboxItem(ctx, req.Path.ID)
	if errors.Is(err, engine.ErrNoInboxItem) {
		return nil, api.NewHTTPError(http.StatusNotFound, "The Inbox has no such item.")
	}
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return nil, err
	}
	found, err := h.inboxItemOf(ctx, item)
	if err != nil {
		return nil, err
	}
	return &MergeInboxItemResponse{Body: Envelope[InboxItem]{Data: found}}, nil
}

// DoneInboxItemRequest is the request of DoneInboxItem.
type DoneInboxItemRequest struct {
	Path struct {
		// ID is the id of the Inbox item
		ID int64 `gork:"id"`
	}
	Body struct {
		// Note is the note of the Owner, or empty
		Note string `gork:"note"`
	}
}

// DoneInboxItemResponse is the response of DoneInboxItem.
type DoneInboxItemResponse struct {
	Body Envelope[InboxItem]
}

// DoneInboxItem marks the todo of an open todo Inbox item as done, with the note, and removes the item from the Inbox.
// When the todo was the last mandatory todo of a task with the automatic merge of Autopilot, Mobius approves the pull
// request for the merge. It returns the changed item. It returns 404 for an unknown item and 409 for an item that is
// not an open todo.
func (h *handlers) DoneInboxItem(ctx context.Context, req DoneInboxItemRequest) (*DoneInboxItemResponse, error) {
	item, err := h.engine.DoneInboxItem(ctx, req.Path.ID, req.Body.Note)
	if errors.Is(err, engine.ErrNoInboxItem) {
		return nil, api.NewHTTPError(http.StatusNotFound, "The Inbox has no such item.")
	}
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return nil, err
	}
	found, err := h.inboxItemOf(ctx, item)
	if err != nil {
		return nil, err
	}
	return &DoneInboxItemResponse{Body: Envelope[InboxItem]{Data: found}}, nil
}

func (h *handlers) inboxItemOf(ctx context.Context, item store.InboxItem) (InboxItem, error) {
	t, err := time.Parse(time.RFC3339Nano, item.Time)
	if err != nil {
		return InboxItem{}, err
	}
	found := InboxItem{
		ID:                   item.ID,
		Kind:                 item.Kind,
		Organization:         item.Organization,
		Repository:           item.Repository,
		WorkstreamRepository: item.WorkstreamRepository,
		Workstream:           item.Workstream,
		Issue:                item.Issue,
		Title:                item.Title,
		Reason:               item.Reason,
		Text:                 item.Text,
		Link:                 item.Link,
		PullRequest:          item.PullRequest,
		ReportURL:            item.ReportLink,
		Time:                 t,
		Merging:              item.Merging != 0,
		MergeRefusal:         item.MergeRefusal,
		Answer:               item.Answer,
		Withdrawn:            item.Withdrawn != 0,
		Questions:            []Question{},
	}
	if item.Questions != "" {
		var questions []engine.Question
		if err := json.Unmarshal([]byte(item.Questions), &questions); err != nil {
			return InboxItem{}, err
		}
		for _, question := range questions {
			options := make([]QuestionOption, 0, len(question.Options))
			for _, option := range question.Options {
				options = append(options, QuestionOption{Title: option.Title, Details: option.Details})
			}
			found.Questions = append(found.Questions, Question{Text: question.Text, Multiple: question.Multiple, Options: options})
		}
	}
	if item.PullRequest != 0 {
		// The web URL of a pull request differs from the web URL of its issue only in the path.
		found.PullRequestURL = strings.TrimSuffix(item.Link, fmt.Sprintf("/issues/%d", item.Issue)) + fmt.Sprintf("/pull/%d", item.PullRequest)
	}
	if item.DismissedAt.Valid {
		dismissedAt, err := time.Parse(time.RFC3339Nano, item.DismissedAt.String)
		if err != nil {
			return InboxItem{}, err
		}
		found.DismissedAt = &dismissedAt
	}
	if item.Kind == "approval" {
		if found.Action, err = h.actionOfItem(ctx, item); err != nil {
			return InboxItem{}, err
		}
	}
	if item.Kind == "todo" {
		todo, err := h.queries.GetDeploymentTodoOfItem(ctx, item.ID)
		if err != nil {
			return InboxItem{}, err
		}
		found.Todo = &Todo{Kind: todo.Kind, Name: todo.Name, Path: todo.Path, Line: todo.Line, Reason: todo.Reason, Mandatory: todo.Mandatory != 0, Note: todo.Note}
	}
	return found, nil
}
