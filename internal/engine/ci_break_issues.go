package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// issueBodyBytes is below the limit of GitHub for the body of an issue: 65,536 characters.
const issueBodyBytes = 60000

const logFormat = "\nEnd of the job log:\n\n````\n%s\n````\n"

// refusedByGitHub tells whether err is an answer of GitHub with a 4xx status, for example for a missing permission.
// The next poll tries a refused call again.
func refusedByGitHub(err error) bool {
	var refused *gh.ErrorResponse
	return errors.As(err, &refused) && refused.Response.StatusCode < 500
}

// createCIBreakIssues creates the task issue of each break of repository that needs one: a hard break that is open, and
// a flaky break. The issue stays in repository and goes below the maintenance Workstream of the organization. An
// organization with no open maintenance Workstream, or with no issue repository that Mobius can find, gets no issue,
// and the next poll tries again. The issue of a flaky break has mobius:ready, so the usual task flow starts
// it. A hard break gets no label. The issue is a row of issues, and its create goes to GitHub as an outgoing change: the
// break keeps the id of the row until the send stores the number. A break whose issue fails gets a log line, and the
// next poll tries it again. It does not stop the other breaks or the poll.
func (e *Engine) createCIBreakIssues(ctx context.Context, repository github.Repository) error {
	breaks, err := e.queries.ListCIBreaksWithoutIssue(ctx, repository.FullName)
	if err != nil || len(breaks) == 0 {
		return err
	}
	workstream, err := e.organizationMaintenance(ctx, repository.Owner())
	if Refused(err) {
		log.Printf("find the maintenance Workstream for %s: %v", repository.FullName, err)
		return nil
	}
	if err != nil || workstream.number == 0 {
		return err
	}
	for _, ciBreak := range breaks {
		if err := e.createCIBreakIssue(ctx, repository, workstream, ciBreak); err != nil {
			log.Printf("create the issue of the CI break %d of %s: %v", ciBreak.ID, repository.FullName, err)
		}
	}
	return nil
}

// createCIBreakIssue creates the issue of ciBreak. The send of the create stores its number in ciBreak and in each open
// failure record with a place of ciBreak and no issue. When such a record already has an issue that is open, ciBreak
// takes that issue and Mobius creates none. When GitHub refuses the list of jobs, Mobius creates no issue.
func (e *Engine) createCIBreakIssue(ctx context.Context, repository github.Repository, workstream issueAt, ciBreak store.CiBreak) error {
	shared, err := e.queries.GetCIBreakFailureIssue(ctx, ciBreak.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		issue, err := repository.Issue(ctx, shared)
		if refusedByGitHub(err) {
			log.Printf("read the issue #%d of %s: %v", shared, repository.FullName, err)
			return nil
		}
		if err != nil {
			return err
		}
		if issue != nil && issue.GetState() == "open" {
			if err := e.queries.SetCIBreakIssue(ctx, store.SetCIBreakIssueParams{Issue: shared, ID: ciBreak.ID}); err != nil {
				return err
			}
			e.publishCIStops(ciBreak.Result)
			return nil
		}
	}
	body, err := ciBreakBody(ctx, repository, ciBreak)
	if refusedByGitHub(err) {
		log.Printf("read the jobs of the run %d of %s: %v", ciBreak.RunID, repository.FullName, err)
		return nil
	}
	if err != nil {
		return err
	}
	title := fmt.Sprintf("Fix the failed workflow \"%s\" on %s", ciBreak.WorkflowName, repository.DefaultBranch)
	return e.createRecordIssue(ctx, repository, workstream, title, body, ciBreak.Result == "flaky", func(q *store.Queries, row int64) error {
		return q.SetCIBreakIssueRow(ctx, store.SetCIBreakIssueRowParams{IssueRow: sql.NullInt64{Int64: row, Valid: true}, ID: ciBreak.ID})
	})
}

