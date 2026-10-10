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

// readyEndpoint is the endpoint of the list of the issues with mobius:ready in the sync_cursors table.
const readyEndpoint = "ready"

// dispatchReady acts on the open issues with mobius:ready of a trusted actor:
//   - An issue with no Workstream goes to the Triager, also when it has open blockers.
//   - A task that waits for a human, and a stopped task with a pull request, continues (Mobius-rust#225,
//     Mobius-rust#253). A mobius:ready of the Mobius App needs Autopilot for that. Each other issue with a live task
//     loses mobius:ready.
//   - An issue with an open blocker waits.
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
		if live {
			if err := e.queries.EndTask(ctx, task.ID); err != nil {
				return err
			}
			e.publish(Change{Workstreams: true})
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
// newer than the last comment of the Mobius App, the question, it answers the question: mobius:question goes away.
// Each such comment gets the reaction of an agent that gets it. For an issue with no live task, see commentsWithoutTask.
func (e *Engine) commentEvents(ctx context.Context, repository github.Repository, issue *gh.Issue, comments []*gh.IssueComment) error {
	number := int64(issue.GetNumber())
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
	if errors.Is(err, sql.ErrNoRows) {
		return e.commentsWithoutTask(ctx, repository, issue, comments)
	}
	if err != nil {
		return err
	}
	replies, answered, err := e.newComments(ctx, repository, task, comments)
	if err != nil {
		return err
	}
	if answered && hasLabel(issue, questionLabel) {
		if err := repository.RemoveLabel(ctx, number, questionLabel); err != nil {
			return err
		}
	}
	return e.commentEventsOf(ctx, repository, task, issue, replies)
}

// commentsWithoutTask acts on the new comments of an issue with no live task. Mobius does not act on a comment on a
// closed issue. An issue with mobius:no-workstream goes to retriage. Any other issue goes to workstreamCommentEvents.
func (e *Engine) commentsWithoutTask(ctx context.Context, repository github.Repository, issue *gh.Issue, comments []*gh.IssueComment) error {
	events, _ := e.replies(repository.AppSlug, comments)
	if len(events) == 0 {
		return nil
	}
	switch {
	case issue.GetState() != "open":
		return e.declineComments(ctx, repository, int64(issue.GetNumber()), events, closedIssueReply)
	case hasLabel(issue, noWorkstreamLabel):
		return e.retriage(ctx, repository, issue, events)
	}
	return e.workstreamCommentEvents(ctx, repository, issue, events)
}

// workstreamCommentEvents adds a comment event for the Lead for each comment of events on an open Workstream issue, or
// on an open issue below a Workstream, that has no live task. The event of the Workstream issue has no issue. An issue
// in no Workstream with a mobius:ready of a trusted author waits for the Triager, which reads the comments. Mobius does
// not act on a comment on another issue in no Workstream.
func (e *Engine) workstreamCommentEvents(ctx context.Context, repository github.Repository, issue *gh.Issue, events []*gh.IssueComment) error {
	number := int64(issue.GetNumber())
	workstream, err := e.commentWorkstream(ctx, repository, issue)
	if err != nil {
		return err
	}
	if workstream == 0 && hasLabel(issue, readyLabel) {
		issueEvents, err := repository.IssueEvents(ctx, number)
		if err != nil {
			return err
		}
		if actor, ok := readyActor(issueEvents, appLogin(repository.AppSlug)); ok && e.TrustedAuthor(repository.AppSlug, actor) {
			return acknowledge(ctx, repository, events)
		}
	}
	if workstream == 0 {
		return e.declineComments(ctx, repository, number, events, noWorkstreamReply)
	}
	if err := acknowledge(ctx, repository, events); err != nil {
		return err
	}
	eventIssue := sql.NullInt64{Int64: number, Valid: workstream != number}
	for _, comment := range events {
		text := eventText(comment.GetCreatedAt().Time, "comment on", issue, comment.GetUser().GetLogin(), comment.GetBody())
		if err := e.addCommentLeadEvent(ctx, repository.FullName, workstream, eventIssue, "comment", text, commentRef{id: comment.GetID()}); err != nil {
			return err
		}
	}
	return nil
}

// commentWorkstream gives the number of the open Workstream of the issue, or 0 when the issue is in no open Workstream.
// It reads the local copy of the Workstream tree first.
func (e *Engine) commentWorkstream(ctx context.Context, repository github.Repository, issue *gh.Issue) (int64, error) {
	number := int64(issue.GetNumber())
	if hasLabel(issue, workstreamLabel) {
		return number, nil
	}
	workstream, ok, err := e.copiedWorkstreamOf(ctx, repository.FullName, number, issue.GetRepositoryURL())
	if err != nil || ok {
		return workstream, err
	}
	workstream, err = workstreamOf(ctx, repository, number)
	if err != nil || workstream == 0 {
		return 0, err
	}
	open, err := repository.Issue(ctx, workstream)
	if err != nil || open.GetState() != "open" {
		return 0, err
	}
	return workstream, nil
}

// pullRequestComments acts on the new comments of the pull request of a live task: a new comment or review comment of
// a trusted user resets the counters of the task. A comment goes to the Lead or to the Judge, never to both
// (Mobius-rust#274). The Judge takes the new comments when the task is in checks, approval, ready_for_review, reviewed or needs_human,
// also the comments from a round before that state. A stopped or dispatched task waits for a human or the Lead, so
// each new comment that is an event, and each new review comment of a trusted user in an open thread, goes to the
// Lead and becomes the last item of the Judge.
//
// Each conversation comment that is an event, and each review comment of a trusted user, gets a reaction. A comment of a
// live task gets the reaction of an agent that gets it, in every state of the task: the Lead gets a conversation comment
// of a stopped or dispatched task, and the Judge gets the other comments after the round of the task. The Judge gets a
// review comment only when its thread is open. Mobius does not act on a comment on a pull request with no live task.
func (e *Engine) pullRequestComments(ctx context.Context, repository github.Repository, pullRequest *gh.Issue, comments []*gh.IssueComment, reviewComments []*gh.PullRequestComment) error {
	number := int64(pullRequest.GetNumber())
	task, err := e.queries.GetLiveTaskByPullRequest(ctx, store.GetLiveTaskByPullRequestParams{
		Repository:  repository.FullName,
		PullRequest: sql.NullInt64{Int64: number, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return e.declineOnPullRequest(ctx, repository, number, comments, reviewComments)
	}
	if err != nil {
		return err
	}
	for _, comment := range reviewComments {
		if e.trustedUser(comment.GetUser().GetLogin()) {
			if err := e.queries.ResetTaskCounters(ctx, task.ID); err != nil {
				return err
			}
			break
		}
	}
	state := e.pull(repository, number)
	for _, comment := range comments {
		state.conversation[comment.GetID()] = comment
	}
	open, err := e.answerReviewComments(ctx, repository, number, reviewComments)
	if err != nil {
		return err
	}
	replies, _, err := e.newComments(ctx, repository, task, comments)
	if err != nil {
		return err
	}
	if task.State != "stopped" && task.State != "dispatched" || len(replies)+len(open) == 0 {
		if err := acknowledge(ctx, repository, replies); err != nil {
			return err
		}
		return acknowledgeReviewComments(ctx, repository, open)
	}
	var newest time.Time
	for _, comment := range replies {
		newest = laterOf(newest, comment.GetCreatedAt().Time)
	}
	for _, comment := range open {
		newest = laterOf(newest, comment.GetCreatedAt().Time)
	}
	if err := e.queries.SetJudgedAt(ctx, store.SetJudgedAtParams{
		JudgedAt: sql.NullString{String: newest.UTC().Format(time.RFC3339Nano), Valid: true},
		ID:       task.ID,
	}); err != nil {
		return err
	}
	if err := acknowledgeReviewComments(ctx, repository, open); err != nil {
		return err
	}
	for _, comment := range open {
		if err := e.addCommentEvent(ctx, task, pullRequest, commentRef{comment.GetID(), true}, comment.GetCreatedAt().Time, "review comment on", comment.GetUser().GetLogin(), comment.GetBody()); err != nil {
			return err
		}
	}
	return e.commentEventsOf(ctx, repository, task, pullRequest, replies)
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// declineOnPullRequest declines each conversation comment that is an event, and each review comment of a trusted user,
// on the pull request number with no live task.
func (e *Engine) declineOnPullRequest(ctx context.Context, repository github.Repository, number int64, comments []*gh.IssueComment, reviewComments []*gh.PullRequestComment) error {
	events, _ := e.replies(repository.AppSlug, comments)
	if err := e.declineComments(ctx, repository, number, events, noTaskReply); err != nil {
		return err
	}
	for _, comment := range reviewComments {
		if !e.trustedUser(comment.GetUser().GetLogin()) {
			continue
		}
		if err := e.declineReviewComment(ctx, repository, number, comment, noTaskReply); err != nil {
			return err
		}
	}
	return nil
}

// newComments resets the counters of the task when comments have a comment of a trusted user. It gives the comments
// that are events, and true when the newest of them is newer than the last comment of the Mobius App.
func (e *Engine) newComments(ctx context.Context, repository github.Repository, task store.Task, comments []*gh.IssueComment) ([]*gh.IssueComment, bool, error) {
	if slices.ContainsFunc(comments, func(comment *gh.IssueComment) bool {
		return e.trustedUser(comment.GetUser().GetLogin())
	}) {
		if err := e.queries.ResetTaskCounters(ctx, task.ID); err != nil {
			return nil, false, err
		}
	}
	replies, answered := e.replies(repository.AppSlug, comments)
	return replies, answered, nil
}

// commentEventsOf adds a comment event for the Lead for each comment on the issue or the pull request of the task, and
// the reaction of an agent that gets the comment. The event gives the state of the task, so the Lead can tell the Owner
// the next step.
func (e *Engine) commentEventsOf(ctx context.Context, repository github.Repository, task store.Task, issue *gh.Issue, comments []*gh.IssueComment) error {
	if err := acknowledge(ctx, repository, comments); err != nil {
		return err
	}
	for _, comment := range comments {
		if err := e.addCommentEvent(ctx, task, issue, commentRef{id: comment.GetID()}, comment.GetCreatedAt().Time, "comment on", comment.GetUser().GetLogin(), comment.GetBody()); err != nil {
			return err
		}
	}
	return nil
}

// addCommentEvent adds a comment event for the Lead of the Workstream of the task. what tells the kind of comment.
func (e *Engine) addCommentEvent(ctx context.Context, task store.Task, issue *gh.Issue, comment commentRef, at time.Time, what, author, body string) error {
	text := eventText(at, what, issue, author, body) + fmt.Sprintf("\n\nThe state of the task of #%d is %s.", task.Issue, task.State)
	return e.addCommentLeadEvent(ctx, task.Repository, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "comment", text, comment)
}

// trustedUser tells if login is a trusted user.
func (e *Engine) trustedUser(login string) bool {
	return slices.ContainsFunc(e.config.TrustedUsers, func(user string) bool { return strings.EqualFold(user, login) })
}

// replies gives the comments that are events, and true when the newest of them is newer than the last
// comment of the Mobius App.
func (e *Engine) replies(appSlug string, comments []*gh.IssueComment) ([]*gh.IssueComment, bool) {
	var askedAt, repliedAt time.Time
	var replies []*gh.IssueComment
	for _, comment := range comments {
		createdAt := comment.GetCreatedAt().Time
		if strings.EqualFold(comment.GetUser().GetLogin(), appLogin(appSlug)) && createdAt.After(askedAt) {
			askedAt = createdAt
		}
		if e.commentIsEvent(appSlug, comment) {
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
	return e.trustedUser(comment.GetUser().GetLogin()) && comment.GetPerformedViaGithubApp().GetSlug() != appSlug
}

// ask posts the question text on the issue of a live task of the Workstream, adds mobius:question, and adds an
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
	if err := repository.AddLabel(ctx, input.N, questionLabel); err != nil {
		return "", err
	}
	err = e.addInboxItem(ctx, store.AddInboxItemParams{
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

// holdTask moves the dispatched task of the issue to needs_human, so it holds no Worker slot, and tells the Owner the
// reason: the comment, mobius:needs-human, an Inbox item, and a stop event for the Lead.
func (e *Engine) holdTask(ctx context.Context, c caller, repository github.Repository, input declineInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	if empty(input.Reason) {
		return "", refuse("reason must not be empty.")
	}
	task, err := e.workstreamTask(ctx, repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	if task.State != "dispatched" {
		return "", refuse("The task of #%d is %s. Only a dispatched task can be held.", input.N, task.State)
	}
	issue, err := existingIssue(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	moved, err := e.handTaskToHuman(ctx, task.ID, "dispatched")
	if err != nil {
		return "", err
	}
	if moved == 0 {
		return "", refuse("The state of the task of #%d changed. Read the task list.", input.N)
	}
	if err := repository.RemoveLabel(ctx, input.N, workingLabel); err != nil {
		return "", err
	}
	if err := addNeedsHuman(ctx, repository, task); err != nil {
		return "", err
	}
	if _, err := repository.AddComment(ctx, input.N, input.Reason); err != nil {
		return "", err
	}
	err = e.addInboxItem(ctx, store.AddInboxItemParams{
		Kind:         questionKind,
		Organization: repository.Owner(),
		Repository:   repository.FullName,
		Workstream:   c.workstream,
		Issue:        input.N,
		Text:         input.Reason,
		Link:         issue.GetHTMLURL(),
	})
	if err != nil {
		return "", err
	}
	err = e.addLeadEvent(ctx, task.Repository, task.Workstream, sql.NullInt64{Int64: task.Issue, Valid: true}, "stop", stopText(task.Issue, issue.GetTitle(), input.Reason))
	return fmt.Sprintf("Held #%d.", input.N), err
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
// fewer tasks are active than roles.implementer.max. An issue that had a task, also an ended one, gets no new task: only a
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
// are active as roles.implementer.max.
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
				if err != nil || active >= int64(e.config.Roles.Implementer.Max) {
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
// open review threads and the failed check runs of its head, and a task with no pull request goes back to the Lead
// with a dispatch event.
// mobius:ready goes away last, so a failure before the round starts leaves the issue in the ready list.
func (e *Engine) resume(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task, actor string) error {
	number := int64(issue.GetNumber())
	if err := repository.AddLabel(ctx, number, workingLabel); err != nil {
		return err
	}
	if err := removeNeedsHuman(ctx, repository, task); err != nil {
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
		to = "checks"
	case pullRequest != nil:
		to = "working"
	}
	var parent sql.NullInt64
	items, ci := "", ""
	if pullRequest != nil {
		var err error
		if parent, err = e.newestSession(ctx, task); err != nil {
			return err
		}
		if !conflict {
			if ci, err = ciItems(ctx, repository, pullRequest.GetHead().GetSHA()); err != nil {
				return err
			}
			if items, err = e.continueItems(ctx, repository, int64(pullRequest.GetNumber()), ci); err != nil {
				return err
			}
		}
	}
	from := task.State
	moved, err := e.setTaskState(ctx, store.SetTaskStateParams{State: to, ID: task.ID, FromState: from})
	if err != nil || moved == 0 {
		return err
	}
	switch {
	case conflict:
		task.State = to
		err = e.conflictRound(ctx, repository, task, pullRequest)
	case pullRequest != nil:
		err = e.fixRound(ctx, repository, round{task: task, title: issue.GetTitle(), pullRequest: pullRequest, counts: true, items: items, parent: parent, failedCheck: ci != ""})
	default:
		text := eventText(time.Now(), "resume of", issue, actor, issue.GetBody())
		err = e.addLeadEvent(ctx, repository.FullName, task.Workstream, sql.NullInt64{Int64: number, Valid: true}, "dispatch", text)
	}
	if err != nil {
		_, stateErr := e.setTaskState(ctx, store.SetTaskStateParams{State: from, ID: task.ID, FromState: to})
		return errors.Join(err, stateErr)
	}
	if ci != "" {
		head := sql.NullString{String: pullRequest.GetHead().GetSHA(), Valid: true}
		if err := e.queries.SetTaskCheckHead(ctx, store.SetTaskCheckHeadParams{CheckHead: head, ID: task.ID}); err != nil {
			return err
		}
	}
	if err := repository.RemoveLabel(ctx, number, readyLabel); err != nil {
		return err
	}
	return e.addActivity(ctx, repository.FullName, task.Workstream, issue, actor, fmt.Sprintf("Continued \"%s\"", issue.GetTitle()))
}

// continueItems gives the items of a fix round that continues the pull request number: each open review thread, with
// the action fix, and then ci. With no open thread and no ci, it tells the Implementer to finish the issue.
func (e *Engine) continueItems(ctx context.Context, repository github.Repository, number int64, ci string) (string, error) {
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
	items, err := fixThreads(ctx, repository, number, open, trusted)
	items += ci
	if items == "" && err == nil {
		return "\nThe human continued the task. Finish the issue and make `.mobius/check` pass.\n", nil
	}
	return items, err
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
