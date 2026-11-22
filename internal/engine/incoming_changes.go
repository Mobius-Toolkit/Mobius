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
	"time"

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
	addedChange     = "added"
	removedChange   = "removed"
)

type parentMetadata struct {
	ParentRepository string `json:"parent_repository"`
	Parent           int64  `json:"parent"`
}

type blockerMetadata struct {
	BlockerRepository string `json:"blocker_repository"`
	Blocker           int64  `json:"blocker"`
}

// changeEvent is an event of an issue number.
type changeEvent struct {
	number int64
	event  *gh.IssueEvent
}

// eventField gives the field of the event and the metadata of its incoming change. It tells false for an event that is
// no change of a field of a Workstream, of a tree issue, of the triage or of the state labels of an issue.
func eventField(event *gh.IssueEvent) (string, map[string]string, bool) {
	switch event.GetEvent() {
	case labeledChange, unlabeledChange:
		label := event.GetLabel().GetName()
		if slices.Contains([]string{workingLabel, reviewLabel, needsHumanLabel, questionLabel}, label) {
			return stateLabelsField, map[string]string{"label": label}, true
		}
		if label == workstreamLabel {
			return workstreamField, map[string]string{"label": label}, true
		}
		if label == projectLabel {
			return kindField, map[string]string{"label": label}, true
		}
		if label == triageLabel {
			return triageField, map[string]string{"label": label, "at": event.GetCreatedAt().UTC().Format(time.RFC3339Nano)}, true
		}
		if label == readyLabel {
			return startField, map[string]string{"label": label}, true
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

func addParentOwnWrite(ctx context.Context, q *store.Queries, repository string, number int64, parentRepository string, parent int64, kind string) (int64, error) {
	metadata, err := json.Marshal(struct {
		ParentRepository string `json:"parent_repository"`
		Parent           int64  `json:"parent"`
		Change           string `json:"change"`
	}{parentRepository, parent, kind})
	if err != nil {
		return 0, err
	}
	return q.AddOwnWrite(ctx, store.AddOwnWriteParams{
		Repository: repository,
		Number:     number,
		Field:      parentField,
		Metadata:   string(metadata),
		CreatedAt:  now(),
	})
}

func addBlockerOwnWrite(ctx context.Context, q *store.Queries, repository string, number int64, blockerRepository string, blocker int64, kind string) (int64, error) {
	metadata, err := json.Marshal(struct {
		BlockerRepository string `json:"blocker_repository"`
		Blocker           int64  `json:"blocker"`
		Change            string `json:"change"`
	}{blockerRepository, blocker, kind})
	if err != nil {
		return 0, err
	}
	return q.AddOwnWrite(ctx, store.AddOwnWriteParams{
		Repository: repository,
		Number:     number,
		Field:      blockersField,
		Metadata:   string(metadata),
		CreatedAt:  now(),
	})
}

// addEventChanges adds an incoming change for each event of a field of a Workstream or a tree issue that the poll did
// not read before. The row keeps the id of the event, so a second read of the event does nothing. An event of the
// Mobius App that matches an own write deletes the oldest matching own write, and its row is done, so no handler
// applies it. The changes of the setting labels come after the other changes, because a person can add a setting label
// before the label mobius:workstream, and the handler of the setting needs the row that the add of the Workstream makes.
// The other changes keep their order.
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
			own, err := q.DeleteOldestOwnWrite(ctx, store.DeleteOldestOwnWriteParams{
				Repository: repository.FullName,
				Number:     item.number,
				Field:      field,
				Label:      fields["label"],
				Change:     change,
			})
			if err != nil {
				return err
			}
			if own == 0 {
				own, err = q.DeleteOldestOwnWrite(ctx, store.DeleteOldestOwnWriteParams{
					Repository: repository.FullName,
					Number:     item.number,
					Field:      bucketField,
					Label:      "",
					Change:     change,
				})
				if err != nil {
					return err
				}
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
	_, err := e.applyIncomingChangesOf(ctx, repository.FullName, repository)
	return err
}

func (e *Engine) applyIncomingChangesOf(ctx context.Context, target string, repository github.Repository) ([]issueRef, error) {
	changes, err := e.queries.ListPendingIncomingChanges(ctx, target)
	if err != nil {
		return nil, err
	}
	var entered []issueRef
	for _, change := range changes {
		added, err := e.applyIncomingChange(ctx, repository, change)
		if err != nil {
			return nil, err
		}
		if added {
			entered = append(entered, lowerRef(change.Repository, change.Number))
		}
		if err := e.queries.SetIncomingChangeDone(ctx, change.ID); err != nil {
			return nil, err
		}
	}
	return entered, nil
}

// applyIncomingChange calls the handler of the field of the incoming change.
func (e *Engine) applyIncomingChange(ctx context.Context, repository github.Repository, change store.IncomingChange) (bool, error) {
	if change.Field == stateLabelsField {
		return false, e.applyStateLabelsChange(ctx, repository, change)
	}
	if change.Field == parentField {
		return e.applyParentChange(ctx, repository, change)
	}
	if change.Field == blockersField {
		return false, e.applyBlockersChange(ctx, repository, change)
	}
	if change.Field == kindField {
		return false, e.applyTreeIssueChange(ctx, repository, change)
	}
	if change.Field == triageField {
		return false, e.applyTriageChange(ctx, repository, change)
	}
	if change.Field == startField {
		return false, e.applyStartChange(ctx, repository, change)
	}
	if _, setting := settingLabels[change.Field]; setting {
		return false, e.applySettingChange(ctx, repository, change)
	}
	if change.Field != workstreamField {
		stored, err := e.queries.HasWorkstream(ctx, store.HasWorkstreamParams{Repository: change.Repository, Number: change.Number})
		if err != nil {
			return false, err
		}
		if !stored {
			row, inTree, err := e.treeIssueOf(ctx, change.Repository, change.Number)
			if err != nil {
				return false, err
			}
			if inTree {
				return false, e.applyTreeIssueRowChange(ctx, repository, row, change)
			}
			if change.Field == stateField && change.Change == reopenedChange && e.trustedUser(change.Actor) {
				return false, e.reopenWorkstreamFromChange(ctx, repository, change)
			}
			return false, nil
		}
	}
	return false, e.applyWorkstreamChange(ctx, repository, change)
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
		issue, err := repository.Issue(ctx, change.Number)
		if err != nil {
			return err
		}
		return e.handleWorkstreamClose(ctx, repository, change.Number, closeReason(issue))
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
// is gone, or that lost the label, has no Workstream to add. The poll copied the trees with the label of the issue, so
// the trees are copied again, and no later event of the removal can do that for an issue with no row.
func (e *Engine) addWorkstreamFromChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	issue, err := repository.Issue(ctx, change.Number)
	if err != nil {
		return err
	}
	if issue == nil || !hasLabel(issue, workstreamLabel) {
		return e.recopyAfterRemoval(ctx, repository, change.Number)
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
		if change.Field == evergreenField {
			row, inTree, err := e.treeIssueOf(ctx, change.Repository, change.Number)
			if err != nil {
				return err
			}
			if inTree {
				return e.applyTreeIssueRowChange(ctx, repository, row, change)
			}
		}
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

func (e *Engine) applyTreeIssueChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	row, inTree, err := e.treeIssueOf(ctx, change.Repository, change.Number)
	if err != nil || !inTree {
		return err
	}
	return e.applyTreeIssueRowChange(ctx, repository, row, change)
}

func (e *Engine) treeIssueOf(ctx context.Context, repository string, number int64) (store.GetTreeIssueRow, bool, error) {
	row, err := e.queries.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: repository, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return store.GetTreeIssueRow{}, false, nil
	}
	if err != nil {
		return store.GetTreeIssueRow{}, false, err
	}
	return row, row.Workstream.Valid && row.WorkstreamRepository.Valid && row.WorkstreamNumber.Valid, nil
}

// applyTreeIssueRowChange applies the change of a field of a tree issue from a trusted user to issues, or adds an
// outgoing change that writes the field of the row back for another actor.
func (e *Engine) applyTreeIssueRowChange(ctx context.Context, repository github.Repository, row store.GetTreeIssueRow, change store.IncomingChange) error {
	if !e.trustedUser(change.Actor) {
		return e.writeBackTreeIssue(ctx, repository, row.ID, change.Repository, change.Number, change.Field)
	}
	var changed int64
	switch change.Field {
	case titleField:
		var meta struct{ Title string }
		if err := json.Unmarshal([]byte(change.Metadata), &meta); err != nil {
			return err
		}
		e.copyWrite.Lock()
		var err error
		changed, err = e.queries.SetIssueTitle(ctx, store.SetIssueTitleParams{Title: meta.Title, ID: row.ID})
		e.copyWrite.Unlock()
		if err != nil {
			return err
		}
	case bodyField:
		found, err := e.changeRepository(change.Repository, repository).Issue(ctx, change.Number)
		if err != nil || found == nil {
			return err
		}
		body := e.WithoutChatLink(row.WorkstreamRepository.String, row.WorkstreamNumber.Int64, withoutWriteMarker(found.GetBody()))
		e.copyWrite.Lock()
		changed, err = e.queries.SetIssueBody(ctx, store.SetIssueBodyParams{Body: body, ID: row.ID})
		e.copyWrite.Unlock()
		if err != nil {
			return err
		}
	case stateField:
		state := "open"
		if change.Change == closedChange {
			state = "closed"
		}
		e.copyWrite.Lock()
		var err error
		changed, err = e.queries.SetIssueState(ctx, store.SetIssueStateParams{State: state, ID: row.ID})
		e.copyWrite.Unlock()
		if err != nil {
			return err
		}
	case kindField:
		var meta struct{ Label string }
		if err := json.Unmarshal([]byte(change.Metadata), &meta); err != nil {
			return err
		}
		if meta.Label != projectLabel {
			return nil
		}
		kind := "task"
		if change.Change == labeledChange {
			kind = projectKind
		}
		e.copyWrite.Lock()
		var err error
		changed, err = e.queries.SetIssueKind(ctx, store.SetIssueKindParams{Kind: kind, ID: row.ID})
		e.copyWrite.Unlock()
		if err != nil {
			return err
		}
	case evergreenField:
		if row.Kind != projectKind {
			return nil
		}
		e.copyWrite.Lock()
		var err error
		changed, err = e.queries.SetIssueEvergreen(ctx, store.SetIssueEvergreenParams{Evergreen: change.Change == labeledChange, ID: row.ID})
		e.copyWrite.Unlock()
		if err != nil {
			return err
		}
	}
	if changed > 0 {
		e.publish(Change{Workstreams: true})
	}
	return nil
}

func (e *Engine) writeBackTreeIssue(ctx context.Context, repository github.Repository, issueID int64, targetRepo string, number int64, field string) error {
	id, err := addIssueChange(ctx, e.queries, targetRepo, number, issueID, field)
	if err != nil {
		return err
	}
	return e.sendNow(ctx, e.changeRepository(targetRepo, repository), id)
}

func (e *Engine) writeBackDetachedParent(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	id, err := e.addOutgoingChange(ctx, e.queries, change.Repository, change.Number, 0, parentField, change.Metadata)
	if err != nil {
		return err
	}
	return e.sendNow(ctx, e.changeRepository(change.Repository, repository), id)
}

func (e *Engine) changeRepository(name string, fallback github.Repository) github.Repository {
	if repo, ok := e.namedRepository(name, fallback); ok {
		return repo
	}
	return fallback
}

func (e *Engine) applyParentChange(ctx context.Context, repository github.Repository, change store.IncomingChange) (bool, error) {
	node, err := e.queries.GetIssueTreeNode(ctx, store.GetIssueTreeNodeParams{Repository: change.Repository, Number: change.Number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	inTree := err == nil && node.Workstream.Valid
	var meta parentMetadata
	if err := json.Unmarshal([]byte(change.Metadata), &meta); err != nil {
		return false, err
	}
	if e.isTreeAppActor(repository, change.Actor, change.Repository, meta) {
		if inTree || change.Change != addedChange {
			return false, nil
		}
	} else if !e.trustedUser(change.Actor) {
		if inTree {
			return false, e.writeBackTreeIssue(ctx, repository, node.ID, change.Repository, change.Number, parentField)
		}
		if change.Change == addedChange {
			return false, e.writeBackDetachedParent(ctx, repository, change)
		}
		return false, nil
	}
	if change.Change == removedChange {
		if !inTree {
			return false, nil
		}
		return false, e.detachIssueSubtree(ctx, node.ID)
	}
	parentRepoName := meta.ParentRepository
	if repo, ok := e.namedRepository(meta.ParentRepository, repository); ok {
		parentRepoName = repo.FullName
	}
	wsID, wsRepo, wsNum, parentID, ok, err := e.resolveParentTarget(ctx, e.queries, parentRepoName, meta.Parent)
	if err != nil {
		return false, err
	}
	if !ok {
		if inTree {
			return false, e.detachIssueSubtree(ctx, node.ID)
		}
		return false, nil
	}
	if !inTree {
		found, err := e.readChildIssue(ctx, repository, change.Repository, change.Number, parentRepoName, meta.Parent)
		if err != nil || found == nil || hasLabel(found, workstreamLabel) {
			return false, err
		}
		metadata, err := githubMetadata(found, 0)
		if err != nil {
			return false, err
		}
		body := e.WithoutChatLink(wsRepo, wsNum, withoutWriteMarker(found.GetBody()))
		e.copyWrite.Lock()
		err = e.inTx(ctx, func(q *store.Queries) error {
			wsID, wsRepo, wsNum, parentID, ok, err = e.resolveParentTarget(ctx, q, parentRepoName, meta.Parent)
			if err != nil || !ok {
				return err
			}
			_, err = q.AddCopiedIssue(ctx, store.AddCopiedIssueParams{
				Repository: change.Repository,
				Number:     change.Number,
				Title:      found.GetTitle(),
				Body:       body,
				State:      found.GetState(),
				Author:     found.GetUser().GetLogin(),
				Workstream: sql.NullInt64{Int64: wsID, Valid: true},
				Parent:     parentID,
				Kind:       issueKind(labelNames(found)),
				Github:     metadata,
			})
			return err
		})
		e.copyWrite.Unlock()
		if err == nil && ok {
			e.publish(Change{Workstreams: true})
			return true, nil
		}
		return false, err
	}
	e.copyWrite.Lock()
	err = e.inTx(ctx, func(q *store.Queries) error {
		subtree, err := subtreeIDs(ctx, q, node.ID)
		if err != nil {
			return err
		}
		if parentID.Valid && slices.Contains(subtree, parentID.Int64) {
			return nil
		}
		ws := sql.NullInt64{Int64: wsID, Valid: true}
		if err := q.SetIssueParentAndWorkstream(ctx, store.SetIssueParentAndWorkstreamParams{Workstream: ws, Parent: parentID, ID: node.ID}); err != nil {
			return err
		}
		if parentID.Valid || !node.Workstream.Valid || node.Workstream.Int64 != wsID {
			if err := q.SetIssueBucket(ctx, store.SetIssueBucketParams{Bucket: 0, ID: node.ID}); err != nil {
				return err
			}
		}
		if !node.Workstream.Valid || node.Workstream.Int64 != wsID {
			for _, id := range subtree {
				if id == node.ID {
					continue
				}
				if err := q.SetIssueWorkstream(ctx, store.SetIssueWorkstreamParams{Workstream: ws, ID: id}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	e.copyWrite.Unlock()
	if err == nil {
		e.publish(Change{Workstreams: true})
		return !inTree, nil
	}
	return false, err
}

func (e *Engine) resolveParentTarget(ctx context.Context, q *store.Queries, repository string, number int64) (wsID int64, wsRepo string, wsNumber int64, parent sql.NullInt64, ok bool, err error) {
	ws, err := q.GetOpenWorkstreamByNumber(ctx, store.GetOpenWorkstreamByNumberParams{Repository: repository, Number: number})
	if err == nil {
		return ws.ID, ws.Repository, ws.Number, sql.NullInt64{}, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, "", 0, sql.NullInt64{}, false, err
	}
	bucketWS, err := q.FindBucketWorkstream(ctx, store.FindBucketWorkstreamParams{Repository: repository, Bucket: number})
	if err == nil {
		return bucketWS.ID, bucketWS.Repository, bucketWS.Number, sql.NullInt64{}, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, "", 0, sql.NullInt64{}, false, err
	}
	parentNode, err := q.GetIssueTreeNode(ctx, store.GetIssueTreeNodeParams{Repository: repository, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", 0, sql.NullInt64{}, false, nil
	}
	if err != nil {
		return 0, "", 0, sql.NullInt64{}, false, err
	}
	if !parentNode.Workstream.Valid || parentNode.WorkstreamState.String != "open" {
		return 0, "", 0, sql.NullInt64{}, false, nil
	}
	return parentNode.Workstream.Int64, parentNode.WorkstreamRepository.String, parentNode.WorkstreamNumber.Int64, sql.NullInt64{Int64: parentNode.ID, Valid: true}, true, nil
}

func (e *Engine) readChildIssue(ctx context.Context, repository github.Repository, childRepoName string, childNumber int64, parentRepoName string, parentNumber int64) (*gh.Issue, error) {
	if childRepo, readable := e.namedRepository(childRepoName, repository); readable {
		return childRepo.Issue(ctx, childNumber)
	}
	parentRepo, ok := e.namedRepository(parentRepoName, repository)
	if !ok {
		return nil, nil
	}
	children, err := parentRepo.SubIssues(ctx, parentNumber)
	if github.NotFound(err) || err != nil {
		return nil, err
	}
	for _, child := range children {
		if strings.EqualFold(blockerRepository(child, parentRepo.FullName), childRepoName) && int64(child.GetNumber()) == childNumber {
			return child, nil
		}
	}
	return nil, nil
}

func (e *Engine) applyBlockersChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	node, err := e.queries.GetIssueTreeNode(ctx, store.GetIssueTreeNodeParams{Repository: change.Repository, Number: change.Number})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !node.Workstream.Valid) {
		return nil
	}
	if err != nil {
		return err
	}
	if !e.trustedUser(change.Actor) {
		return e.writeBackTreeIssue(ctx, repository, node.ID, change.Repository, change.Number, blockersField)
	}
	var meta blockerMetadata
	if err := json.Unmarshal([]byte(change.Metadata), &meta); err != nil {
		return err
	}
	blockerRepoName := meta.BlockerRepository
	if repo, ok := e.namedRepository(meta.BlockerRepository, repository); ok {
		blockerRepoName = repo.FullName
	}
	if change.Change == removedChange {
		e.copyWrite.Lock()
		err := e.inTx(ctx, func(q *store.Queries) error {
			blockerRow, err := q.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: blockerRepoName, Number: meta.Blocker})
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := q.DeleteBlocker(ctx, store.DeleteBlockerParams{Issue: node.ID, Blocker: blockerRow.ID}); err != nil {
				return err
			}
			return deleteUnusedIssues(ctx, q)
		})
		e.copyWrite.Unlock()
		if err == nil {
			e.publish(Change{Workstreams: true})
		}
		return err
	}
	var found *gh.Issue
	_, err = e.queries.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: blockerRepoName, Number: meta.Blocker})
	if errors.Is(err, sql.ErrNoRows) {
		found, blockerRepoName, err = e.readBlockerIssue(ctx, repository, blockerRepoName, meta.Blocker, change.Repository, change.Number)
		if err != nil || found == nil {
			return err
		}
	} else if err != nil {
		return err
	}
	e.copyWrite.Lock()
	err = e.inTx(ctx, func(q *store.Queries) error {
		blockerRow, err := q.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: blockerRepoName, Number: meta.Blocker})
		var blockerID int64
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if found == nil {
				return nil
			}
			metadata, err := githubMetadata(found, 0)
			if err != nil {
				return err
			}
			blockerID, err = q.AddCopiedBlockerIssue(ctx, store.AddCopiedBlockerIssueParams{
				Repository: blockerRepoName,
				Number:     int64(found.GetNumber()),
				Title:      found.GetTitle(),
				Body:       found.GetBody(),
				State:      found.GetState(),
				Author:     found.GetUser().GetLogin(),
				Kind:       issueKind(labelNames(found)),
				Github:     metadata,
			})
			if err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			blockerID = blockerRow.ID
		}
		return q.AddCopiedBlocker(ctx, store.AddCopiedBlockerParams{Issue: node.ID, Blocker: blockerID})
	})
	e.copyWrite.Unlock()
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return err
}

func (e *Engine) readBlockerIssue(ctx context.Context, repository github.Repository, blockerRepoName string, blockerNumber int64, issueRepoName string, issueNumber int64) (*gh.Issue, string, error) {
	if blockerRepo, readable := e.namedRepository(blockerRepoName, repository); readable {
		found, err := blockerRepo.Issue(ctx, blockerNumber)
		return found, blockerRepo.FullName, err
	}
	issueRepo, ok := e.namedRepository(issueRepoName, repository)
	if !ok {
		return nil, blockerRepoName, nil
	}
	blockers, err := allBlockers(ctx, issueRepo, issueNumber)
	if err != nil {
		return nil, blockerRepoName, err
	}
	for _, blocker := range blockers {
		name := blockerRepository(blocker, issueRepo.FullName)
		if strings.EqualFold(name, blockerRepoName) && int64(blocker.GetNumber()) == blockerNumber {
			return blocker, name, nil
		}
	}
	return nil, blockerRepoName, nil
}

func (e *Engine) detachIssueSubtree(ctx context.Context, rootID int64) error {
	e.copyWrite.Lock()
	err := e.inTx(ctx, func(q *store.Queries) error {
		if err := detachIssueSubtreeTx(ctx, q, rootID); err != nil {
			return err
		}
		return deleteUnusedIssues(ctx, q)
	})
	e.copyWrite.Unlock()
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return err
}

func detachIssueSubtreeTx(ctx context.Context, q *store.Queries, rootID int64) error {
	ids, err := subtreeIDs(ctx, q, rootID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := q.DetachIssue(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func subtreeIDs(ctx context.Context, q *store.Queries, rootID int64) ([]int64, error) {
	seen := map[int64]bool{rootID: true}
	order := []int64{rootID}
	for i := 0; i < len(order); i++ {
		children, err := q.ListChildIssueIDs(ctx, sql.NullInt64{Int64: order[i], Valid: true})
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if seen[child] {
				continue
			}
			seen[child] = true
			order = append(order, child)
		}
	}
	return order, nil
}
