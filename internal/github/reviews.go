package github

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Review is a review of a pull request.
type Review struct {
	// ID is the GraphQL node id of the review.
	ID string
	// Author is the REST login of the author. The login of a bot ends with "[bot]".
	Author string
	// State is PENDING, COMMENTED, APPROVED, CHANGES_REQUESTED or DISMISSED.
	State string
	// Commit is the id of the commit that the review applies to.
	Commit string
	// SubmittedAt is the zero time for a pending review.
	SubmittedAt time.Time
}

// ThreadComment is a comment of a review thread.
type ThreadComment struct {
	ID     int64
	Author string
	Body   string
	// CreatedAt has a precision of one second.
	CreatedAt time.Time
}

// ReviewThread is a review thread of a pull request.
type ReviewThread struct {
	// ID is the GraphQL node id of the thread.
	ID string
	// Comment is the id of the first comment of the thread.
	Comment  int64
	Resolved bool
	// Authors are the REST logins of the authors of the comments, in order. The login of a bot ends with "[bot]".
	Authors []string
	Path    string
	// Line is 0 when the thread has no line.
	Line     int
	Comments []ThreadComment
}

// PullRequestReviews are the reviews and the review threads of a pull request.
type PullRequestReviews struct {
	Reviews []Review
	Threads []ReviewThread
}

// pullRequestsPerQuery keeps a query below the limit of 500000 nodes of GitHub.
const pullRequestsPerQuery = 25

const (
	pageInfoFields = `pageInfo { hasNextPage endCursor }`
	reviewFields   = `id state author { __typename login } commit { oid } submittedAt`
	commentFields  = `databaseId author { __typename login } body createdAt`
	threadFields   = `id isResolved path line comments(first: 100) { nodes { ` + commentFields + ` } ` + pageInfoFields + ` }`
)

type connection[T any] struct {
	Nodes    []T `json:"nodes"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

type actor struct {
	Typename string `json:"__typename"`
	Login    string `json:"login"`
}

// login gives the REST login of the actor. GraphQL gives a bot login with no "[bot]".
func (a actor) login() string {
	if a.Typename == "Bot" {
		return a.Login + "[bot]"
	}
	return a.Login
}

type reviewNode struct {
	ID          string               `json:"id"`
	State       string               `json:"state"`
	Author      actor                `json:"author"`
	Commit      struct{ OID string } `json:"commit"`
	SubmittedAt time.Time            `json:"submittedAt"`
}

type commentNode struct {
	DatabaseID int64     `json:"databaseId"`
	Author     actor     `json:"author"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

type threadNode struct {
	ID         string                  `json:"id"`
	IsResolved bool                    `json:"isResolved"`
	Path       string                  `json:"path"`
	Line       int                     `json:"line"`
	Comments   connection[commentNode] `json:"comments"`
}

type pullRequestNode struct {
	ID            string                 `json:"id"`
	Reviews       connection[reviewNode] `json:"reviews"`
	ReviewThreads connection[threadNode] `json:"reviewThreads"`
}

// pullRequestsQuery gives the query for the reviews and the review threads of the pull requests numbers, one page of
// each. The alias of a pull request is "pr" and its number.
func pullRequestsQuery(numbers []int64) string {
	var query strings.Builder
	query.WriteString(`query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) {`)
	for _, number := range numbers {
		fmt.Fprintf(&query, ` pr%[1]d: pullRequest(number: %[1]d) { id reviews(first: 100) { nodes { %[2]s } %[3]s } reviewThreads(first: 100) { nodes { %[4]s } %[3]s } }`,
			number, reviewFields, pageInfoFields, threadFields)
	}
	query.WriteString(` } }`)
	return query.String()
}

// PullRequestReviews gives the reviews and the review threads of the pull requests numbers with one GraphQL call for
// 25 pull requests. It reads the next pages of a connection that has more than one page.
func (r Repository) PullRequestReviews(ctx context.Context, numbers []int64) (map[int64]PullRequestReviews, error) {
	found := map[int64]PullRequestReviews{}
	for chunk := range slices.Chunk(numbers, pullRequestsPerQuery) {
		var data struct {
			Repository map[string]*pullRequestNode `json:"repository"`
		}
		if err := r.graphql(ctx, pullRequestsQuery(chunk), map[string]any{"owner": r.Owner(), "name": r.Name()}, &data); err != nil {
			return nil, err
		}
		for _, number := range chunk {
			node := data.Repository[fmt.Sprintf("pr%d", number)]
			if node == nil {
				return nil, errors.New("GitHub gave no pull request")
			}
			reviews, err := r.pullRequestReviews(ctx, node)
			if err != nil {
				return nil, err
			}
			found[number] = reviews
		}
	}
	return found, nil
}

func (r Repository) pullRequestReviews(ctx context.Context, node *pullRequestNode) (PullRequestReviews, error) {
	var found PullRequestReviews
	reviews, err := remaining(ctx, r, node.ID, "PullRequest", "reviews", reviewFields, node.Reviews)
	if err != nil {
		return found, err
	}
	for _, review := range reviews {
		found.Reviews = append(found.Reviews, Review{ID: review.ID, Author: review.Author.login(), State: review.State, Commit: review.Commit.OID, SubmittedAt: review.SubmittedAt})
	}
	threads, err := remaining(ctx, r, node.ID, "PullRequest", "reviewThreads", threadFields, node.ReviewThreads)
	if err != nil {
		return found, err
	}
	for _, node := range threads {
		comments, err := remaining(ctx, r, node.ID, "PullRequestReviewThread", "comments", commentFields, node.Comments)
		if err != nil {
			return found, err
		}
		if len(comments) == 0 {
			return found, errors.New("GitHub gave a review thread with no comment")
		}
		thread := ReviewThread{ID: node.ID, Comment: comments[0].DatabaseID, Resolved: node.IsResolved, Path: node.Path, Line: node.Line}
		for _, comment := range comments {
			thread.Authors = append(thread.Authors, comment.Author.login())
			thread.Comments = append(thread.Comments, ThreadComment{ID: comment.DatabaseID, Author: comment.Author.login(), Body: comment.Body, CreatedAt: comment.CreatedAt})
		}
		found.Threads = append(found.Threads, thread)
	}
	return found, nil
}

// remaining gives the nodes of the connection field of the object kind with the node id id: the nodes of first and the
// nodes of its next pages. fields are the fields of a node.
func remaining[T any](ctx context.Context, r Repository, id, kind, field, fields string, first connection[T]) ([]T, error) {
	if !first.PageInfo.HasNextPage {
		return first.Nodes, nil
	}
	query := fmt.Sprintf(`query($id: ID!, $after: String) { node(id: $id) { ... on %s { page: %s(first: 100, after: $after) { nodes { %s } %s } } } }`,
		kind, field, fields, pageInfoFields)
	nodes := first.Nodes
	for page := first; page.PageInfo.HasNextPage; {
		var data struct {
			Node struct {
				Page connection[T] `json:"page"`
			} `json:"node"`
		}
		if err := r.graphql(ctx, query, map[string]any{"id": id, "after": page.PageInfo.EndCursor}, &data); err != nil {
			return nil, err
		}
		page = data.Node.Page
		nodes = append(nodes, page.Nodes...)
	}
	return nodes, nil
}

// ReviewThreads gives the review threads of the pull request number.
func (r Repository) ReviewThreads(ctx context.Context, number int64) ([]ReviewThread, error) {
	found, err := r.PullRequestReviews(ctx, []int64{number})
	return found[number].Threads, err
}
