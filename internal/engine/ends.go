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

// checkErrorPolls is the number of polls in a row with an error of checkTask for one task before Mobius tells the Lead
// and the Owner. One or two failed polls can come from a network error or a GitHub rate limit, but an error on 10
// polls in a row is a permanent error.
const checkErrorPolls = 10

// checkTasks checks each live task of the repository, and adds the pull request of each task with work for an agent
// to work. A task that fails to check keeps its entry of the last poll. It first reads the pull requests of the
// reviewed tasks with one call, because GitHub does not document that a resolve of a review thread changes the update
// time of the pull request. It reads the pull requests of the tasks with an approval in the same call, because the
// approval needs the open threads and the dismissals. It reads the pull request of each open Inbox item in the merging
// state in the same call, because the approval of the item is new. When a task has a pull request, it reads the head of
// the default branch one time, for checkTask.
func (e *Engine) checkTasks(ctx context.Context, repository github.Repository, work map[int64]Work) error {
	tasks, err := e.queries.ListLiveTasks(ctx, repository.FullName)
	if err != nil {
		return err
	}
	merging, err := e.mergingPullRequests(ctx, repository)
	if err != nil {
		return err
	}
	var reviewed []int64
	hasPullRequest := false
	for _, task := range tasks {
		if (task.State == "reviewed" || awaitsMerge(task) || slices.Contains(merging, task.PullRequest.Int64)) && task.PullRequest.Valid {
			reviewed = append(reviewed, task.PullRequest.Int64)
		}
		hasPullRequest = hasPullRequest || task.PullRequest.Valid
	}
	if err := e.readPullRequests(ctx, repository, reviewed); err != nil {
		return err
	}
	baseHead := ""
	if hasPullRequest {
		if baseHead, err = repository.BranchHead(ctx, repository.DefaultBranch); err != nil {
			return err
		}
	}
	for _, task := range tasks {
		found, ok, err := e.checkTask(ctx, repository, task, baseHead)
		if err != nil {
			log.Printf("check the task of %s#%d: %v", repository.FullName, task.Issue, err)
			found, ok = e.currentWork()[task.ID]
		}
		if countErr := e.countCheckErrors(ctx, repository, task, err); countErr != nil {
			log.Printf("count the check errors of %s#%d: %v", repository.FullName, task.Issue, countErr)
		}
		if ok {
			work[task.ID] = found
		}
	}
	e.forgetPulls(repository, tasks)
	return nil
}

// countCheckErrors adds 1 to the count of polls in a row with an error of checkTask for the task, or sets the count to
// 0 when checkErr is nil. It writes the count first. When the count becomes checkErrorPolls, it then tells the Lead and
// adds an activity with the last error, so a failed write loses the message and never repeats it. It does not change the
// task.
func (e *Engine) countCheckErrors(ctx context.Context, repository github.Repository, task store.Task, checkErr error) error {
	count := task.CheckErrors + 1
	if checkErr == nil {
		count = 0
	}
	if count == task.CheckErrors {
		return nil
	}
	err := e.queries.SetTaskCheckErrors(ctx, store.SetTaskCheckErrorsParams{CheckErrors: count, ID: task.ID})
	if err != nil || count != checkErrorPolls {
		return err
	}
	rows, err := e.queries.ListCopiedIssuesByNumber(ctx, store.ListCopiedIssuesByNumberParams{Repository: task.Repository, Workstream: task.Workstream, Number: task.Issue})
	if err != nil {
		return err
	}
	var copied store.ListCopiedIssuesByNumberRow
	for _, row := range rows {
		if !otherRepository(row.RepositoryUrl, task.Repository) {
			copied = row
			break
		}
	}
	pullRequest := ""
	if task.PullRequest.Valid {
		pullRequest = fmt.Sprintf(", pull request #%d,", task.PullRequest.Int64)
	}
	text := fmt.Sprintf("Mobius failed to check #%d \"%s\"%s on %d polls in a row. The last error: %v.", task.Issue, copied.Title, pullRequest, checkErrorPolls, checkErr)
	err = e.queries.AddEvent(ctx, store.AddEventParams{
		Time:         now(),
		Organization: repository.Owner(),
		Repository:   task.Repository,
		Workstream:   task.Workstream,
		Issue:        task.Issue,
		Actor:        appLogin(repository.AppSlug),
		Text:         text,
		Link:         copied.HtmlUrl,
	})
	if err != nil {
		return err
	}
	return e.addLeadEvent(ctx, task.Repository, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "check_errors", time.Now().UTC().Format(timeFormat)+" "+text)
}

