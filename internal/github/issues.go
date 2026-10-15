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

// found gives nil with no error when GitHub answers 404 Not Found or 410 Gone.
func found[T any](value *T, err error) (*T, error) {
	var response *gh.ErrorResponse
	if errors.As(err, &response) && (response.Response.StatusCode == http.StatusNotFound || response.Response.StatusCode == http.StatusGone) {
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

// BodyEdit is an edit of the body of an issue. Diff is the full body after the edit.
type BodyEdit struct {
	EditedAt time.Time
	Diff     string
}

type bodyEditNode struct {
	EditedAt time.Time `json:"editedAt"`
	Diff     string    `json:"diff"`
}

// IssueBodyEdits gives the edits of the body of the issue number, the newest first. An issue with no edit has none.
func (r Repository) IssueBodyEdits(ctx context.Context, number int64) ([]BodyEdit, error) {
	const query = `query($owner: String!, $name: String!, $number: Int!, $after: String) { repository(owner: $owner, name: $name) { issue(number: $number) { page: userContentEdits(first: 100, after: $after) { nodes { editedAt diff } ` + pageInfoFields + ` } } } }`
	var edits []BodyEdit
	var after *string
	for {
		var data struct {
			Repository struct {
				Issue struct {
					Page connection[bodyEditNode] `json:"page"`
				} `json:"issue"`
			} `json:"repository"`
		}
		variables := map[string]any{"owner": r.Owner(), "name": r.Name(), "number": number, "after": after}
		if err := r.graphql(ctx, query, variables, &data); err != nil {
			return nil, err
		}
		page := data.Repository.Issue.Page
		for _, node := range page.Nodes {
			edits = append(edits, BodyEdit{EditedAt: node.EditedAt, Diff: node.Diff})
		}
		if !page.PageInfo.HasNextPage {
			return edits, nil
		}
		after = &page.PageInfo.EndCursor
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

// SearchIssues gives the first 20 issues that match query, in the order of GitHub. The search is for the issues of the
// repository, but a "repo:" term in query can add other repositories, so the caller checks each repository URL.
func (r Repository) SearchIssues(ctx context.Context, query string) ([]*gh.Issue, error) {
	result, _, err := r.Client.Search.Issues(ctx, "repo:"+r.FullName+" is:issue "+query, &gh.SearchOptions{ListOptions: gh.ListOptions{PerPage: 20}})
	if err != nil {
		return nil, err
	}
	return result.Issues, nil
}

// IssueEvents gives the events of the issue number, the oldest first.
func (r Repository) IssueEvents(ctx context.Context, number int64) ([]*gh.IssueEvent, error) {
	return all(r.Client.Issues.ListIssueEventsIter(ctx, r.Owner(), r.Name(), int(number), &gh.ListOptions{PerPage: 100}))
}

// AddComment adds a comment with body to the issue or the pull request number, and gives the comment.
func (r Repository) AddComment(ctx context.Context, number int64, body string) (*gh.IssueComment, error) {
	comment, _, err := r.Client.Issues.CreateComment(ctx, r.Owner(), r.Name(), int(number), gh.IssueCommentRequest{Body: body})
	return comment, err
}

// ReactToComment adds the reaction content, for example "eyes", to the conversation comment id.
func (r Repository) ReactToComment(ctx context.Context, id int64, content string) error {
	_, _, err := r.Client.Reactions.CreateIssueCommentReaction(ctx, r.Owner(), r.Name(), id, content)
	return err
}

// ReactToReviewComment adds the reaction content to the review comment id.
func (r Repository) ReactToReviewComment(ctx context.Context, id int64, content string) error {
	_, _, err := r.Client.Reactions.CreatePullRequestCommentReaction(ctx, r.Owner(), r.Name(), id, content)
	return err
}

// ReactedToComment tells whether the Mobius App has the reaction content on the conversation comment id.
func (r Repository) ReactedToComment(ctx context.Context, id int64, content string) (bool, error) {
	reactions, _, err := r.Client.Reactions.ListIssueCommentReactions(ctx, r.Owner(), r.Name(), id, &gh.ListReactionOptions{Content: content, ListOptions: gh.ListOptions{PerPage: 100}})
	return r.ownReaction(reactions) != nil, err
}

// ReactedToReviewComment tells whether the Mobius App has the reaction content on the review comment id.
func (r Repository) ReactedToReviewComment(ctx context.Context, id int64, content string) (bool, error) {
	reactions, _, err := r.Client.Reactions.ListPullRequestCommentReactions(ctx, r.Owner(), r.Name(), id, &gh.ListReactionOptions{Content: content, ListOptions: gh.ListOptions{PerPage: 100}})
	return r.ownReaction(reactions) != nil, err
}

// UnreactToComment removes the reaction content of the Mobius App from the conversation comment id. It does nothing
// when the App has no such reaction.
func (r Repository) UnreactToComment(ctx context.Context, id int64, content string) error {
	reactions, _, err := r.Client.Reactions.ListIssueCommentReactions(ctx, r.Owner(), r.Name(), id, &gh.ListReactionOptions{Content: content, ListOptions: gh.ListOptions{PerPage: 100}})
	if err != nil {
		return err
	}
	if reaction := r.ownReaction(reactions); reaction != nil {
		_, err = r.Client.Reactions.DeleteIssueCommentReaction(ctx, r.Owner(), r.Name(), id, reaction.GetID())
	}
	return err
}

// UnreactToReviewComment removes the reaction content of the Mobius App from the review comment id. It does nothing
// when the App has no such reaction.
func (r Repository) UnreactToReviewComment(ctx context.Context, id int64, content string) error {
	reactions, _, err := r.Client.Reactions.ListPullRequestCommentReactions(ctx, r.Owner(), r.Name(), id, &gh.ListReactionOptions{Content: content, ListOptions: gh.ListOptions{PerPage: 100}})
	if err != nil {
		return err
	}
	if reaction := r.ownReaction(reactions); reaction != nil {
		_, err = r.Client.Reactions.DeletePullRequestCommentReaction(ctx, r.Owner(), r.Name(), id, reaction.GetID())
	}
	return err
}

func (r Repository) ownReaction(reactions []*gh.Reaction) *gh.Reaction {
	for _, reaction := range reactions {
		if reaction.GetUser().GetLogin() == r.AppSlug+"[bot]" {
			return reaction
		}
	}
	return nil
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

// NotFound tells whether err is the answer 404 Not Found or 410 Gone of GitHub.
func NotFound(err error) bool {
	var response *gh.ErrorResponse
	return errors.As(err, &response) && (response.Response.StatusCode == http.StatusNotFound || response.Response.StatusCode == http.StatusGone)
}

// RemoveLabel removes label from the issue number. An issue with no such label is no error.
func (r Repository) RemoveLabel(ctx context.Context, number int64, label string) error {
	_, err := r.Client.Issues.RemoveLabelForIssue(ctx, r.Owner(), r.Name(), int(number), label)
	var response *gh.ErrorResponse
	if errors.As(err, &response) && (response.Response.StatusCode == http.StatusNotFound || response.Response.StatusCode == http.StatusGone) {
		return nil
	}
	return err
}
