package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// addWorkstreamToCreate is the Create handler. In one transaction, it adds the row of the new Workstream with no number,
// adds the outgoing create change, and sets created_workstream of the approved action. A run again of the action finds
// created_workstream, so it adds no second row. It gives the id of the row and the id of its create change.
func (e *Engine) addWorkstreamToCreate(ctx context.Context, repository github.Repository, input workstreamInput, action int64, approved bool) (int64, int64, error) {
	var workstream, change int64
	err := e.inTx(ctx, func(q *store.Queries) error {
		var creator sql.NullInt64
		if approved {
			row, err := q.GetPendingAction(ctx, action)
			if err != nil {
				return err
			}
			if row.CreatedWorkstream.Valid {
				workstream = row.CreatedWorkstream.Int64
				change, err = q.GetCreateChange(ctx, sql.NullInt64{Int64: workstream, Valid: true})
				return err
			}
			if row.Role == "lead" {
				creator = sql.NullInt64{Int64: row.Workstream, Valid: true}
			}
		}
		maintenance := slices.Contains(input.Labels, maintenanceLabel)
		var err error
		workstream, err = q.AddWorkstreamToCreate(ctx, store.AddWorkstreamToCreateParams{
			Repository:        repository.FullName,
			Title:             input.Title,
			Body:              input.Brief,
			CreatedAt:         now(),
			Evergreen:         slices.Contains(input.Labels, evergreenLabel) || maintenance,
			Maintenance:       maintenance,
			Skills:            slices.Contains(input.Labels, skillsLabel),
			CreatorWorkstream: creator,
		})
		if err != nil {
			return err
		}
		if change, err = addCreateChange(ctx, q, repository.FullName, workstream); err != nil {
			return err
		}
		if !approved {
			return nil
		}
		return q.SetPendingActionCreatedWorkstream(ctx, store.SetPendingActionCreatedWorkstreamParams{CreatedWorkstream: sql.NullInt64{Int64: workstream, Valid: true}, ID: action})
	})
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return workstream, change, err
}

// sendCreate creates the issue of the Workstream row of the change, writes its number to the row, and does the steps
// that need the number. A row that has a number has its issue, so a send again does only the steps after the create.
// The issue gets the title and the Brief with the marker of the change. A send again finds an issue that an earlier
// send made, by its marker. The labels, the Brief with the link line of the Lead chat, and the creation event for the
// Lead come after the number.
func (e *Engine) sendCreate(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	row, err := e.queries.GetWorkstreamToCreate(ctx, change.Workstream.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("create the Workstream %d of %s: the row is gone", change.Workstream.Int64, change.Repository)
		return false, e.refuse(ctx, change)
	}
	if err != nil {
		return false, err
	}
	number := row.Number.Int64
	if !row.Number.Valid {
		issue, err := e.findOrCreateIssue(ctx, repository, change, row.Title, row.Body, nil)
		if permanentRefusal(err) {
			log.Printf("create the Workstream %d of %s: %v", change.Workstream.Int64, change.Repository, err)
			err := e.dropCreate(ctx, change)
			if errors.Is(err, errActionNotDone) {
				return true, nil
			}
			return false, err
		}
		if err != nil {
			return e.failed(ctx, repository, change, err)
		}
		number = int64(issue.GetNumber())
		if err := e.addCreatedNumber(ctx, repository, change, row, number); err != nil {
			return false, err
		}
		e.publish(Change{Workstreams: true})
	}
	if err := e.sendNewWorkstream(ctx, repository, number); err != nil {
		return false, err
	}
	if err := e.endCreate(ctx, repository, change.Workstream.Int64, number); err != nil {
		return e.failed(ctx, repository, change, err)
	}
	return false, e.markSent(ctx, change)
}

var errActionNotDone = errors.New("the action that created the Workstream is not done")

// dropCreate marks the refused create change refused, deletes the row of the Workstream that has no issue, and clears
// created_workstream of the action, so that a new approval of the action adds a new row and a new change. When the
// result of the action told that the number comes later, the caller first gets the refusal one time.
func (e *Engine) dropCreate(ctx context.Context, change store.OutgoingChange) error {
	if err := e.endRefusedCreate(ctx, change.Workstream.Int64); err != nil {
		return err
	}
	err := e.inTx(ctx, func(q *store.Queries) error {
		if err := q.SetOutgoingChangeStatus(ctx, store.SetOutgoingChangeStatusParams{Status: "refused", ID: change.ID}); err != nil {
			return err
		}
		if err := q.DeleteWorkstreamToCreate(ctx, change.Workstream.Int64); err != nil {
			return err
		}
		return q.ClearPendingActionCreatedWorkstream(ctx, change.Workstream)
	})
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return err
}

