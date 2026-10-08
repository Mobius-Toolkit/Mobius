package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// checkTasks checks each live task of the repository, and adds the pull request of each task with work for an agent
// to work. A task that fails to check keeps its entry of the last poll. It first reads the pull requests of the
// reviewed tasks with one call, because GitHub does not document that a resolve of a review thread changes the update
// time of the pull request.
func (e *Engine) checkTasks(ctx context.Context, repository github.Repository, work map[int64]Work) error {
	tasks, err := e.queries.ListLiveTasks(ctx, repository.FullName)
	if err != nil {
		return err
	}
	var reviewed []int64
	for _, task := range tasks {
		if task.State == "reviewed" && task.PullRequest.Valid {
			reviewed = append(reviewed, task.PullRequest.Int64)
		}
	}
	if err := e.readPullRequests(ctx, repository, reviewed); err != nil {
		return err
	}
	for _, task := range tasks {
		found, ok, err := e.checkTask(ctx, repository, task)
		if err != nil {
			log.Printf("check the task of %s#%d: %v", repository.FullName, task.Issue, err)
			found, ok = e.currentWork()[task.ID]
		}
		if ok {
			work[task.ID] = found
		}
	}
	e.forgetPulls(repository, tasks)
	return nil
}

// checkTask acts on the state of the issue and the pull request of the task, and gives the pull request when the task
// has work for an agent: the round that the task queues or runs, or the round that the check starts.
//   - A task whose issue is gone, or whose issue closed with no pull request, ends.
//   - A merged or closed pull request ends the task, and a merge closes the open issue (Mobius-rust#226).
//   - A removal of the label that the state needs by a person stops the task: mobius:review for a task in ready_for_review,
//     and mobius:working for a task in another state. A Judge that runs from ready_for_review needs mobius:review, and a
//     Judge that runs from needs_human needs no label.
//   - A pull request of a task in checks, approval or ready_for_review with a merge conflict, or behind its base, gets a
//     conflict round. A failed check run of another App on its head gets a fix round.
//   - Else a task in checks moves to approval when the CI of the head passed (onChecks).
//   - Else the new comments of the pull request of a task in checks, approval, ready_for_review, reviewed or needs_human
//     go to the Judge.
func (e *Engine) checkTask(ctx context.Context, repository github.Repository, task store.Task) (Work, bool, error) {
	issue, err := repository.Issue(ctx, task.Issue)
	if err != nil {
		return Work{}, false, err
	}
	if issue == nil || inOtherRepository(issue, repository.FullName) {
		return Work{}, false, e.endTask(ctx, repository, task)
	}
	var pullRequest *gh.PullRequest
	if task.PullRequest.Valid {
		if pullRequest, err = repository.PullRequest(ctx, task.PullRequest.Int64); err != nil {
			return Work{}, false, err
		}
	}
	if pullRequest != nil && pullRequest.GetState() == "closed" {
		return Work{}, false, e.endPullRequest(ctx, repository, task, issue, pullRequest)
	}
	if pullRequest == nil && issue.GetState() == "closed" {
		return Work{}, false, e.endTask(ctx, repository, task)
	}
	judgeOfHuman := task.Worker.String == JudgeRole && task.WorkerInput.String == "needs_human"
	needed := workingLabel
	if task.State == "ready_for_review" || task.Worker.String == JudgeRole && task.WorkerInput.String == "ready_for_review" {
		needed = reviewLabel
	}
	if task.State != "stopped" && task.State != "needs_human" && !judgeOfHuman && !hasLabel(issue, needed) {
		return Work{}, false, e.labelRemoved(ctx, repository, task, issue, needed)
	}
	if pullRequest == nil {
		return Work{}, false, nil
	}
	work := pullRequestWork(repository, pullRequest)
	conflict := pullRequest.Mergeable != nil && !pullRequest.GetMergeable() || behind(pullRequest)
	switch {
	case task.State == "queued" || task.State == "working":
		return work, slices.Contains([]string{checkRoundWorker, conflictRoundWorker}, task.Worker.String), nil
	case !slices.Contains([]string{"checks", "approval", "ready_for_review", "reviewed", "needs_human"}, task.State):
		return Work{}, false, nil
	case afterReview(task.State) && conflict:
		round, err := e.onConflict(ctx, repository, task, pullRequest)
		return work, round, err
	case afterReview(task.State):
		round, err := e.onFailure(ctx, repository, task, pullRequest)
		if err != nil || round {
			return work, round, err
		}
		if task.State == "checks" {
			if err := e.onChecks(ctx, repository, task, pullRequest); err != nil {
				return Work{}, false, err
			}
		}
	}
	waiting := false
	if task.State == "reviewed" {
		failed, err := e.unhandledFailure(ctx, repository, task, pullRequest)
		if err != nil {
			return Work{}, false, err
		}
		waiting = conflict || failed
	}
	judged, err := e.judge(ctx, repository, task, pullRequest, waiting)
	return work, judged, err
}

// afterReview tells if the state is one of the states of a task whose Reviewer has no open finding: the task waits for
// CI, for the Lead, or for the Owner.
func afterReview(state string) bool {
	return slices.Contains([]string{"checks", "approval", "ready_for_review"}, state)
}

