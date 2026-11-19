package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

type issueMetadata struct {
	GitHub issueGitHub  `json:"github"`
	Mobius *issueMobius `json:"mobius,omitempty"`
}

type issueMobius struct {
	AwaitsNumber bool              `json:"awaits_number,omitempty"`
	Implementer  *issueImplementer `json:"implementer,omitempty"`
}

type issueImplementer struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

type parentToAdd struct {
	id    int64
	at    issueAt
	issue *gh.Issue
}

func parseIssueMetadata(metadata string) (issueMetadata, error) {
	var parsed issueMetadata
	err := json.Unmarshal([]byte(metadata), &parsed)
	return parsed, err
}

// addIssueToCreate is the create handler for a tree issue. In one transaction, it adds the row of issues with no
// number, adds its blockers, and adds the outgoing changes create, parent and blockers in order. It gives the id of
// the row and the id of its create change.
func (e *Engine) addIssueToCreate(ctx context.Context, repository github.Repository, workstream issueRef, title, body, mark string, parent *parentToAdd, blockers []resolvedBlocker, labels []string, setting *implementerSettingInput) (int64, int64, error) {
	meta := issueMetadata{
		GitHub: issueGitHub{Labels: append(make([]string, 0, len(labels)), labels...)},
	}
	if setting != nil {
		meta.Mobius = &issueMobius{
			Implementer: &issueImplementer{
				Harness: setting.Harness,
				Model:   setting.Model,
				Effort:  setting.Effort,
			},
		}
	}
	metadata, err := json.Marshal(meta)
	if err != nil {
		return 0, 0, err
	}
	var issueID, createID int64
	e.copyWrite.Lock()
	err = e.inTx(ctx, func(q *store.Queries) error {
		workstreamID, err := q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: workstream.repository, Number: workstream.number})
		if err != nil {
			return err
		}
		var parentID sql.NullInt64
		if parent != nil {
			if parent.id != 0 {
				parentID = sql.NullInt64{Int64: parent.id, Valid: true}
			} else if parent.issue != nil {
				parentMeta, err := githubMetadata(parent.issue, 0)
				if err != nil {
					return err
				}
				id, err := q.AddCopiedIssue(ctx, store.AddCopiedIssueParams{
					Repository: parent.at.repository.FullName,
					Number:     parent.at.number,
					Title:      parent.issue.GetTitle(),
					Body:       e.WithoutChatLink(workstream.repository, workstream.number, parent.issue.GetBody()),
					State:      parent.issue.GetState(),
					Author:     parent.issue.GetUser().GetLogin(),
					Workstream: sql.NullInt64{Int64: workstreamID, Valid: true},
					Kind:       issueKind(labelNames(parent.issue)),
					Github:     parentMeta,
				})
				if err != nil {
					return err
				}
				parentID = sql.NullInt64{Int64: id, Valid: true}
			}
		}
		issueID, err = q.AddIssueToCreate(ctx, store.AddIssueToCreateParams{
			Repository: repository.FullName,
			Title:      title,
			Body:       e.WithoutChatLink(workstream.repository, workstream.number, body),
			Author:     appLogin(repository.AppSlug),
			Workstream: sql.NullInt64{Int64: workstreamID, Valid: true},
			Parent:     parentID,
			Metadata:   string(metadata),
		})
		if err != nil {
			return err
		}
		for _, blocker := range blockers {
			blockerID := blocker.id
			if blockerID == 0 {
				blockerMeta, err := githubMetadata(blocker.issue, 0)
				if err != nil {
					return err
				}
				blockerID, err = q.AddCopiedBlockerIssue(ctx, store.AddCopiedBlockerIssueParams{
					Repository: blocker.at.repository.FullName,
					Number:     blocker.at.number,
					Title:      blocker.issue.GetTitle(),
					Body:       blocker.issue.GetBody(),
					State:      blocker.issue.GetState(),
					Author:     blocker.issue.GetUser().GetLogin(),
					Kind:       issueKind(labelNames(blocker.issue)),
					Github:     blockerMeta,
				})
				if err != nil {
					return err
				}
			}
			if err := q.AddCopiedBlocker(ctx, store.AddCopiedBlockerParams{Issue: issueID, Blocker: blockerID}); err != nil {
				return err
			}
		}
		if createID, err = addNewIssueChange(ctx, q, repository.FullName, issueID, createField, mark); err != nil {
			return err
		}
		if _, err := addNewIssueChange(ctx, q, repository.FullName, issueID, parentField, ""); err != nil {
			return err
		}
		_, err = addNewIssueChange(ctx, q, repository.FullName, issueID, blockersField, "")
		return err
	})
	e.copyWrite.Unlock()
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return issueID, createID, err
}

