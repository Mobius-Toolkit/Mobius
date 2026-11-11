package engine

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// maxBreakRunAge is the longest age of a failed run to start a CI break.
const maxBreakRunAge = 24 * time.Hour

// ignoredBreakEvent reports whether the event of a workflow run starts no CI break.
func ignoredBreakEvent(event string) bool {
	return event == "workflow_dispatch" || event == "repository_dispatch"
}

// readDefaultBranchCI keeps the breaks of the CI of the default branch. A break of a workflow starts when its newest
// completed run failed, the run has not the event workflow_dispatch or repository_dispatch, and GitHub created it within
// maxBreakRunAge (24 hours). Mobius runs the failed jobs of that run again one time. The result of the break is flaky when
// the new attempt passes, and hard when it fails. While a newer run of the workflow is queued or in progress, Mobius
// does not run the failed jobs again, because the new attempt can cancel that run (a concurrency group of the
// workflow). A hard break ends when a newer run of the workflow passes, or when a
// later attempt of the failed run passes. When GitHub refuses the list of runs with a 4xx status (for example with no
// permission), Mobius skips the CI for this poll. A workflow whose newest failed run already had a break, also an ended
// one, starts a new break only with a newer failed run. A run that passed the time limit of a job counts as a failed run
// (see markTimedOutRuns).
func (e *Engine) readDefaultBranchCI(ctx context.Context, repository github.Repository) error {
	runs, err := repository.BranchWorkflowRuns(ctx, repository.DefaultBranch)
	var refused *gh.ErrorResponse
	if errors.As(err, &refused) && refused.Response.StatusCode < 500 {
		return nil
	}
	if err != nil {
		return err
	}
	runs = slices.DeleteFunc(runs, func(run *gh.WorkflowRun) bool {
		return run.GetHeadRepository().GetFullName() != repository.FullName
	})
	if err := e.markTimedOutRuns(ctx, repository, runs); err != nil {
		return err
	}
	byID := map[int64]*gh.WorkflowRun{}
	for _, run := range runs {
		byID[run.GetID()] = run
	}
	counted := slices.DeleteFunc(slices.Clone(runs), func(run *gh.WorkflowRun) bool {
		return run.GetStatus() != "completed" || run.GetConclusion() == "cancelled" || run.GetConclusion() == "skipped"
	})
	newest := map[int64]*gh.WorkflowRun{}
	for _, run := range newestWorkflowRuns(counted) {
		newest[run.GetWorkflowID()] = run
	}
	breaks, err := e.queries.ListOpenCIBreaks(ctx, repository.FullName)
	if err != nil {
		return err
	}
	open := map[int64]bool{}
	for _, ciBreak := range breaks {
		open[ciBreak.WorkflowID] = true
		if err := e.advanceCIBreak(ctx, repository, ciBreak, byID, newest[ciBreak.WorkflowID]); err != nil {
			return err
		}
	}
	for workflow, run := range newest {
		if open[workflow] || !failedRunConclusion(run.GetConclusion()) {
			continue
		}
		if ignoredBreakEvent(run.GetEvent()) || e.timeNow().Sub(run.GetCreatedAt().Time) > maxBreakRunAge {
			continue
		}
		newestRun, err := e.queries.NewestCIBreakRun(ctx, store.NewestCIBreakRunParams{Repository: repository.FullName, WorkflowID: workflow})
		if err != nil {
			return err
		}
		if run.GetID() <= newestRun {
			continue
		}
		if err := e.startCIBreak(ctx, repository, run); err != nil {
			return err
		}
	}
	if err := e.diagnoseCIBreaks(ctx, repository, byID); err != nil {
		return err
	}
	if err := e.createCIBreakIssues(ctx, repository); err != nil {
		return err
	}
	return e.startCIBreakTasks(ctx, repository)
}