// ciBreakBody gives the body of the issue of ciBreak: the run, the result of the re-run, and each failed job of the
// failed attempt with its failed step and the end of its log. The body has less than issueBodyBytes bytes: a long log
// loses its first lines. A job log that GitHub refuses with a 4xx status, for example an expired log, gives a job with
// no log.
func ciBreakBody(ctx context.Context, repository github.Repository, ciBreak store.CiBreak) (string, error) {
	jobs, err := repository.RunAttemptJobs(ctx, ciBreak.RunID, ciBreak.RunAttempt)
	if err != nil {
		return "", err
	}
	var head strings.Builder
	fmt.Fprintf(&head, "The workflow \"%s\" failed on %s.\n\nRun: %s\nAttempt: %d\n\n", ciBreak.WorkflowName, repository.DefaultBranch, ciBreak.RunUrl, ciBreak.RunAttempt)
	if ciBreak.Result == "flaky" {
		head.WriteString("Result of the re-run: flaky. The re-run of the failed jobs passed.\n")
	} else {
		head.WriteString("Result of the re-run: hard. One of these causes applies: the re-run of the failed jobs failed, GitHub refused the re-run, or a newer run of the workflow failed.\n")
	}
	var sections, logs []string
	used := head.Len()
	for _, job := range jobs {
		timeLimit, err := jobTimeLimit(ctx, repository, job)
		if err != nil {
			return "", err
		}
		if timeLimit == "" && !failedRunConclusion(job.GetConclusion()) {
			continue
		}
		var section strings.Builder
		fmt.Fprintf(&section, "\nFailed job: %s\n", job.GetName())
		if step := failedBreakStep(job, timeLimit); step != "" {
			fmt.Fprintf(&section, "Failed step: %s\n", step)
		}
		if timeLimit != "" {
			fmt.Fprintf(&section, "Time limit: %s\n", timeLimit)
		}
		sections = append(sections, section.String())
		jobLog, err := repository.JobLog(ctx, job.GetID())
		if err != nil && !refusedByGitHub(err) {
			return "", err
		}
		logs = append(logs, strings.TrimRight(jobLog, "\n"))
		used += len(section.String()) + len(fmt.Sprintf(logFormat, ""))
	}
	body := head.String()
	for i, section := range sections {
		body += section
		if logs[i] != "" {
			body += fmt.Sprintf(logFormat, jobLogTail(logs[i], max(0, issueBodyBytes-used)/len(sections)))
		}
	}
	return body, nil
}

// jobLogTail gives the last lines of jobLog, at most jobLogLines lines and at most limit bytes. It always keeps the end of
// the last line.
func jobLogTail(jobLog string, limit int) string {
	lines := strings.Split(jobLog, "\n")
	lines = lines[max(0, len(lines)-jobLogLines):]
	size := 0
	first := len(lines)
	for first > 0 && size+len(lines[first-1])+1 <= limit {
		first--
		size += len(lines[first]) + 1
	}
	if first == len(lines) {
		last := lines[len(lines)-1]
		return strings.ToValidUTF8(last[max(0, len(last)-limit):], "")
	}
	return strings.Join(lines[first:], "\n")
}

// closeCIBreakIssue closes the task issue of ciBreak with a comment that links to the passed run, when the issue is
// open. It tells whether the issue needs no more close: it is closed, or GitHub does not find it. A 4xx answer of GitHub
// is no error, and the break stays open until the next poll tries again.
func (e *Engine) closeCIBreakIssue(ctx context.Context, repository github.Repository, ciBreak store.CiBreak, passed *gh.WorkflowRun) (bool, error) {
	number := ciBreak.Issue
	issue, err := repository.Issue(ctx, number)
	if err == nil && (issue == nil || issue.GetState() == "closed") {
		return true, nil
	}
	if err == nil {
		var ownWrite int64
		ownWrite, err = addOwnWrite(ctx, e.queries, repository.FullName, number, stateField, "", closedChange)
		if err != nil {
			return false, err
		}
		if _, err = repository.CloseIssue(ctx, number, "completed"); err != nil {
			if delErr := e.queries.DeleteOwnWrite(ctx, ownWrite); delErr != nil {
				return false, errors.Join(err, delErr)
			}
		}
	}
	if err == nil {
		if changed, stateErr := e.queries.SetIssueStateByRepoNumber(ctx, store.SetIssueStateByRepoNumberParams{
			State:      "closed",
			Repository: repository.FullName,
			Number:     number,
		}); stateErr != nil {
			return false, stateErr
		} else if changed > 0 {
			e.publish(Change{Workstreams: true})
		}
		text := fmt.Sprintf("The workflow \"%s\" passed again on %s in the run %s. Mobius closes this issue.", ciBreak.WorkflowName, repository.DefaultBranch, passed.GetHTMLURL())
		return true, e.postComment(ctx, repository, number, text)
	}
	if github.NotFound(err) {
		return true, nil
	}
	if refusedByGitHub(err) {
		log.Printf("close the issue #%d of %s: %v", number, repository.FullName, err)
		return false, nil
	}
	return false, err
}

// ciBreakIssueClosed tells whether the task issue number of a hard break is closed, or GitHub does not find it. A 4xx
// answer of GitHub is no error, and the break stays open until the next poll tries again.
func (e *Engine) ciBreakIssueClosed(ctx context.Context, repository github.Repository, number int64) (bool, error) {
	issue, err := repository.Issue(ctx, number)
	if err == nil {
		return issue == nil || issue.GetState() == "closed", nil
	}
	if github.NotFound(err) {
		return true, nil
	}
	if refusedByGitHub(err) {
		log.Printf("read the issue #%d of %s: %v", number, repository.FullName, err)
		return false, nil
	}
	return false, err
}

