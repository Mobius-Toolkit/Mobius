package engine

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// issuesEndpoint is the endpoint of the issue list in the sync_cursors table.
const issuesEndpoint = "issues"

// poll reads the repositories of the Apps. Then, for each repository, it fixes the labels
// at the first sight in this run of the server, and reads the changed issues.
func (e *Engine) poll(ctx context.Context) {
	if err := e.github.Refresh(ctx); err != nil {
		log.Printf("read the repositories of the GitHub Apps: %v", err)
		return
	}
	for _, repository := range e.github.Repositories() {
		if !e.labelsFixed[repository.FullName] {
			e.labelsFixed[repository.FullName] = true
			if err := FixLabels(ctx, repository); err != nil {
				log.Printf("fix the labels of %s: %v", repository.FullName, err)
			}
		}
		if err := e.changedIssues(ctx, repository); err != nil {
			log.Printf("poll the issues of %s: %v", repository.FullName, err)
		}
	}
}

// changedIssues reads the issues that changed at or after the `since` cursor, and moves the cursor
// to the last change. The first poll of a repository has no cursor, so it reads all issues.
func (e *Engine) changedIssues(ctx context.Context, repository github.Repository) error {
	cursor, err := e.queries.GetSyncCursor(ctx, store.GetSyncCursorParams{Repository: repository.FullName, Endpoint: issuesEndpoint})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var since time.Time
	if cursor.Since.Valid {
		if since, err = time.Parse(time.RFC3339, cursor.Since.String); err != nil {
			return err
		}
	}
	page, changed, err := repository.IssuesSince(ctx, since, cursor.Etag.String)
	if err != nil || !changed {
		return err
	}
	for _, issue := range page.Issues {
		if issue.GetUpdatedAt().After(since) {
			since = issue.GetUpdatedAt().Time
		}
	}
	return e.queries.SetSyncCursor(ctx, store.SetSyncCursorParams{
		Repository: repository.FullName,
		Endpoint:   issuesEndpoint,
		// The rows of the Rust version have the same format.
		Since: sql.NullString{String: since.UTC().Format(time.RFC3339), Valid: !since.IsZero()},
		Etag:  sql.NullString{String: page.ETag, Valid: page.ETag != ""},
	})
}
