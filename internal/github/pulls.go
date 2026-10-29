package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	gh "github.com/google/go-github/v92/github"
)

// PullRequest gives the pull request number.
func (r Repository) PullRequest(ctx context.Context, number int64) (*gh.PullRequest, error) {
	pullRequest, _, err := r.Client.PullRequests.Get(ctx, r.Owner(), r.Name(), int(number))
	return pullRequest, err
}

// BranchHead gives the sha of the head commit of the branch.
func (r Repository) BranchHead(ctx context.Context, branch string) (string, error) {
	ref, _, err := r.Client.Git.GetRef(ctx, r.Owner(), r.Name(), "heads/"+branch)
	if err != nil {
		return "", err
	}
	return ref.GetObject().GetSHA(), nil
}

// DeleteBranch deletes the branch.
func (r Repository) DeleteBranch(ctx context.Context, branch string) error {
	_, err := r.Client.Git.DeleteRef(ctx, r.Owner(), r.Name(), "heads/"+branch)
	return err
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

// SetPullRequestBody replaces the description of the pull request number.
func (r Repository) SetPullRequestBody(ctx context.Context, number int64, body string) error {
	_, _, err := r.Client.PullRequests.Edit(ctx, r.Owner(), r.Name(), int(number), &gh.PullRequest{Body: &body})
	return err
}

// MarkReadyForReview makes the draft pull request with the GraphQL node id ready for review.
func (r Repository) MarkReadyForReview(ctx context.Context, id string) error {
	const query = `mutation($id: ID!) { markPullRequestReadyForReview(input: { pullRequestId: $id }) { clientMutationId } }`
	var data any
	return r.graphql(ctx, query, map[string]any{"id": id}, &data)
}

// ConvertToDraft makes the pull request with the GraphQL node id a draft.
func (r Repository) ConvertToDraft(ctx context.Context, id string) error {
	const query = `mutation($id: ID!) { convertPullRequestToDraft(input: { pullRequestId: $id }) { clientMutationId } }`
	var data any
	return r.graphql(ctx, query, map[string]any{"id": id}, &data)
}

// InlineComment is a comment of a new review on a line of a file.
type InlineComment struct {
	// Path is relative to the root of the repository.
	Path string `json:"path"`
	// Line is a line of the new version of the file. It must be in the diff.
	Line int    `json:"line"`
	Body string `json:"body"`
	// FollowUp marks a finding outside the scope of the task. SubmitReview does not send it to GitHub.
	FollowUp bool `json:"follow_up"`
}

// SubmitReview posts a review of the commit of the pull request number with body and the inline comments. The
// review approves nothing and requests no change.
func (r Repository) SubmitReview(ctx context.Context, number int64, commit, body string, comments []InlineComment) error {
	drafts := make([]*gh.DraftReviewComment, 0, len(comments))
	for _, comment := range comments {
		drafts = append(drafts, &gh.DraftReviewComment{Path: &comment.Path, Line: &comment.Line, Body: &comment.Body})
	}
	_, _, err := r.Client.PullRequests.CreateReview(ctx, r.Owner(), r.Name(), int(number), &gh.PullRequestReviewRequest{
		CommitID: &commit,
		Body:     &body,
		Event:    new("COMMENT"),
		Comments: drafts,
	})
	return err
}

// Approve posts a review that approves the commit of the pull request number.
func (r Repository) Approve(ctx context.Context, number int64, commit string) error {
	_, _, err := r.Client.PullRequests.CreateReview(ctx, r.Owner(), r.Name(), int(number), &gh.PullRequestReviewRequest{
		CommitID: &commit,
		Event:    new("APPROVE"),
	})
	return err
}

// CreateCheckRun adds the check run name with status to the commit sha. A completed check run has the conclusion. A
// check run with a title has the title and the summary.
func (r Repository) CreateCheckRun(ctx context.Context, name, sha, status, conclusion, title, summary string) error {
	options := gh.CreateCheckRunOptions{Name: name, HeadSHA: sha, Status: &status}
	if conclusion != "" {
		options.Conclusion = &conclusion
	}
	if title != "" {
		options.Output = &gh.CheckRunOutput{Title: &title, Summary: &summary}
	}
	_, _, err := r.Client.Checks.CreateCheckRun(ctx, r.Owner(), r.Name(), options)
	return err
}

// UpdateCheckRun sets the status of the check run id of name. A completed check run has the conclusion. A check run
// with a title gets the title and the summary.
func (r Repository) UpdateCheckRun(ctx context.Context, id int64, name, status, conclusion, title, summary string) error {
	options := gh.UpdateCheckRunOptions{Name: name, Status: &status}
	if conclusion != "" {
		options.Conclusion = &conclusion
	}
	if title != "" {
		options.Output = &gh.CheckRunOutput{Title: &title, Summary: &summary}
	}
	_, _, err := r.Client.Checks.UpdateCheckRun(ctx, r.Owner(), r.Name(), id, options)
	return err
}

// CheckRuns gives the check runs of the commit sha.
func (r Repository) CheckRuns(ctx context.Context, sha string) ([]*gh.CheckRun, error) {
	return all(r.Client.Checks.ListCheckRunsForRefIter(ctx, r.Owner(), r.Name(), sha, &gh.ListCheckRunsOptions{ListOptions: gh.ListOptions{PerPage: 100}}))
}

// WorkflowRuns gives the workflow runs of GitHub Actions on the commit sha.
func (r Repository) WorkflowRuns(ctx context.Context, sha string) ([]*gh.WorkflowRun, error) {
	return all(r.Client.Actions.ListRepositoryWorkflowRunsIter(ctx, r.Owner(), r.Name(), &gh.ListWorkflowRunsOptions{HeadSHA: sha, ListOptions: gh.ListOptions{PerPage: 100}}))
}

// BranchWorkflowRuns gives the newest workflow runs of GitHub Actions on the branch. It reads one page, because the
// runs of a branch are many.
func (r Repository) BranchWorkflowRuns(ctx context.Context, branch string) ([]*gh.WorkflowRun, error) {
	runs, _, err := r.Client.Actions.ListRepositoryWorkflowRuns(ctx, r.Owner(), r.Name(), &gh.ListWorkflowRunsOptions{Branch: branch, ListOptions: gh.ListOptions{PerPage: 100}})
	if err != nil {
		return nil, err
	}
	return runs.WorkflowRuns, nil
}

// RerunFailedJobs runs the failed jobs of the workflow run id again. It needs the permission actions: write.
func (r Repository) RerunFailedJobs(ctx context.Context, id int64) error {
	_, err := r.Client.Actions.RerunFailedJobsByID(ctx, r.Owner(), r.Name(), id)
	return err
}

// CommitsBehind gives the number of commits of base that the commit head does not contain.
func (r Repository) CommitsBehind(ctx context.Context, base, head string) (int, error) {
	comparison, _, err := r.Client.Repositories.CompareCommits(ctx, r.Owner(), r.Name(), base, head, &gh.ListOptions{PerPage: 1})
	return comparison.GetBehindBy(), err
}

// Compare gives the status of the comparison of head with base, the commits of head that base does not contain, and the
// files that head changes since its merge base with base. It reads all pages of the commits and one page of the files.
func (r Repository) Compare(ctx context.Context, base, head string) (*gh.CommitsComparison, error) {
	comparison, response, err := r.Client.Repositories.CompareCommits(ctx, r.Owner(), r.Name(), base, head, &gh.ListOptions{PerPage: 100})
	if err != nil {
		return nil, err
	}
	for response.NextPage != 0 {
		next, nextResponse, err := r.Client.Repositories.CompareCommits(ctx, r.Owner(), r.Name(), base, head, &gh.ListOptions{PerPage: 100, Page: response.NextPage})
		if err != nil {
			return nil, err
		}
		comparison.Commits = append(comparison.Commits, next.Commits...)
		response = nextResponse
	}
	return comparison, nil
}

// CommitsSince gives the newest commits of the branch with a commit date at or after since. It reads one page, because
// the commits of a branch are many.
func (r Repository) CommitsSince(ctx context.Context, branch string, since time.Time) ([]*gh.RepositoryCommit, error) {
	commits, _, err := r.Client.Repositories.ListCommits(ctx, r.Owner(), r.Name(), &gh.CommitsListOptions{SHA: branch, Since: since, ListOptions: gh.ListOptions{PerPage: 100}})
	return commits, err
}

// CommitFiles gives the names of the files that the commit sha changes. It reads one page.
func (r Repository) CommitFiles(ctx context.Context, sha string) ([]string, error) {
	commit, _, err := r.Client.Repositories.GetCommit(ctx, r.Owner(), r.Name(), sha, &gh.ListOptions{PerPage: 300})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(commit.Files))
	for _, file := range commit.Files {
		names = append(names, file.GetFilename())
	}
	return names, nil
}

