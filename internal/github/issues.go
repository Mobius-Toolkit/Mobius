package github

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v92/github"
)

// Repository gives the repository fullName of the last read.
func (g *GitHub) Repository(fullName string) (Repository, bool) {
	for _, repository := range g.Repositories() {
		if repository.FullName == fullName {
			return repository, true
		}
	}
	return Repository{}, false
}

// Owner gives the owner of the repository.
func (r Repository) Owner() string {
	owner, _, _ := strings.Cut(r.FullName, "/")
	return owner
}

// Name gives the name of the repository with no owner.
func (r Repository) Name() string {
	_, name, _ := strings.Cut(r.FullName, "/")
	return name
}

// found gives nil with no error when GitHub answers 404 Not Found.
func found[T any](value *T, err error) (*T, error) {
	var response *gh.ErrorResponse
	if errors.As(err, &response) && response.Response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	return value, err
}

// Issue gives the issue or the pull request number, or nil when the repository has no such issue.
func (r Repository) Issue(ctx context.Context, number int64) (*gh.Issue, error) {
	issue, _, err := r.Client.Issues.Get(ctx, r.Owner(), r.Name(), int(number))
	return found(issue, err)
}

// Parent gives the parent of the issue number, or nil when the issue has no parent.
func (r Repository) Parent(ctx context.Context, number int64) (*gh.Issue, error) {
	parent, _, err := r.Client.SubIssue.GetParentIssue(ctx, r.Owner(), r.Name(), number)
	return found(parent, err)
}

// ReviewThread is a review thread of a pull request.
type ReviewThread struct {
	// ID is the GraphQL node id of the thread.
	ID string
	// Comment is the id of the first comment of the thread.
	Comment int64
}

type graphqlError struct {
	Message string `json:"message"`
}

// graphql sends the GraphQL query with variables and decodes its data into data.
func (r Repository) graphql(ctx context.Context, query string, variables map[string]any, data any) error {
	request, err := r.Client.NewRequest(ctx, http.MethodPost, "graphql", map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	var response struct {
		Data   any            `json:"data"`
		Errors []graphqlError `json:"errors"`
	}
	response.Data = data
	if _, err := r.Client.Do(request, &response); err != nil {
		return err
	}
	if len(response.Errors) > 0 {
		return errors.New(response.Errors[0].Message)
	}
	return nil
}

// ReviewThreads gives the review threads of the pull request number.
func (r Repository) ReviewThreads(ctx context.Context, number int64) ([]ReviewThread, error) {
	const query = `query($owner: String!, $name: String!, $number: Int!, $after: String) {
		repository(owner: $owner, name: $name) {
			pullRequest(number: $number) {
				reviewThreads(first: 100, after: $after) {
					nodes { id comments(first: 1) { nodes { databaseId } } }
					pageInfo { hasNextPage endCursor }
				}
			}
		}
	}`
	var threads []ReviewThread
	var after *string
	for {
		var data struct {
			Repository struct {
				PullRequest *struct {
					ReviewThreads struct {
						Nodes []struct {
							ID       string `json:"id"`
							Comments struct {
								Nodes []struct {
									DatabaseID int64 `json:"databaseId"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
						PageInfo struct {
							HasNextPage bool    `json:"hasNextPage"`
							EndCursor   *string `json:"endCursor"`
						} `json:"pageInfo"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		variables := map[string]any{"owner": r.Owner(), "name": r.Name(), "number": number, "after": after}
		if err := r.graphql(ctx, query, variables, &data); err != nil {
			return nil, err
		}
		if data.Repository.PullRequest == nil {
			return nil, errors.New("GitHub gave no pull request")
		}
		page := data.Repository.PullRequest.ReviewThreads
		for _, node := range page.Nodes {
			if len(node.Comments.Nodes) == 0 {
				return nil, errors.New("GitHub gave a review thread with no comment")
			}
			threads = append(threads, ReviewThread{ID: node.ID, Comment: node.Comments.Nodes[0].DatabaseID})
		}
		if !page.PageInfo.HasNextPage {
			return threads, nil
		}
		after = page.PageInfo.EndCursor
	}
}

// ResolveReviewThread resolves the review thread with the GraphQL node id.
func (r Repository) ResolveReviewThread(ctx context.Context, id string) error {
	const query = `mutation($id: ID!) { resolveReviewThread(input: { threadId: $id }) { clientMutationId } }`
	var data any
	return r.graphql(ctx, query, map[string]any{"id": id}, &data)
}

func all[T any](seq iter.Seq2[T, error]) ([]T, error) {
	var items []T
	for item, err := range seq {
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// SubIssues gives the sub-issues of the issue number. A sub-issue can be in another repository.
func (r Repository) SubIssues(ctx context.Context, number int64) ([]*gh.Issue, error) {
	subIssues, err := all(r.Client.SubIssue.ListByIssueIter(ctx, r.Owner(), r.Name(), number, &gh.ListOptions{PerPage: 100}))
	issues := make([]*gh.Issue, 0, len(subIssues))
	for _, subIssue := range subIssues {
		issues = append(issues, (*gh.Issue)(subIssue))
	}
	return issues, err
}

// BlockedBy gives the issues that block the issue number.
func (r Repository) BlockedBy(ctx context.Context, number int64) ([]*gh.Issue, error) {
	return all(r.Client.Issues.ListBlockedByIter(ctx, r.Owner(), r.Name(), number, &gh.ListOptions{PerPage: 100}))
}

// Comments gives the comments of the issue or the pull request number.
func (r Repository) Comments(ctx context.Context, number int64) ([]*gh.IssueComment, error) {
	return all(r.Client.Issues.ListCommentsIter(ctx, r.Owner(), r.Name(), int(number), &gh.IssueListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}))
}

// Reviews gives the reviews of the pull request number.
func (r Repository) Reviews(ctx context.Context, number int64) ([]*gh.PullRequestReview, error) {
	return all(r.Client.PullRequests.ListReviewsIter(ctx, r.Owner(), r.Name(), int(number), &gh.ListOptions{PerPage: 100}))
}

// ReviewComments gives the review comments of the pull request number.
func (r Repository) ReviewComments(ctx context.Context, number int64) ([]*gh.PullRequestComment, error) {
	return all(r.Client.PullRequests.ListCommentsIter(ctx, r.Owner(), r.Name(), int(number), &gh.PullRequestListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}))
}
