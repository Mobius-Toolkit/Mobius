package engine

import (
	"context"
	"errors"
	"slices"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// readDefaultBranchCI keeps the breaks of the CI of the default branch. A break of a workflow starts when its newest
// completed run failed. Mobius runs the failed jobs of that run again one time. The result of the break is flaky when
// the new attempt passes, and hard when it fails. A break ends when a newer run of the workflow passes.
func (e *Engine) readDefaultBranchCI(ctx context.Context, repository github.Repository) error {
	runs, err := repository.BranchWorkflowRuns(ctx, repository.DefaultBranch)
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
		if !open[workflow] && (run.GetConclusion() == "failure" || run.GetConclusion() == "timed_out") {
			if err := e.startCIBreak(ctx, repository, run); err != nil {
				return err
			}
		}
	}
	return nil
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

// advanceCIBreak runs the failed jobs again when Mobius did not do it yet, stores the result of the new attempt, and
// ends the break when a newer run of the workflow passed. Mobius stores the mark of the re-run after the call, so a
// run that already has a new attempt (for example after a stop between the call and the mark) is not run again.
// When GitHub refuses the call (for example with no permission, or for a run older than 30 days), Mobius stores the
// mark too. The break then has no result, so it is neither flaky nor hard.
func (e *Engine) advanceCIBreak(ctx context.Context, repository github.Repository, ciBreak store.CiBreak, byID map[int64]*gh.WorkflowRun, newest *gh.WorkflowRun) error {
	run := byID[ciBreak.RunID]
	if ciBreak.RerunAt == "" {
		if run == nil || int64(run.GetRunAttempt()) <= ciBreak.RunAttempt {
			var refused *gh.ErrorResponse
			if err := repository.RerunFailedJobs(ctx, ciBreak.RunID); err != nil && !errors.As(err, &refused) {
				return err
			}
		}
		return e.queries.SetCIBreakRerun(ctx, store.SetCIBreakRerunParams{RerunAt: now(), ID: ciBreak.ID})
	}
	if ciBreak.Result == "" && run != nil && run.GetStatus() == "completed" && int64(run.GetRunAttempt()) > ciBreak.RunAttempt {
		switch run.GetConclusion() {
		case "success":
			return e.queries.EndCIBreak(ctx, store.EndCIBreakParams{Result: "flaky", EndedRunID: run.GetID(), EndedAt: now(), ID: ciBreak.ID})
		case "failure", "timed_out":
			ciBreak.Result = "hard"
			if err := e.queries.SetCIBreakResult(ctx, store.SetCIBreakResultParams{Result: ciBreak.Result, ID: ciBreak.ID}); err != nil {
				return err
			}
		}
	}
	if newest != nil && newest.GetID() > ciBreak.RunID && newest.GetConclusion() == "success" {
		return e.queries.EndCIBreak(ctx, store.EndCIBreakParams{Result: ciBreak.Result, EndedRunID: newest.GetID(), EndedAt: now(), ID: ciBreak.ID})
	}
	return nil
}
