package engine

import (
	"context"
	"database/sql"
	"path"
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

// leadItem is a comment of the Mobius App on GitHub that can be a write of the Lead.
type leadItem struct {
	id    int64
	issue int64
	text  string
	at    time.Time
}

// recordGHWrites stores the comments that the gh of the Lead is about to write.
func (a *Agent) recordGHWrites(ctx context.Context, writes []mcp.GHWrite) error {
	for _, write := range writes {
		if write.Kind != "commented" {
			continue
		}
		err := a.engine.queries.AddLeadWrite(ctx, store.AddLeadWriteParams{
			Session:    a.id,
			Repository: write.Repository,
			Issue:      write.Issue,
			Kind:       write.Kind,
			Labels:     "[]",
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
	if !strings.EqualFold(write.Repository, repository) || write.Issue != item.issue {
		return false
	}
	if write.Item.Valid {
		return write.Item.Int64 == item.id
	}
	createdAt, err := time.Parse(time.RFC3339Nano, write.CreatedAt)
	if err != nil || item.at.Before(createdAt.Add(-leadWriteBefore)) || item.at.After(createdAt.Add(leadWriteAfter)) {
		return false
	}
	return strings.TrimSpace(write.Text) == strings.TrimSpace(item.text)
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
	item := leadItem{id: comment.GetID(), issue: number, text: comment.GetBody(), at: comment.GetCreatedAt().Time}
	return e.matchLeadWrite(ctx, writes, repository.FullName, item)
}
