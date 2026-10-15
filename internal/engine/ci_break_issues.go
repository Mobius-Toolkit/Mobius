package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// refusedByGitHub tells whether err is an answer of GitHub with a 4xx status, for example for a missing permission.
// The next poll tries a refused call again.
func refusedByGitHub(err error) bool {
	var refused *gh.ErrorResponse
	return errors.As(err, &refused) && refused.Response.StatusCode < 500
}

// createCIBreakIssues creates the task issue of each break of repository that needs one: a hard break that is open, and
// a flaky break. The issue goes below the maintenance Workstream. A repository with no open maintenance Workstream gets
// no issue, and the next poll tries again. The issue of a flaky break has mobius:ready, so the usual task flow starts
// it. A hard break gets no label.
func (e *Engine) createCIBreakIssues(ctx context.Context, repository github.Repository) error {
	breaks, err := e.queries.ListCIBreaksWithoutIssue(ctx, repository.FullName)
	if err != nil || len(breaks) == 0 {
		return err
	}
	workstream, err := maintenanceWorkstream(ctx, repository)
	if err != nil || workstream == 0 {
		return err
	}
	created := false
	for _, ciBreak := range breaks {
		number, err := e.createCIBreakIssue(ctx, repository, workstream, ciBreak)
		created = created || number != 0
		if err != nil {
			return err
		}
	}
	if created {
		if err := e.recopyTrees(ctx, repository, workstream); err != nil {
			log.Printf("copy the tree of %s#%d: %v", repository.FullName, workstream, err)
		}
	}
	return nil
}

// createCIBreakIssue creates the issue of ciBreak and stores its number. It gives the number, or 0 when GitHub created
// no issue. A 4xx answer of GitHub is no error.
func (e *Engine) createCIBreakIssue(ctx context.Context, repository github.Repository, workstream int64, ciBreak store.CiBreak) (int, error) {
	body, err := ciBreakBody(ctx, repository, ciBreak)
	if err != nil {
		return 0, err
	}
	var labels []string
	if ciBreak.Result == "flaky" {
		labels = []string{readyLabel}
	}
	title := fmt.Sprintf("Fix the failed workflow \"%s\" on %s", ciBreak.WorkflowName, repository.DefaultBranch)
	number, err := addIssue(ctx, repository, issueInput{Title: title, Body: body, Parent: workstream}, labels)
	if number != 0 {
		if storeErr := e.queries.SetCIBreakIssue(ctx, store.SetCIBreakIssueParams{Issue: int64(number), ID: ciBreak.ID}); storeErr != nil {
			return number, storeErr
		}
	}
	if refusedByGitHub(err) {
		log.Printf("create the issue of the CI break of %s: %v", repository.FullName, err)
		return number, nil
	}
	return number, err
}

// ciBreakBody gives the body of the issue of ciBreak: the run, the result of the re-run, and each failed job of the
// failed attempt with its failed step and the end of its log. When GitHub refuses the list of jobs, the body has no job.
func ciBreakBody(ctx context.Context, repository github.Repository, ciBreak store.CiBreak) (string, error) {
	jobs, err := repository.RunAttemptJobs(ctx, ciBreak.RunID, ciBreak.RunAttempt)
	if refusedByGitHub(err) {
		log.Printf("read the jobs of the run %d of %s: %v", ciBreak.RunID, repository.FullName, err)
	} else if err != nil {
		return "", err
	}
	var body strings.Builder
	fmt.Fprintf(&body, "The workflow \"%s\" failed on %s.\n\nRun: %s\nAttempt: %d\n\n", ciBreak.WorkflowName, repository.DefaultBranch, ciBreak.RunUrl, ciBreak.RunAttempt)
	if ciBreak.Result == "flaky" {
		body.WriteString("Result of the re-run: flaky. The re-run of the failed jobs passed.\n")
	} else {
		body.WriteString("Result of the re-run: hard. The re-run of the failed jobs failed again, or GitHub refused the re-run.\n")
	}
	for _, job := range jobs {
		if !failedRunConclusion(job.GetConclusion()) {
			continue
		}
		fmt.Fprintf(&body, "\nFailed job: %s\n", job.GetName())
		for _, step := range job.Steps {
			if failedRunConclusion(step.GetConclusion()) {
				fmt.Fprintf(&body, "Failed step: %s\n", step.GetName())
				break
			}
		}
		// A job log that cannot download leaves the issue with the job and the step.
		if jobLog, err := repository.JobLog(ctx, job.GetID()); err == nil {
			lines := strings.Split(strings.TrimRight(jobLog, "\n"), "\n")
			fmt.Fprintf(&body, "\nEnd of the job log:\n\n````\n%s\n````\n", strings.Join(lines[max(0, len(lines)-jobLogLines):], "\n"))
		}
	}
	return body.String(), nil
}

// closeCIBreakIssue closes the task issue of ciBreak with a comment that links to the passed run, when the issue is
// open. A closed issue and an issue that GitHub does not find need no close. A 4xx answer of GitHub is no error.
func (e *Engine) closeCIBreakIssue(ctx context.Context, repository github.Repository, ciBreak store.CiBreak, passed *gh.WorkflowRun) error {
	issue, err := repository.Issue(ctx, ciBreak.Issue)
	if err == nil && issue != nil && issue.GetState() != "closed" {
		_, err = repository.CloseIssue(ctx, ciBreak.Issue, "completed")
		if err == nil {
			text := fmt.Sprintf("The workflow \"%s\" passed again on %s in the run %s. Mobius closes this issue.", ciBreak.WorkflowName, repository.DefaultBranch, passed.GetHTMLURL())
			return e.addOrKeepComment(ctx, repository, ciBreak.Issue, text)
		}
	}
	if refusedByGitHub(err) {
		log.Printf("close the issue #%d of %s: %v", ciBreak.Issue, repository.FullName, err)
		return nil
	}
	return err
}