// endPullRequest ends the task of the closed pull request, closes the open issue of a merged pull request as completed,
// and gives the Lead an end event.
func (e *Engine) endPullRequest(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, pullRequest *gh.PullRequest) error {
	if pullRequest.GetMerged() && issue.GetState() == "open" {
		if _, err := repository.CloseIssue(ctx, task.Issue, "completed"); err != nil {
			return err
		}
	}
	if err := e.endTask(ctx, repository, task); err != nil {
		return err
	}
	what := "closed with no merge"
	if pullRequest.GetMerged() {
		what = "merged"
	}
	text := fmt.Sprintf("%s end of #%d \"%s\": pull request #%d %s.", time.Now().UTC().Format(timeFormat), task.Issue, issue.GetTitle(), pullRequest.GetNumber(), what)
	return e.addLeadEvent(ctx, task.Repository, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "end", text)
}

// labelRemoved stops the task when a person removed label from its issue. The pull request and the branch
// stay, and the head of the pull request gets a failed Mobius check.
func (e *Engine) labelRemoved(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, label string) error {
	events, err := repository.IssueEvents(ctx, task.Issue)
	if err != nil {
		return err
	}
	var actor string
	for _, event := range slices.Backward(events) {
		if event.GetEvent() == "unlabeled" && event.GetLabel().GetName() == label {
			actor = event.GetActor().GetLogin()
			break
		}
	}
	if actor == "" || strings.EqualFold(actor, appLogin(repository.AppSlug)) {
		return nil
	}
	return e.stopTask(ctx, repository, task, issue, actor, "Stopped by a label removal.", fmt.Sprintf("Stopped \"%s\" after a removal of %s", issue.GetTitle(), label))
}

// stopTask stops the task and its Worker, and removes the Mobius state labels from its issue. The head
// of its pull request gets a failed Mobius check with summary, and the activity feed gets text. A task that is already
// stopped or ended stays as it is.
func (e *Engine) stopTask(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, actor, summary, text string) error {
	stopped, err := e.queries.StopTask(ctx, task.ID)
	if err != nil || stopped == 0 {
		return err
	}
	e.publish(Change{Workstreams: true})
	if err := e.stopWorkersOf(ctx, task); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, workingLabel); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, needsHumanLabel); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, reviewLabel); err != nil {
		return err
	}
	if task.PullRequest.Valid {
		pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
		if err != nil {
			return err
		}
		if err := repository.CreateFailedCheckRun(ctx, checkRunName, pullRequest.GetHead().GetSHA(), "Stopped", summary); err != nil {
			return err
		}
	}
	return e.addActivity(ctx, task.Repository, task.Workstream, issue, actor, text)
}

// lostAccess ends the live tasks of each repository that no App gives, and stops their Workers.
func (e *Engine) lostAccess(ctx context.Context, repositories []github.Repository) error {
	names, err := e.queries.ListLiveTaskRepositories(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		if slices.ContainsFunc(repositories, func(repository github.Repository) bool { return repository.FullName == name }) {
			continue
		}
		tasks, err := e.queries.ListLiveTasks(ctx, name)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if err := e.queries.EndTask(ctx, task.ID); err != nil {
				return err
			}
			e.publish(Change{Workstreams: true})
			if err := e.stopWorkersOf(ctx, task); err != nil {
				return err
			}
		}
	}
	return nil
}

// endTask ends the task, stops its Worker, and removes the Mobius state labels from its issue. A queued
// Worker of the task leaves the queue. The branch stays.
func (e *Engine) endTask(ctx context.Context, repository github.Repository, task store.Task) error {
	if err := e.queries.EndTask(ctx, task.ID); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	if err := e.stopWorkersOf(ctx, task); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, workingLabel); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, needsHumanLabel); err != nil {
		return err
	}
	return repository.RemoveLabel(ctx, task.Issue, reviewLabel)
}

// stopWorkersOf stops the Worker of the task, wakes the queue, and removes the worktree of the task.
func (e *Engine) stopWorkersOf(ctx context.Context, task store.Task) error {
	e.stop(task.ID)
	e.WakeQueue()
	worktree := runner.TaskDir(e.config.DataDir, task.Repository, task.Issue)
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	if _, err := os.Stat(worktree); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return runner.RemoveWorktree(ctx, e.config.DataDir, task.Repository, worktree)
}

// decline ends the task of the issue with the reason as a comment. An open pull request of the task gets a failed
// Mobius check and the reason as a comment, and then it closes (Mobius-rust#255). The branch stays.
func (e *Engine) decline(ctx context.Context, c caller, repository github.Repository, input declineInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	if empty(input.Reason) {
		return "", refuse("reason must not be empty.")
	}
	task, err := e.workstreamTask(ctx, repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	issue, err := existingIssue(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	if _, err := repository.AddComment(ctx, input.N, input.Reason); err != nil {
		return "", err
	}
	if task.PullRequest.Valid {
		pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
		if err != nil {
			return "", err
		}
		if pullRequest.GetState() == "open" {
			number := task.PullRequest.Int64
			if err := repository.CreateFailedCheckRun(ctx, checkRunName, pullRequest.GetHead().GetSHA(), "Declined", input.Reason); err != nil {
				return "", err
			}
			if _, err := repository.AddComment(ctx, number, input.Reason); err != nil {
				return "", err
			}
			if err := repository.ClosePullRequest(ctx, number); err != nil {
				return "", err
			}
		}
	}
	if err := e.endTask(ctx, repository, task); err != nil {
		return "", err
	}
	if err := e.addActivity(ctx, repository.FullName, c.workstream, issue, appLogin(repository.AppSlug), fmt.Sprintf("Declined \"%s\"", issue.GetTitle())); err != nil {
		return "", err
	}
	return fmt.Sprintf("Declined #%d.", input.N), nil
}
