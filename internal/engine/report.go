package engine

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	reportStart      = "<!-- report -->"
	reportEnd        = "<!-- /report -->"
	screenshotsStart = "<!-- screenshots -->"
	screenshotsEnd   = "<!-- /screenshots -->"
	// maxReport is the longest report in characters. GitHub refuses a description of more than 65,535 characters.
	maxReport = 20_000
)

type reportInput struct {
	Report string `json:"report"`
}

// submitReport stores the report of the Implementer as the report of the task. Mobius writes it in the description of
// the pull request at the end of the turn.
func (e *Engine) submitReport(ctx context.Context, c caller, repository github.Repository, input reportInput) (string, error) {
	if empty(input.Report) {
		return "", refuse("report must not be empty.")
	}
	if utf8.RuneCountInString(input.Report) > maxReport {
		return "", refuse("The report has %d characters. The limit is %d. Shorten the report and call `submit_report` again.", utf8.RuneCountInString(input.Report), maxReport)
	}
	for _, marker := range []string{reportStart, reportEnd, screenshotsStart, screenshotsEnd} {
		if strings.Contains(input.Report, marker) {
			return "", refuse("The report contains the marker `%s`. Mobius uses this marker in the description. Remove it and call `submit_report` again.", marker)
		}
	}
	task, err := e.workstreamTask(ctx, repository, c.agent.spec.WorkstreamRepository, c.workstream, c.agent.spec.Issue.Int64)
	if err != nil {
		return "", err
	}
	if err := e.queries.SetTaskReport(ctx, store.SetTaskReportParams{Report: input.Report, ID: task.ID}); err != nil {
		return "", err
	}
	c.agent.mu.Lock()
	c.agent.report = input.Report
	c.agent.mu.Unlock()
	return "Mobius stored the report. Mobius writes it in the description of the pull request at the end of the turn.", nil
}

// reportSection gives the section of the description with the report of the live task of the issue, or "" when the task
// has no report.
func (e *Engine) reportSection(ctx context.Context, repository github.Repository, task store.Task) (string, error) {
	live, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if errors.Is(err, sql.ErrNoRows) || err == nil && live.Report == "" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	report, err := e.withMark(ctx, caller{role: ImplementerRole, repository: task.Repository, issue: task.Issue}, repository.FullName, live.Report)
	if err != nil {
		return "", err
	}
	return reportStart + "\n### Report\n\n" + report + "\n" + reportEnd, nil
}

// withReport gives body with section in place of the report section of body. Without a report section, it puts section
// before the screenshots section, or at the end of body. It gives body with no change when section is empty.
func withReport(body, section string) string {
	if section == "" {
		return body
	}
	end := strings.Index(body, reportEnd)
	if end >= 0 {
		if start := strings.LastIndex(body[:end], reportStart); start >= 0 {
			return body[:start] + section + body[end+len(reportEnd):]
		}
	}
	if start := strings.Index(body, screenshotsStart); start >= 0 {
		before := strings.TrimRight(body[:start], "\n")
		if before != "" {
			before += "\n\n"
		}
		return before + section + "\n\n" + body[start:]
	}
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return section
	}
	return body + "\n\n" + section
}

// writeReport writes section in the description of the pull request number, when the description has no such section.
func writeReport(ctx context.Context, repository github.Repository, number int64, section string) error {
	if section == "" {
		return nil
	}
	current, err := repository.PullRequest(ctx, number)
	if err != nil {
		return err
	}
	body := withReport(current.GetBody(), section)
	if body == current.GetBody() {
		return nil
	}
	return repository.SetPullRequestBody(ctx, number, body)
}

// reportPrompt gives the section "Report" of the prompt of a round, or "" when the task has no report.
func reportPrompt(report string) string {
	if report == "" {
		return ""
	}
	return "\n# Report\n\n" + report + "\n\nIf your change makes this report wrong, call `submit_report` with the full new report.\n\n"
}
