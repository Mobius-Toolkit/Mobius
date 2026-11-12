package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	// queuedRunLimit is the time that a queued run can wait. A run that waits for a free runner already has its jobs,
	// so a run with no job after this time is a run that GitHub lost.
	queuedRunLimit = 30 * time.Minute
	// githubActions is the slug of the App of GitHub Actions.
	githubActions = "github-actions"
	// jobLogLines is the number of lines of the end of a job log of GitHub Actions in a fix round.
	jobLogLines = 200
	// longCIWait is the time in checks with CI that still runs, and the time between two events about it. A normal CI
	// run completes in much less time, and a run in waiting or action_required must not block a task for a full day
	// before the Lead knows.
	longCIWait = 2 * time.Hour
	// approvalReminder is the time in approval, and the time between two reminders. A pull request that waits for the
	// Lead approval must come back to the Lead after a short time, for fast iterations.
	approvalReminder = 2 * time.Hour
	// dispatchReminder is the time in dispatched with no open question, and the time between two reminders. A
	// dispatched task holds an Autopilot slot while no agent works on it. The value is the same as the limit in checks
	// and in approval.
	dispatchReminder = 2 * time.Hour
)

// screenshotsState is the state of the screenshots section of the description of a pull request.
type screenshotsState int

const (
	screenshotsNone screenshotsState = iota
	screenshotsUnchanged
	screenshotsPending
	screenshotsChanged
)

// screenshotsOf gives the state of the screenshots section of body. The workflow .github/workflows/screenshots.yml
// writes the text of the section: "The screenshots are pending." at each new head, then "No screen changed.", "The
// screenshots failed." or a table of the changed screens. The engine compares these texts.
func screenshotsOf(body string) screenshotsState {
	start, end, found := sectionBounds(body, screenshotsStart, screenshotsEnd)
	if !found {
		return screenshotsNone
	}
	text := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(body[start:end], screenshotsStart), screenshotsEnd))
	text = strings.TrimSpace(strings.TrimPrefix(text, "### UI screenshots"))
	switch {
	case text == "No screen changed.":
		return screenshotsUnchanged
	case strings.HasPrefix(text, "The screenshots are pending."):
		return screenshotsPending
	}
	return screenshotsChanged
}

// ciWait is the head that the poll saw first at a time, for a task in checks.
type ciWait struct {
	head  string
	since time.Time
}

// failedCheckRuns gives the completed check runs of other Apps that failed on the commit sha.
func failedCheckRuns(ctx context.Context, repository github.Repository, sha string) ([]*gh.CheckRun, error) {
	runs, err := repository.CheckRuns(ctx, sha)
	if err != nil {
		return nil, err
	}
	var failed []*gh.CheckRun
	for _, run := range runs {
		if run.GetName() != checkRunName && run.GetStatus() == "completed" && failedConclusion(run.GetConclusion()) {
			failed = append(failed, run)
		}
	}
	return failed, nil
}

// unhandledFailure tells if the head of the pull request of the task has a failed check run of another App and no fix
// round yet.
func (e *Engine) unhandledFailure(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	head := pullRequest.GetHead().GetSHA()
	if task.CheckHead.String == head {
		return false, nil
	}
	runs, err := failedCheckRuns(ctx, repository, head)
	return len(runs) > 0, err
}

// ciItems gives the items of a fix round for the failed check runs of other Apps on the commit head. Each failed check
// run is an item with its annotations, and a check run of GitHub Actions also has the end of its job log.
func ciItems(ctx context.Context, repository github.Repository, head string) (string, error) {
	runs, err := failedCheckRuns(ctx, repository, head)
	if err != nil {
		return "", err
	}
	var items strings.Builder
	for _, run := range runs {
		output := run.GetOutput()
		fmt.Fprintf(&items, "\nCheck run \"%s\", %s:\n%s\n\n%s\n", run.GetName(), run.GetHTMLURL(), output.GetTitle(), output.GetSummary())
		annotations, err := repository.CheckRunAnnotations(ctx, run.GetID())
		if err != nil {
			return "", err
		}
		for _, annotation := range annotations {
			fmt.Fprintf(&items, "- %s line %d: %s\n", annotation.GetPath(), annotation.GetStartLine(), annotation.GetMessage())
		}
		if run.GetApp().GetSlug() == githubActions {
			// A job log that cannot download leaves the round with the annotations.
			if log, err := repository.JobLog(ctx, run.GetID()); err == nil {
				lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
				fmt.Fprintf(&items, "\nEnd of the job log:\n%s\n", strings.Join(lines[max(0, len(lines)-jobLogLines):], "\n"))
			}
		}
		items.WriteString("\nAction: fix\n")
	}
	return items.String(), nil
}

