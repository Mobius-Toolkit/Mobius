package engine

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"path"
	"slices"
	"strconv"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The endpoints of the comment lists in the sync_cursors table. The cursor of a comment list has the creation time of
// its newest comment in since and the id of that comment in etag, because a comment list has no ETag.
const (
	issueCommentsEndpoint  = "issue_comments"
	reviewCommentsEndpoint = "review_comments"
)

type listedComment interface {
	GetID() int64
	GetCreatedAt() gh.Timestamp
}

// newComments reads the comments that list gives for the cursor of endpoint, and gives the new ones by the number of
// their issue or pull request, the oldest first. It gives the cursor that covers them, and the caller saves it after it
// acts on the comments. A comment is new when its id is above the id of the cursor. GitHub gives a higher id to a later
// comment, also in the same second, and an edited comment keeps its id. Thus a comment that GitHub writes in the second
// of the cursor is new, and a comment that Mobius handled or that a human edited is not. A list with no cursor starts at
// issuesSince, the cursor of the issue list: a comment that GitHub created at or before that time is old.
func newComments[C listedComment](ctx context.Context, queries *store.Queries, repository github.Repository, endpoint string, issuesSince time.Time, list func(context.Context, time.Time) ([]C, error), number func(C) int64) (map[int64][]C, store.SetSyncCursorParams, error) {
	cursor, err := queries.GetSyncCursor(ctx, store.GetSyncCursorParams{Repository: repository.FullName, Endpoint: endpoint})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, store.SetSyncCursorParams{}, err
	}
	since := issuesSince
	if cursor.Since.Valid {
		if since, err = time.Parse(time.RFC3339, cursor.Since.String); err != nil {
			return nil, store.SetSyncCursorParams{}, err
		}
	}
	var lastID int64
	if cursor.Etag.Valid {
		if lastID, err = strconv.ParseInt(cursor.Etag.String, 10, 64); err != nil {
			return nil, store.SetSyncCursorParams{}, err
		}
	}
	comments, err := list(ctx, since)
	if err != nil {
		return nil, store.SetSyncCursorParams{}, err
	}
	comments = slices.DeleteFunc(comments, func(comment C) bool {
		return comment.GetID() <= lastID || !cursor.Since.Valid && !comment.GetCreatedAt().After(issuesSince)
	})
	slices.SortFunc(comments, func(a, b C) int { return cmp.Compare(a.GetID(), b.GetID()) })
	next := store.SetSyncCursorParams{Repository: repository.FullName, Endpoint: endpoint, Since: cursor.Since, Etag: cursor.Etag}
	byNumber := map[int64][]C{}
	for _, comment := range comments {
		byNumber[number(comment)] = append(byNumber[number(comment)], comment)
		next.Since = sql.NullString{String: comment.GetCreatedAt().UTC().Format(time.RFC3339), Valid: true}
		next.Etag = sql.NullString{String: strconv.FormatInt(comment.GetID(), 10), Valid: true}
	}
	return byNumber, next, nil
}

// numberOfURL gives the number at the end of the issue_url or the pull_request_url of a comment.
func numberOfURL(url string) int64 {
	number, _ := strconv.ParseInt(path.Base(url), 10, 64)
	return number
}

func issueCommentNumber(comment *gh.IssueComment) int64 { return numberOfURL(comment.GetIssueURL()) }

func reviewCommentNumber(comment *gh.PullRequestComment) int64 {
	return numberOfURL(comment.GetPullRequestURL())
}
