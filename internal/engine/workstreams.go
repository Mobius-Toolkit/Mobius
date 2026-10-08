package engine

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// closedText is the comment on each issue and pull request that the close of a Workstream closes.
const closedText = "Workstream closed"

// CompleteWorkstream closes the open Workstream number of repository as completed, when the Workstream issue has
// sub-issues and each one is closed. Then it does the work of a close on GitHub.
func (e *Engine) CompleteWorkstream(ctx context.Context, repositoryName string, number int64) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return err
	}
	if issue == nil || issue.GetState() != "open" || !hasLabel(issue, workstreamLabel) {
		return refuse("The issue is not an open Workstream.")
	}
	tasks, err := repository.SubIssues(ctx, number)
	if err != nil {
		return err
	}
	if len(tasks) == 0 || slices.ContainsFunc(tasks, func(task *gh.Issue) bool { return task.GetState() != "closed" }) {
		return refuse("The Workstream has no task, or a task is open.")
	}
	closed, err := repository.CloseIssue(ctx, number, "completed")
	if err != nil {
		return err
	}
	if _, err := e.updateCopy(ctx, repository, closed, nil, false); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	// The poll of the close event runs the close again, and the second run changes nothing.
	return e.closeWorkstream(ctx, repository, number)
}

// ClosureItem is an issue or a pull request that the close of a Workstream as "won't do" closes.
type ClosureItem struct {
	PullRequest bool
	Number      int64
	Title       string
	URL         string
}

// WorkstreamClosure gives the items that CloseWorkstreamWontDo closes for the open Workstream number of repository.
func (e *Engine) WorkstreamClosure(ctx context.Context, repositoryName string, number int64) ([]ClosureItem, error) {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return nil, err
	}
	workstreamIssue, err := e.openWorkstream(ctx, repository, number)
	if err != nil {
		return nil, err
	}
	return e.closureItems(ctx, repository, workstreamIssue)
}

// CloseWorkstreamWontDo closes the open Workstream number of repository and the items of WorkstreamClosure as "won't
// do", also when the Workstream has open issues. Then it does the work of a close on GitHub.
func (e *Engine) CloseWorkstreamWontDo(ctx context.Context, repositoryName string, number int64) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	workstreamIssue, err := e.openWorkstream(ctx, repository, number)
	if err != nil {
		return err
	}
	if err := e.stopWorkstream(ctx, repository, number); err != nil {
		return err
	}
	items, err := e.closureItems(ctx, repository, workstreamIssue)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("The Workstream #%d closed as \"won't do\".", number)
	var closed *gh.Issue
	for _, item := range items {
		if _, err := repository.AddComment(ctx, item.Number, text); err != nil {
			return err
		}
		if err := repository.AddLabel(ctx, item.Number, wontDoLabel); err != nil {
			return err
		}
		if item.PullRequest {
			err = repository.ClosePullRequest(ctx, item.Number)
		} else {
			closed, err = repository.CloseIssue(ctx, item.Number, "not_planned")
		}
		if err != nil {
			return err
		}
	}
	// The Workstream issue is the last item.
	if _, err := e.updateCopy(ctx, repository, closed, nil, false); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return e.closeWorkstream(ctx, repository, number)
}

func (e *Engine) openWorkstream(ctx context.Context, repository github.Repository, number int64) (*gh.Issue, error) {
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return nil, err
	}
	if issue == nil || issue.GetState() != "open" || !hasLabel(issue, workstreamLabel) {
		return nil, refuse("The issue is not an open Workstream.")
	}
	return issue, nil
}

