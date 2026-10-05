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

	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// readyEndpoint is the endpoint of the list of the issues with mobius:ready in the sync_cursors table.
const readyEndpoint = "ready"

// dispatchReady acts on the open issues with mobius:ready of a trusted actor:
//   - An issue with no Workstream goes to the Triager, also when it has open blockers.
//   - A task that waits for a human, and a stopped task with a pull request, continues (Mobius#225, Mobius#253). A
//     mobius:ready of the Mobius App needs Autopilot for that. Each other issue with a live task loses mobius:ready.
//   - An issue with an open blocker waits. A mobius:ready of the Mobius App needs Autopilot.
//   - Each other issue gets a new task. A stopped task with no pull request ends first.
//
// The drain holds each new dispatch and Triager, and then the next poll after the drain reads the same list again.
func (e *Engine) dispatchReady(ctx context.Context, repository github.Repository) error {
	if e.draining() {
		return nil
	}
	cursor, err := e.queries.GetSyncCursor(ctx, store.GetSyncCursorParams{Repository: repository.FullName, Endpoint: readyEndpoint})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	page, changed, err := repository.OpenIssuesWithLabelPage(ctx, readyLabel, cursor.Etag.String)
	if err != nil || !changed {
		return err
	}
	held := false
	for _, issue := range page.Issues {
		if issue.IsPullRequest() {
			continue
		}
		number := int64(issue.GetNumber())
		events, err := repository.IssueEvents(ctx, number)
		if err != nil {
			return err
		}
		actor, ok := readyActor(events, appLogin(repository.AppSlug))
		if !ok || !e.TrustedAuthor(repository.AppSlug, actor) {
			continue
		}
		task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
		live := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		switch {
		case live && (task.State == "needs_human" || task.State == "stopped" && task.PullRequest.Valid):
			if strings.EqualFold(actor, appLogin(repository.AppSlug)) {
				on, err := e.workstreamAutopilot(ctx, repository, task.Workstream)
				if err != nil {
					return err
				}
				if !on {
					continue
				}
			}
			if err := e.resume(ctx, repository, issue, task, actor); err != nil {
				return err
			}
			continue
		case live && task.State != "stopped":
			if err := repository.RemoveLabel(ctx, number, readyLabel); err != nil {
				return err
			}
			if err := e.addActivity(ctx, repository.FullName, task.Workstream, issue, actor, fmt.Sprintf("No effect: \"%s\" has a live task", issue.GetTitle())); err != nil {
				return err
			}
			continue
		}
		workstream, err := workstreamOf(ctx, repository, number)
		if err != nil {
			return err
		}
		if workstream == 0 {
			triaged, err := e.triage(ctx, repository, number)
			if err != nil {
				return err
			}
			held = held || !triaged
			continue
		}
		if issue.GetIssueDependenciesSummary().GetBlockedBy() > 0 {
			continue
		}
		if strings.EqualFold(actor, appLogin(repository.AppSlug)) {
			on, err := e.workstreamAutopilot(ctx, repository, workstream)
			if err != nil {
				return err
			}
			if !on {
				continue
			}
		}
		if live {
			if err := e.queries.EndTask(ctx, task.ID); err != nil {
				return err
			}
		}
		if err := e.dispatch(ctx, repository, issue, workstream, actor); err != nil {
			return err
		}
	}
	// The saved ETag would hide an issue that the drain held from the next poll.
	if held {
		return nil
	}
	return e.queries.SetSyncCursor(ctx, store.SetSyncCursorParams{
		Repository: repository.FullName,
		Endpoint:   readyEndpoint,
		Etag:       sql.NullString{String: page.ETag, Valid: page.ETag != ""},
	})
}

