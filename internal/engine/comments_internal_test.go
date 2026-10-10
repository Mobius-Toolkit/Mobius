package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

func TestAListThatGivesACommentTwoTimesGivesItOneTime(t *testing.T) {
	t.Parallel()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	created := gh.Timestamp{Time: time.Unix(1_700_000_000, 0)}
	comment := func(id int64) *gh.IssueComment {
		return &gh.IssueComment{ID: &id, CreatedAt: &created, IssueURL: new("https://api.github.com/repos/owner/shop/issues/41")}
	}
	list := func(context.Context, time.Time) ([]*gh.IssueComment, error) {
		return []*gh.IssueComment{comment(7), comment(5), comment(7), comment(6), comment(5)}, nil
	}

	found, cursor, err := newComments(t.Context(), store.New(db), shopRepository, issueCommentsEndpoint, time.Time{}, list, issueCommentNumber)

	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, comment := range found[41] {
		ids = append(ids, comment.GetID())
	}
	if len(ids) != 3 || ids[0] != 5 || ids[1] != 6 || ids[2] != 7 || cursor.Etag.String != "7" {
		t.Errorf("ids = %v, cursor = %+v", ids, cursor)
	}
}
