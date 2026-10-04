package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// issuesEndpoint is the endpoint of the issue list in the sync_cursors table.
const issuesEndpoint = "issues"

// poll reads the repositories of the Apps. When it read all Apps, it removes the copy of each repository that it did
// not find. Then, for each repository, it fixes the labels at the first sight in this run of the server, copies the
// open Workstreams until that step works one time, hands the lost tasks to a human until that step works one time,
// and reads the changed issues. The work of a repository starts again at its first poll, because the work needs GitHub.
func (e *Engine) poll(ctx context.Context) {
	complete, err := e.github.Refresh(ctx)
	if err != nil {
		log.Printf("read the repositories of the GitHub Apps: %v", err)
		return
	}
	repositories := e.github.Repositories()
	if complete {
		if err := e.forgetCopies(ctx, repositories); err != nil {
			log.Printf("remove the copies of the repositories with no access: %v", err)
		}
	}
	for _, repository := range repositories {
		if !e.labelsFixed[repository.FullName] {
			e.labelsFixed[repository.FullName] = true
			if err := FixLabels(ctx, repository); err != nil {
				log.Printf("fix the labels of %s: %v", repository.FullName, err)
			}
		}
		if !e.copied[repository.FullName] {
			if err := e.syncCopy(ctx, repository); err != nil {
				log.Printf("copy the Workstreams of %s: %v", repository.FullName, err)
			} else {
				e.copied[repository.FullName] = true
			}
		}
		if !e.recovered[repository.FullName] {
			if err := e.handLostTasks(ctx, repository); err != nil {
				log.Printf("hand the lost tasks of %s to a human: %v", repository.FullName, err)
				continue
			}
			e.recovered[repository.FullName] = true
		}
		if err := e.changedIssues(ctx, repository); err != nil {
			log.Printf("poll the issues of %s: %v", repository.FullName, err)
		}
	}
}

// changedIssues reads the issues that changed at or after the `since` cursor, updates the copy, acts on the new
// events of the Workstreams, and moves the cursor to the last change. The first poll of a repository has no cursor,
// so it reads all issues, and it cannot see which event is new.
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
	firstPoll := since.IsZero()
	listChanged := false
	for _, issue := range page.Issues {
		if issue.IsPullRequest() {
			continue
		}
		labeled := hasLabel(issue, workstreamLabel)
		work, err := e.hasWork(ctx, repository.FullName, int64(issue.GetNumber()))
		if err != nil {
			return err
		}
		var events []*gh.IssueEvent
		if labeled || work {
			if events, err = repository.IssueEvents(ctx, int64(issue.GetNumber())); err != nil {
				return err
			}
		}
		// After a failed update, the next poll copies the repository again.
		if e.copied[repository.FullName] {
			copyChanged, err := e.updateCopy(ctx, repository, issue, events, !firstPoll)
			if err != nil {
				log.Printf("update the copy of %s: %v", repository.FullName, err)
				delete(e.copied, repository.FullName)
			}
			listChanged = listChanged || copyChanged
		}
		for _, event := range events {
			if event.Actor == nil || !event.GetCreatedAt().After(since) {
				continue
			}
			if err := e.workstreamEvent(ctx, repository, issue, event, firstPoll); err != nil {
				return err
			}
		}
	}
	if listChanged {
		e.publish(Change{Workstreams: true})
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

// workstreamEvent acts on a new event of the issue. A Workstream label of a trusted actor adds an activity, and
// after the first poll also a creation event for the Lead. After the first poll, a removal of the Workstream label
// stops the work, and a close or a reopen of a trusted actor closes or reopens the Workstream.
func (e *Engine) workstreamEvent(ctx context.Context, repository github.Repository, issue *gh.Issue, event *gh.IssueEvent, firstPoll bool) error {
	number := int64(issue.GetNumber())
	actor := event.GetActor().GetLogin()
	trusted := e.TrustedAuthor(repository.AppSlug, actor)
	workstreamChange := event.GetLabel().GetName() == workstreamLabel
	switch {
	case event.GetEvent() == "labeled" && workstreamChange && trusted:
		err := e.queries.AddEvent(ctx, store.AddEventParams{
			Time:       now(),
			Repository: repository.FullName,
			Workstream: number,
			Issue:      number,
			Actor:      actor,
			Text:       fmt.Sprintf("New Workstream \"%s\"", issue.GetTitle()),
			Link:       issue.GetHTMLURL(),
		})
		if err != nil || firstPoll {
			return err
		}
		return e.addLeadEvent(ctx, repository.FullName, number, sql.NullInt64{}, "creation", eventText(time.Now(), "creation of Workstream", issue, actor))
	case firstPoll:
		return nil
	// A removal from any actor counts, because it only stops work.
	case event.GetEvent() == "unlabeled" && workstreamChange:
		return e.stopWorkstream(ctx, repository, number)
	// The label can go away before the poll sees the close.
	case event.GetEvent() == "closed" && trusted:
		return e.closeWorkstream(ctx, repository, number)
	case event.GetEvent() == "reopened" && trusted && hasLabel(issue, workstreamLabel):
		return e.addLeadEvent(ctx, repository.FullName, number, sql.NullInt64{}, "reopen", eventText(time.Now(), "reopen of Workstream", issue, actor))
	}
	return nil
}