// sendIssueCreateNow sends the outgoing create change of issueID while holding the mutex of its item. When the row
// keeps no number and still exists, it sets awaits_number in the same lock so a later poll sends one issue_created
// event when it fills the number.
func (e *Engine) sendIssueCreateNow(ctx context.Context, repository github.Repository, issueID, createID int64) error {
	item := changeItem{kind: "issue", id: issueID}
	mu := e.outgoingItem(repository.FullName, item)
	mu.Lock()
	defer mu.Unlock()
	change, err := e.queries.GetOutgoingChange(ctx, createID)
	if err != nil {
		return err
	}
	if change.Status == "pending" {
		pending, err := e.pendingItemChanges(ctx, repository.FullName, item)
		if err != nil {
			return err
		}
		if len(pending) > 0 && pending[0].ID == change.ID {
			if _, err := e.send(ctx, repository, change, false); err != nil {
				return err
			}
		}
	}
	row, err := e.queries.GetIssueByID(ctx, issueID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Number.Valid {
		return nil
	}
	_, err = e.queries.SetIssueAwaitsNumber(ctx, issueID)
	return err
}

// sendIssueCreate creates the issue of row on GitHub if it has no number, fills its number and GitHub id, stores any
// kept Implementer setting, adds issue_created when awaits_number is set, and sends the remaining changes of the issue.
func (e *Engine) sendIssueCreate(ctx context.Context, repository github.Repository, change store.OutgoingChange, row store.GetIssueByIDRow) (bool, error) {
	if !row.Number.Valid {
		meta, err := parseIssueMetadata(row.Metadata)
		if err != nil {
			return false, err
		}
		body := e.withChatLink(github.Repository{FullName: row.WorkstreamRepository.String}, row.WorkstreamNumber.Int64, row.Body)
		if change.Text.String != "" {
			body = runner.WithMark(body, change.Text.String)
		}
		var labels []string
		if len(meta.GitHub.Labels) > 0 {
			labels = meta.GitHub.Labels
		}
		issue, err := e.findOrCreateIssue(ctx, repository, change, row.Title, body, labels)
		if permanentRefusal(err) {
			log.Printf("create the issue %d of %s: %v", row.ID, change.Repository, err)
			return false, e.dropIssueCreate(ctx, repository, change, row, err)
		}
		if err != nil {
			return e.failed(ctx, repository, change, err)
		}
		if err := e.addIssueCreatedNumber(ctx, repository, row, int64(issue.GetNumber()), issue.GetID(), meta, change.ID); err != nil {
			return false, err
		}
	} else if err := e.markSent(ctx, change); err != nil {
		return false, err
	}
	return e.sendNewIssue(ctx, repository, row.ID)
}

func (e *Engine) addIssueCreatedNumber(ctx context.Context, repository github.Repository, row store.GetIssueByIDRow, number, githubID int64, meta issueMetadata, createID int64) error {
	awaitsNumber := meta.Mobius != nil && meta.Mobius.AwaitsNumber
	storedBinding := false
	update := func(q *store.Queries) error {
		set, err := q.SetIssueNumber(ctx, store.SetIssueNumberParams{
			Number:   sql.NullInt64{Int64: number, Valid: true},
			GithubID: githubID,
			ID:       row.ID,
		})
		if err != nil {
			return err
		}
		if set == 0 {
			return sql.ErrNoRows
		}
		if err := q.SetOutgoingChangeStatus(ctx, store.SetOutgoingChangeStatusParams{Status: "sent", ID: createID}); err != nil {
			return err
		}
		if meta.Mobius != nil && meta.Mobius.Implementer != nil {
			imp := meta.Mobius.Implementer
			if err := q.UpsertIssueBinding(ctx, store.UpsertIssueBindingParams{
				Repository: repository.FullName,
				Issue:      number,
				Role:       "implementer",
				Harness:    imp.Harness,
				Model:      imp.Model,
				Effort:     imp.Effort,
				UpdatedAt:  now(),
			}); err != nil {
				return err
			}
			storedBinding = true
		}
		return nil
	}
	var err error
	if awaitsNumber {
		text := fmt.Sprintf("Mobius created %s#%d %q of your earlier create_issue call.", repository.FullName, number, row.Title)
		err = e.addLeadEventWith(ctx, row.WorkstreamRepository.String, row.WorkstreamNumber.Int64, repository.FullName, sql.NullInt64{Int64: number, Valid: true}, "issue_created", text, update)
	} else {
		err = e.inTx(ctx, update)
	}
	if err != nil {
		return err
	}
	if storedBinding {
		e.publish(Change{Pauses: true})
		e.workers.changed.notify()
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// sendNewIssue sends the remaining pending outgoing changes of issueID under the already held item mutex.
func (e *Engine) sendNewIssue(ctx context.Context, repository github.Repository, issueID int64) (bool, error) {
	pending, err := e.pendingItemChanges(ctx, repository.FullName, changeItem{kind: "issue", id: issueID})
	if err != nil {
		return false, err
	}
	for _, change := range pending {
		if waiting, err := e.send(ctx, repository, change, false); err != nil || waiting {
			return waiting, err
		}
	}
	return false, nil
}

// dropIssueCreate refuses each pending change of the uncreated issue row, deletes its blockers and the row, and adds
// an issue_create_refused Lead event.
func (e *Engine) dropIssueCreate(ctx context.Context, repository github.Repository, change store.OutgoingChange, row store.GetIssueByIDRow, cause error) error {
	issue := sql.NullInt64{Int64: row.ID, Valid: true}
	text := fmt.Sprintf("GitHub refused the create of the issue %q: %v. Create it again with create_issue.", row.Title, cause)
	err := e.addLeadEventWith(ctx, row.WorkstreamRepository.String, row.WorkstreamNumber.Int64, repository.FullName, sql.NullInt64{}, "issue_create_refused", text, func(q *store.Queries) error {
		if err := q.RefuseIssueChanges(ctx, issue); err != nil {
			return err
		}
		if err := q.SetOutgoingChangeStatus(ctx, store.SetOutgoingChangeStatusParams{Status: "refused", ID: change.ID}); err != nil {
			return err
		}
		if err := q.DeleteCopiedBlockersOfIssue(ctx, row.ID); err != nil {
			return err
		}
		return q.DeleteIssueToCreate(ctx, row.ID)
	})
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return err
}

// sendIssueParent links row as a sub-issue of its parent issue, or of the Workstream issue when row has no parent row.
// It waits for the next poll if the parent row has no number yet.
func (e *Engine) sendIssueParent(ctx context.Context, repository github.Repository, change store.OutgoingChange, row store.GetIssueByIDRow) (bool, error) {
	var parentRepo github.Repository
	var parentNumber int64
	if row.Parent.Valid {
		parentRow, err := e.queries.GetIssueByID(ctx, row.Parent.Int64)
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("write the parent of %s#%d: the parent row is gone", change.Repository, row.Number.Int64)
			return false, e.refuse(ctx, change)
		}
		if err != nil {
			return false, err
		}
		if !parentRow.Number.Valid {
			return true, nil
		}
		var ok bool
		if parentRepo, ok = e.namedRepository(parentRow.Repository, repository); !ok {
			return false, e.refuse(ctx, change)
		}
		parentNumber = parentRow.Number.Int64
	} else {
		var ok bool
		if parentRepo, ok = e.namedRepository(row.WorkstreamRepository.String, repository); !ok {
			return false, e.refuse(ctx, change)
		}
		parentNumber = row.WorkstreamNumber.Int64
	}
	meta, err := parseIssueMetadata(row.Metadata)
	if err != nil {
		return false, err
	}
	if meta.GitHub.ID == 0 {
		found, err := repository.Issue(ctx, row.Number.Int64)
		if err != nil {
			return e.failed(ctx, repository, change, err)
		}
		if found == nil {
			return false, e.refuse(ctx, change)
		}
		meta.GitHub.ID = found.GetID()
	}
	if _, _, err := parentRepo.Client.SubIssue.Add(ctx, parentRepo.Owner(), parentRepo.Name(), parentNumber, gh.SubIssueRequest{SubIssueID: meta.GitHub.ID, ReplaceParent: new(true)}); err != nil {
		return e.failed(ctx, repository, change, err)
	}
	return false, e.markSent(ctx, change)
}
