package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	gh "github.com/google/go-github/v92/github"

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
// record stays open and the next poll tries again. The number of an issue that GitHub created but did not add below the Workstream is
// stored as a negative number, and the next poll adds it. Until the Workstream is open, that record stays open. The record ends when its issue is closed, or GitHub does not
// find it. A 4xx answer of GitHub is no error, and the next poll tries again.
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
	workstream, err := e.organizationMaintenance(ctx, repository.Owner())
	if Refused(err) {
		log.Printf("find the maintenance Workstream for %s: %v", repository.FullName, err)
		return nil
	}
	if err != nil {
		return err
	}
	if workstream.number == 0 {
		if timeout.Issue < 0 {
			return nil
		}
		return e.queries.EndCheckTimeout(ctx, store.EndCheckTimeoutParams{EndedAt: now(), ID: timeout.ID})
	}
	var changed bool
	if timeout.Issue < 0 {
		changed, err = e.addCheckTimeoutIssueBelow(ctx, repository, workstream, timeout)
	} else {
		changed, err = e.createCheckTimeoutIssueBelow(ctx, repository, workstream, timeout)
	}
	if changed {
		if copyErr := e.recopyTrees(ctx, workstream.repository, workstream.number); copyErr != nil {
			log.Printf("copy the tree of %s#%d: %v", workstream.repository.FullName, workstream.number, copyErr)
		}
	}
	return err
}

func (e *Engine) createCheckTimeoutIssueBelow(ctx context.Context, repository github.Repository, workstream issueAt, timeout store.CheckTimeout) (bool, error) {
	title := fmt.Sprintf("Examine the time of the local check of %s", repository.FullName)
	number, err := e.addIssueAt(ctx, repository, workstream, newIssue{title: title, body: checkTimeoutBody(timeout), parent: workstream}, []string{readyLabel})
	if number != 0 {
		stored := int64(number)
		if err != nil {
			stored = -stored
		}
		if storeErr := e.queries.SetCheckTimeoutIssue(ctx, store.SetCheckTimeoutIssueParams{Issue: stored, ID: timeout.ID}); storeErr != nil {
			return true, storeErr
		}
	}
	if refusedByGitHub(err) {
		log.Printf("create the issue of the check timeout of %s: %v", repository.FullName, err)
		return number != 0, nil
	}
	return number != 0, err
}

func (e *Engine) addCheckTimeoutIssueBelow(ctx context.Context, repository github.Repository, workstream issueAt, timeout store.CheckTimeout) (bool, error) {
	number := -timeout.Issue
	issue, err := repository.Issue(ctx, number)
	if err == nil && issue != nil {
		_, _, err = workstream.repository.Client.SubIssue.Add(ctx, workstream.repository.Owner(), workstream.repository.Name(), workstream.number, gh.SubIssueRequest{SubIssueID: issue.GetID(), ReplaceParent: new(true)})
	}
	if refusedByGitHub(err) {
		log.Printf("add the issue #%d of %s below %s#%d: %v", number, repository.FullName, workstream.repository.FullName, workstream.number, err)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return issue != nil, e.queries.SetCheckTimeoutIssue(ctx, store.SetCheckTimeoutIssueParams{Issue: number, ID: timeout.ID})
}
