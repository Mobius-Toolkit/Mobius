package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const failureLinesFormat = "\n````\n%s\n````\n"

// The Diagnoser output has no length bound. These limits, in characters, keep the head of the issue body and the title
// below the limits of GitHub.
const (
	failureReasonChars = 2000
	failureNameChars   = 200
)

// createCIFailureIssues creates the task issue of each open failure record of repository that has two or more places and
// no issue. The issue stays in repository, goes below the maintenance Workstream of the organization and has
// mobius:ready. An organization with no open maintenance Workstream, or with no issue repository that Mobius can find,
// gets no issue, and the next poll tries again. The issue is a row of issues, and its create goes to GitHub as an
// outgoing change: the record keeps the id of the row until the send stores the number.
func (e *Engine) createCIFailureIssues(ctx context.Context, repository github.Repository) error {
	failures, err := e.queries.ListCIFailuresWithoutIssue(ctx, repository.FullName)
	if err != nil || len(failures) == 0 {
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
	for _, failure := range failures {
		if err := e.createCIFailureIssue(ctx, repository, workstream, failure); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) createCIFailureIssue(ctx context.Context, repository github.Repository, workstream issueAt, failure store.CiFailure) error {
	places, err := e.queries.ListCIFailurePlaces(ctx, failure.ID)
	if err != nil {
		return err
	}
	title := "Fix the CI failure of " + cutChars(ciFailureName(failure), failureNameChars)
	return e.createRecordIssue(ctx, repository, workstream, title, ciFailureBody(repository, failure, places), []string{readyLabel}, func(q *store.Queries, row int64) error {
		return q.SetCIFailureIssueRow(ctx, store.SetCIFailureIssueRowParams{IssueRow: sql.NullInt64{Int64: row, Valid: true}, ID: failure.ID})
	})
}

// ciFailureName gives the test of failure, else its location, else the start of its reason.
func ciFailureName(failure store.CiFailure) string {
	switch {
	case failure.Test != "":
		return failure.Test
	case failure.Location != "":
		return failure.Location
	}
	return cutChars(failure.Reason, 80)
}

func cutChars(text string, limit int) string {
	runes := []rune(text)
	return string(runes[:min(len(runes), limit)])
}

// ciFailureBody gives the body of the issue of failure: the reason, the test, the location, and for each place the pull
// request or the run, the links of the failed jobs and the failed lines. The body has less than issueBodyBytes bytes:
// the lines of a place lose their first lines.
func ciFailureBody(repository github.Repository, failure store.CiFailure, places []store.ListCIFailurePlacesRow) string {
	var head strings.Builder
	fmt.Fprintf(&head, "The same CI failure occurs in %d places.\n\nReason: %s\n", len(places), cutChars(strings.TrimSpace(failure.Reason), failureReasonChars))
	if failure.Test != "" {
		fmt.Fprintf(&head, "Test: %s\n", cutChars(failure.Test, failureNameChars))
	}
	if failure.Location != "" {
		fmt.Fprintf(&head, "Location: %s\n", cutChars(failure.Location, failureNameChars))
	}
	sections := make([]string, len(places))
	used := head.Len()
	for i, place := range places {
		var section strings.Builder
		if place.PullRequest != 0 {
			fmt.Fprintf(&section, "\nPull request: https://github.com/%s/pull/%d\nHead: %s\n", repository.FullName, place.PullRequest, place.Head)
		} else {
			fmt.Fprintf(&section, "\nRun on %s: %s\nHead: %s\n", repository.DefaultBranch, place.RunUrl, place.Head)
		}
		for _, url := range strings.Split(place.JobUrls, "\n") {
			if url != "" {
				fmt.Fprintf(&section, "Failed job: %s\n", url)
			}
		}
		sections[i] = section.String()
		used += len(sections[i]) + len(fmt.Sprintf(failureLinesFormat, ""))
	}
	body := head.String()
	for i, place := range places {
		body += sections[i] + fmt.Sprintf(failureLinesFormat, jobLogTail(place.Lines, max(0, issueBodyBytes-used)/len(places)))
	}
	return body
}
