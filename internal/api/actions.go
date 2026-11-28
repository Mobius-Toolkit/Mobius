package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// PendingAction is a tool call of an agent that waits for the approval of the Owner.
type PendingAction struct {
	// ID increases with each new action
	ID int64 `gork:"id"`
	// Role is the Role of the agent that called the tool
	Role string `gork:"role" validate:"oneof=coordinator lead triager"`
	// Tool is the name of the tool
	Tool string `gork:"tool"`
	// Arguments are the arguments of the call in the order of the tool input. A value that is not a string is its JSON
	// text
	Arguments []ActionArgument `gork:"arguments"`
	// State is pending for an action that waits for the approval, done for an action that ran, rejected for an action
	// that the Owner rejected, replaced for an action that a later call of the same tool replaced, and withdrawn for an action
	// that the agent took back
	State string `gork:"state" validate:"oneof=pending done rejected replaced withdrawn"`
	// Error is the error of the last failed run, or empty. A pending action with an error is a failed action
	Error string `gork:"error"`
	// Result is the result text of the tool after the run, or empty
	Result string `gork:"result"`
	// Message is the message of the Owner with the decision, or empty
	Message string `gork:"message"`
	// Time is the time of the call
	Time time.Time `gork:"time"`
	// OldBrief is the Brief that the approval of edit_brief overwrote, with no Lead chat link. It is empty for the
	// other tools, for a pending action, and for an action of an older release
	OldBrief string `gork:"oldBrief"`
	// OldRecurringText is the text of the recurring task that the approval of update_recurring_task or
	// delete_recurring_task changed or deleted. It is empty for the other tools and for a pending action
	OldRecurringText string `gork:"oldRecurringText"`
	// OldRecurringCron is the cron expression of that recurring task, with the same empty cases
	OldRecurringCron string `gork:"oldRecurringCron"`
	// DecidedAt is the time when the action was done, rejected, replaced or withdrawn, or null
	DecidedAt *time.Time `gork:"decidedAt"`
	// Sender is the Workstream of the Lead that made the action. It is null for the other roles
	Sender *ActionSender `gork:"sender"`
}

// ActionSender is the Workstream of the Lead that made an action.
type ActionSender struct {
	// Repository is the full name (owner/name) of the repository of the Workstream issue
	Repository string `gork:"repository"`
	// Number is the number of the Workstream issue
	Number int64 `gork:"number"`
	// Title is the title of the Workstream issue in the local copy, or empty when the copy has no such Workstream
	Title string `gork:"title"`
}

// ActionArgument is an argument of a pending action.
type ActionArgument struct {
	// Name is the name of the argument
	Name string `gork:"name"`
	// Value is the value of the argument
	Value string `gork:"value"`
	// Labels are the label names of the argument labels in their order. It is empty for each other argument
	Labels []ActionLabel `gork:"labels"`
}

// ActionLabel is a label name of an action argument.
type ActionLabel struct {
	// Name is the name of the label
	Name string `gork:"name"`
	// Color is six hex digits with no "#", or empty for a name that is not a Mobius label
	Color string `gork:"color"`
}

// ApproveActionRequest is the request of ApproveAction.
type ApproveActionRequest struct {
	Path struct {
		// ID is the id of the action
		ID int64 `gork:"id"`
	}
	Body struct {
		// Message is the message of the Owner for the agent. It can be empty
		Message string `gork:"message"`
		// Images are the images of the message, PNG, JPEG, GIF or WebP, each at most 5 MB
		Images []api.File `gork:"images" validate:"max=4,dive,max=5242880"`
		// InboxItems are the Inbox items of the message. The request has one part inboxItems for each item, in order,
		// and each part is one JSON object
		InboxItems []ChatInboxItem `gork:"inboxItems"`
		// Selections are the text selections of the message. The request has one part selections for each selection, in order,
		// and each part is one JSON object
		Selections []ChatSelection `gork:"selections" validate:"dive"`
		// Voice is true when the message came from a voice recording
		Voice bool `gork:"voice"`
		// VoiceSeconds is the length of the voice recording in seconds
		VoiceSeconds int64 `gork:"voiceSeconds" validate:"min=0"`
	}
}

// Validate checks the type of each image.
func (r *ApproveActionRequest) Validate() error {
	return validateImages(r.Body.Images)
}

// ApproveActionResponse is the response of ApproveAction.
type ApproveActionResponse struct {
	Body Envelope[PendingAction]
}