// onFailure starts a fix round when check runs of other Apps failed on the head of the pull request of the task in
// ready_for_review, checks or approval, and each check run and workflow run of the head completed. A head gets one
// round. It decides from state, the CI of the head that the caller read. The round waits until the Diagnoser of the head
// ends, and gets its diagnosis. It gives true when a round started, or when the task left its state.
func (e *Engine) onFailure(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest, state ci) (bool, error) {
	head := pullRequest.GetHead().GetSHA()
	if task.CheckHead.String == head || state.running || !state.failedCheck && !state.failedWorkflow {
		return false, nil
	}
	runs, err := failedCheckRuns(ctx, repository, head)
	if err != nil || len(runs) == 0 {
		return false, err
	}
	diagnosis, waits, err := e.pullRequestDiagnosis(ctx, repository, int64(pullRequest.GetNumber()), head)
	if err != nil || waits {
		return false, err
	}
	items, err := ciItems(ctx, repository, head)
	if err != nil {
		return false, err
	}
	if items == "" {
		return false, nil
	}
	items += diagnosis
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return false, err
	}
	parent, err := e.newestSession(ctx, task)
	if err != nil {
		return false, err
	}
	implementer, found := store.Session{}, false
	if task.State == "checks" {
		if implementer, found, err = e.implementerSession(ctx, task); err != nil {
			return false, err
		}
	}
	moved, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "working", ID: task.ID, FromState: task.State})
	if err != nil || moved == 0 {
		return true, err
	}
	if err := e.fixRound(ctx, repository, round{task: task, title: issue.GetTitle(), pullRequest: pullRequest, counts: true, items: items, parent: parent, failedCheck: true, cause: ciCause}); err != nil {
		_, stateErr := e.setTaskState(ctx, store.SetTaskStateParams{State: task.State, ID: task.ID, FromState: "working"})
		return false, errors.Join(err, stateErr)
	}
	if found {
		e.addCIStep(ctx, task, implementer, "failure")
	}
	checkErr := e.queries.SetTaskCheckHead(ctx, store.SetTaskCheckHeadParams{CheckHead: sql.NullString{String: head, Valid: true}, ID: task.ID})
	return true, errors.Join(checkErr, e.dismissReadyForReview(ctx, task, task.State))
}

