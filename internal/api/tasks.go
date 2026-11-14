package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// TaskLine is a task of a Workstream.
type TaskLine struct {
	// Number is the number of the task issue
	Number int64 `gork:"number"`
	// Title is the title of the task issue
	Title string `gork:"title"`
	// State is the Mobius label of the issue with no "mobius:", or open, or closed for a closed issue. A task that waits for a slot shows "queued". A task that waits for CI shows "waits for CI". A task that waits for the Lead shows "waits for Lead". A task whose Lead event is held until the Owner answers shows "waits for Owner". A task that waits for start_implementer shows "waits for start_implementer". A task that waits for an action of the Owner shows "waits for Owner action".
	State string `gork:"state"`
	// URL is the GitHub URL of the task issue
	URL string `gork:"url"`
	// Repository is the full name of the repository of the task issue
	Repository string `gork:"repository"`
	// Depth is 0 for a sub-issue of the Workstream issue, and one more for each level below
	Depth int64 `gork:"depth"`
	// OtherRepository is true for a task in another repository than the Workstream
	OtherRepository bool `gork:"otherRepository"`
	// BlockedBy are the open blockers of the task
	BlockedBy []Blocker `gork:"blockedBy"`
	// Implementer is the own Implementer setting of the task, or null when the task has none
	Implementer *IssueSetting `gork:"implementer"`
}

// Blocker is an open blocker of a task.
type Blocker struct {
	// Number is the number of the blocker issue
	Number int64 `gork:"number"`
	// Repository is the full name of a blocker in another repository, or null
	Repository *string `gork:"repository"`
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
		task := TaskLine{Number: line.Number, Title: line.Title, State: line.State, URL: line.URL, Repository: line.Repository, Depth: line.Depth, OtherRepository: line.OtherRepository, BlockedBy: make([]Blocker, 0, len(line.BlockedBy))}
		if line.Implementer != nil {
			task.Implementer = &IssueSetting{Harness: line.Implementer.Harness, Model: line.Implementer.Model, Effort: line.Implementer.Effort}
		}
		for _, blocker := range line.BlockedBy {
			found := Blocker{Number: blocker.Number}
			if blocker.Repository != "" {
				found.Repository = &blocker.Repository
			}
			if blocker.WorkstreamTitle != "" {
				found.WorkstreamTitle = &blocker.WorkstreamTitle
			}
			task.BlockedBy = append(task.BlockedBy, found)
		}
		tasks = append(tasks, task)
	}
	return &ListTasksResponse{Body: Envelope[[]TaskLine]{Data: tasks}}, nil
}

// TaskDetail is the detail of a task of a Workstream.
type TaskDetail struct {
	// Body is the issue body from the local copy
	Body string `gork:"body"`
	// PullRequest is the number of the pull request of the live task, or null
	PullRequest *int64 `gork:"pullRequest"`
	// PullRequestURL is the GitHub URL of that pull request, or null
	PullRequestURL *string `gork:"pullRequestUrl"`
}

// GetTaskRequest is the request of GetTask.
type GetTaskRequest struct {
	Path struct {
		// Owner is the owner of the Workstream repository
		Owner string `gork:"owner"`
		// Name is the name of the Workstream repository
		Name string `gork:"name"`
		// Number is the number of the Workstream issue
		Number int64 `gork:"number"`
		// TaskOwner is the owner of the repository of the task issue
		TaskOwner string `gork:"taskOwner"`
		// TaskName is the name of the repository of the task issue
		TaskName string `gork:"taskName"`
		// Issue is the number of the task issue
		Issue int64 `gork:"issue"`
	}
}

// GetTaskResponse is the response of GetTask.
type GetTaskResponse struct {
	Body Envelope[TaskDetail]
}

// GetTask returns the body and the pull request of one task of a Workstream, from the local copy of GitHub.
func (h *handlers) GetTask(ctx context.Context, req GetTaskRequest) (*GetTaskResponse, error) {
	detail, err := h.engine.Task(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number, req.Path.TaskOwner+"/"+req.Path.TaskName, req.Path.Issue)
	if engine.Refused(err) {
		return nil, api.NewHTTPError(http.StatusNotFound, err.Error())
	}
	if err != nil {
		return nil, err
	}
	task := TaskDetail{Body: detail.Body}
	if detail.PullRequest != 0 {
		task.PullRequest = &detail.PullRequest
		task.PullRequestURL = &detail.PullRequestURL
	}
	return &GetTaskResponse{Body: Envelope[TaskDetail]{Data: task}}, nil
}

// NeedsHuman is an open task issue with mobius:needs-human or mobius:question.
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
	// Stopped is true when the issue has mobius:needs-human: the task stopped
	Stopped bool `gork:"stopped"`
	// Question is true when the issue has mobius:question: the Lead waits for an answer
	Question bool `gork:"question"`
	// PullRequest is the pull request of the live task of the issue, or null
	PullRequest *int64 `gork:"pullRequest"`
	// PullRequestURL is the GitHub URL of the pull request, or null
	PullRequestURL *string `gork:"pullRequestUrl"`
	// Reason is the text of the newest stop of the task when Stopped is true, or an empty string
	Reason string `gork:"reason"`
}

// ListNeedsHumanRequest is the request of ListNeedsHuman.
type ListNeedsHumanRequest struct{}

// ListNeedsHumanResponse is the response of ListNeedsHuman.
type ListNeedsHumanResponse struct {
	Body Envelope[[]NeedsHuman]
}

