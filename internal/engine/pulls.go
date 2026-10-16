package engine

import (
	"context"
	"slices"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// pullKey is a pull request of a repository.
type pullKey struct {
	repository string
	number     int64
}

// pullState is what the poll keeps of a pull request for the Judge. The Judge needs it in a later poll than the poll
// that read it, for example after review_quiet_period or while a Worker works on the task.
type pullState struct {
	// PullRequestReviews are the reviews and the review threads of the last GraphQL read.
	github.PullRequestReviews
	// conversation holds the conversation comments that the poll read, by id.
	conversation map[int64]*gh.IssueComment
	// read tells that the Judge read the whole pull request after the start of the server. The server does not keep
	// the comments of an earlier run.
	read bool
	// pullRequest is the pull request of the last read of checkTask, and baseHead is the head commit of the default
	// branch at that read. They are nil and "" before the first read.
	pullRequest *gh.PullRequest
	baseHead    string
	// changed tells that the issue list of the poll had the pull request after the last read.
	changed bool
	// updatedHead is the head for which the break wait updated the branch, and updatedAt is the time of the update.
	// GitHub adds the merge commit after its answer.
	updatedHead string
	updatedAt   time.Time
}

// pull gives the state of the pull request number, and adds it when it is missing.
func (e *Engine) pull(repository github.Repository, number int64) *pullState {
	key := pullKey{repository.FullName, number}
	if e.pulls[key] == nil {
		e.pulls[key] = &pullState{conversation: map[int64]*gh.IssueComment{}}
	}
	return e.pulls[key]
}

// pullRequest gives the pull request number. It reads the pull request from GitHub when the poll has no earlier read, the
// issue list showed a change, a Worker pushed to the pull request since the earlier read, the default branch moved since the earlier read, or GitHub had not yet calculated
// mergeable. Else it gives the pull request of the earlier read. A push to the default branch changes mergeable and
// mergeable_state with no change of the update time of the pull request.
func (e *Engine) pullRequest(ctx context.Context, repository github.Repository, number int64, baseHead string) (*gh.PullRequest, error) {
	state := e.pull(repository, number)
	if e.takePushed(repository, number) {
		state.changed = true
	}
	if state.pullRequest != nil && !state.changed && state.baseHead == baseHead && state.pullRequest.Mergeable != nil {
		return state.pullRequest, nil
	}
	pullRequest, err := repository.PullRequest(ctx, number)
	if err != nil {
		return nil, err
	}
	state.pullRequest, state.baseHead, state.changed = pullRequest, baseHead, false
	return pullRequest, nil
}

// markPushed tells the poll that a Worker pushed to the pull request number, so that its next read goes to GitHub. The
// issue list of GitHub can show the push later than the push ends.
func (e *Engine) markPushed(repository github.Repository, number int64) {
	e.pushedMu.Lock()
	defer e.pushedMu.Unlock()
	e.pushed[pullKey{repository.FullName, number}] = true
}

// takePushed tells if a Worker pushed to the pull request number since the last call.
func (e *Engine) takePushed(repository github.Repository, number int64) bool {
	e.pushedMu.Lock()
	defer e.pushedMu.Unlock()
	key := pullKey{repository.FullName, number}
	pushed := e.pushed[key]
	delete(e.pushed, key)
	return pushed
}

// markChangedPullRequests marks each pull request of issues, so that the next read of it goes to GitHub.
func (e *Engine) markChangedPullRequests(repository github.Repository, issues []*gh.Issue) {
	for _, issue := range issues {
		if issue.IsPullRequest() {
			e.pull(repository, int64(issue.GetNumber())).changed = true
		}
	}
}

// readPullRequests reads the reviews and the review threads of the pull requests numbers with one call, and keeps them.
func (e *Engine) readPullRequests(ctx context.Context, repository github.Repository, numbers []int64) error {
	if len(numbers) == 0 {
		return nil
	}
	found, err := repository.PullRequestReviews(ctx, numbers)
	if err != nil {
		return err
	}
	for number, reviews := range found {
		e.pull(repository, number).PullRequestReviews = reviews
	}
	return nil
}

// readPullRequestInFull reads the conversation comments, the reviews and the review threads of the pull request number
// with a call for each, and replaces what the poll kept of them.
func (e *Engine) readPullRequestInFull(ctx context.Context, repository github.Repository, number int64) error {
	comments, err := repository.Comments(ctx, number)
	if err != nil {
		return err
	}
	if err := e.readPullRequests(ctx, repository, []int64{number}); err != nil {
		return err
	}
	state := e.pull(repository, number)
	state.conversation = map[int64]*gh.IssueComment{}
	for _, comment := range comments {
		state.conversation[comment.GetID()] = comment
	}
	state.read = true
	return nil
}

// openPullRequests gives the numbers of the open pull requests of issues.
func openPullRequests(issues []*gh.Issue) []int64 {
	var numbers []int64
	for _, issue := range issues {
		if issue.IsPullRequest() && issue.GetState() == "open" {
			numbers = append(numbers, int64(issue.GetNumber()))
		}
	}
	return numbers
}

// forgetPulls removes the state of each pull request of the repository that has no task in tasks.
func (e *Engine) forgetPulls(repository github.Repository, tasks []store.Task) {
	for key := range e.pulls {
		if key.repository == repository.FullName && !slices.ContainsFunc(tasks, func(task store.Task) bool { return task.PullRequest.Int64 == key.number }) {
			delete(e.pulls, key)
		}
	}
}
