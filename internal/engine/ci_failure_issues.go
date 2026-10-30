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

// The Diagnoser output has no length bound. These limits, in characters, keep the head of the issue body and the title
// below the limits of GitHub.
const (
	failureReasonChars = 2000
	failureNameChars   = 200
)

// createCIFailureIssues creates the task issue of each open failure record of repository that has two or more places and
// no issue. The issue stays in repository, goes below the maintenance Workstream of the organization and has
// mobius:ready. An organization with no open maintenance Workstream, or with no issue repository that Mobius can find,
// gets no issue, and the next poll tries again. The number of an issue that GitHub created but did not add
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
	workstream, err := e.organizationMaintenance(ctx, repository.Owner())
	if Refused(err) {
		log.Printf("find the maintenance Workstream for %s: %v", repository.FullName, err)
		return nil
	}
	if err != nil || workstream.number == 0 {
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
		if err := e.recopyTrees(ctx, workstream.repository, workstream.number); err != nil {
			log.Printf("copy the tree of %s#%d: %v", workstream.repository.FullName, workstream.number, err)
		}
	}
	return nil
}

// createCIFailureIssue creates the issue of failure and stores its number. It gives the number, or 0 when GitHub created
// no issue. When GitHub creates the issue but does not add it below the Workstream, the stored number is negative.
func (e *Engine) createCIFailureIssue(ctx context.Context, repository github.Repository, workstream issueAt, failure store.CiFailure) (int, error) {
	places, err := e.queries.ListCIFailurePlaces(ctx, failure.ID)
	if err != nil {
		return 0, err
	}
	title := "Fix the CI failure of " + cutChars(ciFailureName(failure), failureNameChars)
	number, err := e.addIssueAt(ctx, repository, workstream, newIssue{title: title, body: ciFailureBody(repository, failure, places), parent: workstream}, []string{readyLabel})
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
func (e *Engine) addCIFailureIssueBelow(ctx context.Context, repository github.Repository, workstream issueAt, failure store.CiFailure) (bool, error) {
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
	if issue != nil && hasLabel(issue, readyLabel) {
		// The add below the Workstream does not change the list of the issues with mobius:ready, and dispatchReady can
		// have stored its ETag before the issue was below the Workstream. The new label event makes dispatchReady read
		// the list again. An issue with no mobius:ready keeps no label, because a label that a person removed must not
		// come back.
		if err := repository.RemoveLabel(ctx, number, readyLabel); err != nil {
			return issue != nil, labelWriteError(repository, number, "remove", err)
		}
		if err := repository.AddLabel(ctx, number, readyLabel); err != nil {
			return issue != nil, labelWriteError(repository, number, "add", err)
		}
	}
	if err := e.queries.SetCIFailureIssue(ctx, store.SetCIFailureIssueParams{Issue: number, ID: failure.ID}); err != nil {
		return issue != nil, err
	}
	return issue != nil, nil
}

// labelWriteError gives nil for a 4xx answer of GitHub, so the poll does not stop and the next poll writes the label
// again with the negative number of the issue. It gives each other error.
func labelWriteError(repository github.Repository, number int64, action string, err error) error {
	if !refusedByGitHub(err) {
		return err
	}
	log.Printf("%s the label %s of #%d of %s: %v", action, readyLabel, number, repository.FullName, err)
	return nil
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