// onChecks moves the task in checks to approval when the CI of the head of its pull request passed and the pull request
// has no merge conflict, and gives the Lead the event ready_for_approval one time for the head. A task that is not in
// checks, for example after a decline, stays as it is. A pull request whose mergeability GitHub still calculates waits.
// A head whose CI failed after its fix round waits until each run of the head completes. Then its failed jobs run
// again one time, and then the task goes to a human. After the re-run, the task waits while a run of that head does
// not complete, or while a run that Mobius ran again is still in its first attempt, because
// GitHub can list the first attempt and its failed check runs for some time after the re-run. A failed run that Mobius
// did not run again goes to a human. A head whose workflow run failed with no failed check run to fix goes to a human.
func (e *Engine) onChecks(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest, state ci) error {
	if pullRequest.Mergeable == nil {
		return nil
	}
	head := pullRequest.GetHead().GetSHA()
	if err := e.retryCancelledRuns(ctx, repository, pullRequest, &state); err != nil {
		return err
	}
	screenshots := screenshotsOf(pullRequest.GetBody())
	switch {
	case task.RerunHead.String == head && (state.running || len(rerunWaiting(task, state)) > 0):
		return e.onLongCIWait(ctx, repository, task, pullRequest, append(state.incomplete, rerunWaiting(task, state)...))
	case state.failedCheck && task.CheckHead.String != head:
		if state.running {
			return e.onLongCIWait(ctx, repository, task, pullRequest, state.incomplete)
		}
		return nil
	case state.failedCheck || state.failedWorkflow:
		if task.CheckHead.String == head && task.RerunHead.String != head {
			if state.running {
				return e.onLongCIWait(ctx, repository, task, pullRequest, state.incomplete)
			}
			rerun, err := e.rerunFailedJobs(ctx, repository, task, pullRequest)
			if err != nil || rerun {
				return err
			}
		}
		return e.ciFailed(ctx, repository, task, pullRequest)
	case state.running:
		incomplete, err := e.handleStuckRuns(ctx, repository, pullRequest, state)
		if err != nil {
			return err
		}
		return e.onLongCIWait(ctx, repository, task, pullRequest, incomplete)
	case state.absent && !e.quietPeriodEnded(task.ID, head):
		return nil
	case screenshots == screenshotsPending:
		return e.onLongCIWait(ctx, repository, task, pullRequest, []string{"screenshots"})
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return err
	}
	if err := setApprovalWaitStep(ctx, repository, head); err != nil {
		return err
	}
	implementer, found, err := e.implementerSession(ctx, task)
	if err != nil {
		return err
	}
	approvedHead, again, err := e.approvedHeadText(ctx, repository, task, pullRequest)
	if err != nil {
		return err
	}
	readyText, err := readyForApprovalText(ctx, repository, task, issue, pullRequest, approvedHead, state.checkRuns, screenshots == screenshotsChanged)
	if err != nil {
		return err
	}
	reviewed := !again && task.ReviewedHead.String == head && !hasLabel(issue, questionLabel) && screenshots != screenshotsChanged
	var level string
	var checkRun int64
	if again || reviewed {
		level, err = e.workstreamLevel(ctx, task.WorkstreamRepository, task.Workstream)
		if err != nil {
			return err
		}
		if checkRun, err = openCheckRun(ctx, repository, head); err != nil {
			return err
		}
		if checkRun == 0 {
			return fmt.Errorf("the head %s of pull request #%d has no open Mobius check run", head, pullRequest.GetNumber())
		}
	}
	moved, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "approval", ID: task.ID, FromState: "checks"})
	if err != nil || moved == 0 {
		return err
	}
	if found {
		e.addCIStep(ctx, task, implementer, "success")
	}
	delete(e.ciWait, task.ID)
	if again || reviewed {
		approve := e.approveAgain
		if reviewed {
			approve = e.approveReviewed
		}
		err := approve(ctx, repository, task, issue, pullRequest, level, checkRun)
		if err == nil {
			return nil
		}
		var refused *gh.ErrorResponse
		if errors.As(err, &refused) && refused.Response.StatusCode < 500 {
			return e.addReadyForApproval(ctx, task, readyText)
		}
		_, stateErr := e.setTaskState(ctx, store.SetTaskStateParams{State: "checks", ID: task.ID, FromState: "approval"})
		return errors.Join(err, stateErr)
	}
	return e.addReadyForApproval(ctx, task, readyText)
}

// readyForApprovalText gives the text of the event ready_for_approval for the head of the pull request.
func readyForApprovalText(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, pullRequest *gh.PullRequest, approvedHead string, runs []*gh.CheckRun, screensChanged bool) (string, error) {
	head := pullRequest.GetHead().GetSHA()
	var ciParts []string
	for _, run := range runs {
		if run.GetName() != checkRunName && run.GetStatus() == "completed" {
			ciParts = append(ciParts, fmt.Sprintf("%s %s", run.GetName(), run.GetConclusion()))
		}
	}
	ciLine := fmt.Sprintf("CI of head %.8s: %s", head, strings.Join(ciParts, ", "))
	if len(ciParts) == 0 {
		ciLine = fmt.Sprintf("CI of head %.8s:", head)
	}
	files, err := repository.PullRequestFiles(ctx, int64(pullRequest.GetNumber()))
	if err != nil {
		return "", err
	}
	var filesLine string
	switch {
	case len(files) == 0:
		filesLine = "Changed files (0):"
	case len(files) <= 100:
		filesLine = fmt.Sprintf("Changed files (%d): %s", len(files), strings.Join(files, ", "))
	default:
		filesLine = fmt.Sprintf("Changed files (%d): %s, and %d more", len(files), strings.Join(files[:100], ", "), len(files)-100)
	}
	text := fmt.Sprintf("%s ready for Lead approval of %s \"%s\": pull request %s %s.", time.Now().UTC().Format(timeFormat), taskEventRef(task, task.Issue), issue.GetTitle(), taskEventRef(task, int64(pullRequest.GetNumber())), pullRequest.GetHTMLURL()) + approvedHead
	text += fmt.Sprintf("\n%s\n%s", ciLine, filesLine)
	if screensChanged {
		text += "\nThe screenshots section of the pull request shows changed screens, or the screenshots failed. Compare each changed screen with the issue before the approval."
	}
	return text, nil
}