// ApproveAction runs a pending action with its stored arguments. When the run succeeds, the action is done. When the
// run fails, the action stays pending with the error, and the Owner can approve it again. The agent gets the result in
// a chat message, with the images, the Inbox items and the selections of the request. For a Triager, the comment has no
// image. It returns 404 for an unknown action, 400 for a request that has more than 4 images or an image that is not
// PNG, JPEG, GIF or WebP, and 409 for an action that is not pending.
func (h *handlers) ApproveAction(ctx context.Context, req ApproveActionRequest) (*ApproveActionResponse, engine.Record, error) {
	body := req.Body
	action, record, err := h.engine.ApproveAction(ctx, req.Path.ID, unixLines(body.Message), attachmentsOf(body.Images, body.InboxItems, body.Selections, body.Voice, body.VoiceSeconds))
	found, err := h.decided(ctx, action, err)
	if err != nil {
		return nil, engine.Record{}, err
	}
	return &ApproveActionResponse{Body: Envelope[PendingAction]{Data: found}}, record, nil
}

// RejectActionRequest is the request of RejectAction.
type RejectActionRequest struct {
	Path struct {
		// ID is the id of the action
		ID int64 `gork:"id"`
	}
	Body struct {
		// Message is the message of the Owner for the agent. It can be empty
		Message string `gork:"message"`
		// Images are the images of the message, PNG, JPEG, GIF or WebP, each at most 5 MB
		Images []api.File `gork:"images" validate:"max=4,dive,max=5242880"`
		// InboxItems are the Inbox items of the message. The request has one part inboxItems for each item, in order,
		// and each part is one JSON object
		InboxItems []ChatInboxItem `gork:"inboxItems"`
		// Selections are the text selections of the message. The request has one part selections for each selection, in order,
		// and each part is one JSON object
		Selections []ChatSelection `gork:"selections" validate:"dive"`
		// Voice is true when the message came from a voice recording
		Voice bool `gork:"voice"`
		// VoiceSeconds is the length of the voice recording in seconds
		VoiceSeconds int64 `gork:"voiceSeconds" validate:"min=0"`
	}
}

// Validate checks the type of each image.
func (r *RejectActionRequest) Validate() error {
	return validateImages(r.Body.Images)
}

// RejectActionResponse is the response of RejectAction.
type RejectActionResponse struct {
	Body Envelope[PendingAction]
}

// RejectAction rejects a pending action. No action runs, and the agent gets the message of the Owner in a chat
// message, with the images, the Inbox items and the selections of the request. For a Triager, the comment has no image.
// It returns 404 for an unknown action, 400 for a request that has more than 4 images or an image that is not PNG,
// JPEG, GIF or WebP, and 409 for an action that is not pending.
func (h *handlers) RejectAction(ctx context.Context, req RejectActionRequest) (*RejectActionResponse, engine.Record, error) {
	body := req.Body
	action, record, err := h.engine.RejectAction(ctx, req.Path.ID, unixLines(body.Message), attachmentsOf(body.Images, body.InboxItems, body.Selections, body.Voice, body.VoiceSeconds))
	found, err := h.decided(ctx, action, err)
	if err != nil {
		return nil, engine.Record{}, err
	}
	return &RejectActionResponse{Body: Envelope[PendingAction]{Data: found}}, record, nil
}

// GetActionBriefRequest is the request of GetActionBrief.
type GetActionBriefRequest struct {
	Path struct {
		// ID is the id of the action
		ID int64 `gork:"id"`
	}
}

// ActionBrief is the current Brief of the Workstream of an edit_brief action.
type ActionBrief struct {
	// Brief is the body of the Workstream issue now, with no Lead chat link line and no white space at the end
	Brief string `gork:"brief"`
}

// GetActionBriefResponse is the response of GetActionBrief.
type GetActionBriefResponse struct {
	Body Envelope[ActionBrief]
}

// GetActionBrief reads the Workstream issue of an edit_brief action from GitHub. It returns 404 for an unknown
// action, and 409 for an action of another tool.
func (h *handlers) GetActionBrief(ctx context.Context, req GetActionBriefRequest) (*GetActionBriefResponse, error) {
	brief, err := h.engine.ActionBrief(ctx, req.Path.ID)
	if errors.Is(err, engine.ErrNoAction) {
		return nil, api.NewHTTPError(http.StatusNotFound, "There is no such action.")
	}
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return nil, err
	}
	return &GetActionBriefResponse{Body: Envelope[ActionBrief]{Data: ActionBrief{Brief: brief}}}, nil
}

// GetActionRecurringRequest is the request of GetActionRecurring.
type GetActionRecurringRequest struct {
	Path struct {
		// ID is the id of the action
		ID int64 `gork:"id"`
	}
}

