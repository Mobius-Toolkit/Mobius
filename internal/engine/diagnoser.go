package engine

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

//go:embed prompts/diagnoser.md
var diagnoserPrompt string

// failureLines is the number of lines of a failure that Mobius keeps in a place.
const failureLines = 100

// diagnoserKey is the key in e.stops of the Diagnoser worker of the repository.
type diagnoserKey string

type reportFailureInput struct {
	Reason              string `json:"reason"`
	Test                string `json:"test"`
	Location            string `json:"location"`
	Lines               string `json:"lines"`
	CausedByPullRequest bool   `json:"caused_by_pull_request"`
	SameRecord          int64  `json:"same_record"`
}

// pullRequestDiagnosis gives the section of the items of a fix round with the diagnosis of the failed head of the pull
// request. It tells that the round waits while the Diagnoser of the head has not ended. A head with no row gets a row
// and a Diagnoser. The section is empty when the Diagnoser reported no failure.
func (e *Engine) pullRequestDiagnosis(ctx context.Context, repository github.Repository, pullRequest int64, head string) (string, bool, error) {
	diagnosis, err := e.queries.GetPullRequestCIDiagnosis(ctx, store.GetPullRequestCIDiagnosisParams{Repository: repository.FullName, PullRequest: pullRequest, Head: head})
	if errors.Is(err, sql.ErrNoRows) {
		diagnosis = store.CiDiagnosis{}
		_, err = e.queries.AddCIDiagnosis(ctx, store.AddCIDiagnosisParams{Repository: repository.FullName, PullRequest: pullRequest, Head: head, CreatedAt: now()})
	}
	if err != nil {
		return "", false, err
	}
	if diagnosis.EndedAt == "" {
		e.startDiagnoser(repository.FullName)
		return "", true, nil
	}
	return diagnosisSection(diagnosis.Reasons), false, nil
}

// endedDiagnosis gives the section of the items of a fix round with the diagnosis of an ended Diagnoser of the head. It
// waits for no Diagnoser and starts none. The section is empty when the head has no ended row.
func (e *Engine) endedDiagnosis(ctx context.Context, repository github.Repository, pullRequest int64, head string) (string, error) {
	diagnosis, err := e.queries.GetPullRequestCIDiagnosis(ctx, store.GetPullRequestCIDiagnosisParams{Repository: repository.FullName, PullRequest: pullRequest, Head: head})
	if errors.Is(err, sql.ErrNoRows) || err == nil && diagnosis.EndedAt == "" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return diagnosisSection(diagnosis.Reasons), nil
}

func diagnosisSection(reasons string) string {
	if reasons == "" {
		return ""
	}
	return "\nDiagnosis of the failed jobs:\n" + reasons
}

// startDiagnoser starts the Diagnoser worker of repository, when none runs. The worker runs one Diagnoser at a time,
// for the oldest row with no end, until no row is left. A row that the worker misses stays open, and the poll of its
// task starts the worker again.
func (e *Engine) startDiagnoser(repository string) {
	e.diagnosersMu.Lock()
	defer e.diagnosersMu.Unlock()
	if e.diagnosers[repository] {
		return
	}
	key := diagnoserKey(repository)
	e.diagnosers[repository] = e.startWorker(key, func(ctx context.Context) {
		e.diagnoseRows(ctx, repository)
		e.diagnosersMu.Lock()
		defer e.diagnosersMu.Unlock()
		e.stop(key)
		delete(e.diagnosers, repository)
	})
}

// diagnoseRows runs a Diagnoser for each row of repository with no end, the oldest first. A row ends after its
// session, also after a failed session. A stop of Mobius keeps the row open.
func (e *Engine) diagnoseRows(ctx context.Context, repository string) {
	for ctx.Err() == nil {
		row, err := e.queries.NextCIDiagnosis(ctx, repository)
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			log.Printf("read the next diagnosis of %s: %v", repository, err)
			return
		}
		failure := e.diagnose(ctx, row)
		if ctx.Err() != nil {
			return
		}
		if failure != nil {
			log.Printf("Diagnoser of %s#%d: %v", repository, row.PullRequest, failure)
		}
		if err := e.queries.EndCIDiagnosis(ctx, store.EndCIDiagnosisParams{EndedAt: now(), ID: row.ID}); err != nil {
			log.Printf("end the diagnosis %d of %s: %v", row.ID, repository, err)
			return
		}
	}
}

