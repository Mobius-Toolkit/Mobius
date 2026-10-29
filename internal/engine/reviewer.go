package engine

import (
	"context"
	"database/sql"
	_ "embed"
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

//go:embed prompts/reviewer.md
var reviewerPrompt string

const deploymentNeedsPrompt = "Call `report_deployment_needs` one time with the deployment needs of the full diff. Give an empty list when the diff adds no need."

var deploymentNeedKinds = []string{"env_var", "config", "secret", "manual_step"}

// reviewJob is the review of the head that an Implementer pushed to the pull request of the task.
type reviewJob struct {
	task        store.Task
	title       string
	pullRequest *gh.PullRequest
	head        string
	// parent is the session of the Implementer that pushed the head.
	parent sql.NullInt64
}

// round gives the fix round of the review of the task with the items.
func (j *reviewJob) round(task store.Task, items string, parent sql.NullInt64) round {
	return round{task: task, title: j.title, pullRequest: j.pullRequest, counts: true, items: items, parent: parent, cause: reviewCause}
}

// afterConflictRound tells that the task has the review of the head that a conflict round pushed. Such a review does
// not count as a review round. The tasks table keeps the mark in worker_input, so a restart of the server keeps it.
func afterConflictRound(task store.Task) bool {
	return task.Worker.String == ReviewerRole && task.WorkerInput.String == conflictRoundWorker
}

// reviewRoundText gives the end of the title of the review comment of the task.
func (e *Engine) reviewRoundText(task store.Task) string {
	return e.roundText(task, afterConflictRound(task))
}

// roundText gives reviewRoundText for a review that follows a conflict round when afterConflict is true.
func (e *Engine) roundText(task store.Task, afterConflict bool) string {
	if afterConflict {
		return fmt.Sprintf(" after a conflict round. It does not count (%d of %d)", task.ReviewRounds, e.config.MaxFixRounds)
	}
	return fmt.Sprintf(", round %d of %d", task.ReviewRounds+1, e.config.MaxFixRounds)
}

// review queues the Reviewer of the head that the done Implementer session pushed. A task that is not working, for
// example after a decline of the Lead during the Implementer session, gets no Reviewer.
func (e *Engine) review(ctx context.Context, j *job, p pushed, session int64) error {
	queued, err := e.queueTask(ctx, store.QueueTaskParams{QueuedAt: sql.NullString{String: now(), Valid: true}, ID: j.task.ID, FromState: "working"})
	if err != nil || queued == 0 {
		return err
	}
	worker := store.SetTaskWorkerParams{Worker: sql.NullString{String: ReviewerRole, Valid: true}, ID: j.task.ID}
	if j.conflictRound {
		worker.WorkerInput = sql.NullString{String: conflictRoundWorker, Valid: true}
	}
	if err := e.queries.SetTaskWorker(ctx, worker); err != nil {
		return err
	}
	e.runReviewer(reviewJob{task: j.task, title: j.title, pullRequest: p.pullRequest, head: p.head, parent: sql.NullInt64{Int64: session, Valid: true}})
	return nil
}

// restartReviewer starts the Reviewer of the queued or working task again after a restart of the server, or after the
// poll found it with no Worker (lost). The head of the pull request gets a Mobius check run when it has none. An error in the
// steps before the session starts the Worker again.
func (e *Engine) restartReviewer(repository github.Repository, task store.Task, lost bool) {
	if !task.PullRequest.Valid || !task.Branch.Valid {
		return
	}
	var j reviewJob
	reason := "Mobius restarted before the run ended."
	if lost {
		reason = "The Worker of the task stopped before the run ended."
	}
	e.runPreparedWorker(task, &j.title, "Reviewer", lost, func(ctx context.Context) (bool, error) {
		if err := e.abandonRound(ctx, repository, task, reason); err != nil {
			log.Printf("update the review comment of %s#%d: %v", task.Repository, task.Issue, err)
		}
		issue, err := existingIssue(ctx, repository, task.Issue)
		if err != nil {
			return false, err
		}
		j.title = issue.GetTitle()
		pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
		if err != nil {
			return false, err
		}
		parent, err := e.restartParent(ctx, task, ReviewerRole)
		if err != nil {
			return false, err
		}
		queued, err := e.requeueTask(ctx, task.ID)
		if err != nil || queued == 0 {
			return false, err
		}
		head := pullRequest.GetHead().GetSHA()
		if err := e.setReviewStep(ctx, repository, head, task, afterConflictRound(task)); err != nil {
			return false, err
		}
		j = reviewJob{task: task, title: j.title, pullRequest: pullRequest, head: head, parent: parent}
		return true, nil
	}, func(ctx context.Context) error { return e.reviewer(ctx, &j) })
}

// runReviewer runs the review job in the background until the task stops. After a failure, the Reviewer starts again
// after the wait of RestartWorker.
func (e *Engine) runReviewer(j reviewJob) {
	e.runWorker(j.task, &j.title, "Reviewer", false, func(ctx context.Context) error { return e.reviewer(ctx, &j) })
}

// reviewer runs one Reviewer session of the job, and acts on the open review threads after its turn. At
// max_fix_rounds review rounds, the task goes to a human with no session. A review after a conflict round has no
// limit, because it does not count. The end of ctx stops the session. An error
// tells that the Worker must start again.
func (e *Engine) reviewer(ctx context.Context, j *reviewJob) error {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: j.task.Repository, Issue: j.task.Issue})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	repository, err := e.repository(task.Repository)
	if err != nil {
		return err
	}
	number := int64(j.pullRequest.GetNumber())
	if limit := e.config.MaxFixRounds; !afterConflictRound(task) && task.ReviewRounds >= int64(limit) {
		if err := e.stopAtLimit(ctx, j.round(task, "", j.parent), "review"); err != nil {
			return err
		}
		return e.addOrKeepComment(ctx, repository, number, fmt.Sprintf("Review not started. Limit reached (%d of %d). Mobius added mobius:needs-human. Add a comment on this pull request to continue.", limit, limit))
	}
	a, err := e.addAgent(ctx, Spec{
		Role:                 ReviewerRole,
		Organization:         owner(task.Repository),
		Repository:           task.Repository,
		WorkstreamRepository: task.WorkstreamRepository,
		Workstream:           task.Workstream,
		Issue:                sql.NullInt64{Int64: task.Issue, Valid: true},
		Parent:               j.parent,
		Task:                 task.ID,
		PullRequest:          number,
	})
	if err != nil {
		if errors.Is(err, ErrLeftQueue) || ctx.Err() != nil {
			return nil
		}
		return err
	}
	ended := context.WithoutCancel(ctx)
	// A comment of a trusted user during the wait for the slot resets the counters of the task.
	task, err = e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if errors.Is(err, sql.ErrNoRows) {
		return a.End(ended, "stopped")
	}
	if err != nil {
		return a.Fail(ended, err)
	}
	a.spec.Dir = runner.ReviewDir(e.config.DataDir, task.Repository, a.id)
	a.head = j.head
	err = e.reviewRound(ctx, a, j, task)
	reason := ""
	switch {
	case ctx.Err() != nil:
		reason = "The task stopped."
	case err != nil:
		reason = "The run failed: " + err.Error()
	}
	if reason != "" {
		if abandonErr := e.abandonRound(ended, repository, task, reason); abandonErr != nil {
			log.Printf("update the review comment of %s#%d: %v", task.Repository, task.Issue, abandonErr)
		}
	}
	e.gitMu.Lock()
	if _, statErr := os.Stat(a.spec.Dir); !errors.Is(statErr, fs.ErrNotExist) {
		err = errors.Join(err, runner.RemoveWorktree(ended, e.config.DataDir, task.Repository, a.spec.Dir))
	}
	e.gitMu.Unlock()
	switch {
	case ctx.Err() != nil:
		return a.End(ended, "stopped")
	case errors.Is(err, errHung):
		return e.endHungTask(ended, a, task, j.title)
	case err != nil:
		return a.Fail(ended, err)
	}
	return a.End(ended, "done")
}