// diagnoseCIBreaks adds a diagnosis for each break of repository that has a result and no issue and no diagnosis yet, and
// starts the Diagnoser while a diagnosis is open. A break whose run is not in runs gets no diagnosis. The head of a
// diagnosis is the head commit of the failed run.
func (e *Engine) diagnoseCIBreaks(ctx context.Context, repository github.Repository, runs map[int64]*gh.WorkflowRun) error {
	breaks, err := e.queries.ListCIBreaksToDiagnose(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, ciBreak := range breaks {
		run := runs[ciBreak.RunID]
		if run == nil {
			continue
		}
		if _, err := e.queries.AddCIDiagnosis(ctx, store.AddCIDiagnosisParams{Repository: repository.FullName, CiBreak: ciBreak.ID, Head: run.GetHeadSHA(), CreatedAt: now()}); err != nil {
			return err
		}
	}
	if _, err := e.queries.NextCIDiagnosis(ctx, repository.FullName); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	e.startDiagnoser(repository.FullName)
	return nil
}

// timeLimitPrefix starts the message of the annotation that GitHub adds to a job that passed its time limit. GitHub gives
// such a job, its run and its step the conclusion cancelled, as it does for a cancel by a newer push.
const timeLimitPrefix = "The job has exceeded the maximum execution time"

// markTimedOutRuns sets the conclusion timed_out on the newest run of a workflow when the run is completed, is cancelled,
// has an id above the newest break run of its workflow, and one of its jobs passed its time limit. A 4xx answer of GitHub
// for the jobs or the annotations means that the run is not timed out.
func (e *Engine) markTimedOutRuns(ctx context.Context, repository github.Repository, runs []*gh.WorkflowRun) error {
	for _, run := range newestWorkflowRuns(runs) {
		if run.GetStatus() != "completed" || run.GetConclusion() != "cancelled" {
			continue
		}
		newestRun, err := e.queries.NewestCIBreakRun(ctx, store.NewestCIBreakRunParams{Repository: repository.FullName, WorkflowID: run.GetWorkflowID()})
		if err != nil {
			return err
		}
		if run.GetID() <= newestRun {
			continue
		}
		timedOut, err := attemptTimedOut(ctx, repository, run.GetID(), int64(run.GetRunAttempt()))
		if err != nil {
			return err
		}
		if timedOut {
			run.Conclusion = new("timed_out")
		}
	}
	return nil
}

// attemptTimedOut tells whether a job of the attempt of the run passed its time limit.
func attemptTimedOut(ctx context.Context, repository github.Repository, run, attempt int64) (bool, error) {
	jobs, err := repository.RunAttemptJobs(ctx, run, attempt)
	if refusedByGitHub(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		limit, err := jobTimeLimit(ctx, repository, job)
		if limit != "" || err != nil {
			return limit != "", err
		}
	}
	return false, nil
}

// jobTimeLimit gives the message of the annotation of a job that passed its time limit, or "" for another job. A 4xx
// answer of GitHub for the annotations gives "".
func jobTimeLimit(ctx context.Context, repository github.Repository, job *gh.WorkflowJob) (string, error) {
	if job.GetConclusion() != "cancelled" {
		return "", nil
	}
	annotations, err := repository.CheckRunAnnotations(ctx, job.GetID())
	if refusedByGitHub(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, annotation := range annotations {
		if strings.HasPrefix(annotation.GetMessage(), timeLimitPrefix) {
			return annotation.GetMessage(), nil
		}
	}
	return "", nil
}

// failedBreakStep gives the name of the first failed step of job. A job that passed its time limit has no failed step,
// so the first cancelled step is the one.
func failedBreakStep(job *gh.WorkflowJob, timeLimit string) string {
	for _, step := range job.Steps {
		if failedRunConclusion(step.GetConclusion()) || timeLimit != "" && step.GetConclusion() == "cancelled" {
			return step.GetName()
		}
	}
	return ""
}

func failedRunConclusion(conclusion string) bool {
	return conclusion == "failure" || conclusion == "timed_out" || conclusion == "startup_failure"
}

// startCIBreak stores the break of the failed run. A run with a later attempt is already run again, so its failure is
// a hard break.
func (e *Engine) startCIBreak(ctx context.Context, repository github.Repository, run *gh.WorkflowRun) error {
	params := store.AddCIBreakParams{
		Repository:   repository.FullName,
		WorkflowID:   run.GetWorkflowID(),
		WorkflowName: run.GetName(),
		RunID:        run.GetID(),
		RunUrl:       run.GetHTMLURL(),
		RunAttempt:   int64(run.GetRunAttempt()),
		RunEvent:     run.GetEvent(),
		StartedAt:    now(),
	}
	if run.GetRunAttempt() > 1 {
		params.RerunAt = params.StartedAt
		params.Result = "hard"
	}
	return e.queries.AddCIBreak(ctx, params)
}

// newerRunRuns reports whether a run of the workflow of ciBreak with an id above the failed run is queued or in progress.
func newerRunRuns(byID map[int64]*gh.WorkflowRun, ciBreak store.CiBreak) bool {
	for _, run := range byID {
		if run.GetWorkflowID() == ciBreak.WorkflowID && run.GetID() > ciBreak.RunID && (run.GetStatus() == "queued" || run.GetStatus() == "in_progress") {
			return true
		}
	}
	return false
}

// advanceCIBreak runs the failed jobs again when Mobius did not do it yet, no newer run of the workflow passed or failed
// and no newer run of the workflow is queued or in progress (the break then waits with no result). A newer failed run
// makes the break hard with no re-run. It stores the result of the new attempt, and ends the break when a newer run of the workflow passed. A hard break also
// ends when a later attempt of the failed run passed: the list of runs gives the newest attempt of each run. An open
// break of a run with the event workflow_dispatch or repository_dispatch ends with no ended run, also when the run is not
// in the page of runs (see breakRunEvent). An open break whose
// workflow is no longer active on the default branch (GitHub answers 404, or the workflow state is not active) ends
// with no ended run when the workflow has no completed run newer than the failed run in the page of runs. A new
// attempt that completes with a conclusion other than success, cancelled and skipped, and a newer failed run, make the
// break hard. A cancelled or skipped attempt (a newer push cancels it) gives no result: the next completed run of the
// workflow decides the break, unless a job of the attempt passed its time limit: then the attempt failed. Mobius stores
// the mark of the re-run after the call, so a run that already has a new attempt (for example after a stop between the
// call and the mark) is not run again. When GitHub refuses the call with a 4xx status (for example with no permission,
// or for a run older than 30 days), the failure stays a failure, so the break is hard.
// Another error returns, and the next poll tries again. A hard break that ends with a passed run closes its open task
// issue first, unless an open failure record with a place on a pull request has the same issue: the issue then closes
// only with its fix. A hard break with a task issue also ends when that issue is closed or GitHub does not find it, with no
// ended run.
func (e *Engine) advanceCIBreak(ctx context.Context, repository github.Repository, ciBreak store.CiBreak, byID map[int64]*gh.WorkflowRun, newest *gh.WorkflowRun) error {
	run := byID[ciBreak.RunID]
	event, handled, err := e.breakRunEvent(ctx, repository, ciBreak, run)
	if err != nil || handled {
		return err
	}
	if ignoredBreakEvent(event) {
		return e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: ciBreak.Result, EndedAt: now(), ID: ciBreak.ID})
	}
	passedAgain := newest != nil && newest.GetID() > ciBreak.RunID && newest.GetConclusion() == "success"
	if ciBreak.RerunAt == "" {
		if passedAgain {
			return e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: ciBreak.Result, EndedRunID: newest.GetID(), EndedAt: now(), ID: ciBreak.ID})
		}
		if newerRunRuns(byID, ciBreak) {
			return nil
		}
		if newest != nil && newest.GetID() > ciBreak.RunID && failedRunConclusion(newest.GetConclusion()) {
			if err := e.queries.SetCIBreakResult(ctx, store.SetCIBreakResultParams{Result: "hard", ID: ciBreak.ID}); err != nil {
				return err
			}
			return e.queries.SetCIBreakRerun(ctx, store.SetCIBreakRerunParams{RerunAt: now(), ID: ciBreak.ID})
		}
		state, err := repository.WorkflowState(ctx, ciBreak.WorkflowID)
		if err != nil {
			return err
		}
		if state != "active" {
			return e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: ciBreak.Result, EndedAt: now(), ID: ciBreak.ID})
		}
		if run == nil || int64(run.GetRunAttempt()) <= ciBreak.RunAttempt {
			var refused *gh.ErrorResponse
			if err := repository.RerunFailedJobs(ctx, ciBreak.RunID); err != nil {
				if !errors.As(err, &refused) || refused.Response.StatusCode >= 500 {
					return err
				}
				if err := e.queries.SetCIBreakResult(ctx, store.SetCIBreakResultParams{Result: "hard", ID: ciBreak.ID}); err != nil {
					return err
				}
			}
		}
		return e.queries.SetCIBreakRerun(ctx, store.SetCIBreakRerunParams{RerunAt: now(), ID: ciBreak.ID})
	}
	passedAgain = passedAgain || newest != nil && newest.GetID() == ciBreak.RunID && newest.GetConclusion() == "success"
	rerunEnded := run != nil && run.GetStatus() == "completed" && int64(run.GetRunAttempt()) > ciBreak.RunAttempt && run.GetConclusion() != "skipped"
	rerunCompleted := rerunEnded && run.GetConclusion() != "cancelled"
	if ciBreak.Result == "" && rerunEnded && !rerunCompleted {
		var err error
		if rerunCompleted, err = attemptTimedOut(ctx, repository, run.GetID(), int64(run.GetRunAttempt())); err != nil {
			return err
		}
	}
	if ciBreak.Result == "" && rerunCompleted && run.GetConclusion() == "success" {
		return e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: "flaky", EndedRunID: run.GetID(), EndedAt: now(), ID: ciBreak.ID})
	}
	newerFailed := newest != nil && newest.GetID() > ciBreak.RunID && failedRunConclusion(newest.GetConclusion())
	if ciBreak.Result == "" && (rerunCompleted || newerFailed) {
		ciBreak.Result = "hard"
		if err := e.queries.SetCIBreakResult(ctx, store.SetCIBreakResultParams{Result: ciBreak.Result, ID: ciBreak.ID}); err != nil {
			return err
		}
	}
	if passedAgain {
		if ciBreak.Result == "hard" && ciBreak.Issue != 0 {
			shared, err := e.queries.HasOpenPullRequestFailureWithIssue(ctx, store.HasOpenPullRequestFailureWithIssueParams{Repository: repository.FullName, Issue: max(ciBreak.Issue, -ciBreak.Issue)})
			if err != nil {
				return err
			}
			if !shared {
				if closed, err := e.closeCIBreakIssue(ctx, repository, ciBreak, newest); err != nil || !closed {
					return err
				}
			}
		}
		return e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: ciBreak.Result, EndedRunID: newest.GetID(), EndedAt: now(), ID: ciBreak.ID})
	}
	if ciBreak.Result == "hard" && ciBreak.Issue > 0 {
		closed, err := e.ciBreakIssueClosed(ctx, repository, ciBreak.Issue)
		if err != nil {
			return err
		}
		if closed {
			return e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: ciBreak.Result, EndedAt: now(), ID: ciBreak.ID})
		}
	}
	hasNewer := newest != nil && newest.GetID() > ciBreak.RunID || newerRunRuns(byID, ciBreak)
	if !hasNewer {
		state, err := repository.WorkflowState(ctx, ciBreak.WorkflowID)
		if err != nil {
			return err
		}
		if state != "active" {
			return e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: ciBreak.Result, EndedAt: now(), ID: ciBreak.ID})
		}
	}
	return nil
}

