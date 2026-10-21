package engine

import (
	"context"
	"database/sql"
	"errors"
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
	ctx = context.WithoutCancel(ctx)
	defer e.beginClose(repository.FullName, number)()
	closed, err := repository.CloseIssue(ctx, number, "completed")
	if err != nil {
		return err
	}
	if _, err := e.updateCopy(ctx, repository, closed, nil, false, e.labelChangeCount(repository.FullName)); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	// The poll of the close event runs the close again, and the second run changes nothing.
	return e.closeWorkstream(ctx, repository, number)
}

// WorkstreamDetails is what GitHub knows about a Workstream issue.
type WorkstreamDetails struct {
	CreatedAt      time.Time
	Open           bool
	CompletedTasks int64
	OpenTasks      int64
}

// walkTasks calls visit for each task below the issue parent of repository, depth first in the order of the sub-issues,
// with the repository of the task. A nested Workstream, the issues below it, and an issue in a repository that the App
// cannot read are no tasks. When visit gives true, the walk ends and walkTasks gives true.
func (e *Engine) walkTasks(ctx context.Context, repository github.Repository, parent int64, visit func(github.Repository, *gh.Issue) (bool, error)) (bool, error) {
	issues, err := repository.SubIssues(ctx, parent)
	if err != nil {
		return false, err
	}
	for _, issue := range issues {
		if hasLabel(issue, workstreamLabel) {
			continue
		}
		source, ok := e.issueRepository(issue, repository)
		if !ok {
			continue
		}
		if done, err := visit(source, issue); err != nil || done {
			return done, err
		}
		if done, err := e.walkTasks(ctx, source, int64(issue.GetNumber()), visit); err != nil || done {
			return done, err
		}
	}
	return false, nil
}

// WorkstreamDetails reads the Workstream number of repository from GitHub. A task is an issue in the sub-issue tree of
// the Workstream, also an issue of another repository. A nested Workstream and the issues below it are no tasks.
func (e *Engine) WorkstreamDetails(ctx context.Context, repositoryName string, number int64) (WorkstreamDetails, error) {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return WorkstreamDetails{}, err
	}
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return WorkstreamDetails{}, err
	}
	if issue == nil || !hasLabel(issue, workstreamLabel) {
		return WorkstreamDetails{}, refuse("The issue is not a Workstream.")
	}
	details := WorkstreamDetails{CreatedAt: issue.GetCreatedAt().Time, Open: issue.GetState() == "open"}
	_, err = e.walkTasks(ctx, repository, number, func(_ github.Repository, task *gh.Issue) (bool, error) {
		if task.GetState() == "open" {
			details.OpenTasks++
		} else {
			details.CompletedTasks++
		}
		return false, nil
	})
	return details, err
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
	ctx = context.WithoutCancel(ctx)
	defer e.beginClose(repository.FullName, number)()
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
	if _, err := e.updateCopy(ctx, repository, closed, nil, false, e.labelChangeCount(repository.FullName)); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return e.closeWorkstream(ctx, repository, number)
}

// beginClose hides the Workstream number of repository from the Workstream list. The returned function shows it
// again, unless the close removed it from the copy.
func (e *Engine) beginClose(repository string, number int64) (end func()) {
	e.setClosing(pullKey{repository, number}, true)
	return func() { e.setClosing(pullKey{repository, number}, false) }
}

func (e *Engine) setClosing(key pullKey, closing bool) {
	e.closingMu.Lock()
	if closing {
		e.closing[key] = true
	} else {
		delete(e.closing, key)
	}
	e.closingMu.Unlock()
	e.publish(Change{Workstreams: true})
}