// reviewRound posts the comment of the review round, runs the turn of the Reviewer a in a worktree detached at the
// head, and acts on the open review threads after the turn.
func (e *Engine) reviewRound(ctx context.Context, a *Agent, j *reviewJob, task store.Task) error {
	repository, err := e.repository(task.Repository)
	if err != nil {
		return err
	}
	number := int64(j.pullRequest.GetNumber())
	// Each run gets a new comment, also a restart with the same round number.
	started, err := repository.AddComment(ctx, number, "Review started"+e.reviewRoundText(task))
	if err != nil {
		return err
	}
	comment := started.GetID()
	if err := e.queries.SetReviewComment(ctx, store.SetReviewCommentParams{ReviewComment: sql.NullInt64{Int64: comment, Valid: true}, ID: task.ID}); err != nil {
		return err
	}
	token, err := repository.Token(ctx)
	if err != nil {
		return err
	}
	dataDir, name := e.config.DataDir, repository.FullName
	var base string
	e.gitMu.Lock()
	err = e.fetch(ctx, repository, token)
	if err == nil {
		err = runner.AddDetachedWorktree(ctx, dataDir, name, a.spec.Dir, j.head)
	}
	if err == nil {
		base, err = runner.MergeBase(ctx, dataDir, name, "origin/"+repository.DefaultBranch, j.head)
	}
	e.gitMu.Unlock()
	if err != nil {
		return err
	}
	brief, err := e.workstreamBrief(ctx, task)
	if err != nil {
		return err
	}
	sections, err := e.repositorySections(ctx, repository, ReviewerRole)
	if err != nil {
		return err
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return err
	}
	trusted := func(login string) bool { return e.TrustedAuthor(repository.AppSlug, login) }
	reviewComments, err := repository.ReviewComments(ctx, number)
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("%s\n%s# Brief\n\n%s\n\n# Issue\n\n#%d %s\n\n%s\n\n# Commits\n\nBase commit: %s\nHead commit: %s\n\nThe changes are `git diff %s %s`.\n\n# Review threads\n%s",
		reviewerPrompt, sections, brief, task.Issue, issue.GetTitle(), issue.GetBody(), base, j.head, base, j.head, threads(reviewComments, trusted))
	if err := a.open(ctx); err != nil {
		return err
	}
	if err := a.Prompt(ctx, prompt, nil); err != nil {
		return err
	}
	if err := a.waitBackground(ctx); err != nil {
		return err
	}
	if err := e.requireDeploymentNeeds(ctx, a); err != nil {
		return err
	}
	a.closeHarness()
	e.gitMu.Lock()
	err = runner.RemoveWorktree(ctx, dataDir, name, a.spec.Dir)
	e.gitMu.Unlock()
	if err != nil {
		return err
	}
	return e.endReview(ctx, repository, j, comment, a.id)
}

