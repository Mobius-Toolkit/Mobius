package engine

import (
	"context"
	"fmt"
	"log"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const failureLinesFormat = "\n````\n%s\n````\n"

// createCIFailureIssues creates the task issue of each open failure record of repository that has two or more places and
// no issue. The issue goes below the maintenance Workstream and has no label. A repository with no open maintenance
// Workstream gets no issue, and the next poll tries again. The number of an issue that GitHub created but did not add
// below the Workstream is stored as a negative number, and the next poll adds the issue below the Workstream. A 4xx
// answer of GitHub is no error.
func (e *Engine) createCIFailureIssues(ctx context.Context, repository github.Repository) error {
	failures, err := e.queries.ListCIFailuresWithoutIssue(ctx, repository.FullName)
	if err != nil {
		return err
	}
	pending, err := e.queries.ListCIFailuresNotBelowWorkstream(ctx, repository.FullName)
	if err != nil || len(failures)+len(pending) == 0 {
		return err
	}
	workstream, err := maintenanceWorkstream(ctx, repository)
	if err != nil || workstream == 0 {
		return err
	}
	changed := false
	for _, failure := range pending {
		added, err := e.addCIFailureIssueBelow(ctx, repository, workstream, failure)
		changed = changed || added
		if err != nil {
			return err
		}
	}
	for _, failure := range failures {
		number, err := e.createCIFailureIssue(ctx, repository, workstream, failure)
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

// createCIFailureIssue creates the issue of failure and stores its number. It gives the number, or 0 when GitHub created
// no issue. When GitHub creates the issue but does not add it below the Workstream, the stored number is negative.
func (e *Engine) createCIFailureIssue(ctx context.Context, repository github.Repository, workstream int64, failure store.CiFailure) (int, error) {
	places, err := e.queries.ListCIFailurePlaces(ctx, failure.ID)
	if err != nil {
		return 0, err
	}
	title := "Fix the CI failure of " + ciFailureName(failure)
	number, err := e.addIssue(ctx, repository, workstream, issueInput{Title: title, Body: ciFailureBody(repository, failure, places), Parent: workstream}, nil)
	if number != 0 {
		stored := int64(number)
		if err != nil {
			stored = -stored
		}
		if storeErr := e.queries.SetCIFailureIssue(ctx, store.SetCIFailureIssueParams{Issue: stored, ID: failure.ID}); storeErr != nil {
			return number, storeErr
		}
	}
	if refusedByGitHub(err) {
		log.Printf("create the issue of the CI failure %d of %s: %v", failure.ID, repository.FullName, err)
		return number, nil
	}
	return number, err
}

// addCIFailureIssueBelow adds the issue of failure below the Workstream, and stores its number as a positive number. It
// tells whether the issue is below the Workstream. A 4xx answer of GitHub is no error, and the next poll tries again.
func (e *Engine) addCIFailureIssueBelow(ctx context.Context, repository github.Repository, workstream int64, failure store.CiFailure) (bool, error) {
	number := -failure.Issue
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
	if err := e.queries.SetCIFailureIssue(ctx, store.SetCIFailureIssueParams{Issue: number, ID: failure.ID}); err != nil {
		return issue != nil, err
	}
	return issue != nil, nil
}

// ciFailureName gives the test of failure, else its location, else the start of its reason.
func ciFailureName(failure store.CiFailure) string {
	switch {
	case failure.Test != "":
		return failure.Test
	case failure.Location != "":
		return failure.Location
	}
	reason := []rune(failure.Reason)
	return string(reason[:min(len(reason), 80)])
}

// ciFailureBody gives the body of the issue of failure: the reason, the test, the location, and for each place the pull
// request or the run, the links of the failed jobs and the failed lines. The body has less than issueBodyBytes bytes:
// the lines of a place lose their first lines.
func ciFailureBody(repository github.Repository, failure store.CiFailure, places []store.ListCIFailurePlacesRow) string {
	var head strings.Builder
	fmt.Fprintf(&head, "The same CI failure occurs in %d places.\n\nReason: %s\n", len(places), strings.TrimSpace(failure.Reason))
	if failure.Test != "" {
		fmt.Fprintf(&head, "Test: %s\n", failure.Test)
	}
	if failure.Location != "" {
		fmt.Fprintf(&head, "Location: %s\n", failure.Location)
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
