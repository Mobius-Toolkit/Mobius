package engine

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// readDefaultBranchCI keeps the breaks of the CI of the default branch. A break of a workflow starts when its newest
// completed run failed. Mobius runs the failed jobs of that run again one time. The result of the break is flaky when
// the new attempt passes, and hard when it fails. A hard break ends when a newer run of the workflow passes, or when a
// later attempt of the failed run passes. When GitHub refuses the list of runs with a 4xx status (for example with no
// permission), Mobius skips the CI for this poll. A workflow whose newest failed run already had a break, also an ended
// one, starts a new break only with a newer failed run.
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
		StartedAt:    now(),
	}
	if run.GetRunAttempt() > 1 {
		params.RerunAt = params.StartedAt
		params.Result = "hard"
	}
	return e.queries.AddCIBreak(ctx, params)
}

// advanceCIBreak runs the failed jobs again when Mobius did not do it yet and no newer run of the workflow passed,
// stores the result of the new attempt, and ends the break when a newer run of the workflow passed. A hard break also
// ends when a later attempt of the failed run passed: the list of runs gives the newest attempt of each run. A new
// attempt that completes with a conclusion other than success, cancelled and skipped, and a newer failed run, make the
// break hard. A cancelled or skipped attempt (a newer push cancels it) gives no result: the next completed run of the
// workflow decides the break. Mobius stores the mark of the re-run after the call, so a run that already has a new attempt (for example
// after a stop between the call and the mark) is not run again. When GitHub refuses the call with a 4xx status (for
// example with no permission, or for a run older than 30 days), the failure stays a failure, so the break is hard.
// Another error returns, and the next poll tries again. A hard break that ends with a passed run closes its open task
// issue first, unless an open failure record with a place on a pull request has the same issue: the issue then closes
// only with its fix. A hard break with a task issue also ends when that issue is closed or GitHub does not find it, with no
// ended run.
func (e *Engine) advanceCIBreak(ctx context.Context, repository github.Repository, ciBreak store.CiBreak, byID map[int64]*gh.WorkflowRun, newest *gh.WorkflowRun) error {
	run := byID[ciBreak.RunID]
	passedAgain := newest != nil && newest.GetID() > ciBreak.RunID && newest.GetConclusion() == "success"
	if ciBreak.RerunAt == "" {
		if passedAgain {
			return e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: ciBreak.Result, EndedRunID: newest.GetID(), EndedAt: now(), ID: ciBreak.ID})
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
	rerunCompleted := run != nil && run.GetStatus() == "completed" && int64(run.GetRunAttempt()) > ciBreak.RunAttempt &&
		run.GetConclusion() != "cancelled" && run.GetConclusion() != "skipped"
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
		if closed, err := e.ciBreakIssueClosed(ctx, repository, ciBreak.Issue); err != nil || !closed {
			return err
		}
		return e.endCIBreak(ctx, ciBreak, store.EndCIBreakParams{Result: ciBreak.Result, EndedAt: now(), ID: ciBreak.ID})
	}
	return nil
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
