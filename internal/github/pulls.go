package github

import (
	"context"
	"fmt"
	"io"
	"net/http"

	gh "github.com/google/go-github/v92/github"
)

// PullRequest gives the pull request number.
func (r Repository) PullRequest(ctx context.Context, number int64) (*gh.PullRequest, error) {
	pullRequest, _, err := r.Client.PullRequests.Get(ctx, r.Owner(), r.Name(), int(number))
	return pullRequest, err
}

// CreateDraftPullRequest opens a draft pull request from the branch head into the branch base.
func (r Repository) CreateDraftPullRequest(ctx context.Context, title, head, base, body string) (*gh.PullRequest, error) {
	pullRequest, _, err := r.Client.PullRequests.Create(ctx, r.Owner(), r.Name(), gh.CreatePullRequest{
		Title: &title,
		Head:  head,
		Base:  base,
		Body:  &body,
		Draft: new(true),
	})
	return pullRequest, err
}

// MarkReadyForReview makes the draft pull request with the GraphQL node id ready for review.
func (r Repository) MarkReadyForReview(ctx context.Context, id string) error {
	const query = `mutation($id: ID!) { markPullRequestReadyForReview(input: { pullRequestId: $id }) { clientMutationId } }`
	var data any
	return r.graphql(ctx, query, map[string]any{"id": id}, &data)
}

// CreateCheckRun adds the check run name with status to the commit sha, and gives its id.
func (r Repository) CreateCheckRun(ctx context.Context, name, sha, status string) (int64, error) {
	checkRun, _, err := r.Client.Checks.CreateCheckRun(ctx, r.Owner(), r.Name(), gh.CreateCheckRunOptions{Name: name, HeadSHA: sha, Status: &status})
	return checkRun.GetID(), err
}

// CreateFailedCheckRun adds the completed check run name with the conclusion failure, title and summary to the
// commit sha.
func (r Repository) CreateFailedCheckRun(ctx context.Context, name, sha, title, summary string) error {
	_, _, err := r.Client.Checks.CreateCheckRun(ctx, r.Owner(), r.Name(), gh.CreateCheckRunOptions{
		Name:       name,
		HeadSHA:    sha,
		Status:     new("completed"),
		Conclusion: new("failure"),
		Output:     &gh.CheckRunOutput{Title: &title, Summary: &summary},
	})
	return err
}

// CompleteCheckRun completes the check run id of name with conclusion, for example success.
func (r Repository) CompleteCheckRun(ctx context.Context, id int64, name, conclusion string) error {
	_, _, err := r.Client.Checks.UpdateCheckRun(ctx, r.Owner(), r.Name(), id, gh.UpdateCheckRunOptions{Name: name, Status: new("completed"), Conclusion: &conclusion})
	return err
}

// CheckRuns gives the check runs of the commit sha.
func (r Repository) CheckRuns(ctx context.Context, sha string) ([]*gh.CheckRun, error) {
	return all(r.Client.Checks.ListCheckRunsForRefIter(ctx, r.Owner(), r.Name(), sha, &gh.ListCheckRunsOptions{ListOptions: gh.ListOptions{PerPage: 100}}))
}

// CheckRunAnnotations gives the annotations of the check run id.
func (r Repository) CheckRunAnnotations(ctx context.Context, id int64) ([]*gh.CheckRunAnnotation, error) {
	return all(r.Client.Checks.ListCheckRunAnnotationsIter(ctx, r.Owner(), r.Name(), id, &gh.ListOptions{PerPage: 100}))
}

// JobLog gives the log of the job of GitHub Actions with the id. The job of a check run of GitHub Actions has the id
// of the check run. The log comes from a link of GitHub that needs no token, so the token does not go to that host.
func (r Repository) JobLog(ctx context.Context, id int64) (string, error) {
	link, _, err := r.Client.Actions.GetWorkflowJobLogs(ctx, r.Owner(), r.Name(), id, 1)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), http.NoBody)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the job log of %d: %s", id, response.Status)
	}
	text, err := io.ReadAll(response.Body)
	return string(text), err
}

// UserID gives the id of the account login.
func (r Repository) UserID(ctx context.Context, login string) (int64, error) {
	user, _, err := r.Client.Users.Get(ctx, login)
	return user.GetID(), err
}