// diagnose runs the session of a Diagnoser for row in a worktree that is detached at the head of the row.
func (e *Engine) diagnose(ctx context.Context, row store.CiDiagnosis) error {
	a, err := e.addAgent(ctx, Spec{Role: DiagnoserRole, Organization: owner(row.Repository), Repository: row.Repository, PullRequest: row.PullRequest})
	if err != nil {
		return err
	}
	ended := context.WithoutCancel(ctx)
	if err := e.queries.SetCIDiagnosisSession(ctx, store.SetCIDiagnosisSessionParams{Session: sql.NullInt64{Int64: a.id, Valid: true}, ID: row.ID}); err != nil {
		return a.Fail(ended, err)
	}
	a.spec.Dir = runner.DiagnoserDir(e.config.DataDir, row.Repository, a.id)
	logs := filepath.Join(e.config.DataDir, "scratch", strconv.FormatInt(a.id, 10))
	err = e.diagnoseTurn(ctx, a, row, logs)
	a.closeHarness()
	err = errors.Join(err, e.removeCopy(ended, row.Repository, a.spec.Dir), os.RemoveAll(logs))
	switch {
	case ctx.Err() != nil:
		return a.End(ended, "stopped")
	case errors.Is(err, errHung):
		return errors.Join(err, a.endHung(ended))
	case err != nil:
		return a.Fail(ended, err)
	}
	return a.End(ended, "done")
}

// diagnoseTurn makes the worktree of the Diagnoser a, writes the logs of the failed jobs to the directory logs, and runs
// its turn.
func (e *Engine) diagnoseTurn(ctx context.Context, a *Agent, row store.CiDiagnosis, logs string) error {
	repository, err := e.repository(row.Repository)
	if err != nil {
		return err
	}
	var ciBreak store.CiBreak
	var place string
	if row.CiBreak != 0 {
		ciBreak, err = e.queries.GetCIBreak(ctx, row.CiBreak)
		place = fmt.Sprintf("# Run on the default branch\n\nRun: %s\nAttempt: %d\nHead commit: %s\n", ciBreak.RunUrl, ciBreak.RunAttempt, row.Head)
	} else {
		place, err = pullRequestPlace(ctx, repository, row)
	}
	if err != nil {
		return err
	}
	token, err := repository.Token(ctx)
	if err != nil {
		return err
	}
	e.gitMu.Lock()
	err = e.fetch(ctx, repository, token)
	if err == nil {
		err = runner.AddDetachedWorktree(ctx, e.config.DataDir, row.Repository, a.spec.Dir, row.Head)
	}
	e.gitMu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(logs, 0o750); err != nil {
		return err
	}
	var jobs, urls string
	if row.CiBreak != 0 {
		jobs, urls, err = breakJobs(ctx, repository, ciBreak, logs)
	} else {
		jobs, urls, err = failedJobs(ctx, repository, row.Head, logs)
	}
	if err != nil {
		return err
	}
	failures, err := e.openFailures(ctx, row.Repository)
	if err != nil {
		return err
	}
	a.jobURLs = urls
	if err := a.open(ctx); err != nil {
		return err
	}
	prompt := fmt.Sprintf("%s\n%s%s\n# Failed jobs\n\n%s%s", diagnoserPrompt, serverSection(), place, jobs, failures)
	return a.Prompt(ctx, prompt, nil)
}

func pullRequestPlace(ctx context.Context, repository github.Repository, row store.CiDiagnosis) (string, error) {
	pullRequest, err := repository.PullRequest(ctx, row.PullRequest)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# Pull request\n\n#%d %s\nHead commit: %s\n", pullRequest.GetNumber(), pullRequest.GetHTMLURL(), row.Head), nil
}

