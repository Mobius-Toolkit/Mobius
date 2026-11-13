package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The changes of a label on GitHub. They are the values of event of an issue event, of incoming_changes.change and of
// the change in the metadata of own_writes.
const (
	labeledChange   = "labeled"
	unlabeledChange = "unlabeled"
)

// settingEvent is a label event of the Workstream issue number.
type settingEvent struct {
	number int64
	event  *gh.IssueEvent
}

// settingField gives the field of the setting that the label name writes.
func settingField(label string) (string, bool) {
	for field, name := range settingLabels {
		if name == label {
			return field, true
		}
	}
	return "", false
}

// addOwnWrite stores that the send of change writes the label with the kind of change kind, so the poll finds no
// incoming change in the event of that write. It gives the id of the row.
func addOwnWrite(ctx context.Context, q *store.Queries, change store.OutgoingChange, label, kind string) (int64, error) {
	metadata, err := json.Marshal(map[string]string{"label": label, "change": kind})
	if err != nil {
		return 0, err
	}
	return q.AddOwnWrite(ctx, store.AddOwnWriteParams{
		Repository: change.Repository,
		Number:     change.Number,
		Field:      change.Field,
		Metadata:   string(metadata),
		CreatedAt:  now(),
	})
}

// addSettingChanges adds an incoming change for each event of a setting label, except the event of an own write of the
// Mobius App. An own write is deleted when its event matches. The unique index of the external id stops a second row
// for an event that the poll reads again.
func addSettingChanges(ctx context.Context, q *store.Queries, repository github.Repository, events []settingEvent) error {
	app := appLogin(repository.AppSlug)
	for _, item := range events {
		label := item.event.GetLabel().GetName()
		change := item.event.GetEvent()
		field, setting := settingField(label)
		if !setting || change != labeledChange && change != unlabeledChange {
			continue
		}
		actor := item.event.GetActor().GetLogin()
		if strings.EqualFold(actor, app) {
			own, err := q.DeleteOldestOwnWrite(ctx, store.DeleteOldestOwnWriteParams{Repository: repository.FullName, Number: item.number, Label: label, Change: change})
			if err != nil {
				return err
			}
			if own > 0 {
				continue
			}
		}
		metadata, err := json.Marshal(map[string]string{"label": label})
		if err != nil {
			return err
		}
		err = q.AddIncomingChange(ctx, store.AddIncomingChangeParams{
			ExternalID: strconv.FormatInt(item.event.GetID(), 10),
			Repository: repository.FullName,
			Number:     item.number,
			Field:      field,
			Change:     change,
			Actor:      actor,
			Metadata:   string(metadata),
			CreatedAt:  now(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// applyIncomingChanges applies each pending incoming change of the repository in the order of the rows, and marks it
// done. A change that fails stays pending, and holds the later changes until the next poll. The changes wait while the
// copy of the repository failed, because the copy adds the row of a new Workstream.
func (e *Engine) applyIncomingChanges(ctx context.Context, repository github.Repository) error {
	if !e.copied[repository.FullName] {
		return nil
	}
	changes, err := e.queries.ListPendingIncomingChanges(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, change := range changes {
		if err := e.applyIncomingChange(ctx, repository, change); err != nil {
			return err
		}
		if err := e.queries.SetIncomingChangeDone(ctx, change.ID); err != nil {
			return err
		}
	}
	return nil
}

// applyIncomingChange calls the handler of the setting of the incoming change, when its actor is a trusted user. For
// another actor, and for an evergreen change of a Workstream that Mobius manages, it adds an outgoing change of the
// field, so the send writes the label of the row back. A Workstream with no copy has no setting to change.
func (e *Engine) applyIncomingChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	copied, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: change.Repository, Number: change.Number})
	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("apply the %s of %s#%d: the Workstream has no copy", change.Field, change.Repository, change.Number)
		return nil
	}
	if err != nil {
		return err
	}
	on := change.Change == labeledChange
	switch {
	case !e.trustedUser(change.Actor):
		return e.writeBack(ctx, repository, change)
	case change.Field == autopilotField || change.Field == autoMergeField:
		settings, err := e.queries.GetWorkstreamSettings(ctx, store.GetWorkstreamSettingsParams{Repository: change.Repository, Number: change.Number})
		if err != nil {
			return err
		}
		autopilot, autoMerge := settings.Autopilot, settings.AutoMerge
		if change.Field == autopilotField {
			autopilot = on
		} else {
			autoMerge = on
		}
		return e.changeLevel(ctx, repository, change.Number, copied.Title, AutopilotLevel(autopilot, autoMerge), "@"+change.Actor)
	case change.Field == evergreenField && (copied.Maintenance || copied.Skills):
		return e.writeBack(ctx, repository, change)
	}
	switch change.Field {
	case evergreenField:
		err = e.setEvergreen(ctx, repository, change.Number, on)
	case maintenanceField:
		err = e.setMaintenance(ctx, repository, change.Number, on)
	case skillsField:
		err = e.setSkills(ctx, repository, change.Number, on)
	}
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// writeBack adds and sends the outgoing change that writes the label of the field of the incoming change from the row of
// workstreams.
func (e *Engine) writeBack(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	id, err := addSettingChange(ctx, e.queries, change.Repository, change.Number, change.Field)
	if err != nil {
		return err
	}
	return e.sendNow(ctx, repository, id)
}