// requireDeploymentNeeds sends the Reviewer one more prompt when its turn made no valid report_deployment_needs
// call. A second turn with no valid call fails the run.
func (e *Engine) requireDeploymentNeeds(ctx context.Context, a *Agent) error {
	if e.deploymentNeedsReported(a) {
		return nil
	}
	if err := a.Prompt(ctx, deploymentNeedsPrompt, nil); err != nil {
		return err
	}
	if ctx.Err() == nil && !e.deploymentNeedsReported(a) {
		return refuse("The Reviewer gave no deployment needs.")
	}
	return nil
}

func (e *Engine) deploymentNeedsReported(a *Agent) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reported
}

// DeploymentNeeds gives the stored deployment needs of the task, the oldest first.
func (e *Engine) DeploymentNeeds(ctx context.Context, task int64) ([]store.DeploymentNeed, error) {
	return e.queries.ListDeploymentNeeds(ctx, task)
}

// reportDeploymentNeeds replaces the stored deployment needs of the task of the Reviewer of c with the needs of
// input, at the head that it reviews.
func (e *Engine) reportDeploymentNeeds(ctx context.Context, c caller, _ github.Repository, input deploymentNeedsInput) (string, error) {
	for i, need := range input.Needs {
		if !slices.Contains(deploymentNeedKinds, need.Kind) {
			return "", refuse("Need %d (%s): kind must be one of %s.", i+1, need.Name, strings.Join(deploymentNeedKinds, ", "))
		}
		if empty(need.Name) || empty(need.Path) || empty(need.Reason) || need.Line < 1 {
			return "", refuse("Need %d (%s): name, path and reason must not be empty, and line must be 1 or more.", i+1, need.Name)
		}
	}
	c.agent.mu.Lock()
	defer c.agent.mu.Unlock()
	err := e.inTx(ctx, func(q *store.Queries) error {
		if err := q.DeleteDeploymentNeeds(ctx, c.agent.spec.Task); err != nil {
			return err
		}
		for _, need := range input.Needs {
			mandatory := int64(0)
			if need.Mandatory {
				mandatory = 1
			}
			err := q.AddDeploymentNeed(ctx, store.AddDeploymentNeedParams{
				Task: c.agent.spec.Task, Head: c.agent.head, Kind: need.Kind, Name: need.Name, Path: need.Path,
				Line: need.Line, Reason: need.Reason, Mandatory: mandatory,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	c.agent.reported = true
	return fmt.Sprintf("Stored %d deployment needs.", len(input.Needs)), nil
}

// endReview acts on the open review threads after the turn of the Reviewer session. It first sends the follow-up
// threads to the Lead and resolves them. With no other open thread, the task waits for the CI of the head, or gets a
// fix round with the findings that the Lead kept. The findings of the Reviewer go to a fix round, or at max_fix_rounds
// the task goes to a human. Each other open thread has a reply of a trusted user or bot, so the Judge takes it.
func (e *Engine) endReview(ctx context.Context, repository github.Repository, j *reviewJob, comment, session int64) error {
	// A comment of a trusted user during the turn resets the counters of the task.
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: j.task.Repository, Issue: j.task.Issue})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	number := int64(j.pullRequest.GetNumber())
	trusted := func(login string) bool { return e.TrustedAuthor(repository.AppSlug, login) }
	app := appLogin(repository.AppSlug)
	reviewThreads, err := repository.ReviewThreads(ctx, number)
	if err != nil {
		return err
	}
	if err := e.sendFollowUps(ctx, repository, j.task, j.title, j.pullRequest.GetNumber(), reviewThreads, app, trusted); err != nil {
		return err
	}
	open := slices.DeleteFunc(reviewThreads, func(thread github.ReviewThread) bool { return !openThread(thread, trusted, app) })
	limit := int64(e.config.MaxFixRounds)
	if len(open) == 0 {
		if err := setCIWaitStep(ctx, repository, j.head); err != nil {
			return err
		}
		if err := e.endRound(ctx, repository, j, task, comment, "No open findings. Mobius waits for CI.", open); err != nil {
			return err
		}
		moved, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "checks", ID: task.ID, FromState: "working"})
		if err != nil || moved == 0 {
			return err
		}
		task.State = "checks"
		_, err = e.startKeptRound(ctx, repository, j.round(task, "", sql.NullInt64{}))
		return err
	}
	// A finding of the Reviewer has only comments of the Mobius App.
	var findings []int64
	for _, thread := range open {
		if !slices.ContainsFunc(thread.Authors, func(author string) bool { return trusted(author) && !strings.EqualFold(author, app) }) {
			findings = append(findings, thread.Comment)
		}
	}
	if len(findings) == 0 {
		if err := e.endRound(ctx, repository, j, task, comment, "The Judge takes the open threads.", open); err != nil {
			return err
		}
		_, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "reviewed", ID: task.ID, FromState: "working"})
		return err
	}
	reviewLimit := !afterConflictRound(task) && task.ReviewRounds+1 >= limit
	atLimit := reviewLimit || task.FixRounds >= limit
	items := ""
	result := "A fix round started."
	switch {
	case reviewLimit:
		result = fmt.Sprintf("Limit reached (%d of %d). Mobius added mobius:needs-human. Add a comment on this pull request to continue.", limit, limit)
	case atLimit:
		result = fmt.Sprintf("Fix round limit reached (%d of %d). Mobius added mobius:needs-human. Add a comment on this pull request to continue.", limit, limit)
	default:
		if items, err = fixThreads(ctx, repository, number, findings, trusted); err != nil {
			return err
		}
	}
	if err := e.endRound(ctx, repository, j, task, comment, result, open); err != nil {
		return err
	}
	r := j.round(task, items, sql.NullInt64{Int64: session, Valid: true})
	switch {
	case reviewLimit:
		return e.stopAtLimit(ctx, r, "review")
	case atLimit:
		return e.stopAtLimit(ctx, r, "fix")
	}
	return e.fixRound(ctx, repository, r)
}