// dispatch adds a task for the issue in the Workstream, with an activity and a dispatch event for the Lead.
// mobius:working comes before the removal of mobius:ready, so a failure between the two leaves the issue in the
// list of the next poll.
func (e *Engine) dispatch(ctx context.Context, repository github.Repository, issue *gh.Issue, workstream int64, actor string) error {
	number := int64(issue.GetNumber())
	if err := repository.AddLabel(ctx, number, workingLabel); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, number, readyLabel); err != nil {
		return err
	}
	_, err := e.queries.AddTask(ctx, store.AddTaskParams{Repository: repository.FullName, Issue: number, Workstream: workstream, DispatchedAt: now()})
	if err != nil {
		return err
	}
	if err := e.addActivity(ctx, repository.FullName, workstream, issue, actor, fmt.Sprintf("Dispatched \"%s\"", issue.GetTitle())); err != nil {
		return err
	}
	text := eventText(time.Now(), "dispatch of", issue, actor, issue.GetBody())
	return e.addLeadEvent(ctx, repository.FullName, workstream, sql.NullInt64{Int64: number, Valid: true}, "dispatch", text)
}

// addActivity adds an activity about the issue to the feed.
func (e *Engine) addActivity(ctx context.Context, repository string, workstream int64, issue *gh.Issue, actor, text string) error {
	return e.queries.AddEvent(ctx, store.AddEventParams{
		Time:       now(),
		Repository: repository,
		Workstream: workstream,
		Issue:      int64(issue.GetNumber()),
		Actor:      actor,
		Text:       text,
		Link:       issue.GetHTMLURL(),
	})
}

// readyActor gives the actor of the last mobius:ready of events. After a move_issue of the Triager, the Mobius App
// removes mobius:no-workstream and adds mobius:ready again. Only this sequence, after a triage of the Mobius App with
// no other mobius:ready between, counts with the actor of the mobius:ready before the triage.
func readyActor(events []*gh.IssueEvent, app string) (string, bool) {
	byApp := func(event *gh.IssueEvent) bool { return strings.EqualFold(event.GetActor().GetLogin(), app) }
	last, ready := lastEvent(events, "labeled", readyLabel)
	if ready == nil || ready.Actor == nil {
		return "", false
	}
	login := ready.GetActor().GetLogin()
	earlier := events[:last]
	_, moved := lastEvent(earlier, "", "")
	if !byApp(ready) || moved == nil || moved.GetEvent() != "unlabeled" || moved.GetLabel().GetName() != noWorkstreamLabel || !byApp(moved) {
		return login, true
	}
	triage, triaged := lastEvent(earlier, "labeled", noWorkstreamLabel)
	if triaged == nil || !byApp(triaged) {
		return login, true
	}
	if _, between := lastEvent(earlier[triage:], "labeled", readyLabel); between != nil {
		return login, true
	}
	_, first := lastEvent(earlier[:triage], "labeled", readyLabel)
	if first == nil || first.Actor == nil {
		return "", false
	}
	return first.GetActor().GetLogin(), true
}

// lastEvent gives the last event of kind with label in events, and its index. With an empty kind, it gives the last
// event. It gives -1 and nil when events has no such event.
func lastEvent(events []*gh.IssueEvent, kind, label string) (int, *gh.IssueEvent) {
	for i, event := range slices.Backward(events) {
		if kind == "" || event.GetEvent() == kind && event.GetLabel().GetName() == label {
			return i, event
		}
	}
	return -1, nil
}

// commentEvents acts on the new comments of the issue of a live task: a new comment of a trusted user resets the
// counters of the task, and each new comment that is an event goes to the Lead. When the newest such comment is
// newer than the last comment of the Mobius App, the question, it answers the question: mobius:needs-human goes away
// unless the task waits for a human. since is zero at the first poll.
func (e *Engine) commentEvents(ctx context.Context, repository github.Repository, issue *gh.Issue, since time.Time) error {
	number := int64(issue.GetNumber())
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	comments, err := repository.Comments(ctx, number)
	if err != nil {
		return err
	}
	replies, answered, err := e.newComments(ctx, repository, task, comments, since)
	if err != nil {
		return err
	}
	if answered && task.State != "needs_human" && hasLabel(issue, needsHumanLabel) {
		if err := repository.RemoveLabel(ctx, number, needsHumanLabel); err != nil {
			return err
		}
	}
	return e.commentEventsOf(ctx, task, issue, replies)
}

