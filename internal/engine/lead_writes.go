package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// A write of the Lead matches an item of the Mobius App when the item is not more than leadWriteBefore before the record
// and not more than leadWriteAfter after it.
const (
	leadWriteBefore = time.Minute
	leadWriteAfter  = 10 * time.Minute
)

// leadItem is an item of the Mobius App on GitHub that can be a write of the Lead.
type leadItem struct {
	// kind is labeled, unlabeled or commented.
	kind  string
	id    int64
	issue int64
	label string
	// text is the body of the comment.
	text string
	at   time.Time
}

// recordGHWrites stores the writes that the gh of the Lead is about to make.
func (a *Agent) recordGHWrites(ctx context.Context, writes []mcp.GHWrite) error {
	for _, write := range writes {
		labels, err := json.Marshal(append([]string{}, write.Labels...))
		if err != nil {
			return err
		}
		err = a.engine.queries.AddLeadWrite(ctx, store.AddLeadWriteParams{
			Session:    a.id,
			Repository: write.Repository,
			Issue:      write.Issue,
			Kind:       write.Kind,
			Labels:     string(labels),
			Text:       write.Text,
			CreatedAt:  now(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// matchesLeadWrite tells if the item of the repository is the write. A write that already has an item matches only that
// item.
func matchesLeadWrite(write store.LeadWrite, repository string, item leadItem) bool {
	if !strings.EqualFold(write.Repository, repository) || write.Kind != item.kind || write.Issue != item.issue {
		return false
	}
	if write.Item.Valid {
		return write.Item.Int64 == item.id
	}
	createdAt, err := time.Parse(time.RFC3339Nano, write.CreatedAt)
	if err != nil || item.at.Before(createdAt.Add(-leadWriteBefore)) || item.at.After(createdAt.Add(leadWriteAfter)) {
		return false
	}
	if item.kind == "commented" {
		return strings.TrimSpace(write.Text) == strings.TrimSpace(item.text)
	}
	var labels []string
	return json.Unmarshal([]byte(write.Labels), &labels) == nil && slices.Contains(labels, item.label)
}

// matchLeadWrite tells if the item of the repository is a write of the Lead in writes. At the first match it stores the
// item in the write, so the write matches no other item.
func (e *Engine) matchLeadWrite(ctx context.Context, writes []store.LeadWrite, repository string, item leadItem) (bool, error) {
	for i, write := range writes {
		if !matchesLeadWrite(write, repository, item) {
			continue
		}
		if write.Item.Valid {
			return true, nil
		}
		writes[i].Item = sql.NullInt64{Int64: item.id, Valid: true}
		return true, e.queries.SetLeadWriteItem(ctx, store.SetLeadWriteItemParams{Item: writes[i].Item, ID: write.ID})
	}
	return false, nil
}

// issueEvents gives the events of the issue or the pull request number. A label event of the Mobius App that matches a
// write of the Lead has the first trusted user as its actor, so each check treats it as the action of a person. A
// setting label of a Workstream, mobius:workstream, mobius:triage and mobius:ready are no such labels: Mobius
// writes them from workstreams and from issues, or ignores mobius:ready from the App.
func (e *Engine) issueEvents(ctx context.Context, repository github.Repository, number int64) ([]*gh.IssueEvent, error) {
	events, err := repository.IssueEvents(ctx, number)
	if err != nil {
		return nil, err
	}
	app := appLogin(repository.AppSlug)
	isApp := func(event *gh.IssueEvent) bool { return strings.EqualFold(event.GetActor().GetLogin(), app) }
	if len(e.config.TrustedUsers) == 0 || !slices.ContainsFunc(events, isApp) {
		return events, nil
	}
	writes, err := e.queries.ListLeadWrites(ctx, store.ListLeadWritesParams{Repository: repository.FullName, Issue: number})
	if err != nil || len(writes) == 0 {
		return events, err
	}
	result := slices.Clone(events)
	for i, event := range events {
		label := event.GetLabel().GetName()
		if !isApp(event) || event.GetEvent() != "labeled" && event.GetEvent() != "unlabeled" || isSettingLabel(label) || label == workstreamLabel || label == triageLabel || label == readyLabel {
			continue
		}
		item := leadItem{kind: event.GetEvent(), id: event.GetID(), issue: number, label: event.GetLabel().GetName(), at: event.GetCreatedAt().Time}
		matched, err := e.matchLeadWrite(ctx, writes, repository.FullName, item)
		if err != nil {
			return nil, err
		}
		if matched {
			person := *event
			person.Actor = &gh.User{Login: new(e.config.TrustedUsers[0])}
			result[i] = &person
		}
	}
	return result, nil
}

// leadCommented tells if the comment is a comment of the Mobius App that is a write of the Lead.
func (e *Engine) leadCommented(ctx context.Context, repository github.Repository, comment *gh.IssueComment) (bool, error) {
	number, err := strconv.ParseInt(path.Base(comment.GetIssueURL()), 10, 64)
	if err != nil {
		return false, nil
	}
	writes, err := e.queries.ListLeadWrites(ctx, store.ListLeadWritesParams{Repository: repository.FullName, Issue: number})
	if err != nil {
		return false, err
	}
	item := leadItem{kind: "commented", id: comment.GetID(), issue: number, text: comment.GetBody(), at: comment.GetCreatedAt().Time}
	return e.matchLeadWrite(ctx, writes, repository.FullName, item)
}