// endRound writes the result of the review round of the task with the links of the open threads into the comment of
// the round, and counts the round. A failed update stays in comment_writes. A review after a conflict round is not counted.
func (e *Engine) endRound(ctx context.Context, repository github.Repository, j *reviewJob, task store.Task, comment int64, result string, open []github.ReviewThread) error {
	var links strings.Builder
	for _, thread := range open {
		fmt.Fprintf(&links, "- %s#discussion_r%d\n", j.pullRequest.GetHTMLURL(), thread.Comment)
	}
	body := fmt.Sprintf("Review ended%s\n\nResult: %s\nOpen findings: %d\n\n%s", e.reviewRoundText(task), result, len(open), links.String())
	if err := e.updateOrKeepComment(ctx, repository, comment, strings.TrimRight(body, "\n")); err != nil {
		return err
	}
	if err := e.queries.SetReviewComment(ctx, store.SetReviewCommentParams{ID: task.ID}); err != nil {
		return err
	}
	if afterConflictRound(task) {
		return nil
	}
	return e.queries.AddReviewRound(ctx, task.ID)
}

// abandonRound writes the reason into the comment of the review run of the task that did not end. A task with no
// comment of a run needs no update. A failed update stays in comment_writes.
func (e *Engine) abandonRound(ctx context.Context, repository github.Repository, task store.Task, reason string) error {
	comment, err := e.queries.GetReviewComment(ctx, task.ID)
	if err != nil || !comment.Valid {
		return err
	}
	updateErr := e.updateOrKeepComment(ctx, repository, comment.Int64, fmt.Sprintf("Review stopped%s\n\n%s", e.reviewRoundText(task), reason))
	return errors.Join(e.queries.SetReviewComment(ctx, store.SetReviewCommentParams{ID: task.ID}), updateErr)
}

