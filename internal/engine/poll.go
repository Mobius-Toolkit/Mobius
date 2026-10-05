package engine

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// issuesEndpoint is the endpoint of the issue list in the sync_cursors table.
const issuesEndpoint = "issues"

// poll reads the repositories of the Apps, and ends the tasks of each repository that no App gives. When it read all
// Apps, it removes the copy of each repository that it did not find. Then, for each repository, it fixes the labels
// at the first sight in this run of the server, copies the open Workstreams until that step works one time, hands
// the lost tasks to a human and starts the Workers of the earlier run again until that step works one time, reads the
// changed issues, dispatches the ready issues, starts the tasks of the Workstreams with Autopilot, and checks the live
// tasks. The work of a repository starts again at its first poll, because the work needs GitHub. At the end, the
// pull requests with work for an agent replace the ones of the last poll. With no App, the poll does nothing.
func (e *Engine) poll(ctx context.Context) {
	apps, err := e.queries.ListGitHubApps(ctx)
	if err != nil || len(apps) == 0 {
		if err != nil {
			log.Printf("read the GitHub Apps: %v", err)
		}
		return
	}
	complete, err := e.github.Refresh(ctx)
	if err != nil {
		log.Printf("read the repositories of the GitHub Apps: %v", err)
		return
	}
	repositories := e.github.Repositories()
	if err := e.lostAccess(ctx, repositories); err != nil {
		log.Printf("end the tasks of the repositories with no access: %v", err)
	}
	if complete {
		if err := e.forgetCopies(ctx, repositories); err != nil {
			log.Printf("remove the copies of the repositories with no access: %v", err)
		}
	}
	work := map[int64]Work{}
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
		if err := e.pollRepository(ctx, repository, work); err != nil {
			log.Printf("poll %s: %v", repository.FullName, err)
			// A failed poll keeps the old work of the repository, so that it does not let a new ticket pass.
			for task, old := range e.currentWork() {
				if _, ok := work[task]; !ok && old.Repository == repository.FullName {
					work[task] = old
				}
			}
		}
	}
	e.ReplaceWork(work)
}

// recover hands the lost tasks of repository to a human, gives the events that wait from the earlier run of the
// server to the Leads, and starts each Worker of the earlier run again. The Workers start only one time.
func (e *Engine) recover(ctx context.Context, repository github.Repository) error {
	if err := e.handLostTasks(ctx, repository); err != nil {
		return err
	}
	waiting, err := e.queries.ListWaitingLeadWorkstreams(ctx)
	if err != nil {
		return err
	}
	for _, workstream := range waiting {
		if workstream.Repository != repository.FullName {
			continue
		}
		if err := e.wakeEvents(ctx, workstream.Repository, workstream.Workstream); err != nil {
			return err
		}
	}
	e.recovered[repository.FullName] = true
	tasks, err := e.queries.ListLiveTasks(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.State != "queued" && task.State != "working" {
			continue
		}
		var err error
		switch task.Worker.String {
		case ImplementerRole, conflictRoundWorker:
			err = e.restartImplementer(ctx, repository, task)
		case ReviewerRole:
			err = e.restartReviewer(ctx, repository, task)
		// The poll gives the items to a new Judge.
		case JudgeRole:
			before := cmp.Or(task.WorkerInput.String, "reviewed")
			_, err = e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: before, ID: task.ID, FromState: "working"})
		}
		if err != nil {
			log.Printf("start the %s of %s#%d again: %v", task.Worker.String, repository.FullName, task.Issue, err)
		}
	}
	return nil
}

// pollRepository recovers the work of the earlier run until that works one time, and then acts on the changes of the
// repository. It adds the pull requests with work for an agent to work.
func (e *Engine) pollRepository(ctx context.Context, repository github.Repository, work map[int64]Work) error {
	if !e.recovered[repository.FullName] {
		if err := e.recover(ctx, repository); err != nil {
			return err
		}
	}
	if err := e.changedIssues(ctx, repository); err != nil {
		return err
	}
	if err := e.dispatchReady(ctx, repository); err != nil {
		return err
	}
	if err := e.startAutopilot(ctx, repository); err != nil {
		return err
	}
	return e.checkTasks(ctx, repository, work)
}

// changedIssues reads the issues and pull requests that changed at or after the `since` cursor, acts on their new
// comments, stops a Triager, updates the copy, acts on the new events of the Workstreams, and moves the cursor to the
// last change. The first poll of a repository has no cursor, so it reads all issues, and it cannot see which event or
// comment is new.
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
			if err := e.pullRequestComments(ctx, repository, issue, since); err != nil {
				return err
			}
			continue
		}
		if err := e.commentEvents(ctx, repository, issue, since); err != nil {
			return err
		}
		if !hasLabel(issue, noWorkstreamLabel) {
			if err := e.stopTriager(ctx, repository, int64(issue.GetNumber())); err != nil {
				return err
			}
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

// workstreamEvent acts on a new event of the issue. A change of mobius:autopilot makes the next poll read the ready
// list in full, because a mobius:ready of the Mobius App can dispatch with Autopilot. A Workstream label of a trusted
// actor adds an activity, and after the first poll also a creation event for the Lead. After the first poll, a
// removal of the Workstream label stops the work, and a close or a reopen of a trusted actor closes or reopens the
// Workstream.
func (e *Engine) workstreamEvent(ctx context.Context, repository github.Repository, issue *gh.Issue, event *gh.IssueEvent, firstPoll bool) error {
	number := int64(issue.GetNumber())
	actor := event.GetActor().GetLogin()
	trusted := e.TrustedAuthor(repository.AppSlug, actor)
	workstreamChange := event.GetLabel().GetName() == workstreamLabel
	switch {
	case event.GetLabel().GetName() == autopilotLabel:
		return e.queries.SetSyncCursor(ctx, store.SetSyncCursorParams{Repository: repository.FullName, Endpoint: readyEndpoint})
	case event.GetEvent() == "labeled" && workstreamChange && trusted:
		err := e.addActivity(ctx, repository.FullName, number, issue, actor, fmt.Sprintf("New Workstream \"%s\"", issue.GetTitle()))
		if err != nil || firstPoll {
			return err
		}
		return e.addLeadEvent(ctx, repository.FullName, number, sql.NullInt64{}, "creation", eventText(time.Now(), "creation of Workstream", issue, actor, issue.GetBody()))
	case firstPoll:
		return nil
	// A removal from any actor counts, because it only stops work.
	case event.GetEvent() == "unlabeled" && workstreamChange:
		return e.stopWorkstream(ctx, repository, number)
	// The label can go away before the poll sees the close.
	case event.GetEvent() == "closed" && trusted:
		return e.closeWorkstream(ctx, repository, number)
	case event.GetEvent() == "reopened" && trusted && hasLabel(issue, workstreamLabel):
		return e.addLeadEvent(ctx, repository.FullName, number, sql.NullInt64{}, "reopen", eventText(time.Now(), "reopen of Workstream", issue, actor, issue.GetBody()))
	}
	return nil
}