// closureItems gives the open items that a close as "won't do" closes: the open pull requests of the tasks, also
// when the issue of the task is closed, the open task issues, and the Workstream issue last. It walks the issues below
// the Workstream as closeWorkstream does.
func (e *Engine) closureItems(ctx context.Context, repository github.Repository, workstreamIssue *gh.Issue) ([]ClosureItem, error) {
	workstream := int64(workstreamIssue.GetNumber())
	var issues []*gh.Issue
	parents := []int64{workstream}
	for len(parents) > 0 {
		subIssues, err := repository.SubIssues(ctx, parents[0])
		if err != nil {
			return nil, err
		}
		parents = parents[1:]
		for _, subIssue := range subIssues {
			if hasLabel(subIssue, workstreamLabel) || inOtherRepository(subIssue, repository.FullName) {
				continue
			}
			parents = append(parents, int64(subIssue.GetNumber()))
			if subIssue.GetState() == "open" {
				issues = append(issues, subIssue)
			}
		}
	}
	numbers, err := e.queries.ListTaskPullRequests(ctx, store.ListTaskPullRequestsParams{Repository: repository.FullName, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	var items []ClosureItem
	var seen []int64
	for _, number := range numbers {
		if slices.Contains(seen, number) {
			continue
		}
		seen = append(seen, number)
		pullRequest, err := repository.Issue(ctx, number)
		if err != nil {
			return nil, err
		}
		if pullRequest.GetState() == "open" {
			items = append(items, closureItem(pullRequest, true))
		}
	}
	for _, issue := range append(issues, workstreamIssue) {
		items = append(items, closureItem(issue, false))
	}
	return items, nil
}

func closureItem(issue *gh.Issue, pullRequest bool) ClosureItem {
	return ClosureItem{PullRequest: pullRequest, Number: int64(issue.GetNumber()), Title: issue.GetTitle(), URL: issue.GetHTMLURL()}
}

// closeWorkstream ends the work of the Workstream, removes mobius:autopilot, closes the open pull requests of its
// tasks, and closes the issues below it as not planned. A nested Workstream and the issues below it stay open.
func (e *Engine) closeWorkstream(ctx context.Context, repository github.Repository, workstream int64) error {
	if err := e.stopWorkstream(ctx, repository, workstream); err != nil {
		return err
	}
	if err := e.startCurator(ctx, repository.FullName); err != nil {
		return err
	}
	// A later reopen needs a new mobius:autopilot from a trusted user.
	if err := repository.RemoveLabel(ctx, workstream, autopilotLabel); err != nil {
		return err
	}
	pullRequests, err := e.queries.ListTaskPullRequests(ctx, store.ListTaskPullRequestsParams{Repository: repository.FullName, Workstream: workstream})
	if err != nil {
		return err
	}
	for _, number := range pullRequests {
		pullRequest, err := repository.Issue(ctx, number)
		if err != nil {
			return err
		}
		if pullRequest.GetState() != "open" {
			continue
		}
		if _, err := repository.AddComment(ctx, number, closedText); err != nil {
			return err
		}
		if err := repository.ClosePullRequest(ctx, number); err != nil {
			return err
		}
	}
	parents := []int64{workstream}
	for len(parents) > 0 {
		issues, err := repository.SubIssues(ctx, parents[0])
		if err != nil {
			return err
		}
		parents = parents[1:]
		for _, issue := range issues {
			if hasLabel(issue, workstreamLabel) || inOtherRepository(issue, repository.FullName) {
				continue
			}
			number := int64(issue.GetNumber())
			parents = append(parents, number)
			for _, label := range []string{readyLabel, workingLabel, needsHumanLabel, reviewLabel} {
				if !hasLabel(issue, label) {
					continue
				}
				if err := repository.RemoveLabel(ctx, number, label); err != nil {
					return err
				}
			}
			if issue.GetState() != "open" {
				continue
			}
			if _, err := repository.AddComment(ctx, number, closedText); err != nil {
				return err
			}
			if _, err := repository.CloseIssue(ctx, number, "not_planned"); err != nil {
				return err
			}
		}
	}
	return nil
}

// stopWorkstream stops the Lead and ends the live tasks of the Workstream. The queued events of the Lead go to no
// later Lead.
func (e *Engine) stopWorkstream(ctx context.Context, repository github.Repository, workstream int64) error {
	e.stopLead(repository.FullName, workstream)
	err := e.queries.DeliverLeadEvents(ctx, store.DeliverLeadEventsParams{
		DeliveredAt: sql.NullString{String: now(), Valid: true},
		Repository:  repository.FullName,
		Workstream:  workstream,
	})
	if err != nil {
		return err
	}
	tasks, err := e.queries.ListLiveTasks(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Workstream != workstream {
			continue
		}
		if err := e.endTask(ctx, repository, task); err != nil {
			return err
		}
	}
	return nil
}

// hasWork tells if the Workstream has a Lead or live tasks. An issue that lost mobius:workstream can still have them.
func (e *Engine) hasWork(ctx context.Context, repository string, workstream int64) (bool, error) {
	if e.hasLead(repository, workstream) {
		return true, nil
	}
	tasks, err := e.queries.ListLiveTasks(ctx, repository)
	return slices.ContainsFunc(tasks, func(task store.Task) bool { return task.Workstream == workstream }), err
}

// SetAutopilot turns Autopilot of the Workstream number of repository on or off. The label change uses the user
// token of the Owner, because the label counts only when a trusted user added it last.
func (e *Engine) SetAutopilot(ctx context.Context, repositoryName string, number int64, on bool) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	asOwner, err := e.github.AsOwner(ctx, repository)
	if err != nil {
		// The text tells the Owner how to authorize the Mobius App.
		return refusal(err.Error())
	}
	// GitHub records no labeled event for a label that the issue has, so a last labeled event of an untrusted actor
	// would stay. Remove before the add.
	if err := asOwner.RemoveLabel(ctx, number, autopilotLabel); err != nil {
		return err
	}
	if on {
		if err := asOwner.AddLabel(ctx, number, autopilotLabel); err != nil {
			return err
		}
	}
	err = e.queries.SetCopiedAutopilot(ctx, store.SetCopiedAutopilotParams{Autopilot: on, Repository: repositoryName, Number: number})
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// workstreamAutopilot tells if a trusted user turned Autopilot on for the Workstream number.
func (e *Engine) workstreamAutopilot(ctx context.Context, repository github.Repository, number int64) (bool, error) {
	issue, err := repository.Issue(ctx, number)
	if err != nil || issue == nil {
		return false, err
	}
	return e.issueAutopilot(ctx, repository, issue)
}

// issueAutopilot tells if a trusted user turned Autopilot on for the Workstream issue.
func (e *Engine) issueAutopilot(ctx context.Context, repository github.Repository, issue *gh.Issue) (bool, error) {
	if !hasLabel(issue, autopilotLabel) {
		return false, nil
	}
	events, err := repository.IssueEvents(ctx, int64(issue.GetNumber()))
	return e.addedByTrustedUser(events), err
}

// addedByTrustedUser tells if a trusted user added mobius:autopilot in the last labeled event of that label in events.
// Thus a trusted bot or the Mobius App cannot turn Autopilot on.
func (e *Engine) addedByTrustedUser(events []*gh.IssueEvent) bool {
	for _, event := range slices.Backward(events) {
		if event.GetEvent() != "labeled" || event.GetLabel().GetName() != autopilotLabel {
			continue
		}
		return slices.ContainsFunc(e.config.TrustedUsers, func(user string) bool {
			return strings.EqualFold(user, event.GetActor().GetLogin())
		})
	}
	return false
}

// eventText gives the text of a Lead event about the issue, for example "creation of Workstream", with the
// quoted body, for example the body of the issue or of a comment.
func eventText(t time.Time, what string, issue *gh.Issue, actor, body string) string {
	return fmt.Sprintf("%s %s #%d \"%s\" by @%s:\n\n%s", t.UTC().Format(timeFormat), what, issue.GetNumber(), issue.GetTitle(), actor, quote(body))
}
