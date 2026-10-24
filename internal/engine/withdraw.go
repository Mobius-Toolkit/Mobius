package engine

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const withdrawDescription = "Take back your own open request when the Owner answers or decides in the chat. The ID is the ID that ask of the Coordinator and each tool that waits for the approval of the Owner return. For a question or a pending action, Mobius closes its Inbox item and its row in the tray. A pending action does not run. The Lead closes a question on GitHub with close_question, not with withdraw."

type withdrawInput struct {
	ID int64 `json:"id"`
}

// withdraw closes the open request id of c. The refusal for a request that does not exist is the same as the refusal
// for the request of another agent.
func (e *Engine) withdraw(ctx context.Context, c caller, _ github.Repository, input withdrawInput) (string, error) {
	notYours := refuse("You have no open request with the ID %d.", input.ID)
	item, err := e.queries.GetInboxItem(ctx, input.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && item.DismissedAt.Valid {
		return "", notYours
	}
	if err != nil {
		return "", err
	}
	switch {
	case item.Kind == questionKind && item.Issue != 0 && c.role == LeadRole:
		if item.WorkstreamRepository != c.repository || item.Workstream != c.workstream {
			return "", notYours
		}
		return "", refuse("Close a question on GitHub with close_question.")
	case isCoordinatorQuestion(item) && c.role == CoordinatorRole && item.Organization == c.organization:
		if err := e.queries.SetInboxItemWithdrawn(ctx, item.ID); err != nil {
			return "", err
		}
		if err := e.Dismiss(ctx, item.ID); err != nil {
			return "", err
		}
		return "The request is withdrawn.", nil
	case item.Kind == approvalKind:
		return e.withdrawAction(ctx, c, item, notYours)
	}
	return "", notYours
}

func (e *Engine) withdrawAction(ctx context.Context, c caller, item store.InboxItem, notYours error) (string, error) {
	found, err := e.queries.GetPendingActionByInboxItem(ctx, sql.NullInt64{Int64: item.ID, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return "", notYours
	}
	if err != nil {
		return "", err
	}
	if found.Role != actionRole(c.role) || found.Organization != c.organization || found.Repository != c.repository || found.Workstream != c.workstream {
		return "", notYours
	}
	action, err := e.queries.WithdrawPendingAction(ctx, store.WithdrawPendingActionParams{DecidedAt: sql.NullString{String: now(), Valid: true}, ID: found.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", refuse("The action runs now or the Owner decided it. Withdraw changes nothing.")
	}
	if err != nil {
		return "", err
	}
	if err := e.dismissAction(ctx, action); err != nil {
		return "", err
	}
	e.publish(Change{Action: &action})
	return "The request is withdrawn.", nil
}
