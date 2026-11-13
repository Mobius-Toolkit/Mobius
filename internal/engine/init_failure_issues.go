package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	gh "github.com/google/go-github/v92/github"

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
	workstream, err := e.organizationMaintenance(ctx, repository.Owner())
	if Refused(err) {
		log.Printf("find the maintenance Workstream for %s: %v", repository.FullName, err)
		return nil
	}
	if err != nil {
		return err
	}
	if workstream.number == 0 {
		if failure.Issue < 0 {
			return nil
		}
		return e.queries.EndInitFailure(ctx, store.EndInitFailureParams{EndedAt: now(), ID: failure.ID})
	}
	var changed bool
	if failure.Issue < 0 {
		changed, err = e.addInitFailureIssueBelow(ctx, repository, workstream, failure)
	} else {
		changed, err = e.createInitFailureIssueBelow(ctx, repository, workstream, failure)
	}
	if changed {
		if copyErr := e.recopyTrees(ctx, workstream.repository, workstream.number); copyErr != nil {
			log.Printf("copy the tree of %s#%d: %v", workstream.repository.FullName, workstream.number, copyErr)
		}
	}
	return err
}

func (e *Engine) createInitFailureIssueBelow(ctx context.Context, repository github.Repository, workstream issueAt, failure store.InitFailure) (bool, error) {
	title := fmt.Sprintf("Fix the setup script .mobius/init of %s", repository.FullName)
	number, err := e.addIssueAt(ctx, repository, workstream, newIssue{title: title, body: initFailureBody(failure, e.config.CheckTimeout.String()), parent: workstream}, []string{readyLabel})
	if number != 0 {
		stored := int64(number)
		if err != nil {
			stored = -stored
		}
		if storeErr := e.queries.SetInitFailureIssue(ctx, store.SetInitFailureIssueParams{Issue: stored, ID: failure.ID}); storeErr != nil {
			return true, storeErr
		}
	}
	if refusedByGitHub(err) {
		log.Printf("create the issue of the init failure of %s: %v", repository.FullName, err)
		return number != 0, nil
	}
	return number != 0, err
}

func (e *Engine) addInitFailureIssueBelow(ctx context.Context, repository github.Repository, workstream issueAt, failure store.InitFailure) (bool, error) {
	number := -failure.Issue
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
	return issue != nil, e.queries.SetInitFailureIssue(ctx, store.SetInitFailureIssueParams{Issue: number, ID: failure.ID})
}