// followUpMarker ends the body of a follow-up comment on GitHub. It is an HTML comment, so GitHub does not show it.
// endReview finds the follow-up threads by it.
const followUpMarker = "\n\n<!-- mobius:follow-up -->"

// submitReview posts the review of the Reviewer of c on its pull request, at the head that it reviews. The body of
// each follow-up comment gets followUpMarker.
func (e *Engine) submitReview(ctx context.Context, c caller, repository github.Repository, input reviewInput) (string, error) {
	if c.agent.reviewed {
		return "", refuse("The review is already on GitHub. Do not call submit_review again.")
	}
	if empty(input.Body) {
		return "", refuse("body must not be empty.")
	}
	if slices.ContainsFunc(input.Comments, func(comment github.InlineComment) bool {
		return empty(comment.Path) || comment.Line < 1 || empty(comment.Body)
	}) {
		return "", refuse("Each comment needs a path, a line of 1 or more, and a body.")
	}
	comments := slices.Clone(input.Comments)
	for i := range comments {
		if comments[i].FollowUp {
			comments[i].Body += followUpMarker
		}
	}
	body, err := e.withMark(ctx, c, repository.FullName, input.Body)
	if err != nil {
		return "", err
	}
	if err := repository.SubmitReview(ctx, c.agent.spec.PullRequest, c.agent.head, body, comments); err != nil {
		return "", err
	}
	c.agent.reviewed = true
	return "Posted the review.", nil
}

// followUpBody gives the body of the first comment of thread without followUpMarker. It tells if the thread is a
// follow-up thread: the Mobius App with the login app started it with followUpMarker, and no trusted user has replied.
// A reply of a trusted user makes the thread a normal thread, so the Judge takes the reply.
func followUpBody(thread github.ReviewThread, app string, trusted func(string) bool) (string, bool) {
	first := thread.Comments[0]
	body, found := strings.CutSuffix(first.Body, followUpMarker)
	body = runner.WithoutMark(body)
	replied := slices.ContainsFunc(thread.Comments[1:], func(comment github.ThreadComment) bool {
		return trusted(comment.Author) && !strings.EqualFold(comment.Author, app)
	})
	return body, found && strings.EqualFold(first.Author, app) && !replied
}

// sendFollowUps sends each open follow-up thread of reviewThreads of the pull request number to the Lead, and then
// resolves the thread. A failure leaves the thread open, so the next call resolves it again. A thread with an event in
// lead_events gets no second event.
func (e *Engine) sendFollowUps(ctx context.Context, repository github.Repository, task store.Task, title string, number int, reviewThreads []github.ReviewThread, app string, trusted func(string) bool) error {
	for i := range reviewThreads {
		thread := &reviewThreads[i]
		body, isFollowUp := followUpBody(*thread, app, trusted)
		if thread.Resolved || !isFollowUp {
			continue
		}
		sent, err := e.queries.HasFollowUpEvent(ctx, store.HasFollowUpEventParams{WorkstreamRepository: task.WorkstreamRepository, Repository: task.Repository, Issue: sql.NullInt64{Int64: task.Issue, Valid: true}, Item: thread.Comment})
		if err != nil {
			return err
		}
		if !sent {
			event := fmt.Sprintf("%s follow-up on pull request %s of %s \"%s\", item %d, %s line %d:\n\n%s",
				time.Now().UTC().Format(timeFormat), taskEventRef(task, int64(number)), taskEventRef(task, task.Issue), title, thread.Comment, thread.Path, thread.Line, quote(body))
			if err := e.addLeadEvent(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, "follow_up", event); err != nil {
				return err
			}
		}
		if err := repository.ResolveReviewThread(ctx, thread.ID); err != nil {
			return err
		}
		thread.Resolved = true
	}
	return nil
}
