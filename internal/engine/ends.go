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
// time of the pull request. It reads the pull requests of the tasks with an approval in the same call, because the
// approval needs the open threads and the dismissals.
func (e *Engine) checkTasks(ctx context.Context, repository github.Repository, work map[int64]Work) error {
	tasks, err := e.queries.ListLiveTasks(ctx, repository.FullName)
	if err != nil {
		return err
	}
	var reviewed []int64
	for _, task := range tasks {
		if (task.State == "reviewed" || awaitsMerge(task)) && task.PullRequest.Valid {
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
//   - A removal of mobius:needs-human by a trusted user continues a task in needs_human, and a removal by the Mobius App
//     continues it when Autopilot is on (continueByRemoval).
//   - A task in needs_human gets the labels that handToHuman sets, and a live task in another state loses
//     mobius:needs-human on its pull request (labelsOfNeedsHuman).
//   - A pull request of a task in checks, approval or ready_for_review with a merge conflict, or behind its base, gets a
//     conflict round. A failed check run of another App on its head gets a fix round.
//   - Else a task in checks moves to approval when the CI of the head passed (onChecks).
//   - Else a pull request with the approval of a trusted user gets a squash merge when its head agrees with the
//     conditions (mergeApproved), and the next poll ends the task.
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
	if continued, err := e.continueByRemoval(ctx, repository, task, issue, pullRequest); err != nil || continued {
		return Work{}, false, err
	}
	if err := e.labelsOfNeedsHuman(ctx, repository, task, issue, pullRequest, judgeOfHuman); err != nil {
		return Work{}, false, err
	}
	if pullRequest == nil {
		return Work{}, false, nil
	}
	if task, err = e.keepApproval(ctx, repository, task); err != nil {
		return Work{}, false, err
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
	if merged, err := e.mergeApproved(ctx, repository, task, pullRequest); err != nil || merged {
		return Work{}, false, err
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

// continueByRemoval resumes the task in needs_human when an actor removed mobius:needs-human from its issue or from its
// pull request, and the actor is a trusted user, or the Mobius App with Autopilot on. It reads the label events only
// when one of the two items lacks the label. A removal counts only when it is newer than the last addition of the label
// to the other item, because handToHuman adds the label to the issue and to the pull request after the move to
// needs_human, and resume removes it from both. An issue with mobius:ready waits for the dispatch.
func (e *Engine) continueByRemoval(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, pullRequest *gh.PullRequest) (bool, error) {
	issueHas := hasLabel(issue, needsHumanLabel)
	pullHas := pullRequest == nil || hasPullRequestLabel(pullRequest, needsHumanLabel)
	if task.State != "needs_human" || hasLabel(issue, readyLabel) || issueHas && pullHas {
		return false, nil
	}
	issueEvents, err := repository.IssueEvents(ctx, task.Issue)
	if err != nil {
		return false, err
	}
	var pullEvents []*gh.IssueEvent
	if pullRequest != nil {
		if pullEvents, err = repository.IssueEvents(ctx, task.PullRequest.Int64); err != nil {
			return false, err
		}
	}
	var actors []string
	if !issueHas {
		actors = append(actors, removalActor(issueEvents, pullEvents))
	}
	if !pullHas {
		actors = append(actors, removalActor(pullEvents, issueEvents))
	}
	for _, actor := range actors {
		if actor == "" || !e.TrustedAuthor(repository.AppSlug, actor) {
			continue
		}
		if strings.EqualFold(actor, appLogin(repository.AppSlug)) {
			on, err := e.workstreamAutopilot(ctx, repository, task.Workstream)
			if err != nil {
				return false, err
			}
			if !on {
				continue
			}
		}
		return true, e.resume(ctx, repository, issue, task, actor)
	}
	return false, nil
}

// removalActor gives the actor of the last removal of mobius:needs-human in events, or "" when events have no removal
// that is newer than the last addition of the label in other.
func removalActor(events, other []*gh.IssueEvent) string {
	_, removed := lastEvent(events, "unlabeled", needsHumanLabel)
	if removed == nil {
		return ""
	}
	if _, added := lastEvent(other, "labeled", needsHumanLabel); added != nil && !removed.GetCreatedAt().After(added.GetCreatedAt().Time) {
		return ""
	}
	return removed.GetActor().GetLogin()
}

// labelsOfNeedsHuman gives the issue and the pull request of a task in needs_human the labels that handToHuman sets, and
// writes nothing when they have them. A mobius:ready label waits for the dispatch, which resumes the task. The pull
// request of a task in another state, except a task with the Judge of a human task, loses mobius:needs-human.
func (e *Engine) labelsOfNeedsHuman(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, pullRequest *gh.PullRequest, judgeOfHuman bool) error {
	if task.State != "needs_human" {
		if pullRequest == nil || judgeOfHuman || !hasPullRequestLabel(pullRequest, needsHumanLabel) {
			return nil
		}
		return repository.RemoveLabel(ctx, task.PullRequest.Int64, needsHumanLabel)
	}
	if hasLabel(issue, readyLabel) {
		return nil
	}
	for _, label := range []string{workingLabel, reviewLabel} {
		if hasLabel(issue, label) {
			if err := repository.RemoveLabel(ctx, task.Issue, label); err != nil {
				return err
			}
		}
	}
	if !hasLabel(issue, needsHumanLabel) {
		if err := repository.AddLabel(ctx, task.Issue, needsHumanLabel); err != nil {
			return err
		}
	}
	if pullRequest == nil || hasPullRequestLabel(pullRequest, needsHumanLabel) {
		return nil
	}
	return repository.AddLabel(ctx, task.PullRequest.Int64, needsHumanLabel)
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
	if err := removeNeedsHuman(ctx, repository, task); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, questionLabel); err != nil {
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
	if err := removeNeedsHuman(ctx, repository, task); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, questionLabel); err != nil {
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