// breakRunEvent gives the event of the failed run of ciBreak: the event of run when the page of runs has it, else the
// stored event. A break with no stored event reads the run from GitHub one time and stores its event. handled is true
// when the break ended because GitHub answers 404 for the run, or when GitHub refuses the read with another 4xx status
// (the next poll tries again).
func (e *Engine) breakRunEvent(ctx context.Context, repository github.Repository, ciBreak store.CiBreak, run *gh.WorkflowRun) (event string, handled bool, err error) {
	if run != nil {
		event = run.GetEvent()
	} else {
		event = ciBreak.RunEvent
	}
	if event == "" {
		run, err = repository.WorkflowRun(ctx, ciBreak.RunID)
		if refusedByGitHub(err) {
			return "", true, nil
		}
		if err != nil {
			return "", false, err
		}
		if run == nil {
			return "", true, e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: ciBreak.Result, EndedAt: now(), ID: ciBreak.ID})
		}
		event = run.GetEvent()
	}
	if ciBreak.RunEvent == "" {
		err = e.queries.SetCIBreakRunEvent(ctx, store.SetCIBreakRunEventParams{RunEvent: event, ID: ciBreak.ID})
	}
	return event, false, err
}

// endCIBreak ends ciBreak. The end of a hard break with a task issue ends the stop of its repository.
func (e *Engine) endCIBreak(ctx context.Context, ciBreak store.CiBreak, params store.EndCIBreakParams) error {
	if err := e.queries.EndCIBreak(ctx, params); err != nil {
		return err
	}
	if ciBreak.Issue != 0 {
		e.publishCIStops(params.Result)
	}
	return nil
}