// ActionRecurring is the current recurring task of an update_recurring_task or delete_recurring_task action.
type ActionRecurring struct {
	// Text is the text of the recurring task now
	Text string `gork:"text"`
	// Cron is the cron expression of the recurring task now
	Cron string `gork:"cron"`
}

// GetActionRecurringResponse is the response of GetActionRecurring.
type GetActionRecurringResponse struct {
	Body Envelope[ActionRecurring]
}

// GetActionRecurring reads the recurring task of the id argument of an update_recurring_task or delete_recurring_task
// action. It returns 404 for an unknown action or a recurring task that does not exist, and 409 for an action of
// another tool.
func (h *handlers) GetActionRecurring(ctx context.Context, req GetActionRecurringRequest) (*GetActionRecurringResponse, error) {
	text, cron, err := h.engine.ActionRecurring(ctx, req.Path.ID)
	if errors.Is(err, engine.ErrNoAction) {
		return nil, api.NewHTTPError(http.StatusNotFound, "There is no such action.")
	}
	if errors.Is(err, engine.ErrNoRecurringTask) {
		return nil, api.NewHTTPError(http.StatusNotFound, "The Workstream has no such recurring task.")
	}
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return nil, err
	}
	return &GetActionRecurringResponse{Body: Envelope[ActionRecurring]{Data: ActionRecurring{Text: text, Cron: cron}}}, nil
}

func (h *handlers) decided(ctx context.Context, action store.PendingAction, err error) (PendingAction, error) {
	if errors.Is(err, engine.ErrNoAction) {
		return PendingAction{}, api.NewHTTPError(http.StatusNotFound, "There is no such action.")
	}
	if engine.Refused(err) {
		return PendingAction{}, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return PendingAction{}, err
	}
	return h.actionOf(ctx, action)
}

func (h *handlers) actionOf(ctx context.Context, action store.PendingAction) (PendingAction, error) {
	t, err := time.Parse(time.RFC3339Nano, action.Time)
	if err != nil {
		return PendingAction{}, err
	}
	arguments, err := engine.ActionArguments(action.Tool, action.Arguments)
	if err != nil {
		return PendingAction{}, err
	}
	found := PendingAction{
		ID:               action.ID,
		Role:             action.Role,
		Tool:             action.Tool,
		Arguments:        make([]ActionArgument, 0, len(arguments)),
		State:            action.State,
		Error:            action.Error,
		Result:           action.Result,
		Message:          action.Message,
		OldBrief:         action.OldBrief,
		OldRecurringText: action.OldRecurringText,
		OldRecurringCron: action.OldRecurringCron,
		Time:             t,
	}
	for _, argument := range arguments {
		labels, err := actionLabels(argument)
		if err != nil {
			return PendingAction{}, err
		}
		found.Arguments = append(found.Arguments, ActionArgument{Name: argument.Name, Value: argument.Value, Labels: labels})
	}
	if action.DecidedAt.Valid {
		decidedAt, err := time.Parse(time.RFC3339Nano, action.DecidedAt.String)
		if err != nil {
			return PendingAction{}, err
		}
		found.DecidedAt = &decidedAt
	}
	if action.Role == "lead" {
		found.Sender = &ActionSender{Repository: action.Repository, Number: action.Workstream}
		workstream, err := h.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: action.Repository, Number: action.Workstream})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return PendingAction{}, err
		}
		found.Sender.Title = workstream.Title
	}
	return found, nil
}

func actionLabels(argument engine.ActionArgument) ([]ActionLabel, error) {
	labels := []ActionLabel{}
	if argument.Name != "labels" {
		return labels, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(argument.Value), &names); err != nil {
		return nil, err
	}
	for _, name := range names {
		index := slices.IndexFunc(engine.Labels, func(label engine.Label) bool { return label.Name == name })
		color := ""
		if index >= 0 {
			color = engine.Labels[index].Color
		}
		labels = append(labels, ActionLabel{Name: name, Color: color})
	}
	return labels, nil
}

// actionOfMessage gives the action of a chat message of the author Action, or nil when the call stopped before it
// linked the card.
func (h *handlers) actionOfMessage(ctx context.Context, message store.ChatMessage) (*PendingAction, error) {
	action, err := h.queries.GetPendingActionByChatMessage(ctx, sql.NullInt64{Int64: message.ID, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	found, err := h.actionOf(ctx, action)
	return &found, err
}

// actionOfItem gives the action of an Inbox item of the kind approval, or nil when the call stopped before it linked
// the item.
func (h *handlers) actionOfItem(ctx context.Context, item store.InboxItem) (*PendingAction, error) {
	action, err := h.queries.GetPendingActionByInboxItem(ctx, sql.NullInt64{Int64: item.ID, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	found, err := h.actionOf(ctx, action)
	return &found, err
}
