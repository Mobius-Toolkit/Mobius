package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	gh "github.com/google/go-github/v92/github"
)

const readyUpgradeEndpoint = "ready_upgrade"

type startMetadata struct {
	Label   string `json:"label"`
	Upgrade bool   `json:"upgrade,omitempty"`
}

// requestStart sets start_requested_at and start_requested_by of the issue number in issues. An issue with no row gets a
// row with no Workstream.
func (e *Engine) requestStart(ctx context.Context, repository github.Repository, number int64, by string) error {
	_, err := e.queries.GetIssueStart(ctx, store.GetIssueStartParams{Repository: repository.FullName, Number: number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var found *gh.Issue
	var metadata string
	if err != nil {
		if found, err = repository.Issue(ctx, number); err != nil || found == nil || found.GetState() != "open" {
			return err
		}
		if metadata, err = githubMetadata(found, 0); err != nil {
			return err
		}
	}
	requestedAt := now()
	e.copyWrite.Lock()
	err = e.inTx(ctx, func(q *store.Queries) error {
		if found == nil {
			return q.SetIssueStart(ctx, store.SetIssueStartParams{
				StartRequestedAt: sql.NullString{String: requestedAt, Valid: true},
				StartRequestedBy: by,
				Repository:       repository.FullName,
				Number:           number,
			})
		}
		return q.AddStartIssue(ctx, store.AddStartIssueParams{
			Repository:       repository.FullName,
			Number:           number,
			Title:            found.GetTitle(),
			Body:             found.GetBody(),
			State:            found.GetState(),
			Author:           found.GetUser().GetLogin(),
			Kind:             issueKind(labelNames(found)),
			StartRequestedAt: sql.NullString{String: requestedAt, Valid: true},
			StartRequestedBy: by,
			Github:           metadata,
		})
	})
	e.copyWrite.Unlock()
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return err
}

// clearStart clears start_requested_at and start_requested_by of the issue number in issues.
func (e *Engine) clearStart(ctx context.Context, repositoryName string, number int64) error {
	e.copyWrite.Lock()
	cleared, err := e.queries.ClearIssueStart(ctx, store.ClearIssueStartParams{Repository: repositoryName, Number: number})
	e.copyWrite.Unlock()
	if err != nil {
		return err
	}
	if cleared > 0 {
		e.publish(Change{Workstreams: true})
	}
	return nil
}

// upgradeReadyStarts reads the open issues with mobius:ready one time on the first poll of a repository or after an
// upgrade, and records each add event as an incoming start change so waiting Starts from before the issues cursor
// are not lost.
func (e *Engine) upgradeReadyStarts(ctx context.Context, repository github.Repository) error {
	_, err := e.queries.GetSyncCursor(ctx, store.GetSyncCursorParams{Repository: repository.FullName, Endpoint: readyUpgradeEndpoint})
	if errors.Is(err, sql.ErrNoRows) {
		if !e.copied[repository.FullName] {
			return nil
		}
		_, err = e.queries.GetSyncCursor(ctx, store.GetSyncCursorParams{Repository: repository.FullName, Endpoint: issuesEndpoint})
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	} else if err != nil {
		return err
	}
	issues, err := repository.OpenIssuesWithLabel(ctx, readyLabel)
	if err != nil {
		return err
	}
	var events []changeEvent
	for _, issue := range issues {
		if issue.IsPullRequest() {
			continue
		}
		number := int64(issue.GetNumber())
		issueEvents, err := repository.IssueEvents(ctx, number)
		if err != nil {
			return err
		}
		ready := lastEvent(issueEvents, labeledChange, readyLabel)
		if ready == nil || ready.Actor == nil {
			continue
		}
		events = append(events, changeEvent{number: number, event: ready})
	}
	metadata, err := json.Marshal(startMetadata{Label: readyLabel, Upgrade: true})
	if err != nil {
		return err
	}
	return e.inTx(ctx, func(q *store.Queries) error {
		for _, item := range events {
			externalID := strconv.FormatInt(item.event.GetID(), 10)
			known, err := q.HasIncomingChange(ctx, externalID)
			if err != nil {
				return err
			}
			if known {
				continue
			}
			err = q.AddIncomingChange(ctx, store.AddIncomingChangeParams{
				ExternalID: externalID,
				Repository: repository.FullName,
				Number:     item.number,
				Field:      startField,
				Change:     labeledChange,
				Actor:      item.event.GetActor().GetLogin(),
				Metadata:   string(metadata),
				Status:     "pending",
				CreatedAt:  now(),
			})
			if err != nil {
				return err
			}
		}
		return q.DeleteSyncCursor(ctx, store.DeleteSyncCursorParams{Repository: repository.FullName, Endpoint: readyUpgradeEndpoint})
	})
}

func isStartEvent(event *gh.IssueEvent) bool {
	field, _, ok := eventField(event)
	return ok && field == startField
}

// applyStartChange requests a Start for an add of mobius:ready by a trusted user, and adds an outgoing change that
// removes mobius:ready as an own write. An upgrade event also accepts the Mobius App, because an open mobius:ready
// from before the upgrade can have the App as its actor. A removal of mobius:ready changes nothing.
func (e *Engine) applyStartChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	if change.Change != labeledChange {
		return nil
	}
	repository = e.changeRepository(change.Repository, repository)
	var meta startMetadata
	if err := json.Unmarshal([]byte(change.Metadata), &meta); err != nil {
		return err
	}
	trusted := e.trustedUser(change.Actor)
	if meta.Upgrade {
		trusted = e.TrustedAuthor(repository.AppSlug, change.Actor)
	}
	if trusted {
		if err := e.requestStart(ctx, repository, change.Number, change.Actor); err != nil {
			return err
		}
	}
	return e.writeBack(ctx, repository, change)
}

// sendStart removes the label mobius:ready from the issue as an own write, and drops it from the copied labels of the
// row.
func (e *Engine) sendStart(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	number := change.Number.Int64
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return e.failed(ctx, repository, change, err)
	}
	if issue == nil {
		return false, e.refuse(ctx, change)
	}
	if pending, err := e.sendLabel(ctx, repository, change, issue, readyLabel, false); pending || err != nil {
		return pending, err
	}
	if !hasLabel(issue, readyLabel) {
		return false, nil
	}
	sent, err := e.queries.GetOutgoingChange(ctx, change.ID)
	if err != nil || sent.Status != "sent" {
		return false, err
	}
	return false, e.changeCopiedLabels(ctx, change.Repository, number, nil, []string{readyLabel})
}