// ListNeedsHuman returns the open issues of trusted authors with mobius:needs-human or mobius:question in the trees of
// all Workstreams, from the local copy of GitHub, by repository, Workstream and number.
func (h *handlers) ListNeedsHuman(ctx context.Context, _ ListNeedsHumanRequest) (*ListNeedsHumanResponse, error) {
	found, err := h.engine.NeedsHuman(ctx)
	if err != nil {
		return nil, err
	}
	issues := make([]NeedsHuman, 0, len(found))
	for _, issue := range found {
		row := NeedsHuman{Repository: issue.Repository, Workstream: issue.Workstream, Number: issue.Number, Title: issue.Title, URL: issue.URL, Stopped: issue.Stopped, Question: issue.Question, Reason: issue.Reason}
		if issue.PullRequest != 0 {
			row.PullRequest = &issue.PullRequest
			row.PullRequestURL = &issue.PullRequestURL
		}
		issues = append(issues, row)
	}
	return &ListNeedsHumanResponse{Body: Envelope[[]NeedsHuman]{Data: issues}}, nil
}

// BoardTask is a task on the Tasks board.
type BoardTask struct {
	// Repository is the full name of the repository of the task issue
	Repository string `gork:"repository"`
	// Number is the number of the task issue
	Number int64 `gork:"number"`
	// Title is the title of the task issue
	Title string `gork:"title"`
	// URL is the GitHub URL of the task issue
	URL string `gork:"url"`
	// WorkstreamRepository is the full name of the repository of the Workstream issue
	WorkstreamRepository string `gork:"workstreamRepository"`
	// Workstream is the number of the Workstream issue
	Workstream int64 `gork:"workstream"`
	// WorkstreamTitle is the title of the Workstream
	WorkstreamTitle string `gork:"workstreamTitle"`
	// State is the state of the task
	State string `gork:"state" validate:"oneof=dispatched queued working reviewed needs_human ready_for_review stopped ended checks approval asked answered owner_action"`
	// StateAt is the time of the last state change
	StateAt time.Time `gork:"stateAt"`
	// PullRequest is the pull request of the task, or null
	PullRequest *int64 `gork:"pullRequest"`
	// PullRequestURL is the GitHub URL of the pull request, or null
	PullRequestURL *string `gork:"pullRequestUrl"`
	// FixRounds is the number of fix rounds of the task
	FixRounds int64 `gork:"fixRounds"`
	// ReviewRounds is the number of review rounds of the task
	ReviewRounds int64 `gork:"reviewRounds"`
	// Merging is true when the task is in ready_for_review and the Owner started its merge
	Merging bool `gork:"merging"`
	// MergeRefusal is the reason that GitHub gave for the refused merge when the task is in ready_for_review, or an empty string
	MergeRefusal string `gork:"mergeRefusal"`
	// MergeQueuePlace is the place of the task in the merge queue of its repository, from 1, or null when the task is not in the queue
	MergeQueuePlace *int64 `gork:"mergeQueuePlace"`
	// MergeQueueSize is the number of tasks in the merge queue of that repository, or 0 when mergeQueuePlace is null
	MergeQueueSize int64 `gork:"mergeQueueSize"`
}

// ListBoardTasksRequest is the request of ListBoardTasks.
type ListBoardTasksRequest struct{}

// ListBoardTasksResponse is the response of ListBoardTasks.
type ListBoardTasksResponse struct {
	Body Envelope[[]BoardTask]
}

// ListBoardTasks returns the tasks of all Workstreams that have not ended, and the tasks that ended in the last 24
// hours, newest state change first, from the local copy of GitHub.
func (h *handlers) ListBoardTasks(ctx context.Context, _ ListBoardTasksRequest) (*ListBoardTasksResponse, error) {
	found, err := h.engine.BoardTasks(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]BoardTask, 0, len(found))
	for _, task := range found {
		row := BoardTask{Repository: task.Repository, Number: task.Number, Title: task.Title, URL: task.URL, WorkstreamRepository: task.WorkstreamRepository, Workstream: task.Workstream, WorkstreamTitle: task.WorkstreamTitle, State: task.State, StateAt: task.StateAt, FixRounds: task.FixRounds, ReviewRounds: task.ReviewRounds, Merging: task.Merging, MergeRefusal: task.MergeRefusal, MergeQueuePlace: task.MergeQueuePlace, MergeQueueSize: task.MergeQueueSize}
		if task.PullRequest != 0 {
			row.PullRequest = &task.PullRequest
			row.PullRequestURL = &task.PullRequestURL
		}
		tasks = append(tasks, row)
	}
	return &ListBoardTasksResponse{Body: Envelope[[]BoardTask]{Data: tasks}}, nil
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

// ResumeIssue removes mobius:needs-human from an issue and from the pull request of its task, so Mobius continues the
// task. Mobius changes the labels with the user token of the Owner, so the Owner must authorize the Mobius App first. It returns 409 with
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

// StopIssueRequest is the request of StopIssue.
type StopIssueRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the issue
		Number int64 `gork:"number"`
	}
}

// StopIssue stops the live task of an issue and its agents. An issue with mobius:ready and no live task gets
// mobius:triage instead, so Mobius does not start it. Mobius changes the labels of a ready issue with the user token of
// the Owner, so the Owner must authorize the Mobius App first. It returns 409 with the steps when the Owner did not, and
// with the reason when the issue has no task to stop.
func (h *handlers) StopIssue(ctx context.Context, req StopIssueRequest) error {
	err := h.engine.StopIssue(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}