// endRefusedCreate gives the caller of the action that created the Workstream row the refusal of GitHub, when the
// result of the action told that the number comes later. The Triager gets a comment, and a caller with a chat gets an
// Approval message. dropCreate clears awaits_number, so a run again gives no second message. It gives
// errActionNotDone while approve has not yet posted the result that the number comes later, so the change stays
// pending and the refusal comes after that result.
func (e *Engine) endRefusedCreate(ctx context.Context, workstream int64) error {
	e.approvedResultMu.Lock()
	defer e.approvedResultMu.Unlock()
	action, err := e.queries.GetPendingActionByCreatedWorkstream(ctx, sql.NullInt64{Int64: workstream, Valid: true})
	if errors.Is(err, sql.ErrNoRows) || err == nil && action.AwaitsNumber == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	if action.State != "done" {
		return errActionNotDone
	}
	name, err := e.actionName(ctx, action)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("The Owner approved %s. The result: GitHub refused the write.", name)
	c := actionCaller(action)
	if c.role != TriagerRole {
		return e.postResult(ctx, action, text)
	}
	triaged, err := e.repository(c.repository)
	if err != nil {
		return err
	}
	return e.commentToTriager(ctx, triaged, c.issue, text)
}

// findOrCreateIssue gives the issue of the change: the issue of the App that has its marker, or a new issue.
func (e *Engine) findOrCreateIssue(ctx context.Context, repository github.Repository, change store.OutgoingChange, title, brief string, labels []string) (*gh.Issue, error) {
	issues, err := repository.NewestIssues(ctx)
	if err != nil {
		return nil, err
	}
	marker := writeMarker(change.ID)
	app := appLogin(repository.AppSlug)
	for _, issue := range issues {
		if strings.EqualFold(issue.GetUser().GetLogin(), app) && strings.Contains(issue.GetBody(), marker) {
			return issue, nil
		}
	}
	body := brief + "\n\n" + marker
	issue, _, err := repository.Client.Issues.Create(ctx, repository.Owner(), repository.Name(), gh.CreateIssueRequest{Title: title, Body: &body, Labels: labels})
	return issue, err
}

// addCreatedNumber writes the number of the new issue to the Workstream row. In the same transaction, it adds the
// outgoing changes that write the label, the settings and the Brief to the issue, and the creation event for the Lead.
func (e *Engine) addCreatedNumber(ctx context.Context, repository github.Repository, change store.OutgoingChange, row store.GetWorkstreamToCreateRow, number int64) error {
	fields := []string{workstreamField}
	if row.Evergreen {
		fields = append(fields, evergreenField)
	}
	if row.Maintenance {
		fields = append(fields, maintenanceField)
	}
	if row.Skills {
		fields = append(fields, skillsField)
	}
	fields = append(fields, bodyField)
	text := eventText(time.Now(), "creation of Workstream", fmt.Sprintf("#%d", number), &gh.Issue{Title: &row.Title}, appLogin(repository.AppSlug), row.Body)
	return e.addLeadEventWith(ctx, repository.FullName, number, "", sql.NullInt64{}, "creation", text, func(q *store.Queries) error {
		err := q.SetWorkstreamNumber(ctx, store.SetWorkstreamNumberParams{Number: sql.NullInt64{Int64: number, Valid: true}, ID: change.Workstream.Int64})
		if err != nil {
			return err
		}
		for _, field := range fields {
			if _, err := addFieldChange(ctx, q, repository.FullName, number, field); err != nil {
				return err
			}
		}
		return nil
	})
}

// sendNewWorkstream sends the pending outgoing changes of the new Workstream number in the order of the rows. A change
// that stays pending holds the later changes until the next poll.
func (e *Engine) sendNewWorkstream(ctx context.Context, repository github.Repository, number int64) error {
	pending, err := e.pendingChanges(ctx, repository.FullName, number)
	if err != nil {
		return err
	}
	for _, change := range pending {
		if waiting, err := e.sendLocked(ctx, repository, change); err != nil || waiting {
			return err
		}
	}
	return nil
}

// endCreate does the steps of the caller of the approved action that created the Workstream row. When the result of the
// action told that the number comes later, the caller gets a second Approval message with the number one time
// (awaits_number is the record). Then it publishes the creation for the Coordinator, and ends the triage for the
// Triager. The Triager gets no message.
func (e *Engine) endCreate(ctx context.Context, repository github.Repository, workstream, number int64) error {
	e.approvedResultMu.Lock()
	defer e.approvedResultMu.Unlock()
	action, err := e.queries.GetPendingActionByCreatedWorkstream(ctx, sql.NullInt64{Int64: workstream, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if action.AwaitsNumber != 0 {
		if action.State != "done" {
			return errActionNotDone
		}
		name, err := e.actionName(ctx, action)
		if err != nil {
			return err
		}
		if err := e.postResult(ctx, action, fmt.Sprintf("The Owner approved %s. The result: Created the Workstream #%d.", name, number)); err != nil {
			return err
		}
		if err := e.queries.ClearPendingActionAwaitsNumber(ctx, action.ID); err != nil {
			return err
		}
	}
	switch c := actionCaller(action); c.role {
	case CoordinatorRole:
		e.publish(Change{Created: &Created{repository.FullName, number}})
	case TriagerRole:
		return e.endTriage(ctx, c, repository, number, action.Time)
	}
	return nil
}
