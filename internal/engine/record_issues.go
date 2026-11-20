package engine

import (
	"context"
	"database/sql"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// createRecordIssue creates the issue of a record of Mobius (a CI break, a CI failure, a check timeout or an init
// failure) below the Workstream, with the create handler. record stores the id of the new row in the record in the
// same transaction. The send of the create writes the number to the record. A send that fails leaves the create
// pending, and a later poll sends it again.
func (e *Engine) createRecordIssue(ctx context.Context, repository github.Repository, workstream issueAt, title, body string, start bool, record func(*store.Queries, int64) error) error {
	_, createID, err := e.addIssueToCreate(ctx, repository, issueRef{workstream.repository.FullName, workstream.number}, title, body, "", nil, nil, nil, nil, false, start, record)
	if err != nil {
		return err
	}
	return e.sendNow(ctx, repository, createID)
}

// setRecordIssueNumber writes the number of the created issue row to each record with that row. A failure record with
// a place of a break takes the number of the issue of the break.
func setRecordIssueNumber(ctx context.Context, q *store.Queries, row, number int64) error {
	stored := sql.NullInt64{Int64: row, Valid: true}
	if err := q.SetCIBreakIssueOfRow(ctx, store.SetCIBreakIssueOfRowParams{Issue: number, IssueRow: stored}); err != nil {
		return err
	}
	if err := q.SetCIBreakFailuresIssueOfRow(ctx, store.SetCIBreakFailuresIssueOfRowParams{Issue: number, IssueRow: stored}); err != nil {
		return err
	}
	if err := q.SetCIFailureIssueOfRow(ctx, store.SetCIFailureIssueOfRowParams{Issue: number, IssueRow: stored}); err != nil {
		return err
	}
	if err := q.SetCheckTimeoutIssueOfRow(ctx, store.SetCheckTimeoutIssueOfRowParams{Issue: number, IssueRow: stored}); err != nil {
		return err
	}
	return q.SetInitFailureIssueOfRow(ctx, store.SetInitFailureIssueOfRowParams{Issue: number, IssueRow: stored})
}

// clearRecordIssueRow clears the id of the dropped issue row in each record that has it.
func clearRecordIssueRow(ctx context.Context, q *store.Queries, row int64) error {
	stored := sql.NullInt64{Int64: row, Valid: true}
	if err := q.ClearCIBreakIssueRow(ctx, stored); err != nil {
		return err
	}
	if err := q.ClearCIFailureIssueRow(ctx, stored); err != nil {
		return err
	}
	if err := q.ClearCheckTimeoutIssueRow(ctx, stored); err != nil {
		return err
	}
	return q.ClearInitFailureIssueRow(ctx, stored)
}
