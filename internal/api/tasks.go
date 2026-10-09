package api

import (
	"context"
	"net/http"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// TaskLine is a task of a Workstream.
type TaskLine struct {
	// Number is the number of the task issue
	Number int64 `gork:"number"`
	// Title is the title of the task issue
	Title string `gork:"title"`
	// State is the Mobius label of the issue with no "mobius:", or open, or closed for a closed issue. A task that waits for a slot shows "queued". A task that waits for CI shows "waits for CI". A task that waits for the Lead shows "waits for Lead". A task that waits for start_implementer shows "waits for start_implementer".
	State string `gork:"state"`
	// URL is the GitHub URL of the task issue
	URL string `gork:"url"`
	// Depth is 0 for a sub-issue of the Workstream issue, and one more for each level below
	Depth int64 `gork:"depth"`
	// OtherRepository is true for a task in another repository than the Workstream
	OtherRepository bool `gork:"otherRepository"`
	// BlockedBy are the open blockers of the task
	BlockedBy []Blocker `gork:"blockedBy"`
}

// Blocker is an open blocker of a task.
type Blocker struct {
	// Number is the number of the blocker issue
	Number int64 `gork:"number"`
	// WorkstreamTitle is the title of the Workstream of a blocker in another Workstream, or null
	WorkstreamTitle *string `gork:"workstreamTitle"`
}

// ListTasksRequest is the request of ListTasks.
type ListTasksRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the Workstream issue
		Number int64 `gork:"number"`
	}
}

// ListTasksResponse is the response of ListTasks.
type ListTasksResponse struct {
	Body Envelope[[]TaskLine]
}

// ListTasks returns the open and closed tasks of trusted authors in the tree of a Workstream, from the local copy of GitHub. A
// nested task follows its parent. The copy can be one poll interval old.
func (h *handlers) ListTasks(ctx context.Context, req ListTasksRequest) (*ListTasksResponse, error) {
	lines, err := h.engine.Tasks(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number)
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusNotFound, err.Error())
	}
	if err != nil {
		return nil, err
	}
	tasks := make([]TaskLine, 0, len(lines))
	for _, line := range lines {
		task := TaskLine{Number: line.Number, Title: line.Title, State: line.State, URL: line.URL, Depth: line.Depth, OtherRepository: line.OtherRepository, BlockedBy: make([]Blocker, 0, len(line.BlockedBy))}
		for _, blocker := range line.BlockedBy {
			found := Blocker{Number: blocker.Number}
			if blocker.WorkstreamTitle != "" {
				found.WorkstreamTitle = &blocker.WorkstreamTitle
			}
			task.BlockedBy = append(task.BlockedBy, found)
		}
		tasks = append(tasks, task)
	}
	return &ListTasksResponse{Body: Envelope[[]TaskLine]{Data: tasks}}, nil
}

// NeedsHuman is an open task issue with mobius:needs-human.
type NeedsHuman struct {
	// Repository is the repository as "owner/name"
	Repository string `gork:"repository"`
	// Workstream is the number of the Workstream issue
	Workstream int64 `gork:"workstream"`
	// Number is the number of the issue
	Number int64 `gork:"number"`
	// Title is the title of the issue
	Title string `gork:"title"`
	// URL is the GitHub URL of the issue
	URL string `gork:"url"`
	// PullRequest is the pull request of the live task of the issue, or null
	PullRequest *int64 `gork:"pullRequest"`
	// PullRequestURL is the GitHub URL of the pull request, or null
	PullRequestURL *string `gork:"pullRequestUrl"`
}

// ListNeedsHumanRequest is the request of ListNeedsHuman.
type ListNeedsHumanRequest struct{}

// ListNeedsHumanResponse is the response of ListNeedsHuman.
type ListNeedsHumanResponse struct {
	Body Envelope[[]NeedsHuman]
}

// ListNeedsHuman returns the open issues of trusted authors with mobius:needs-human in the trees of all Workstreams,
// from the local copy of GitHub, by repository, Workstream and number.
func (h *handlers) ListNeedsHuman(ctx context.Context, _ ListNeedsHumanRequest) (*ListNeedsHumanResponse, error) {
	found, err := h.engine.NeedsHuman(ctx)
	if err != nil {
		return nil, err
	}
	issues := make([]NeedsHuman, 0, len(found))
	for _, issue := range found {
		row := NeedsHuman{Repository: issue.Repository, Workstream: issue.Workstream, Number: issue.Number, Title: issue.Title, URL: issue.URL}
		if issue.PullRequest != 0 {
			row.PullRequest = &issue.PullRequest
			row.PullRequestURL = &issue.PullRequestURL
		}
		issues = append(issues, row)
	}
	return &ListNeedsHumanResponse{Body: Envelope[[]NeedsHuman]{Data: issues}}, nil
}

// ResumeIssueRequest is the request of ResumeIssue.
type ResumeIssueRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the issue
		Number int64 `gork:"number"`
	}
}

// ResumeIssue replaces mobius:needs-human of an issue with mobius:ready, so Mobius continues the task. Mobius changes
// the labels with the user token of the Owner, so the Owner must authorize the Mobius App first. It returns 409 with
// the steps when the Owner did not.
func (h *handlers) ResumeIssue(ctx context.Context, req ResumeIssueRequest) error {
	err := h.engine.ResumeIssue(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}

// StartIssueRequest is the request of StartIssue.
type StartIssueRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the issue
		Number int64 `gork:"number"`
	}
}

// StartIssue adds mobius:ready to an issue, so Mobius starts the task. Mobius adds the label with the user token of
// the Owner, so the Owner must authorize the Mobius App first. It returns 409 with the steps when the Owner did not.
func (h *handlers) StartIssue(ctx context.Context, req StartIssueRequest) error {
	err := h.engine.StartIssue(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}
