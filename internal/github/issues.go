package github

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"slices"
	"strings"
	"time"

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

// CommentsSince gives the conversation comments of all issues and pull requests of the repository that changed at or
// after since.
func (r Repository) CommentsSince(ctx context.Context, since time.Time) ([]*gh.IssueComment, error) {
	options := &gh.IssueListCommentsOptions{Sort: new("updated"), Direction: new("asc"), Since: &since, ListOptions: gh.ListOptions{PerPage: 100}}
	return all(r.Client.Issues.ListCommentsIter(ctx, r.Owner(), r.Name(), 0, options))
}

// Reviews gives the reviews of the pull request number.
func (r Repository) Reviews(ctx context.Context, number int64) ([]*gh.PullRequestReview, error) {
	return all(r.Client.PullRequests.ListReviewsIter(ctx, r.Owner(), r.Name(), int(number), &gh.ListOptions{PerPage: 100}))
}

// ReviewComments gives the review comments of the pull request number.
func (r Repository) ReviewComments(ctx context.Context, number int64) ([]*gh.PullRequestComment, error) {
	return all(r.Client.PullRequests.ListCommentsIter(ctx, r.Owner(), r.Name(), int(number), &gh.PullRequestListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}))
}

// ReviewCommentsSince gives the review comments of all pull requests of the repository that changed at or after since.
func (r Repository) ReviewCommentsSince(ctx context.Context, since time.Time) ([]*gh.PullRequestComment, error) {
	options := &gh.PullRequestListCommentsOptions{Sort: "updated", Direction: "asc", Since: since, ListOptions: gh.ListOptions{PerPage: 100}}
	return all(r.Client.PullRequests.ListCommentsIter(ctx, r.Owner(), r.Name(), 0, options))
}

// OpenIssuesWithLabel gives the open issues with label. It gives no pull request.
func (r Repository) OpenIssuesWithLabel(ctx context.Context, label string) ([]*gh.Issue, error) {
	issues, err := all(r.Client.Issues.ListByRepoIter(ctx, r.Owner(), r.Name(), &gh.IssueListByRepoOptions{State: "open", Labels: []string{label}, ListOptions: gh.ListOptions{PerPage: 100}}))
	return slices.DeleteFunc(issues, (*gh.Issue).IsPullRequest), err
}

// IssueEvents gives the events of the issue number, the oldest first.
func (r Repository) IssueEvents(ctx context.Context, number int64) ([]*gh.IssueEvent, error) {
	return all(r.Client.Issues.ListIssueEventsIter(ctx, r.Owner(), r.Name(), int(number), &gh.ListOptions{PerPage: 100}))
}

// AddComment adds a comment with body to the issue or the pull request number, and gives the id of the comment.
func (r Repository) AddComment(ctx context.Context, number int64, body string) (int64, error) {
	comment, _, err := r.Client.Issues.CreateComment(ctx, r.Owner(), r.Name(), int(number), gh.IssueCommentRequest{Body: body})
	return comment.GetID(), err
}

// ReactToComment adds the reaction content, for example "eyes", to the conversation comment id. It tells if the
// reaction is new: GitHub gives status 200 for a reaction that already exists.
func (r Repository) ReactToComment(ctx context.Context, id int64, content string) (bool, error) {
	_, response, err := r.Client.Reactions.CreateIssueCommentReaction(ctx, r.Owner(), r.Name(), id, content)
	if err != nil {
		return false, err
	}
	return response.StatusCode == http.StatusCreated, nil
}

// ReactToReviewComment adds the reaction content to the review comment id. It tells if the reaction is new.
func (r Repository) ReactToReviewComment(ctx context.Context, id int64, content string) (bool, error) {
	_, response, err := r.Client.Reactions.CreatePullRequestCommentReaction(ctx, r.Owner(), r.Name(), id, content)
	if err != nil {
		return false, err
	}
	return response.StatusCode == http.StatusCreated, nil
}

// ReplyToReviewComment adds a reply with body to the review thread that starts with the comment root of the pull
// request number.
func (r Repository) ReplyToReviewComment(ctx context.Context, number, root int64, body string) error {
	_, _, err := r.Client.PullRequests.CreateCommentInReplyTo(ctx, r.Owner(), r.Name(), int(number), body, root)
	return err
}

// UpdateComment replaces the body of the comment id of an issue or a pull request.
func (r Repository) UpdateComment(ctx context.Context, id int64, body string) error {
	_, _, err := r.Client.Issues.UpdateComment(ctx, r.Owner(), r.Name(), id, gh.IssueCommentRequest{Body: body})
	return err
}

// CloseIssue closes the issue number with reason, completed or not_planned, and gives the closed issue.
func (r Repository) CloseIssue(ctx context.Context, number int64, reason string) (*gh.Issue, error) {
	issue, _, err := r.Client.Issues.Update(ctx, r.Owner(), r.Name(), int(number), gh.UpdateIssueRequest{State: new("closed"), StateReason: &reason})
	return issue, err
}

// ClosePullRequest closes the pull request number with no merge.
func (r Repository) ClosePullRequest(ctx context.Context, number int64) error {
	_, _, err := r.Client.PullRequests.Edit(ctx, r.Owner(), r.Name(), int(number), &gh.PullRequest{State: new("closed")})
	return err
}

// AddLabel adds label to the issue number.
func (r Repository) AddLabel(ctx context.Context, number int64, label string) error {
	_, _, err := r.Client.Issues.AddLabelsToIssue(ctx, r.Owner(), r.Name(), int(number), []string{label})
	return err
}

// RemoveLabel removes label from the issue number. An issue with no such label is no error.
func (r Repository) RemoveLabel(ctx context.Context, number int64, label string) error {
	_, err := r.Client.Issues.RemoveLabelForIssue(ctx, r.Owner(), r.Name(), int(number), label)
	var response *gh.ErrorResponse
	if errors.As(err, &response) && response.Response.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}