// addReadyForApproval gives the Lead the event ready_for_approval for the head of the pull request with text.
func (e *Engine) addReadyForApproval(ctx context.Context, task store.Task, text string) error {
	return e.addLeadEvent(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, "ready_for_approval", text)
}

// approveAgain approves the head again with the report of the Lead, after onChecks moved the task to approval. It
// approves the task with the same steps as approvePullRequest, and adds the quiet Lead event approved_again.
func (e *Engine) approveAgain(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, pullRequest *gh.PullRequest, level string, checkRun int64) error {
	head, approved := pullRequest.GetHead().GetSHA(), task.ApprovedHead.String
	comment := approvalAgainComment(repository.DefaultBranch, head, approved, task.ApprovedReportHead.String, task.ApprovedReport.String)
	result, err := e.finishApproval(ctx, repository, task, issue, pullRequest, level, checkRun, comment, "", "", true)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s Mobius approved again %s \"%s\": pull request %s %s, head %.8s. Only clean merges of %s came after the approved head %.8s. %s",
		time.Now().UTC().Format(timeFormat), taskEventRef(task, task.Issue), issue.GetTitle(), taskEventRef(task, int64(pullRequest.GetNumber())), pullRequest.GetHTMLURL(), head, repository.DefaultBranch, approved, result)
	return e.addLeadEvent(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, "approved_again", text)
}

// approveReviewed approves the head that the last review round of the Reviewer ended with no open finding, after
// onChecks moved the task to approval. It approves the task with the same steps as approvePullRequest, stores the text of
// the approval as the report, and adds the quiet Lead event auto_approved.
func (e *Engine) approveReviewed(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, pullRequest *gh.PullRequest, level string, checkRun int64) error {
	head := pullRequest.GetHead().GetSHA()
	report := fmt.Sprintf("Mobius approved head %.8s: the Reviewer found no open finding on this head, and each check of the head passed.", head)
	result, err := e.finishApproval(ctx, repository, task, issue, pullRequest, level, checkRun, report, "", report, false)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s Mobius approved %s \"%s\": pull request %s %s, head %.8s. The Reviewer found no open finding on this head. %s",
		time.Now().UTC().Format(timeFormat), taskEventRef(task, task.Issue), issue.GetTitle(), taskEventRef(task, int64(pullRequest.GetNumber())), pullRequest.GetHTMLURL(), head, result)
	return e.addLeadEvent(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, "auto_approved", text)
}

// onLongCIWait gives the Lead the event long_ci_wait when longCIWait passed since the task entered checks, or since the
// last event of this kind. It does not change the task.
func (e *Engine) onLongCIWait(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest, incomplete []string) error {
	return e.remindLead(ctx, task, longCIWait, "long_ci_wait", func(now time.Time, _ time.Duration) (string, error) {
		issue, err := existingIssue(ctx, repository, task.Issue)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s the CI of %s \"%s\" runs for more than %s: pull request %s %s, head %s. These runs did not complete: %s.", now.UTC().Format(timeFormat), taskEventRef(task, task.Issue), issue.GetTitle(), longCIWait, taskEventRef(task, int64(pullRequest.GetNumber())), pullRequest.GetHTMLURL(), pullRequest.GetHead().GetSHA(), strings.Join(incomplete, ", ")), nil
	})
}