// PullRequestFiles gives the names of the files that the pull request number changes.
func (r Repository) PullRequestFiles(ctx context.Context, number int64) ([]string, error) {
	files, err := all(r.Client.PullRequests.ListFilesIter(ctx, r.Owner(), r.Name(), int(number), &gh.ListOptions{PerPage: 100}))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.GetFilename())
	}
	return names, nil
}

// RunAttemptJobs gives the jobs of the attempt of the workflow run id. It reads one page, because a run has few jobs.
func (r Repository) RunAttemptJobs(ctx context.Context, id, attempt int64) ([]*gh.WorkflowJob, error) {
	jobs, _, err := r.Client.Actions.ListWorkflowJobsAttempt(ctx, r.Owner(), r.Name(), id, attempt, &gh.ListOptions{PerPage: 100})
	if err != nil {
		return nil, err
	}
	return jobs.Jobs, nil
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
	response, err := (&http.Client{Timeout: Timeout}).Do(request)
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

// MergePullRequest squash merges the pull request number if its head is sha. It tells if GitHub merged the pull
// request. It gives the reason when GitHub refuses the merge, for example for a rule of the base branch. A head that is
// not sha is no refusal and no merge, because the next poll reads the new head.
func (r Repository) MergePullRequest(ctx context.Context, number int64, sha string) (bool, string, error) {
	_, _, err := r.Client.PullRequests.Merge(ctx, r.Owner(), r.Name(), int(number), "", &gh.PullRequestOptions{MergeMethod: "squash", SHA: sha})
	var response *gh.ErrorResponse
	if errors.As(err, &response) && response.Response.StatusCode == http.StatusConflict {
		return false, "", nil
	}
	if errors.As(err, &response) && (response.Response.StatusCode == http.StatusMethodNotAllowed || response.Response.StatusCode == http.StatusUnprocessableEntity) {
		return false, response.Message, nil
	}
	return err == nil, "", err
}

// UpdatePullRequestBranch merges the base branch of the pull request number into its head branch, if the head is sha.
// GitHub answers 202 Accepted when it starts the update, which is a success.
func (r Repository) UpdatePullRequestBranch(ctx context.Context, number int64, sha string) error {
	_, _, err := r.Client.PullRequests.UpdateBranch(ctx, r.Owner(), r.Name(), int(number), &gh.PullRequestBranchUpdateOptions{ExpectedHeadSHA: &sha})
	var accepted *gh.AcceptedError
	if errors.As(err, &accepted) {
		return nil
	}
	return err
}

// UserID gives the id of the account login.
func (r Repository) UserID(ctx context.Context, login string) (int64, error) {
	user, _, err := r.Client.Users.Get(ctx, login)
	return user.GetID(), err
}
