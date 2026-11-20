package engine

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	gh "github.com/google/go-github/v92/github"
)

// setTriage sets the triage state of the issue number in issues and adds the outgoing change that writes the label
// mobius:triage. An issue with no row gets a row with no Workstream, so the row is no tree issue. It sends the change at
// once.
func (e *Engine) setTriage(ctx context.Context, repository github.Repository, number int64, triage bool) error {
	_, err := e.queries.GetIssueTriage(ctx, store.GetIssueTriageParams{Repository: repository.FullName, Number: number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var found *gh.Issue
	var metadata string
	if err != nil {
		if found, err = repository.Issue(ctx, number); err != nil || found == nil {
			return err
		}
		if metadata, err = githubMetadata(found, 0); err != nil {
			return err
		}
	}
	var id int64
	e.copyWrite.Lock()
	err = e.inTx(ctx, func(q *store.Queries) error {
		var err error
		if found == nil {
			err = q.SetIssueTriage(ctx, store.SetIssueTriageParams{Triage: triage, Repository: repository.FullName, Number: number})
		} else {
			err = q.AddTriageIssue(ctx, store.AddTriageIssueParams{
				Repository: repository.FullName,
				Number:     number,
				Title:      found.GetTitle(),
				Body:       found.GetBody(),
				State:      found.GetState(),
				Author:     found.GetUser().GetLogin(),
				Kind:       issueKind(labelNames(found)),
				Triage:     triage,
				Github:     metadata,
			})
		}
		if err != nil {
			return err
		}
		id, err = addFieldChange(ctx, q, repository.FullName, number, triageField)
		return err
	})
	e.copyWrite.Unlock()
	if err != nil {
		return err
	}
	return e.sendNow(ctx, repository, id)
}

// hasTriageState tells if the issue has the label mobius:triage or a row in issues. The poll reads the events of such an
// issue, so it sees each change of the label.
func (e *Engine) hasTriageState(ctx context.Context, repository string, issue *gh.Issue) (bool, error) {
	if hasLabel(issue, triageLabel) {
		return true, nil
	}
	_, err := e.queries.GetIssueTriage(ctx, store.GetIssueTriageParams{Repository: repository, Number: int64(issue.GetNumber())})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func isTriageEvent(event *gh.IssueEvent) bool {
	field, _, ok := eventField(event)
	return ok && field == triageField
}

// applyTriageChange sets the triage state from the incoming change of the label mobius:triage, when its actor is a
// trusted user. For another actor, it adds an outgoing change, so the send writes the state of issues back.
func (e *Engine) applyTriageChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	repository = e.changeRepository(change.Repository, repository)
	if !e.trustedUser(change.Actor) {
		return e.writeBack(ctx, repository, change)
	}
	return e.setTriage(ctx, repository, change.Number, change.Change == labeledChange)
}

// sendTriage writes the label mobius:triage to the issue when GitHub differs from issues.triage, and copies the label to
// the labels of the row. An issue with no row has no triage.
func (e *Engine) sendTriage(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	number := change.Number.Int64
	triage, err := e.queries.GetIssueTriage(ctx, store.GetIssueTriageParams{Repository: change.Repository, Number: number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return e.failed(ctx, repository, change, err)
	}
	if issue == nil {
		return false, e.refuse(ctx, change)
	}
	if pending, err := e.sendLabel(ctx, repository, change, issue, triageLabel, triage); pending || err != nil {
		return pending, err
	}
	if triage {
		return false, e.changeCopiedLabels(ctx, change.Repository, number, []string{triageLabel}, nil)
	}
	return false, e.changeCopiedLabels(ctx, change.Repository, number, nil, []string{triageLabel})
}