// breakJobs gives the failed jobs of the failed attempt of the run of ciBreak as a prompt text, and their links, one for
// each line. It writes the logs as failedJobs does.
func breakJobs(ctx context.Context, repository github.Repository, ciBreak store.CiBreak, logs string) (string, string, error) {
	jobs, err := repository.RunAttemptJobs(ctx, ciBreak.RunID, ciBreak.RunAttempt)
	if err != nil {
		return "", "", err
	}
	var text strings.Builder
	var urls []string
	for _, job := range jobs {
		timeLimit, err := jobTimeLimit(ctx, repository, job)
		if err != nil {
			return "", "", err
		}
		if timeLimit == "" && !failedRunConclusion(job.GetConclusion()) {
			continue
		}
		fmt.Fprintf(&text, "- Job \"%s\"\n  Link: %s\n", job.GetName(), job.GetHTMLURL())
		if step := failedBreakStep(job, timeLimit); step != "" {
			fmt.Fprintf(&text, "  Failed step: %s\n", step)
		}
		if timeLimit != "" {
			fmt.Fprintf(&text, "  Time limit: %s\n", timeLimit)
		}
		if err := writeJobLog(ctx, repository, job.GetID(), logs, &text); err != nil {
			return "", "", err
		}
		urls = append(urls, job.GetHTMLURL())
	}
	return text.String() + "\n", strings.Join(urls, "\n"), nil
}

// failedStepName gives the name of the first failed step of job, or "".
func failedStepName(job *gh.WorkflowJob) string {
	for _, step := range job.Steps {
		if failedRunConclusion(step.GetConclusion()) {
			return step.GetName()
		}
	}
	return ""
}