// checkTask acts on the state of the issue and the pull request of the task, and gives the pull request when the task
// has work for an agent: the round that the task queues or runs, or the round that the check starts.
//   - A task whose issue is gone, or whose issue closed with no pull request, ends.
//   - A merged or closed pull request ends the task, and a merge closes the open issue (Mobius-rust#226).
//   - A removal of the label that the state needs by a person stops the task: mobius:review for a task in ready_for_review,
//     and mobius:working for a task in another live state. A Judge that runs from ready_for_review needs mobius:review,
//     and a Judge that runs from needs_human needs no label.
//   - A removal of mobius:needs-human by a trusted user or by the Mobius App continues a task in needs_human
//     (continueByRemoval).
//   - The issue and the pull request of a live task get the labels of its state, and lose the other Mobius state labels
//     (setStateLabels). A person who removed the needed label stopped the task before. When the App removed it, or
//     nobody did, the label comes back and the check goes on.
//   - A task in dispatched for more than 2 hours, with no open question, gives the Lead a reminder (remindDispatch).
//   - A task in needs_human continues with no Resume when its pull request has a merge conflict, or when the CI of the
//     task passes on its head, also on a new head (continueNeedsHuman).
//   - A pull request of a task in checks, approval or ready_for_review with a merge conflict, or behind its base, gets a
//     conflict round. A failed check run of another App on its head gets a fix round.
//   - A task whose head failed its CI while the default branch has an open break of its CI waits, with no fix round and no
//     stop, and a pull request that is only behind its base gets no conflict round (waitsForBreak). When the break
//     ends, the branch of the pull request updates (endBreakWait). The task of the issue of the break does not wait.
//   - Else a task in checks, approval or ready_for_review with kept findings of the Lead gets a fix round with them
//     (startKeptRound).
//   - Else a task in checks moves to approval when the CI of the head passed (onChecks).
//   - Else a task in approval for more than 2 hours gives the Lead a reminder (remindApproval).
//   - Else a pull request with the approval of a trusted user gets a squash merge when its head agrees with the
//     conditions (mergeApproved), and the next poll ends the task.
//   - Else the new comments of the pull request of a task in checks, approval, ready_for_review, reviewed or needs_human
//     go to the Judge.
func (e *Engine) checkTask(ctx context.Context, repository github.Repository, task store.Task, baseHead string) (Work, bool, error) {
	issue, err := repository.Issue(ctx, task.Issue)
	if err != nil {
		return Work{}, false, err
	}
	if issue == nil || inOtherRepository(issue, repository.FullName) {
		return Work{}, false, e.endTask(ctx, repository, task)
	}
	var pullRequest *gh.PullRequest
	if task.PullRequest.Valid {
		if pullRequest, err = e.pullRequest(ctx, repository, task.PullRequest.Int64, baseHead); err != nil {
			return Work{}, false, err
		}
	}
	if pullRequest != nil && pullRequest.GetState() == "closed" {
		return Work{}, false, e.endPullRequest(ctx, repository, task, issue, pullRequest)
	}
	if pullRequest == nil && issue.GetState() == "closed" {
		return Work{}, false, e.endTask(ctx, repository, task)
	}
	needed := neededLabel(task)
	if slices.Contains([]string{workingLabel, reviewLabel}, needed) && !hasLabel(issue, needed) {
		if stopped, err := e.labelRemoved(ctx, repository, task, issue, needed); err != nil || stopped {
			return Work{}, false, err
		}
	}
	if continued, err := e.continueByRemoval(ctx, repository, task, issue, pullRequest); err != nil || continued {
		return Work{}, false, err
	}
	if err := e.setStateLabels(ctx, repository, task, issue, pullRequest); err != nil {
		return Work{}, false, err
	}
	if task.State == "dispatched" && !hasLabel(issue, questionLabel) {
		if err := e.remindDispatch(ctx, task, issue); err != nil {
			return Work{}, false, err
		}
	}
	if pullRequest == nil {
		return Work{}, false, nil
	}
	if task, err = e.keepApproval(ctx, repository, task); err != nil {
		return Work{}, false, err
	}
	if updated, err := e.endBreakWait(ctx, repository, task, pullRequest); err != nil || updated {
		return Work{}, false, err
	}
	var state ci
	if afterReview(task.State) {
		if state, err = ciOf(ctx, repository, pullRequest.GetHead().GetSHA()); err != nil {
			return Work{}, false, err
		}
	}
	breakWaits, err := e.waitsForBreak(ctx, repository, task, pullRequest, state)
	if err != nil {
		return Work{}, false, err
	}
	work := pullRequestWork(repository, pullRequest)
	conflict := pullRequest.Mergeable != nil && !pullRequest.GetMergeable() || behind(pullRequest) && !breakWaits
	if task.State == "needs_human" {
		var round bool
		if task, round, err = e.continueNeedsHuman(ctx, repository, task, pullRequest, conflict); err != nil || round {
			return work, round, err
		}
	}
	switch {
	case task.State == "queued" || task.State == "working":
		return work, slices.Contains([]string{checkRoundWorker, conflictRoundWorker}, task.Worker.String), nil
	case !slices.Contains([]string{"checks", "approval", "ready_for_review", "reviewed", "needs_human"}, task.State):
		return Work{}, false, nil
	case afterReview(task.State) && conflict:
		round, err := e.onConflict(ctx, repository, task, pullRequest)
		return work, round, err
	case afterReview(task.State) && task.LeadFindings != "":
		round, err := e.startKeptRound(ctx, repository, round{task: task, title: issue.GetTitle(), pullRequest: pullRequest})
		return work, round, err
	case afterReview(task.State) && !breakWaits:
		round, err := e.onFailure(ctx, repository, task, pullRequest, state)
		if err != nil || round {
			return work, round, err
		}
		if task.State == "checks" {
			if err := e.onChecks(ctx, repository, task, pullRequest, state); err != nil {
				return Work{}, false, err
			}
		}
		if task.State == "approval" {
			if err := e.remindApproval(ctx, repository, task, pullRequest); err != nil {
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
// pull request, and the actor is a trusted user or the Mobius App. It reads the label events only
// when one of the two items lacks the label. A removal counts only when it is newer than the move of the task to
// needs_human, because resume removes the label from both items.
func (e *Engine) continueByRemoval(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, pullRequest *gh.PullRequest) (bool, error) {
	issueHas := hasLabel(issue, needsHumanLabel)
	pullHas := pullRequest == nil || hasPullRequestLabel(pullRequest, needsHumanLabel)
	if task.State != "needs_human" || issueHas && pullHas {
		return false, nil
	}
	var since time.Time
	if task.NeedsHumanAt.Valid {
		var err error
		if since, err = time.Parse(time.RFC3339Nano, task.NeedsHumanAt.String); err != nil {
			return false, err
		}
	}
	var actors []string
	if !issueHas {
		issueEvents, err := repository.IssueEvents(ctx, task.Issue)
		if err != nil {
			return false, err
		}
		actors = append(actors, removalActor(issueEvents, since))
	}
	if !pullHas {
		pullEvents, err := repository.IssueEvents(ctx, task.PullRequest.Int64)
		if err != nil {
			return false, err
		}
		actors = append(actors, removalActor(pullEvents, since))
	}
	for _, actor := range actors {
		if actor == "" || !e.TrustedAuthor(repository.AppSlug, actor) {
			continue
		}
		return true, e.resume(ctx, repository, issue, task, actor)
	}
	return false, nil
}

// removalActor gives the actor of the last removal of mobius:needs-human in events, or "" when events have no removal
// that is newer than since.
func removalActor(events []*gh.IssueEvent, since time.Time) string {
	_, removed := lastEvent(events, "unlabeled", needsHumanLabel)
	if removed == nil || !removed.GetCreatedAt().After(since) {
		return ""
	}
	return removed.GetActor().GetLogin()
}

// neededLabel gives the Mobius state label that the issue of the live task needs: mobius:needs-human for needs_human,
// mobius:review for ready_for_review and for a Judge that runs from it, none for asked, answered and stopped, and
// mobius:working else. A Judge that runs from needs_human needs none.
func neededLabel(task store.Task) string {
	switch {
	case judgesHuman(task), slices.Contains([]string{"stopped", "asked", "answered"}, task.State):
		return ""
	case task.State == "needs_human":
		return needsHumanLabel
	case task.State == "ready_for_review", task.Worker.String == JudgeRole && task.WorkerInput.String == "ready_for_review":
		return reviewLabel
	}
	return workingLabel
}

// judgesHuman tells if the Worker of the task is a Judge that runs from needs_human.
func judgesHuman(task store.Task) bool {
	return task.Worker.String == JudgeRole && task.WorkerInput.String == "needs_human"
}

// setStateLabels gives the issue of the live task the label needed, and removes the other Mobius state labels
// (mobius:working, mobius:review and mobius:needs-human) from it. The pull request has mobius:needs-human only in
// needs_human. It writes only a label that is wrong, and it writes nothing for a Judge that runs from needs_human. It
// does not change mobius:question. It reads the task again, because a hand over can change the task and write its
// labels while checkTask reads GitHub.
func (e *Engine) setStateLabels(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, pullRequest *gh.PullRequest) error {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if errors.Is(err, sql.ErrNoRows) || judgesHuman(task) {
		return nil
	}
	if err != nil {
		return err
	}
	needed := neededLabel(task)
	stateLabels := []string{workingLabel, reviewLabel, needsHumanLabel}
	for _, label := range stateLabels {
		if label != needed && hasLabel(issue, label) {
			if err := repository.RemoveLabel(ctx, task.Issue, label); err != nil {
				return err
			}
		}
	}
	if needed != "" && !hasLabel(issue, needed) {
		if err := repository.AddLabel(ctx, task.Issue, needed); err != nil {
			return err
		}
	}
	if pullRequest == nil {
		return nil
	}
	if needed == needsHumanLabel && !hasPullRequestLabel(pullRequest, needsHumanLabel) {
		return repository.AddLabel(ctx, task.PullRequest.Int64, needsHumanLabel)
	}
	if needed != needsHumanLabel && hasPullRequestLabel(pullRequest, needsHumanLabel) {
		return repository.RemoveLabel(ctx, task.PullRequest.Int64, needsHumanLabel)
	}
	return nil
}

// clearEndedTaskLabels removes the Mobius state labels and mobius:question from each open issue of repository whose
// newest task ended and that has no live task. It reads the labels from the local copy. An issue with an open Inbox
// item of a lost task keeps mobius:needs-human. A removal that GitHub refuses is logged, and the next poll tries it
// again, because the local copy still has the label.
func (e *Engine) clearEndedTaskLabels(ctx context.Context, repository github.Repository) error {
	rows, err := e.queries.ListEndedTaskIssueLabels(ctx, store.ListEndedTaskIssueLabelsParams{
		Repository: repository.FullName,
		Working:    workingLabel,
		Review:     reviewLabel,
		Question:   questionLabel,
		NeedsHuman: needsHumanLabel,
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if otherRepository(row.RepositoryUrl, repository.FullName) {
			continue
		}
		if err := repository.RemoveLabel(ctx, row.Number, row.Name); err != nil {
			log.Printf("remove %s from %s#%d: %v", row.Name, repository.FullName, row.Number, err)
		}
	}
	return nil
}

// continueNeedsHuman continues the task in needs_human with no Resume, and gives the task in its new state, and true
// when a conflict round started. Counters stay as they are.
//   - A pull request with a merge conflict, or behind its base, gets a conflict round. A head gets one automatic round
//     (conflict_head), so a round that fails does not start again on each poll. A pull request older than stale_pr_age
//     gets no round, and no new event or Inbox item.
//   - A pull request with no conflict, whose CI stopped the task (ci_failed_head) and passes now on its head, which can
//     be a new head, moves to checks. The review was clean before the stop. A task that stopped for another reason, for
//     example the round limit, stays.
//
// After a conflict round, the issue keeps mobius:needs-human until setStateLabels removes it at the next check. The CI
// branch removes the label at once.
func (e *Engine) continueNeedsHuman(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest, conflict bool) (store.Task, bool, error) {
	head := pullRequest.GetHead().GetSHA()
	if conflict {
		if task.ConflictHead.String == head || time.Since(pullRequest.GetCreatedAt().Time) > e.config.StalePRAge {
			return task, false, nil
		}
		if err := repository.AddLabel(ctx, task.Issue, workingLabel); err != nil {
			return task, false, err
		}
		return task, true, e.conflictRound(ctx, repository, task, pullRequest)
	}
	if !task.CiFailedHead.Valid || pullRequest.Mergeable == nil {
		return task, false, nil
	}
	state, err := ciOf(ctx, repository, head)
	if err != nil || state.failedCheck || state.failedWorkflow || state.running || state.absent {
		return task, false, err
	}
	moved, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "checks", ID: task.ID, FromState: "needs_human"})
	if err != nil || moved == 0 {
		return task, false, err
	}
	task, err = e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if err != nil {
		return task, false, err
	}
	if err := repository.AddLabel(ctx, task.Issue, workingLabel); err != nil {
		return task, false, err
	}
	return task, false, removeNeedsHuman(ctx, repository, task)
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

// labelRemoved stops the task when a person removed label from its issue, and tells if it did. The pull request and
// the branch stay, and the head of the pull request gets a failed Mobius check.
func (e *Engine) labelRemoved(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, label string) (bool, error) {
	events, err := repository.IssueEvents(ctx, task.Issue)
	if err != nil {
		return false, err
	}
	var actor string
	for _, event := range slices.Backward(events) {
		if event.GetEvent() == "unlabeled" && event.GetLabel().GetName() == label {
			actor = event.GetActor().GetLogin()
			break
		}
	}
	if actor == "" || strings.EqualFold(actor, appLogin(repository.AppSlug)) {
		return false, nil
	}
	return true, e.stopTask(ctx, repository, task, issue, actor, "Stopped by a label removal.", fmt.Sprintf("Stopped \"%s\" after a removal of %s", issue.GetTitle(), label))
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
	from := task.State
	if task.State == "working" && task.Worker.String == JudgeRole {
		from = task.WorkerInput.String
	}
	return errors.Join(e.dismissReadyForReview(ctx, task, from), e.finishStop(ctx, repository, task, issue, actor, summary, text))
}

func (e *Engine) finishStop(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, actor, summary, text string) error {
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
		if err := setCheckRun(ctx, repository, pullRequest.GetHead().GetSHA(), "completed", "failure", "Stopped", summary); err != nil {
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
	if err := e.dismissIssueItems(ctx, task.Repository, task.Issue, readyForReviewKind); err != nil {
		return err
	}
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
	if err := closeOpenPullRequest(ctx, repository, task, "Declined", input.Reason); err != nil {
		return "", err
	}
	if err := e.endTask(ctx, repository, task); err != nil {
		return "", err
	}
	if err := e.addActivity(ctx, repository.FullName, c.workstream, issue, appLogin(repository.AppSlug), fmt.Sprintf("Declined \"%s\"", issue.GetTitle())); err != nil {
		return "", err
	}
	return fmt.Sprintf("Declined #%d.", input.N), nil
}

// closeOpenPullRequest sets a failed Mobius check with title and reason on the open pull request of the task, posts
// the reason on it, and closes it. It does nothing for a task with no open pull request.
func closeOpenPullRequest(ctx context.Context, repository github.Repository, task store.Task, title, reason string) error {
	if !task.PullRequest.Valid {
		return nil
	}
	number := task.PullRequest.Int64
	pullRequest, err := repository.PullRequest(ctx, number)
	if err != nil {
		return err
	}
	if pullRequest.GetState() != "open" {
		return nil
	}
	if err := setCheckRun(ctx, repository, pullRequest.GetHead().GetSHA(), "completed", "failure", title, reason); err != nil {
		return err
	}
	if _, err := repository.AddComment(ctx, number, reason); err != nil {
		return err
	}
	return repository.ClosePullRequest(ctx, number)
}

// closeTask closes a task issue of the Workstream that has no work left. The branch stays.
func (e *Engine) closeTask(ctx context.Context, c caller, repository github.Repository, input declineInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	if empty(input.Reason) {
		return "", refuse("reason must not be empty.")
	}
	issue, err := existingIssue(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	workstream, known, err := e.copiedWorkstreamOf(ctx, repository.FullName, input.N, issue.GetRepositoryURL())
	if err != nil {
		return "", err
	}
	if !known || workstream != c.workstream || input.N == c.workstream {
		return "", refuse("#%d is not a task issue of this Workstream.", input.N)
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: input.N})
	live := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if live && task.Workstream != c.workstream {
		return "", refuse("#%d has no live task in this Workstream.", input.N)
	}
	if _, err := repository.AddComment(ctx, input.N, input.Reason); err != nil {
		return "", err
	}
	if live {
		if err := closeOpenPullRequest(ctx, repository, task, "Closed", input.Reason); err != nil {
			return "", err
		}
		if err := e.endTask(ctx, repository, task); err != nil {
			return "", err
		}
	}
	if err := repository.RemoveLabel(ctx, input.N, readyLabel); err != nil {
		return "", err
	}
	closed, err := repository.CloseIssue(ctx, input.N, "not_planned")
	if err != nil {
		return "", err
	}
	if _, err := e.updateCopy(ctx, repository, closed, nil, false, e.labelChangeCount(repository.FullName)); err != nil {
		return "", err
	}
	e.publish(Change{Workstreams: true})
	if err := e.addActivity(ctx, repository.FullName, c.workstream, issue, appLogin(repository.AppSlug), fmt.Sprintf("Closed \"%s\"", issue.GetTitle())); err != nil {
		return "", err
	}
	return fmt.Sprintf("Closed #%d.", input.N), nil
}
