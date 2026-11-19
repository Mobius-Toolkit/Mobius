package engine

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The changes of an issue on GitHub. They are the values of event of an issue event, of incoming_changes.change and of
// the change in the metadata of own_writes. editedChange has no issue event: it is the change of a body edit.
const (
	labeledChange   = "labeled"
	unlabeledChange = "unlabeled"
	closedChange    = "closed"
	reopenedChange  = "reopened"
	renamedChange   = "renamed"
	editedChange    = "edited"
)

// changeEvent is an event of the Workstream issue number.
type changeEvent struct {
	number int64
	event  *gh.IssueEvent
}

// eventField gives the field of the event and the metadata of its incoming change. It tells false for an event that is
// no change of a field of a Workstream.
func eventField(event *gh.IssueEvent) (string, map[string]string, bool) {
	switch event.GetEvent() {
	case labeledChange, unlabeledChange:
		label := event.GetLabel().GetName()
		if label == workstreamLabel {
			return workstreamField, map[string]string{"label": label}, true
		}
		field, setting := settingField(label)
		return field, map[string]string{"label": label}, setting
	case closedChange, reopenedChange:
		return stateField, map[string]string{}, true
	case renamedChange:
		return titleField, map[string]string{"title": event.GetRename().GetTo()}, true
	}
	return "", nil, false
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

// addOwnWrite stores that the send of the field of the issue number writes the change kind, so the poll finds no
// incoming change in the event of that write. The label is the label of the write, or "" for a write with no label. It
// gives the id of the row.
func addOwnWrite(ctx context.Context, q *store.Queries, repository string, number int64, field, label, kind string) (int64, error) {
	metadata, err := json.Marshal(map[string]string{"label": label, "change": kind})
	if err != nil {
		return 0, err
	}
	return q.AddOwnWrite(ctx, store.AddOwnWriteParams{
		Repository: repository,
		Number:     number,
		Field:      field,
		Metadata:   string(metadata),
		CreatedAt:  now(),
	})
}

// addEventChanges adds an incoming change for each event of a field of a Workstream that the poll did not read before.
// The row keeps the id of the event, so a second read of the event does nothing. An event of the Mobius App that
// matches an own write deletes the oldest matching own write, and its row is done, so no handler applies it. The
// changes of the setting labels come after the other changes, because a person can add a setting label before the
// label mobius:workstream, and the handler of the setting needs the row that the add of the Workstream makes. The
// other changes keep their order.
func addEventChanges(ctx context.Context, q *store.Queries, repository github.Repository, events []changeEvent) error {
	app := appLogin(repository.AppSlug)
	rank := func(item changeEvent) int {
		if _, setting := settingField(item.event.GetLabel().GetName()); setting {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(events, func(a, b changeEvent) int { return cmp.Compare(rank(a), rank(b)) })
	for _, item := range events {
		change := item.event.GetEvent()
		field, fields, ok := eventField(item.event)
		if !ok {
			continue
		}
		externalID := strconv.FormatInt(item.event.GetID(), 10)
		known, err := q.HasIncomingChange(ctx, externalID)
		if err != nil {
			return err
		}
		if known {
			continue
		}
		actor := item.event.GetActor().GetLogin()
		status := "pending"
		if strings.EqualFold(actor, app) {
			own, err := q.DeleteOldestOwnWrite(ctx, store.DeleteOldestOwnWriteParams{Repository: repository.FullName, Number: item.number, Label: fields["label"], Change: change})
			if err != nil {
				return err
			}
			if own > 0 {
				status = "done"
			}
		}
		metadata, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		err = q.AddIncomingChange(ctx, store.AddIncomingChangeParams{
			ExternalID: externalID,
			Repository: repository.FullName,
			Number:     item.number,
			Field:      field,
			Change:     change,
			Actor:      actor,
			Metadata:   string(metadata),
			Status:     status,
			CreatedAt:  now(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// applyIncomingChanges applies each pending incoming change of the repository in the order of the rows, and marks it
// done. A change that fails stays pending, and holds the later changes until the next poll.
func (e *Engine) applyIncomingChanges(ctx context.Context, repository github.Repository) error {
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

// applyIncomingChange calls the handler of the field of the incoming change.
func (e *Engine) applyIncomingChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	if _, setting := settingLabels[change.Field]; setting {
		return e.applySettingChange(ctx, repository, change)
	}
	return e.applyWorkstreamChange(ctx, repository, change)
}

// applyWorkstreamChange calls the handler of the field of the incoming change, when its actor is a trusted user. For
// another actor, it adds an outgoing change of the field, so the send writes the value of workstreams back. An add of
// the label of a Workstream that has a row, and a removal of the label of an issue with no row, change nothing. A
// field of an issue with no row has no value to write back.
func (e *Engine) applyWorkstreamChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	stored, err := e.queries.HasWorkstream(ctx, store.HasWorkstreamParams{Repository: change.Repository, Number: change.Number})
	if err != nil {
		return err
	}
	trusted := e.trustedUser(change.Actor)
	switch {
	case change.Field == workstreamField && change.Change == labeledChange && !stored:
		if !trusted {
			return e.writeBack(ctx, repository, change)
		}
		return e.addWorkstreamFromChange(ctx, repository, change)
	case change.Field == workstreamField && change.Change == unlabeledChange && stored:
		if !trusted {
			return e.writeBack(ctx, repository, change)
		}
		return e.removeWorkstream(ctx, repository, change.Number)
	case change.Field == workstreamField:
		return nil
	case change.Change == reopenedChange && trusted:
		return e.reopenWorkstreamFromChange(ctx, repository, change)
	case change.Change == closedChange && trusted:
		return e.handleWorkstreamClose(ctx, repository, change.Number)
	case !stored:
		return nil
	case !trusted:
		return e.writeBack(ctx, repository, change)
	}
	var metadata struct{ Title, Body string }
	if err := json.Unmarshal([]byte(change.Metadata), &metadata); err != nil {
		return err
	}
	switch change.Field {
	case titleField:
		return e.setTitle(ctx, repository, change.Number, metadata.Title)
	case bodyField:
		return e.changeBrief(ctx, repository, change.Number, change.Actor, metadata.Body)
	}
	return nil
}

// addWorkstreamFromChange reads the issue of the incoming change and calls the Add handler for it. An issue that
// is gone has no Workstream to add.
func (e *Engine) addWorkstreamFromChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	issue, err := repository.Issue(ctx, change.Number)
	if err != nil || issue == nil {
		return err
	}
	copyFailed, err := e.addWorkstreamFromEvent(ctx, repository, issue, change.Actor, e.copied[repository.FullName])
	if copyFailed {
		delete(e.copied, repository.FullName)
	}
	return err
}

// reopenWorkstreamFromChange reads the issue of the incoming change and calls the Reopen handler for it, when the issue
// has the label of a Workstream.
func (e *Engine) reopenWorkstreamFromChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	issue, err := repository.Issue(ctx, change.Number)
	if err != nil || issue == nil || !hasLabel(issue, workstreamLabel) {
		return err
	}
	copyFailed, err := e.reopenWorkstream(ctx, repository, issue, change.Actor, e.copied[repository.FullName])
	if copyFailed {
		delete(e.copied, repository.FullName)
	}
	return err
}

// applySettingChange calls the handler of the setting of the incoming change, when its actor is a trusted user. For
// another actor, and for an evergreen change of a Workstream that Mobius manages, it adds an outgoing change of the
// field, so the send writes the label of the row back. A Workstream with no copy has no setting to change.
func (e *Engine) applySettingChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
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
		level := AutopilotLevel(autopilot, autoMerge)
		if err := e.changeLevel(ctx, repository, change.Number, copied.Title, level, "@"+change.Actor); err != nil {
			return err
		}
		// The level is stored, so a failed read of GitHub must not hold the change or the poll.
		if err := e.applyLevel(ctx, change.Repository, change.Number, level); err != nil {
			log.Printf("act on the live tasks of %s#%d after the level %s: %v", change.Repository, change.Number, level, err)
		}
		return nil
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

// writeBack adds and sends the outgoing change that writes the field of the incoming change from the row of
// workstreams.
func (e *Engine) writeBack(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	id, err := addFieldChange(ctx, e.queries, change.Repository, change.Number, change.Field)
	if err != nil {
		return err
	}
	return e.sendNow(ctx, repository, id)
}