// pullRequestComments acts on the new comments of the pull request of a live task: a new comment or review comment of
// a trusted user resets the counters of the task, and each new comment that is an event goes to the Lead.
func (e *Engine) pullRequestComments(ctx context.Context, repository github.Repository, pullRequest *gh.Issue, since time.Time) error {
	number := int64(pullRequest.GetNumber())
	task, err := e.queries.GetLiveTaskByPullRequest(ctx, store.GetLiveTaskByPullRequestParams{
		Repository:  repository.FullName,
		PullRequest: sql.NullInt64{Int64: number, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	comments, err := repository.Comments(ctx, number)
	if err != nil {
		return err
	}
	reviewComments, err := repository.ReviewComments(ctx, number)
	if err != nil {
		return err
	}
	for _, comment := range reviewComments {
		if e.newUserComment(comment.GetUser().GetLogin(), comment.GetCreatedAt().Time, since) {
			if err := e.queries.ResetTaskCounters(ctx, task.ID); err != nil {
				return err
			}
			break
		}
	}
	replies, _, err := e.newComments(ctx, repository, task, comments, since)
	if err != nil {
		return err
	}
	return e.commentEventsOf(ctx, task, pullRequest, replies)
}

// newComments resets the counters of the task when comments have a new comment of a trusted user. It gives the new
// comments that are events, and true when the newest of them is newer than the last comment of the Mobius App.
func (e *Engine) newComments(ctx context.Context, repository github.Repository, task store.Task, comments []*gh.IssueComment, since time.Time) ([]*gh.IssueComment, bool, error) {
	if slices.ContainsFunc(comments, func(comment *gh.IssueComment) bool {
		return e.newUserComment(comment.GetUser().GetLogin(), comment.GetCreatedAt().Time, since)
	}) {
		if err := e.queries.ResetTaskCounters(ctx, task.ID); err != nil {
			return nil, false, err
		}
	}
	replies, answered := e.replies(repository.AppSlug, comments, since)
	return replies, answered, nil
}

// commentEventsOf adds a comment event for the Lead for each comment on the issue or the pull request of the task.
// The event gives the state of the task, so the Lead can tell the Owner the next step.
func (e *Engine) commentEventsOf(ctx context.Context, task store.Task, issue *gh.Issue, comments []*gh.IssueComment) error {
	for _, comment := range comments {
		text := eventText(comment.GetCreatedAt().Time, "comment on", issue, comment.GetUser().GetLogin(), comment.GetBody()) +
			fmt.Sprintf("\n\nThe state of the task of #%d is %s.", task.Issue, task.State)
		if err := e.addLeadEvent(ctx, task.Repository, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "comment", text); err != nil {
			return err
		}
	}
	return nil
}

// newUserComment tells if a comment of login at createdAt is a comment of a trusted user after since.
func (e *Engine) newUserComment(login string, createdAt, since time.Time) bool {
	return createdAt.After(since) && slices.ContainsFunc(e.config.TrustedUsers, func(user string) bool { return strings.EqualFold(user, login) })
}

// replies gives the comments after since that are events, and true when the newest of them is newer than the last
// comment of the Mobius App.
func (e *Engine) replies(appSlug string, comments []*gh.IssueComment, since time.Time) ([]*gh.IssueComment, bool) {
	var askedAt, repliedAt time.Time
	var replies []*gh.IssueComment
	for _, comment := range comments {
		createdAt := comment.GetCreatedAt().Time
		if strings.EqualFold(comment.GetUser().GetLogin(), appLogin(appSlug)) && createdAt.After(askedAt) {
			askedAt = createdAt
		}
		if createdAt.After(since) && e.commentIsEvent(appSlug, comment) {
			replies = append(replies, comment)
			if createdAt.After(repliedAt) {
				repliedAt = createdAt
			}
		}
	}
	return replies, len(replies) > 0 && repliedAt.After(askedAt)
}

// commentIsEvent tells if the comment is an event for the Lead: a comment of a trusted user. A comment of the Lead
// session has a trusted user as its author and the Mobius App in performed_via_github_app, so it is no event.
func (e *Engine) commentIsEvent(appSlug string, comment *gh.IssueComment) bool {
	trusted := slices.ContainsFunc(e.config.TrustedUsers, func(user string) bool { return strings.EqualFold(user, comment.GetUser().GetLogin()) })
	return trusted && comment.GetPerformedViaGithubApp().GetSlug() != appSlug
}

// ask posts the question text on the issue of a live task of the Workstream, adds mobius:needs-human, and adds an
// Inbox item. The reply comes later as a comment event.
func (e *Engine) ask(ctx context.Context, c caller, repository github.Repository, input textInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	if empty(input.Text) {
		return "", refuse("text must not be empty.")
	}
	if _, err := e.workstreamTask(ctx, repository, c.workstream, input.N); err != nil {
		return "", err
	}
	issue, err := repository.Issue(ctx, input.N)
	if err != nil {
		return "", err
	}
	if issue == nil {
		return "", refuse("#%d does not exist.", input.N)
	}
	if _, err := repository.AddComment(ctx, input.N, input.Text); err != nil {
		return "", err
	}
	if err := repository.AddLabel(ctx, input.N, needsHumanLabel); err != nil {
		return "", err
	}
	_, err = e.addInboxItem(ctx, store.AddInboxItemParams{
		Kind:         questionKind,
		Organization: repository.Owner(),
		Repository:   repository.FullName,
		Workstream:   c.workstream,
		Issue:        input.N,
		Text:         input.Text,
		Link:         issue.GetHTMLURL(),
	})
	return fmt.Sprintf("Asked on #%d.", input.N), err
}

// workstreamTask gives the live task of the issue number in the Workstream.
func (e *Engine) workstreamTask(ctx context.Context, repository github.Repository, workstream, number int64) (store.Task, error) {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
	if errors.Is(err, sql.ErrNoRows) || err == nil && task.Workstream != workstream {
		return store.Task{}, refuse("#%d has no live task in this Workstream.", number)
	}
	return task, err
}

// startAutopilot dispatches the tasks of each open Workstream with Autopilot, in the order of the sub-issues, while
// fewer tasks are active than max_agents. An issue that had a task, also an ended one, gets no new task: only a
// trusted user starts it again. The drain holds each new dispatch.
func (e *Engine) startAutopilot(ctx context.Context, repository github.Repository) error {
	if e.draining() {
		return nil
	}
	workstreams, err := repository.OpenIssuesWithLabel(ctx, workstreamLabel)
	if err != nil {
		return err
	}
	for _, workstream := range workstreams {
		on, err := e.issueAutopilot(ctx, repository, workstream)
		if err != nil {
			return err
		}
		if !on {
			continue
		}
		full, err := e.autopilotTree(ctx, repository, int64(workstream.GetNumber()), int64(workstream.GetNumber()))
		if err != nil || full {
			return err
		}
	}
	return nil
}

// autopilotTree dispatches the tasks below parent in the Workstream, depth first. It gives true when as many tasks
// are active as max_agents.
func (e *Engine) autopilotTree(ctx context.Context, repository github.Repository, workstream, parent int64) (bool, error) {
	issues, err := repository.SubIssues(ctx, parent)
	if err != nil {
		return false, err
	}
	for _, issue := range issues {
		if hasLabel(issue, workstreamLabel) || inOtherRepository(issue, repository.FullName) {
			continue
		}
		number := int64(issue.GetNumber())
		if issue.GetState() == "open" && e.TrustedAuthor(repository.AppSlug, issue.GetUser().GetLogin()) && issue.GetIssueDependenciesSummary().GetBlockedBy() == 0 {
			had, err := e.queries.HasTask(ctx, store.HasTaskParams{Repository: repository.FullName, Issue: number})
			if err != nil {
				return false, err
			}
			if !had {
				active, err := e.queries.CountActiveTasks(ctx)
				if err != nil || active >= int64(e.config.MaxAgents) {
					return true, err
				}
				if err := e.dispatch(ctx, repository, issue, workstream, appLogin(repository.AppSlug)); err != nil {
					return false, err
				}
			}
		}
		full, err := e.autopilotTree(ctx, repository, workstream, number)
		if err != nil || full {
			return full, err
		}
	}
	return false, nil
}

// resume continues the task of the issue that waits for a human, or the stopped task with a pull request, for the
// actor: a pull request with a merge conflict gets a conflict round, another pull request gets a fix round with its
// open review threads, and a task with no pull request gets the Implementer on the same branch.
// mobius:ready goes away last, so a failure before the round starts leaves the issue in the ready list.
func (e *Engine) resume(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task, actor string) error {
	number := int64(issue.GetNumber())
	if err := repository.AddLabel(ctx, number, workingLabel); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, number, needsHumanLabel); err != nil {
		return err
	}
	if err := e.queries.ResetTaskCounters(ctx, task.ID); err != nil {
		return err
	}
	var pullRequest *gh.PullRequest
	if task.PullRequest.Valid {
		var err error
		if pullRequest, err = repository.PullRequest(ctx, task.PullRequest.Int64); err != nil {
			return err
		}
	}
	conflict := pullRequest != nil && (pullRequest.Mergeable != nil && !pullRequest.GetMergeable() || behind(pullRequest))
	to := "dispatched"
	switch {
	case conflict:
		to = "ready_for_review"
	case pullRequest != nil:
		to = "working"
	}
	parent, err := e.newestSession(ctx, task)
	if err != nil {
		return err
	}
	items := ""
	if pullRequest != nil && !conflict {
		if items, err = e.continueItems(ctx, repository, int64(pullRequest.GetNumber())); err != nil {
			return err
		}
	}
	from := task.State
	moved, err := e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: to, ID: task.ID, FromState: from})
	if err != nil || moved == 0 {
		return err
	}
	switch {
	case conflict:
		err = e.conflictRound(ctx, repository, task, pullRequest)
	case pullRequest != nil:
		err = e.fixRound(ctx, repository, round{task: task, title: issue.GetTitle(), pullRequest: pullRequest, items: items, parent: parent})
	default:
		_, err = e.startImplementer(ctx, repository, task.Workstream, number, issue.GetBody(), parent)
	}
	if err != nil {
		_, stateErr := e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: from, ID: task.ID, FromState: to})
		return errors.Join(err, stateErr)
	}
	if err := repository.RemoveLabel(ctx, number, readyLabel); err != nil {
		return err
	}
	return e.addActivity(ctx, repository.FullName, task.Workstream, issue, actor, fmt.Sprintf("Continued \"%s\"", issue.GetTitle()))
}