// writeJobLog writes the log of the job id to the file job-<id>.log in logs and adds the path of the file to text. A job
// log that GitHub refuses with a 4xx status gives no file.
func writeJobLog(ctx context.Context, repository github.Repository, id int64, logs string, text *strings.Builder) error {
	jobLog, err := repository.JobLog(ctx, id)
	if refusedByGitHub(err) {
		return nil
	}
	if err != nil {
		return err
	}
	path := filepath.Join(logs, fmt.Sprintf("job-%d.log", id))
	if err := os.WriteFile(path, []byte(jobLog), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(text, "  Log file: %s\n", path)
	return nil
}

// failedJobs gives the failed check runs of the head as a prompt text, and their links, one for each line. The full log
// of the job of a check run of GitHub Actions is the file job-<id>.log in logs. A job log that GitHub refuses with a 4xx
// status gives a job with no file.
func failedJobs(ctx context.Context, repository github.Repository, head, logs string) (string, string, error) {
	runs, err := failedCheckRuns(ctx, repository, head)
	if err != nil {
		return "", "", err
	}
	steps, err := failedSteps(ctx, repository, head)
	if err != nil {
		return "", "", err
	}
	var text strings.Builder
	var urls []string
	for _, run := range runs {
		fmt.Fprintf(&text, "- Job \"%s\"\n  Link: %s\n", run.GetName(), run.GetHTMLURL())
		if step := steps[run.GetID()]; step != "" {
			fmt.Fprintf(&text, "  Failed step: %s\n", step)
		}
		if run.GetApp().GetSlug() == githubActions {
			if err := writeJobLog(ctx, repository, run.GetID(), logs, &text); err != nil {
				return "", "", err
			}
		}
		urls = append(urls, run.GetHTMLURL())
	}
	return text.String() + "\n", strings.Join(urls, "\n"), nil
}

// failedSteps gives the name of the first failed step of each failed job of the newest failed workflow runs of the head,
// by the id of the job. The job of GitHub Actions has the id of its check run. A run whose jobs GitHub refuses with a
// 4xx status gives no step.
func failedSteps(ctx context.Context, repository github.Repository, head string) (map[int64]string, error) {
	workflows, err := repository.WorkflowRuns(ctx, head)
	if err != nil {
		return nil, err
	}
	steps := map[int64]string{}
	for _, run := range newestWorkflowRuns(workflows) {
		if !failedWorkflowRun(run) {
			continue
		}
		jobs, err := repository.RunAttemptJobs(ctx, run.GetID(), int64(max(run.GetRunAttempt(), 1)))
		if err != nil && !refusedByGitHub(err) {
			return nil, err
		}
		for _, job := range jobs {
			if step := failedStepName(job); step != "" {
				steps[job.GetID()] = step
			}
		}
	}
	return steps, nil
}

// openFailures gives the open failure records of repository as a prompt section, or "" for no record.
func (e *Engine) openFailures(ctx context.Context, repository string) (string, error) {
	records, err := e.queries.ListOpenCIFailures(ctx, repository)
	if err != nil || len(records) == 0 {
		return "", err
	}
	var section strings.Builder
	section.WriteString("# Open failure records\n\n")
	for _, record := range records {
		fmt.Fprintf(&section, "- Record %d, %d places. Test: %s. Location: %s. Reason: %s\n", record.ID, record.Places, record.Test, record.Location, record.Reason)
	}
	return section.String(), nil
}

// reportFailure saves one failure that the Diagnoser found in the place of its session. The place is the pull request and
// the head of the diagnosis. The failure goes in the reasons of the diagnosis, which the fix round reads. A failure that
// the pull request did not cause also goes in a record, and so does each failure of a break of the default branch. The
// record is the one that same_record names, else an open record with the same test and location, else a new record.
func (e *Engine) reportFailure(ctx context.Context, c caller, _ github.Repository, input reportFailureInput) (string, error) {
	if empty(input.Reason) || empty(input.Lines) {
		return "", refuse("reason and lines must not be empty.")
	}
	diagnosis, err := e.queries.GetCIDiagnosisBySession(ctx, sql.NullInt64{Int64: c.session, Valid: true})
	if err != nil {
		return "", err
	}
	causedByPullRequest := input.CausedByPullRequest && diagnosis.CiBreak == 0
	result := "Saved. The pull request caused the failure, so it has no record."
	if !causedByPullRequest {
		id, err := e.failureRecord(ctx, diagnosis, c.agent.jobURLs, input)
		if err != nil {
			return "", err
		}
		result = fmt.Sprintf("Saved in the record %d.", id)
	}
	var reasons strings.Builder
	fmt.Fprintf(&reasons, "- %s", strings.TrimSpace(input.Reason))
	if input.Test != "" {
		fmt.Fprintf(&reasons, " Test: %s.", input.Test)
	}
	if input.Location != "" {
		fmt.Fprintf(&reasons, " Location: %s.", input.Location)
	}
	if causedByPullRequest {
		reasons.WriteString(" The change of the pull request caused this failure.\n")
	} else {
		reasons.WriteString(" The change of the pull request did not cause this failure.\n")
	}
	return result, e.queries.AddCIDiagnosisReason(ctx, store.AddCIDiagnosisReasonParams{Reasons: reasons.String(), ID: diagnosis.ID})
}

// failureRecord adds the place of diagnosis to the record of the failure of input, and gives the id of the record.
func (e *Engine) failureRecord(ctx context.Context, diagnosis store.CiDiagnosis, jobURLs string, input reportFailureInput) (int64, error) {
	var id int64
	switch {
	case input.SameRecord < 0:
		return 0, refuse("same_record must not be negative.")
	case input.SameRecord > 0:
		record, err := e.queries.GetOpenCIFailure(ctx, store.GetOpenCIFailureParams{ID: input.SameRecord, Repository: diagnosis.Repository})
		if errors.Is(err, sql.ErrNoRows) {
			return 0, refuse("%d is not an open failure record of this repository.", input.SameRecord)
		}
		if err != nil {
			return 0, err
		}
		id = record.ID
	case input.Test != "" && input.Location != "":
		record, err := e.queries.FindOpenCIFailure(ctx, store.FindOpenCIFailureParams{Repository: diagnosis.Repository, Test: input.Test, Location: input.Location})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		id = record.ID
	}
	if id == 0 {
		var err error
		id, err = e.queries.AddCIFailure(ctx, store.AddCIFailureParams{Repository: diagnosis.Repository, Reason: strings.TrimSpace(input.Reason), Test: input.Test, Location: input.Location, CreatedAt: now()})
		if err != nil {
			return 0, err
		}
	}
	lines := strings.Split(strings.TrimRight(input.Lines, "\n"), "\n")
	return id, e.queries.AddCIFailurePlace(ctx, store.AddCIFailurePlaceParams{
		Failure:     id,
		PullRequest: diagnosis.PullRequest,
		CiBreak:     diagnosis.CiBreak,
		Head:        diagnosis.Head,
		JobUrls:     jobURLs,
		Lines:       strings.Join(lines[max(0, len(lines)-failureLines):], "\n"),
		CreatedAt:   now(),
	})
}
