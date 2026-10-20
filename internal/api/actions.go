package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
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
	// that the Owner rejected, and replaced for an action that a later call of the same tool replaced
	State string `gork:"state" validate:"oneof=pending done rejected replaced"`
	// Error is the error of the last failed run, or empty. A pending action with an error is a failed action
	Error string `gork:"error"`
	// Result is the result text of the tool after the run, or empty
	Result string `gork:"result"`
	// Message is the message of the Owner with the decision, or empty
	Message string `gork:"message"`
	// Time is the time of the call
	Time time.Time `gork:"time"`
	// DecidedAt is the time when the action was done, rejected or replaced, or null
	DecidedAt *time.Time `gork:"decidedAt"`
}

// ActionArgument is an argument of a pending action.
type ActionArgument struct {
	// Name is the name of the argument
	Name string `gork:"name"`
	// Value is the value of the argument
	Value string `gork:"value"`
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
	}
}

// ApproveActionResponse is the response of ApproveAction.
type ApproveActionResponse struct {
	Body Envelope[PendingAction]
}

// ApproveAction runs a pending action with its stored arguments. When the run succeeds, the action is done. When the
// run fails, the action stays pending with the error, and the Owner can approve it again. The agent gets the result in
// a chat message. It returns 404 for an unknown action, and 409 for an action that is not pending.
func (h *handlers) ApproveAction(ctx context.Context, req ApproveActionRequest) (*ApproveActionResponse, error) {
	action, err := h.engine.ApproveAction(ctx, req.Path.ID, req.Body.Message)
	found, err := h.decided(action, err)
	if err != nil {
		return nil, err
	}
	return &ApproveActionResponse{Body: Envelope[PendingAction]{Data: found}}, nil
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
	}
}

// RejectActionResponse is the response of RejectAction.
type RejectActionResponse struct {
	Body Envelope[PendingAction]
}

// RejectAction rejects a pending action. No action runs, and the agent gets the message of the Owner in a chat
// message. It returns 404 for an unknown action, and 409 for an action that is not pending.
func (h *handlers) RejectAction(ctx context.Context, req RejectActionRequest) (*RejectActionResponse, error) {
	action, err := h.engine.RejectAction(ctx, req.Path.ID, req.Body.Message)
	found, err := h.decided(action, err)
	if err != nil {
		return nil, err
	}
	return &RejectActionResponse{Body: Envelope[PendingAction]{Data: found}}, nil
}

func (h *handlers) decided(action store.PendingAction, err error) (PendingAction, error) {
	if errors.Is(err, engine.ErrNoAction) {
		return PendingAction{}, api.NewHTTPError(http.StatusNotFound, "There is no such action.")
	}
	if engine.Refused(err) {
		return PendingAction{}, api.NewHTTPError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return PendingAction{}, err
	}
	return actionOf(action)
}

func actionOf(action store.PendingAction) (PendingAction, error) {
	t, err := time.Parse(time.RFC3339Nano, action.Time)
	if err != nil {
		return PendingAction{}, err
	}
	arguments, err := engine.ActionArguments(action.Arguments)
	if err != nil {
		return PendingAction{}, err
	}
	found := PendingAction{
		ID:        action.ID,
		Role:      action.Role,
		Tool:      action.Tool,
		Arguments: make([]ActionArgument, 0, len(arguments)),
		State:     action.State,
		Error:     action.Error,
		Result:    action.Result,
		Message:   action.Message,
		Time:      t,
	}
	for _, argument := range arguments {
		found.Arguments = append(found.Arguments, ActionArgument{Name: argument.Name, Value: argument.Value})
	}
	if action.DecidedAt.Valid {
		decidedAt, err := time.Parse(time.RFC3339Nano, action.DecidedAt.String)
		if err != nil {
			return PendingAction{}, err
		}
		found.DecidedAt = &decidedAt
	}
	return found, nil
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
	found, err := actionOf(action)
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
	found, err := actionOf(action)
	return &found, err
}