// continueItems gives the items of a fix round that continues the pull request number: each open review thread, with
// the action fix. With no open thread, it tells the Implementer to finish the issue.
func (e *Engine) continueItems(ctx context.Context, repository github.Repository, number int64) (string, error) {
	trusted := func(login string) bool { return e.TrustedAuthor(repository.AppSlug, login) }
	threads, err := repository.ReviewThreads(ctx, number)
	if err != nil {
		return "", err
	}
	var open []int64
	for _, thread := range threads {
		if openThread(thread, trusted, appLogin(repository.AppSlug)) {
			open = append(open, thread.Comment)
		}
	}
	reviewComments, err := repository.ReviewComments(ctx, number)
	if err != nil {
		return "", err
	}
	var items strings.Builder
	for _, root := range reviewComments {
		if slices.Contains(open, root.GetID()) {
			items.WriteString(thread(reviewComments, root, trusted) + "\nAction: fix\n")
		}
	}
	if items.Len() == 0 {
		return "\nThe human continued the task. Finish the issue and make `.mobius/check` pass.\n", nil
	}
	return items.String(), nil
}

// openThread tells if the review thread is open: it is not resolved, a trusted author started it, and its last trusted
// comment is not a reply of the Mobius App with the login app. The first comment of the Mobius App is a finding of the
// Reviewer.
func openThread(thread github.ReviewThread, trusted func(string) bool, app string) bool {
	var authors []string
	for _, author := range thread.Authors {
		if trusted(author) {
			authors = append(authors, author)
		}
	}
	if thread.Resolved || len(authors) == 0 || thread.Authors[0] != authors[0] {
		return false
	}
	return len(authors) == 1 || !strings.EqualFold(authors[len(authors)-1], app)
}
