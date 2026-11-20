package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// initFailureBody gives the body of the issue of a failed init script: the task issue, the session, the commit, the
// time limit of a timeout and the end of the output. The body has less than issueBodyBytes bytes.
func initFailureBody(failure store.InitFailure, timeLimit string) string {
	reason := "failed"
	if failure.Result == "timeout" {
		reason = "did not end in the time limit"
	}
	head := fmt.Sprintf("The setup script `.mobius/init` %s.\n\nRepository: %s\nTask issue: %s\nSession: %d\nCommit: %s\n", reason, failure.Repository, issueLink(failure.Repository, failure.TaskIssue), failure.Session, failure.CommitSha)
	if failure.Result == "timeout" {
		head += fmt.Sprintf("Time limit: %s\n", timeLimit)
	}
	if failure.Output == "" {
		return head
	}
	return head + fmt.Sprintf("\nEnd of the output:\n\n````\n%s\n````\n", jobLogTail(strings.TrimRight(failure.Output, "\n"), max(0, issueBodyBytes-len(head)-len("\nEnd of the output:\n\n````\n\n````\n"))))
}

// recordInitFailure stores the failure of the init script of the session a, when its repository has no open record.
// The output has no local path.
func (e *Engine) recordInitFailure(ctx context.Context, a *Agent, j *job, result, output string) error {
	commit, err := runner.RevParse(ctx, e.config.DataDir, a.spec.Dir, "HEAD")
	if err != nil {
		return err
	}
	return e.queries.AddInitFailure(ctx, store.AddInitFailureParams{
		Repository: j.task.Repository,
		TaskIssue:  j.task.Issue,
		Session:    a.id,
		CommitSha:  commit,
		Result:     result,
		Output:     tail(e.hidePaths(output, a.spec.Dir), logTail),
		CreatedAt:  now(),
	})
}

// createInitFailureIssue acts on the open record of an init failure of repository, with the rules of
// createCheckTimeoutIssue.
func (e *Engine) createInitFailureIssue(ctx context.Context, repository github.Repository) error {
	failure, err := e.queries.GetOpenInitFailure(ctx, repository.FullName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if failure.Issue > 0 {
		closed, err := e.ciBreakIssueClosed(ctx, repository, failure.Issue)
		if err != nil || !closed {
			return err
		}
		return e.queries.EndInitFailure(ctx, store.EndInitFailureParams{EndedAt: now(), ID: failure.ID})
	}
	if failure.IssueRow.Valid {
		return nil
	}
	workstream, err := e.organizationMaintenance(ctx, repository.Owner())
	if Refused(err) {
		log.Printf("find the maintenance Workstream for %s: %v", repository.FullName, err)
		return nil
	}
	if err != nil {
		return err
	}
	if workstream.number == 0 {
		return e.queries.EndInitFailure(ctx, store.EndInitFailureParams{EndedAt: now(), ID: failure.ID})
	}
	title := fmt.Sprintf("Fix the setup script .mobius/init of %s", repository.FullName)
	return e.createRecordIssue(ctx, repository, workstream, title, initFailureBody(failure, e.config.CheckTimeout.String()), true, func(q *store.Queries, row int64) error {
		return q.SetInitFailureIssueRow(ctx, store.SetInitFailureIssueRowParams{IssueRow: sql.NullInt64{Int64: row, Valid: true}, ID: failure.ID})
	})
}