// remindApproval gives the Lead the event approval_reminder when approvalReminder passed since the task entered
// approval, or since the last event of this kind. It does not change the task.
func (e *Engine) remindApproval(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) error {
	return e.remindLead(ctx, task, approvalReminder, "approval_reminder", func(now time.Time, waited time.Duration) (string, error) {
		issue, err := existingIssue(ctx, repository, task.Issue)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s \"%s\" waits for Lead approval for %s: pull request %s %s.", now.UTC().Format(timeFormat), taskEventRef(task, task.Issue), issue.GetTitle(), waited.Round(time.Minute), taskEventRef(task, int64(pullRequest.GetNumber())), pullRequest.GetHTMLURL()), nil
	})
}

// remindDispatch gives the Lead the event dispatch_reminder when dispatchReminder passed since the task entered
// dispatched, or since the last event of this kind. It gives no event while an earlier Lead event of the issue is not
// delivered. It does not change the task.
func (e *Engine) remindDispatch(ctx context.Context, task store.Task, issue *gh.Issue) error {
	waiting, err := e.queries.HasUndeliveredLeadEventOfIssue(ctx, store.HasUndeliveredLeadEventOfIssueParams{WorkstreamRepository: task.WorkstreamRepository, Repository: task.Repository, Issue: sql.NullInt64{Int64: task.Issue, Valid: true}})
	if err != nil || waiting {
		return err
	}
	return e.remindLead(ctx, task, dispatchReminder, "dispatch_reminder", func(now time.Time, waited time.Duration) (string, error) {
		return fmt.Sprintf("%s %s \"%s\" waits for an Implementer for %s and holds an Autopilot slot.", now.UTC().Format(timeFormat), taskEventRef(task, task.Issue), issue.GetTitle(), waited.Round(time.Minute)), nil
	})
}

// remindLead gives the Lead an event of the kind when limit passed since the task entered its state, or since the last
// event of the kind. text gets the time of the event and the time since the task entered its state.
func (e *Engine) remindLead(ctx context.Context, task store.Task, limit time.Duration, kind string, text func(now time.Time, waited time.Duration) (string, error)) error {
	entered, err := time.Parse(time.RFC3339Nano, task.StateAt)
	if err != nil {
		return err
	}
	last := entered
	if task.LongWaitAt.Valid {
		if last, err = time.Parse(time.RFC3339Nano, task.LongWaitAt.String); err != nil {
			return err
		}
	}
	now := e.timeNow()
	if now.Sub(last) < limit {
		return nil
	}
	body, err := text(now, now.Sub(entered))
	if err != nil {
		return err
	}
	if err := e.addLeadEvent(ctx, task.WorkstreamRepository, task.Workstream, task.Repository, sql.NullInt64{Int64: task.Issue, Valid: true}, kind, body); err != nil {
		return err
	}
	return e.queries.SetTaskLongWaitAt(ctx, store.SetTaskLongWaitAtParams{LongWaitAt: sql.NullString{String: now.UTC().Format(time.RFC3339Nano), Valid: true}, ID: task.ID})
}

