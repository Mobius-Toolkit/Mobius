package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	gh "github.com/google/go-github/v92/github"
)

// setTriage sets the triage state of the issue number in issues and adds the outgoing change that writes the label
// mobius:triage. An issue with no row gets a row with no Workstream, so the row is no tree issue. A row in no tree gets
// the text of the issue when the state changes to true, so the Triager reads the text of that time. After the commit,
// the change to true starts the Triager of an open issue in no Workstream, and the change to false stops it. It sends
// the change at once, and it tells if the state changed.
func (e *Engine) setTriage(ctx context.Context, repository github.Repository, number int64, triage bool) (bool, error) {
	return e.setTriageAt(ctx, repository, number, triage, time.Time{})
}

// setTriageAt is setTriage with the text of the time at, when at is not zero (issueTextAt). The time of the label event
// of an incoming change is such a time, so an edit of the author after the label does not change the text.
func (e *Engine) setTriageAt(ctx context.Context, repository github.Repository, number int64, triage bool, at time.Time) (bool, error) {
	before, err := e.queries.GetIssueTriage(ctx, store.GetIssueTriageParams{Repository: repository.FullName, Number: number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var found *gh.Issue
	var row store.AddTriageIssueParams
	if err != nil || triage && !before {
		if found, err = repository.Issue(ctx, number); err != nil || found == nil {
			return false, err
		}
		if !at.IsZero() {
			if err = e.issueTextAt(ctx, repository, found, at); err != nil {
				return false, err
			}
		}
		if row, err = triageIssueParams(repository.FullName, found, triage); err != nil {
			return false, err
		}
	}
	var id int64
	e.copyWrite.Lock()
	err = e.inTx(ctx, func(q *store.Queries) error {
		var err error
		if found == nil {
			err = q.SetIssueTriage(ctx, store.SetIssueTriageParams{Triage: triage, Repository: repository.FullName, Number: number})
		} else {
			err = q.AddTriageIssue(ctx, row)
		}
		if err != nil {
			return err
		}
		id, err = addFieldChange(ctx, q, repository.FullName, number, triageField)
		return err
	})
	e.copyWrite.Unlock()
	if err != nil {
		return false, err
	}
	changed := triage != before
	if changed && !triage {
		if err := e.stopTriager(ctx, repository, number); err != nil {
			return true, err
		}
	}
	if changed && triage {
		triaging, err := e.triaging(ctx, repository, found)
		if err == nil && triaging {
			err = e.restartTriager(ctx, repository, number)
		}
		if err != nil {
			return true, err
		}
	}
	return changed, e.sendNow(ctx, repository, id)
}

// triageIssueParams gives the row of issues for the issue with the triage state.
func triageIssueParams(repository string, issue *gh.Issue, triage bool) (store.AddTriageIssueParams, error) {
	metadata, err := githubMetadata(issue, 0)
	return store.AddTriageIssueParams{
		Repository: repository,
		Number:     int64(issue.GetNumber()),
		Title:      issue.GetTitle(),
		Body:       issue.GetBody(),
		State:      issue.GetState(),
		Author:     issue.GetUser().GetLogin(),
		Kind:       issueKind(labelNames(issue)),
		Triage:     triage,
		Github:     metadata,
	}, err
}

// triageRowsEndpoint is the endpoint in the sync_cursors table whose row tells that addTriageRows ran for a repository.
const triageRowsEndpoint = "triage_rows"

// addTriageRows adds a row with the triage state and no Workstream for each open issue of repository that has the label
// mobius:triage and no row in issues, with the text of the issue. It adds no outgoing change, because the label is
// already on GitHub. It runs one time for each repository: a row in sync_cursors keeps that.
func (e *Engine) addTriageRows(ctx context.Context, repository github.Repository) error {
	_, err := e.queries.GetSyncCursor(ctx, store.GetSyncCursorParams{Repository: repository.FullName, Endpoint: triageRowsEndpoint})
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	issues, err := repository.OpenIssuesWithLabel(ctx, triageLabel)
	if err != nil {
		return err
	}
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	return e.inTx(ctx, func(q *store.Queries) error {
		for _, issue := range issues {
			number := int64(issue.GetNumber())
			_, err := q.GetIssueTriage(ctx, store.GetIssueTriageParams{Repository: repository.FullName, Number: number})
			if err == nil {
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			row, err := triageIssueParams(repository.FullName, issue, true)
			if err != nil {
				return err
			}
			if err := q.AddTriageIssue(ctx, row); err != nil {
				return err
			}
		}
		return q.SetSyncCursor(ctx, store.SetSyncCursorParams{Repository: repository.FullName, Endpoint: triageRowsEndpoint})
	})
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
// trusted user. For another actor, it adds an outgoing change, so the send writes the state of issues back. A change
// changes nothing when the Mobius App wrote the label later, because the row already has that state. The poll can read
// the event after the copy of the tree gave the row its state from the label, or after accept_issue changed the state.
func (e *Engine) applyTriageChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	repository = e.changeRepository(change.Repository, repository)
	if !e.trustedUser(change.Actor) {
		return e.writeBack(ctx, repository, change)
	}
	old, err := e.triageWrittenLater(ctx, repository, change)
	if err != nil || old {
		return err
	}
	var at time.Time
	if change.Change == labeledChange {
		var metadata struct{ At string }
		if err := json.Unmarshal([]byte(change.Metadata), &metadata); err != nil {
			return err
		}
		if metadata.At != "" {
			if at, err = time.Parse(time.RFC3339Nano, metadata.At); err != nil {
				return err
			}
		}
	}
	_, err = e.setTriageAt(ctx, repository, change.Number, change.Change == labeledChange, at)
	return err
}

// triageWrittenLater tells if the Mobius App wrote the label mobius:triage of the issue of the incoming change after
// the event of the change. The poll can store the event of a person before it reads the event of a write of the App, so
// when it has no later event of the App, the function reads the events of the issue from GitHub.
func (e *Engine) triageWrittenLater(ctx context.Context, repository github.Repository, change store.IncomingChange) (bool, error) {
	id, err := strconv.ParseInt(change.ExternalID, 10, 64)
	if err != nil {
		return false, err
	}
	app := appLogin(repository.AppSlug)
	later, err := e.queries.HasLaterTriageChangeOfActor(ctx, store.HasLaterTriageChangeOfActorParams{
		Repository: change.Repository,
		Number:     change.Number,
		Actor:      app,
		After:      id,
	})
	if later || err != nil {
		return later, err
	}
	events, err := repository.IssueEvents(ctx, change.Number)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(events, func(event *gh.IssueEvent) bool {
		return event.GetID() > id && isTriageEvent(event) && strings.EqualFold(event.GetActor().GetLogin(), app)
	}), nil
}

// issueTextAt sets the title and the body of issue to the text of the time at: the body is the newest edit at or before
// at, and the title is the old name of the first rename after at. The current text stays where no edit or rename shows
// the change.
func (e *Engine) issueTextAt(ctx context.Context, repository github.Repository, issue *gh.Issue, at time.Time) error {
	number := int64(issue.GetNumber())
	events, err := repository.IssueEvents(ctx, number)
	if err != nil {
		return err
	}
	edits, err := repository.IssueBodyEdits(ctx, number)
	if err != nil {
		return err
	}
	for _, edit := range edits {
		if !edit.EditedAt.After(at) {
			issue.Body = new(edit.Diff)
			break
		}
	}
	for _, event := range events {
		if event.GetEvent() == renamedChange && event.GetCreatedAt().After(at) {
			issue.Title = new(event.GetRename().GetFrom())
			break
		}
	}
	return nil
}

// sendTriage writes the label mobius:triage to the issue when GitHub differs from issues.triage, and then copies the
// label to the labels of the row. A send that changes nothing leaves the labels of the row. An issue with no row has no
// triage.
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
	if hasLabel(issue, triageLabel) == triage {
		return false, nil
	}
	sent, err := e.queries.GetOutgoingChange(ctx, change.ID)
	if err != nil || sent.Status != "sent" {
		return false, err
	}
	if triage {
		return false, e.changeCopiedLabels(ctx, change.Repository, number, []string{triageLabel}, nil)
	}
	return false, e.changeCopiedLabels(ctx, change.Repository, number, nil, []string{triageLabel})
}