// ciStop gives the task issue numbers of the open hard breaks of repository that have an issue, also an issue that is
// not below the Workstream yet. While it has some, Mobius starts no task in repository except these issues. An open
// hard break with no issue and an open diagnosis also starts the stop: the map then holds the number 0, which is no
// issue.
func (e *Engine) ciStop(ctx context.Context, repository string) (map[int64]bool, error) {
	breaks, err := e.queries.ListOpenHardCIBreaks(ctx, repository)
	if err != nil {
		return nil, err
	}
	stop := map[int64]bool{}
	for _, ciBreak := range breaks {
		if ciBreak.Issue != 0 {
			stop[ciBreak.Issue] = true
		}
	}
	diagnosing, err := e.queries.HasDiagnosingHardCIBreak(ctx, repository)
	if err != nil {
		return nil, err
	}
	if diagnosing {
		stop[0] = true
	}
	return stop, nil
}

// CIStops gives the open hard breaks that have a task issue, in the order of the repositories and then of the break
// ids. Each one stops the dispatch of its repository.
func (e *Engine) CIStops(ctx context.Context) ([]store.CiBreak, error) {
	return e.queries.ListAllOpenHardCIBreaks(ctx)
}

// publishCIStops tells the listeners that the stop changed, when result is the result of a hard break.
func (e *Engine) publishCIStops(result string) {
	if result == "hard" {
		e.publish(Change{CIStops: true})
	}
}

// ciStopRefusal gives the refusal for a start of a task of issue number while the stop of repository is on, or nil.
func (e *Engine) ciStopRefusal(ctx context.Context, repository github.Repository, number int64) error {
	stop, err := e.ciStop(ctx, repository.FullName)
	if err != nil || len(stop) == 0 || stop[number] {
		return err
	}
	var links []string
	for _, issue := range slices.Sorted(maps.Keys(stop)) {
		if issue != 0 {
			links = append(links, issueLink(repository.FullName, issue))
		}
	}
	if len(links) == 0 {
		return refuse("Mobius started no task: the CI of %s has a hard break. Mobius makes its task issue.", repository.DefaultBranch)
	}
	return refuse("Mobius started no task: the CI of %s has a hard break. Fix it first in %s.", repository.DefaultBranch, strings.Join(links, ", "))
}

// ciTaskInstructions are the instructions of the Implementer of a CI task.
const ciTaskInstructions = "Mobius started this task for a hard break of the CI of the default branch. The issue body gives the failed run, the failed jobs, the failed steps and the end of the logs."

// startCIBreakTasks dispatches the task issue of each open hard break that never had a task, and starts its Implementer
// at once, also with Autopilot off. An issue that had a task, also an ended one, gets no new start: the Lead or the
// Owner continues it. When the Implementer fails to start, the task stays dispatched, and the Lead can start it. The
// drain holds the start.
func (e *Engine) startCIBreakTasks(ctx context.Context, repository github.Repository) error {
	if e.draining() {
		return nil
	}
	breaks, err := e.queries.ListOpenHardCIBreaks(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, ciBreak := range breaks {
		if ciBreak.Issue == 0 {
			continue
		}
		had, err := e.queries.HasTask(ctx, store.HasTaskParams{Repository: repository.FullName, Issue: ciBreak.Issue})
		if err != nil {
			return err
		}
		if had {
			continue
		}
		workstream, err := e.organizationMaintenance(ctx, repository.Owner())
		if Refused(err) {
			log.Printf("find the maintenance Workstream for %s: %v", repository.FullName, err)
			return nil
		}
		if err != nil || workstream.number == 0 {
			return err
		}
		issue, err := repository.Issue(ctx, ciBreak.Issue)
		if refusedByGitHub(err) {
			log.Printf("read the issue #%d of %s: %v", ciBreak.Issue, repository.FullName, err)
			continue
		}
		if err != nil {
			return err
		}
		if issue == nil || issue.GetState() != "open" {
			continue
		}
		off, err := e.holdStart(ctx, repository.FullName, ciBreak.Issue)
		if err != nil {
			return err
		}
		if !off {
			off, err = e.holdRepairs(ctx, repository.FullName, ciBreak.Issue)
			if err != nil {
				return err
			}
		}
		if off {
			continue
		}
		if err := e.dispatch(ctx, repository, issue, workstream.repository.FullName, workstream.number, appLogin(repository.AppSlug)); err != nil {
			return err
		}
		if _, err := e.startImplementer(ctx, repository, workstream.repository.FullName, workstream.number, ciBreak.Issue, ciTaskInstructions, sql.NullInt64{}); err != nil {
			log.Printf("start the Implementer of #%d of %s: %v", ciBreak.Issue, repository.FullName, err)
		}
	}
	return nil
}