// rerunFailedJobs runs the failed jobs of each newest workflow run of the head again, when the run is in its first
// attempt, and stores the head as the re-run head of the task after all calls. It tells if the task waits for the new
// attempts, or for the jobs of a run that did not complete: GitHub refuses a re-run while such a job runs, so the task
// waits and the next poll reads the jobs again. A 4xx answer of the list of jobs counts as completed jobs. A 4xx answer
// of the re-run, for example with no permission, is a refusal: the failure stays a failure. Another error returns, and
// the next poll tries again. A run that already started its second attempt is not run again.
func (e *Engine) rerunFailedJobs(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (bool, error) {
	head := pullRequest.GetHead().GetSHA()
	workflows, err := repository.WorkflowRuns(ctx, head)
	if err != nil {
		return false, err
	}
	failed := slices.DeleteFunc(newestWorkflowRuns(workflows), func(run *gh.WorkflowRun) bool {
		return !failedWorkflowRun(run) || run.GetRunAttempt() != 1
	})
	if len(failed) == 0 {
		return false, nil
	}
	var running []string
	for _, run := range failed {
		jobs, err := repository.RunAttemptJobs(ctx, run.GetID(), int64(run.GetRunAttempt()))
		var refused *gh.ErrorResponse
		if errors.As(err, &refused) && refused.Response.StatusCode < 500 {
			continue
		} else if err != nil {
			return false, err
		}
		for _, job := range jobs {
			if job.GetStatus() != "completed" {
				running = append(running, job.GetName())
			}
		}
	}
	if len(running) > 0 {
		return true, e.onLongCIWait(ctx, repository, task, pullRequest, running)
	}
	ids := make([]string, 0, len(failed))
	for _, run := range failed {
		ids = append(ids, strconv.FormatInt(run.GetID(), 10))
		var refused *gh.ErrorResponse
		if err := repository.RerunFailedJobs(ctx, run.GetID()); errors.As(err, &refused) && refused.Response.StatusCode < 500 {
			return false, nil
		} else if err != nil {
			return false, err
		}
	}
	return true, e.queries.SetTaskRerunHead(ctx, store.SetTaskRerunHeadParams{RerunHead: sql.NullString{String: head, Valid: true}, RerunRuns: sql.NullString{String: strings.Join(ids, ","), Valid: true}, ID: task.ID})
}

// rerunWaiting gives the names of the runs that Mobius ran again and that are still in their first attempt and failed.
func rerunWaiting(task store.Task, state ci) []string {
	ids := strings.Split(task.RerunRuns.String, ",")
	var names []string
	for _, run := range state.failedFirstAttempt {
		if slices.Contains(ids, strconv.FormatInt(run.GetID(), 10)) {
			names = append(names, run.GetName())
		}
	}
	return names
}

// retryCancelledRuns runs again each newest workflow run of the head that Mobius cancelled and whose rerun is still to
// do, when the run completed. GitHub cancels a run after it answers the cancel, so the first rerun can come too early.
// A run that GitHub refuses again goes to updateBranchOfRun. A cancelled first attempt that Mobius acted on does not
// count as a failed workflow, because the read of the poll can still show the cancel after the rerun.
func (e *Engine) retryCancelledRuns(ctx context.Context, repository github.Repository, pullRequest *gh.PullRequest, state *ci) error {
	var updatedBranch bool
	for _, run := range state.newestWorkflows {
		runState := e.refusedRuns[run.GetID()]
		if !cancelledRun(run) || (runState != refusedRunPendingRerun && runState != refusedRunPendingBranch) {
			continue
		}
		var refusal error
		if runState == refusedRunPendingRerun {
			err := repository.RerunWorkflowRun(ctx, run.GetID())
			if err == nil {
				e.refusedRuns[run.GetID()] = refusedRunRerunSucceeded
				continue
			}
			var refused *gh.ErrorResponse
			if !errors.As(err, &refused) || refused.Response.StatusCode >= 500 {
				return err
			}
			refusal = err
			e.refusedRuns[run.GetID()] = refusedRunPendingBranch
		}
		if err := e.updateBranchOfRun(ctx, repository, pullRequest, run.GetID(), refusal, &updatedBranch); err != nil {
			return err
		}
	}
	state.failedWorkflow = false
	for _, run := range state.newestWorkflows {
		switch {
		case !failedWorkflowRun(run):
		case cancelledRun(run) && run.GetRunAttempt() == 1 && e.refusedRuns[run.GetID()] != 0:
			state.running = true
			if !slices.Contains(state.incomplete, run.GetName()) {
				state.incomplete = append(state.incomplete, run.GetName())
			}
		default:
			state.failedWorkflow = true
		}
	}
	return nil
}

func cancelledRun(run *gh.WorkflowRun) bool {
	return run.GetStatus() == "completed" && run.GetConclusion() == "cancelled"
}

// updateBranchOfRun updates the branch of the pull request one time in a poll step, when the head is behind its base
// branch, because a new head gets new workflow runs. Else it logs the refusal. Then it records the refusal of the run
// as done. A temporary error keeps the run in refusedRunPendingBranch, so the next poll tries again.
func (e *Engine) updateBranchOfRun(ctx context.Context, repository github.Repository, pullRequest *gh.PullRequest, runID int64, refusal error, updatedBranch *bool) error {
	behind, err := repository.CommitsBehind(ctx, repository.DefaultBranch, pullRequest.GetHead().GetSHA())
	if err != nil {
		return err
	}
	switch {
	case behind > 0 && !*updatedBranch:
		if err := repository.UpdatePullRequestBranch(ctx, int64(pullRequest.GetNumber()), pullRequest.GetHead().GetSHA()); err != nil {
			var refusedUpdate *gh.ErrorResponse
			if !errors.As(err, &refusedUpdate) || refusedUpdate.Response.StatusCode >= 500 {
				return err
			}
			log.Printf("update the branch of pull request %d of %s: %v", pullRequest.GetNumber(), repository.FullName, err)
		}
		*updatedBranch = true
	case refusal != nil:
		log.Printf("stuck workflow run %d: %v", runID, refusal)
	default:
		log.Printf("stuck workflow run %d was refused", runID)
	}
	e.refusedRuns[runID] = refusedRunDone
	return nil
}

// handleStuckRuns cancels and runs again each newest workflow run of the head that is queued with no job for more than
// queuedRunLimit. When GitHub refuses the cancel or the rerun with a 4xx, it updates the branch of the pull request if
// the head is behind its base branch. It gives the incomplete runs for the Lead, with each stuck run named as
// "<workflow> (queued with no job)".
func (e *Engine) handleStuckRuns(ctx context.Context, repository github.Repository, pullRequest *gh.PullRequest, state ci) ([]string, error) {
	var updatedBranch bool
	stuckNames := map[string]bool{}
	for _, run := range state.newestWorkflows {
		runState := e.refusedRuns[run.GetID()]
		if runState == refusedRunDone || runState == refusedRunPendingRerun {
			if run.GetStatus() == "queued" || cancelledRun(run) {
				stuckNames[run.GetName()] = true
			}
			continue
		}
		if runState == refusedRunRerunSucceeded || run.GetStatus() != "queued" {
			continue
		}
		var refusal error
		if runState == 0 {
			if run.GetRunAttempt() != 1 {
				continue
			}
			createdAt := run.GetCreatedAt().Time
			if createdAt.IsZero() || e.timeNow().Sub(createdAt) < queuedRunLimit {
				continue
			}
			jobs, err := repository.WorkflowJobs(ctx, run.GetID())
			var refusedJobs *gh.ErrorResponse
			if errors.As(err, &refusedJobs) && refusedJobs.Response.StatusCode < 500 {
				log.Printf("workflow jobs of run %d: %v", run.GetID(), err)
				continue
			} else if err != nil {
				return nil, err
			}
			if len(jobs) > 0 {
				continue
			}
			stuckNames[run.GetName()] = true
			err = repository.CancelWorkflowRun(ctx, run.GetID())
			cancelled := err == nil
			if cancelled {
				e.refusedRuns[run.GetID()] = refusedRunPendingRerun
				err = repository.RerunWorkflowRun(ctx, run.GetID())
				if err == nil {
					e.refusedRuns[run.GetID()] = refusedRunRerunSucceeded
					delete(stuckNames, run.GetName())
					continue
				}
			}
			var refused *gh.ErrorResponse
			if !errors.As(err, &refused) || refused.Response.StatusCode >= 500 {
				return nil, err
			}
			if cancelled {
				continue
			}
			refusal = err
		} else {
			stuckNames[run.GetName()] = true
		}
		e.refusedRuns[run.GetID()] = refusedRunPendingBranch
		if err := e.updateBranchOfRun(ctx, repository, pullRequest, run.GetID(), refusal, &updatedBranch); err != nil {
			return nil, err
		}
	}
	incomplete := slices.Clone(state.incomplete)
	for i, name := range incomplete {
		if stuckNames[name] {
			incomplete[i] = fmt.Sprintf("%s (queued with no job)", name)
		}
	}
	return incomplete, nil
}

// ciFailed hands the task in checks to a human, with a failed Mobius check and a stop event for the Lead. The task
// keeps the head in ci_failed_head, so that continueNeedsHuman can tell this stop from another stop.
func (e *Engine) ciFailed(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) error {
	implementer, found, err := e.implementerSession(ctx, task)
	if err != nil {
		return err
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return err
	}
	reason := "the CI of the head commit failed, and a fix round cannot change it. Mobius set the Mobius check to failure and added mobius:needs-human."
	handed, err := e.handToHuman(ctx, task, issue.GetTitle(), stopText(task, issue.GetTitle(), reason), pullRequest.GetHead().GetSHA(), stopNotice{
		reason: "CI failed",
		text:   "The CI of the head commit failed, and a fix round cannot change it.",
		next: fmt.Sprintf("Read the failed jobs of PR #%d. When the default branch fails the same jobs, Resume after the fix of the default branch. Else fix the cause or Resume for a fix round.",
			pullRequest.GetNumber()),
		checkTitle:   "CI failed",
		checkSummary: "The CI of the head commit failed, and a fix round cannot change it.",
		createCheck:  true,
	})
	if err != nil || !handed {
		return err
	}
	if found {
		e.addCIStep(ctx, task, implementer, "failure")
	}
	return e.sendStopNotices(ctx, task.Repository, task.ID)
}

// ci is what the check runs of other Apps and the workflow runs of GitHub Actions on a head show.
type ci struct {
	checkRuns                            []*gh.CheckRun
	newestWorkflows                      []*gh.WorkflowRun
	failedCheck, failedWorkflow, running bool
	// openCheckRun is true when the head has a Mobius check run that is not complete.
	openCheckRun bool
	// failedFirstAttempt holds the newest workflow runs that completed with a failure in their first attempt.
	failedFirstAttempt []*gh.WorkflowRun
	// incomplete holds the names of the check runs and the workflow runs that did not complete.
	incomplete []string
	// absent is true for a head with no check run of another App and no workflow run: its CI has not started, or the
	// repository has no CI.
	absent bool
}

// ciOf reads the CI of the head. A workflow run can exist before its jobs have check runs.
func ciOf(ctx context.Context, repository github.Repository, head string) (ci, error) {
	runs, err := repository.CheckRuns(ctx, head)
	if err != nil {
		return ci{}, err
	}
	workflows, err := repository.WorkflowRuns(ctx, head)
	if err != nil {
		return ci{}, err
	}
	newest := newestWorkflowRuns(workflows)
	state := ci{checkRuns: runs, newestWorkflows: newest}
	others := 0
	for _, run := range runs {
		if run.GetName() == checkRunName {
			state.openCheckRun = state.openCheckRun || run.GetStatus() != "completed"
			continue
		}
		others++
		switch {
		case run.GetStatus() != "completed":
			state.running = true
			state.incomplete = append(state.incomplete, run.GetName())
		case failedConclusion(run.GetConclusion()):
			state.failedCheck = true
		}
	}
	for _, workflow := range newest {
		switch {
		case workflow.GetStatus() != "completed":
			state.running = true
			state.incomplete = append(state.incomplete, workflow.GetName())
		case failedWorkflowRun(workflow):
			state.failedWorkflow = true
			if workflow.GetRunAttempt() == 1 {
				state.failedFirstAttempt = append(state.failedFirstAttempt, workflow)
			}
		}
	}
	state.absent = others+len(workflows) == 0
	return state, nil
}

// failedWorkflowRun tells if the workflow run completed with a failure.
func failedWorkflowRun(run *gh.WorkflowRun) bool {
	return run.GetStatus() == "completed" && (failedConclusion(run.GetConclusion()) || run.GetConclusion() == "startup_failure")
}

// newestWorkflowRuns keeps the run with the highest id of each workflow, as the check runs keep the latest run of each
// name. An older run that a newer run replaced, for example a cancelled one, does not count.
func newestWorkflowRuns(runs []*gh.WorkflowRun) []*gh.WorkflowRun {
	newest := map[int64]*gh.WorkflowRun{}
	for _, run := range runs {
		if current, ok := newest[run.GetWorkflowID()]; !ok || run.GetID() > current.GetID() {
			newest[run.GetWorkflowID()] = run
		}
	}
	return slices.Collect(maps.Values(newest))
}

// quietPeriodEnded tells if review_quiet_period passed since the first poll that saw the head with no CI, so a
// repository with no CI does not wait forever.
func (e *Engine) quietPeriodEnded(taskID int64, head string) bool {
	seen, ok := e.ciWait[taskID]
	if !ok || seen.head != head {
		seen = ciWait{head, time.Now()}
		e.ciWait[taskID] = seen
	}
	return time.Since(seen.since) >= e.config.ReviewQuietPeriod
}

// failedConclusion tells if the conclusion of a completed check run is a failure.
func failedConclusion(conclusion string) bool {
	return conclusion == "failure" || conclusion == "timed_out" || conclusion == "cancelled"
}
