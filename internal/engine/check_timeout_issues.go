package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// checkTimeoutBody gives the body of the issue of timeout: the task issue, the session, the time limit and the end of
// the output. The body has less than issueBodyBytes bytes.
func checkTimeoutBody(timeout store.CheckTimeout) string {
	head := fmt.Sprintf("The local check `.mobius/check` did not end in the time limit.\n\nTask issue: %s\nSession: %d\nTime limit: %s\n", issueLink(timeout.Repository, timeout.TaskIssue), timeout.Session, timeout.Timeout)
	if timeout.Output == "" {
		return head
	}
	return head + fmt.Sprintf("\nEnd of the output:\n\n````\n%s\n````\n", jobLogTail(strings.TrimRight(timeout.Output, "\n"), max(0, issueBodyBytes-len(head)-len("\nEnd of the output:\n\n````\n\n````\n"))))
}

// hidePaths replaces the worktree, the data directory and the home directory in output with fixed names. The longest
// path comes first.
func (e *Engine) hidePaths(output, worktree string) string {
	pairs := []string{worktree, "<worktree>", e.config.DataDir, "<data_dir>"}
	if home, err := os.UserHomeDir(); err == nil {
		pairs = append(pairs, home, "<home>")
	}
	var replacements []string
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i] != "" {
			replacements = append(replacements, pairs[i], pairs[i+1])
		}
	}
	return strings.NewReplacer(replacements...).Replace(output)
}

// recordCheckTimeout stores the timeout of the local check of the session a, when its repository has no open record.
// The output has no local path.
func (e *Engine) recordCheckTimeout(ctx context.Context, a *Agent, j *job, output string) error {
	return e.queries.AddCheckTimeout(ctx, store.AddCheckTimeoutParams{
		Repository: j.task.Repository,
		TaskIssue:  j.task.Issue,
		Session:    a.id,
		Timeout:    e.config.CheckTimeout.String(),
		Output:     tail(e.hidePaths(output, a.spec.Dir), logTail),
		CreatedAt:  now(),
	})
}

// createCheckTimeoutIssue acts on the open record of a check timeout of repository. A record with no issue gets an
// issue in repository below the maintenance Workstream of the organization, with mobius:ready. An organization with no
// open maintenance Workstream gets no issue, and the record ends. When Mobius cannot find the issue repository, the
// record stays open and the next poll tries again. The issue is a row of issues, and its create goes to GitHub as an
// outgoing change: the record keeps the id of the row until the send stores the number. The record ends when its issue
// is closed, or GitHub does not find it.
func (e *Engine) createCheckTimeoutIssue(ctx context.Context, repository github.Repository) error {
	timeout, err := e.queries.GetOpenCheckTimeout(ctx, repository.FullName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if timeout.Issue > 0 {
		closed, err := e.ciBreakIssueClosed(ctx, repository, timeout.Issue)
		if err != nil || !closed {
			return err
		}
		return e.queries.EndCheckTimeout(ctx, store.EndCheckTimeoutParams{EndedAt: now(), ID: timeout.ID})
	}
	if timeout.IssueRow.Valid {
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
		return e.queries.EndCheckTimeout(ctx, store.EndCheckTimeoutParams{EndedAt: now(), ID: timeout.ID})
	}
	title := fmt.Sprintf("Examine the time of the local check of %s", repository.FullName)
	return e.createRecordIssue(ctx, repository, workstream, title, checkTimeoutBody(timeout), []string{readyLabel}, func(q *store.Queries, row int64) error {
		return q.SetCheckTimeoutIssueRow(ctx, store.SetCheckTimeoutIssueRowParams{IssueRow: sql.NullInt64{Int64: row, Valid: true}, ID: timeout.ID})
	})
}