// IsClosing tells if the close of the Workstream number of repository runs.
func (e *Engine) IsClosing(repository string, number int64) bool {
	e.closingMu.Lock()
	defer e.closingMu.Unlock()
	return e.closing[pullKey{repository, number}]
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
	_, err := e.walkTasks(ctx, repository, workstream, func(_ github.Repository, subIssue *gh.Issue) (bool, error) {
		if subIssue.GetState() == "open" {
			issues = append(issues, subIssue)
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	rows, err := e.queries.ListTaskPullRequests(ctx, store.ListTaskPullRequestsParams{WorkstreamRepository: repository.FullName, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	var items []ClosureItem
	var seen []pullKey
	for _, row := range rows {
		key := pullKey{row.Repository, row.PullRequest}
		if slices.Contains(seen, key) {
			continue
		}
		seen = append(seen, key)
		source, err := e.repository(row.Repository)
		if err != nil {
			return nil, err
		}
		pullRequest, err := source.Issue(ctx, row.PullRequest)
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

// closeWorkstream ends the work of the Workstream, removes mobius:autopilot and mobius:question, dismisses the Inbox
// items of its issues, closes the open pull requests of its
// tasks, and closes the issues below it as not planned. A nested Workstream and the issues below it stay open.
func (e *Engine) closeWorkstream(ctx context.Context, repository github.Repository, workstream int64) error {
	if err := e.stopWorkstream(ctx, repository, workstream); err != nil {
		return err
	}
	// A later reopen needs a new mobius:autopilot from a trusted user.
	if err := repository.RemoveLabel(ctx, workstream, autopilotLabel); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, workstream, questionLabel); err != nil {
		return err
	}
	if err := e.dismissIssueItems(ctx, repository.FullName, workstream, inboxKinds...); err != nil {
		return err
	}
	pullRequests, err := e.queries.ListTaskPullRequests(ctx, store.ListTaskPullRequestsParams{WorkstreamRepository: repository.FullName, Workstream: workstream})
	if err != nil {
		return err
	}
	for _, row := range pullRequests {
		source, err := e.repository(row.Repository)
		if err != nil {
			return err
		}
		pullRequest, err := source.Issue(ctx, row.PullRequest)
		if err != nil {
			return err
		}
		if pullRequest.GetState() != "open" {
			continue
		}
		if _, err := source.AddComment(ctx, row.PullRequest, closedText); err != nil {
			return err
		}
		if err := source.ClosePullRequest(ctx, row.PullRequest); err != nil {
			return err
		}
	}
	_, err = e.walkTasks(ctx, repository, workstream, func(source github.Repository, issue *gh.Issue) (bool, error) {
		number := int64(issue.GetNumber())
		if err := e.dismissIssueItems(ctx, source.FullName, number, inboxKinds...); err != nil {
			return false, err
		}
		for _, label := range []string{readyLabel, workingLabel, needsHumanLabel, questionLabel, reviewLabel} {
			if !hasLabel(issue, label) {
				continue
			}
			if err := source.RemoveLabel(ctx, number, label); err != nil {
				return false, err
			}
		}
		if issue.GetState() != "open" {
			return false, nil
		}
		if _, err := source.AddComment(ctx, number, closedText); err != nil {
			return false, err
		}
		_, err := source.CloseIssue(ctx, number, "not_planned")
		return false, err
	})
	return err
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
	tasks, err := e.queries.ListWorkstreamLiveTasks(ctx, store.ListWorkstreamLiveTasksParams{WorkstreamRepository: repository.FullName, Workstream: workstream})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		source, err := e.repository(task.Repository)
		if err != nil {
			return err
		}
		if err := e.endTask(ctx, source, task); err != nil {
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
	tasks, err := e.queries.ListWorkstreamLiveTasks(ctx, store.ListWorkstreamLiveTasksParams{WorkstreamRepository: repository, Workstream: workstream})
	return len(tasks) > 0, err
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

// workstreamAutopilot tells if the copy has Autopilot on for the Workstream number.
func (e *Engine) workstreamAutopilot(ctx context.Context, repository string, number int64) (bool, error) {
	on, err := e.queries.GetCopiedAutopilot(ctx, store.GetCopiedAutopilotParams{Repository: repository, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return on, err
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
