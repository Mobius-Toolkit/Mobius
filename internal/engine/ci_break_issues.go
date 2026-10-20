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
// a flaky break. The issue goes below the maintenance Workstream. A repository with no open maintenance Workstream gets
// no issue, and the next poll tries again. The issue of a flaky break has mobius:ready, so the usual task flow starts
// it. A hard break gets no label. The number of an issue that GitHub created but did not add below the Workstream is
// stored as a negative number, and the next poll adds the issue below the Workstream.
func (e *Engine) createCIBreakIssues(ctx context.Context, repository github.Repository) error {
	breaks, err := e.queries.ListCIBreaksWithoutIssue(ctx, repository.FullName)
	if err != nil {
		return err
	}
	pending, err := e.queries.ListCIBreaksNotBelowWorkstream(ctx, repository.FullName)
	if err != nil || len(breaks)+len(pending) == 0 {
		return err
	}
	workstream, err := maintenanceWorkstream(ctx, repository)
	if err != nil || workstream == 0 {
		return err
	}
	changed := false
	for _, ciBreak := range pending {
		added, err := e.addCIBreakIssueBelow(ctx, repository, workstream, ciBreak)
		changed = changed || added
		if err != nil {
			return err
		}
	}
	for _, ciBreak := range breaks {
		number, err := e.createCIBreakIssue(ctx, repository, workstream, ciBreak)
		changed = changed || number != 0
		if err != nil {
			return err
		}
	}
	if changed {
		if err := e.recopyTrees(ctx, repository, workstream); err != nil {
			log.Printf("copy the tree of %s#%d: %v", repository.FullName, workstream, err)
		}
	}
	return nil
}

// createCIBreakIssue creates the issue of ciBreak and stores its number in ciBreak and in each open failure record with
// a place of ciBreak and no issue. When such a record already has an issue that is open, ciBreak takes that issue and
// Mobius creates none. It gives the number, or 0 when GitHub created no issue. A 4xx answer of GitHub is no error. When GitHub refuses
// the list of jobs, it creates no issue. When GitHub creates the issue but does not add it below the Workstream, the
// stored number is negative.
func (e *Engine) createCIBreakIssue(ctx context.Context, repository github.Repository, workstream int64, ciBreak store.CiBreak) (int, error) {
	shared, err := e.queries.GetCIBreakFailureIssue(ctx, ciBreak.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err == nil {
		number := max(shared, -shared)
		issue, err := repository.Issue(ctx, number)
		if refusedByGitHub(err) {
			log.Printf("read the issue #%d of %s: %v", number, repository.FullName, err)
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		if issue != nil && issue.GetState() == "open" {
			if err := e.queries.SetCIBreakIssue(ctx, store.SetCIBreakIssueParams{Issue: shared, ID: ciBreak.ID}); err != nil {
				return 0, err
			}
			e.publishCIStops(ciBreak.Result)
			return 0, nil
		}
	}
	body, err := ciBreakBody(ctx, repository, ciBreak)
	if refusedByGitHub(err) {
		log.Printf("read the jobs of the run %d of %s: %v", ciBreak.RunID, repository.FullName, err)
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var labels []string
	if ciBreak.Result == "flaky" {
		labels = []string{readyLabel}
	}
	title := fmt.Sprintf("Fix the failed workflow \"%s\" on %s", ciBreak.WorkflowName, repository.DefaultBranch)
	number, err := e.addIssue(ctx, repository, workstream, issueInput{Title: title, Body: body, Parent: workstream}, labels)
	if number != 0 {
		stored := int64(number)
		if err != nil {
			stored = -stored
		}
		if storeErr := e.queries.SetCIBreakIssue(ctx, store.SetCIBreakIssueParams{Issue: stored, ID: ciBreak.ID}); storeErr != nil {
			return number, storeErr
		}
		if storeErr := e.queries.SetCIBreakFailuresIssue(ctx, store.SetCIBreakFailuresIssueParams{Issue: stored, CiBreak: ciBreak.ID}); storeErr != nil {
			return number, storeErr
		}
		e.publishCIStops(ciBreak.Result)
	}
	if refusedByGitHub(err) {
		log.Printf("create the issue of the CI break of %s: %v", repository.FullName, err)
		return number, nil
	}
	return number, err
}

// addCIBreakIssueBelow adds the issue of ciBreak below the Workstream, and stores its number as a positive number. It
// tells whether the issue is below the Workstream. A 4xx answer of GitHub is no error, and the next poll tries again.
func (e *Engine) addCIBreakIssueBelow(ctx context.Context, repository github.Repository, workstream int64, ciBreak store.CiBreak) (bool, error) {
	number := -ciBreak.Issue
	issue, err := repository.Issue(ctx, number)
	if err == nil && issue != nil {
		_, _, err = repository.Client.SubIssue.Add(ctx, repository.Owner(), repository.Name(), workstream, gh.SubIssueRequest{SubIssueID: issue.GetID(), ReplaceParent: new(true)})
	}
	if refusedByGitHub(err) {
		log.Printf("add the issue #%d of %s below #%d: %v", number, repository.FullName, workstream, err)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := e.queries.SetCIBreakIssue(ctx, store.SetCIBreakIssueParams{Issue: number, ID: ciBreak.ID}); err != nil {
		return issue != nil, err
	}
	e.publishCIStops(ciBreak.Result)
	return issue != nil, nil
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
		head.WriteString("Result of the re-run: hard. The re-run of the failed jobs failed again, or GitHub refused the re-run.\n")
	}
	var sections, logs []string
	used := head.Len()
	for _, job := range jobs {
		if !failedRunConclusion(job.GetConclusion()) {
			continue
		}
		var section strings.Builder
		fmt.Fprintf(&section, "\nFailed job: %s\n", job.GetName())
		for _, step := range job.Steps {
			if failedRunConclusion(step.GetConclusion()) {
				fmt.Fprintf(&section, "Failed step: %s\n", step.GetName())
				break
			}
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
	number := max(ciBreak.Issue, -ciBreak.Issue)
	issue, err := repository.Issue(ctx, number)
	if err == nil && (issue == nil || issue.GetState() == "closed") {
		return true, nil
	}
	if err == nil {
		_, err = repository.CloseIssue(ctx, number, "completed")
	}
	if err == nil {
		text := fmt.Sprintf("The workflow \"%s\" passed again on %s in the run %s. Mobius closes this issue.", ciBreak.WorkflowName, repository.DefaultBranch, passed.GetHTMLURL())
		return true, e.addOrKeepComment(ctx, repository, number, text)
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
			stop[max(ciBreak.Issue, -ciBreak.Issue)] = true
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
		if ciBreak.Issue <= 0 {
			continue
		}
		had, err := e.queries.HasTask(ctx, store.HasTaskParams{Repository: repository.FullName, Issue: ciBreak.Issue})
		if err != nil {
			return err
		}
		if had {
			continue
		}
		workstream, err := maintenanceWorkstream(ctx, repository)
		if err != nil || workstream == 0 {
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
		if err := e.dispatch(ctx, repository, issue, workstream, appLogin(repository.AppSlug)); err != nil {
			return err
		}
		if _, err := e.startImplementer(ctx, repository, workstream, ciBreak.Issue, ciTaskInstructions, sql.NullInt64{}); err != nil {
			log.Printf("start the Implementer of #%d of %s: %v", ciBreak.Issue, repository.FullName, err)
		}
	}
	return nil
}
