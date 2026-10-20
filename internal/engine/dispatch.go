package engine

import (
	"context"
	"database/sql"
	"encoding/json"
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

// fullSlotsWait is the time with all Autopilot slots full before Mobius tells the Owner. A normal task leaves
// dispatched in some minutes, so a full queue for 30 minutes shows that a task is stuck.
const fullSlotsWait = 30 * time.Minute

// dispatchReady acts on the open issues with mobius:ready of a trusted actor:
//   - An issue with no Workstream waits.
//   - A stopped task with a pull request continues (Mobius-rust#225, Mobius-rust#253). Each other issue with a live
//     task, also a task that waits for a human, loses mobius:ready.
//   - An issue with an open blocker or with mobius:triage waits.
//   - An issue with no live task continues its earlier task when the pull request of that task is still open.
//   - Each other issue gets a new task. A stopped task with no pull request ends first.
//
// The drain holds each new dispatch, and then the next poll after the drain reads the same list again. The stop of a
// hard break of the CI holds each issue except the issue of a CI task in the same way: the issue keeps mobius:ready.
func (e *Engine) dispatchReady(ctx context.Context, repository github.Repository) error {
	if e.draining() {
		return nil
	}
	stop, err := e.ciStop(ctx, repository.FullName)
	if err != nil {
		return err
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
		if len(stop) > 0 && !stop[number] {
			held = true
			continue
		}
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
		case live && task.State == "stopped" && task.PullRequest.Valid:
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
			continue
		}
		if issue.GetIssueDependenciesSummary().GetBlockedBy() > 0 || hasLabel(issue, triageLabel) {
			continue
		}
		if !live {
			earlier, found, err := e.endedTaskToContinue(ctx, repository, number)
			if err != nil {
				return err
			}
			if found {
				if err := e.continueEndedTask(ctx, repository, issue, earlier, actor); err != nil {
					return err
				}
				continue
			}
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
	if err := e.changeCopiedLabels(ctx, repository.FullName, number, []string{workingLabel}, []string{readyLabel}); err != nil {
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
	organization, _, _ := strings.Cut(repository, "/")
	return e.queries.AddEvent(ctx, store.AddEventParams{
		Time:         now(),
		Organization: organization,
		Repository:   repository,
		Workstream:   workstream,
		Issue:        int64(issue.GetNumber()),
		Actor:        actor,
		Text:         text,
		Link:         issue.GetHTMLURL(),
	})
}

// mobiusActor is the actor of an activity that Mobius adds with no GitHub login.
const mobiusActor = "Mobius"

// issueLink gives the GitHub URL of the issue, or an empty text for the issue 0 of a session with no Workstream.
func issueLink(repository string, number int64) string {
	if number == 0 {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/issues/%d", repository, number)
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

// commentEvents acts on the new comments of the issue of a live task: a new comment that is an event resets the
// counters of the task and goes to the Lead. It also continues a task in needs_human, as the author of the newest such
// comment, when that comment is newer than the move of the task to needs_human. When the newest such comment is newer
// than the last comment of the Mobius App, the question, it answers the question: mobius:question goes away.
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
		if err := e.releaseEvents(ctx, repository.FullName, task.Workstream); err != nil {
			return err
		}
	}
	if task.State == "asked" || task.State == "answered" {
		if err := acknowledge(ctx, repository, replies); err != nil {
			return err
		}
		if answered && task.State == "asked" {
			return e.answerAsked(ctx, repository, issue, task)
		}
		return nil
	}
	if task.State == "needs_human" && len(replies) > 0 {
		newest := replies[0]
		for _, reply := range replies {
			if reply.GetCreatedAt().After(newest.GetCreatedAt().Time) {
				newest = reply
			}
		}
		var since time.Time
		if task.NeedsHumanAt.Valid {
			if since, err = time.Parse(time.RFC3339Nano, task.NeedsHumanAt.String); err != nil {
				return err
			}
		}
		if newest.GetCreatedAt().After(since) {
			if err := e.resume(ctx, repository, issue, task, newest.GetUser().GetLogin()); err != nil {
				return err
			}
			if task, err = e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number}); err != nil {
				return err
			}
		}
	}
	return e.commentEventsOf(ctx, repository, task, issue, replies)
}

// answerAsked acts on the reply of a trusted user to the question of the task in asked. With Autopilot on, the task
// waits in answered for a free slot. Else the task dispatches again at once. During the stop of a hard break of the CI,
// the task of another issue than a CI task waits in answered in both cases.
func (e *Engine) answerAsked(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task) error {
	on, err := e.workstreamAutopilot(ctx, repository, task.Workstream)
	if err != nil {
		return err
	}
	stop, err := e.ciStop(ctx, repository.FullName)
	if err != nil {
		return err
	}
	if on || len(stop) > 0 && !stop[task.Issue] {
		_, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "answered", ID: task.ID, FromState: "asked"})
		return err
	}
	return e.dispatchAnswer(ctx, repository, issue, task)
}

// chatAnswerPrefix starts the comment that closeQuestionTool posts. It is the answer of the Owner, not a question.
const chatAnswerPrefix = "The Owner answered in the Lead chat:"

// answerReplies returns the comments of a trusted user after the last question comment of the Mobius App, the
// comments that dispatchAnswer puts in the dispatch event. It also returns the time of that question comment.
func (e *Engine) answerReplies(ctx context.Context, repository github.Repository, comments []*gh.IssueComment) ([]*gh.IssueComment, time.Time, error) {
	var askedAt time.Time
	for _, comment := range comments {
		if strings.EqualFold(comment.GetUser().GetLogin(), appLogin(repository.AppSlug)) && !strings.HasPrefix(comment.GetBody(), chatAnswerPrefix) && comment.GetCreatedAt().After(askedAt) {
			askedAt = comment.GetCreatedAt().Time
		}
	}
	var replies []*gh.IssueComment
	for _, comment := range comments {
		if !comment.GetCreatedAt().After(askedAt) {
			continue
		}
		isEvent, err := e.commentIsEvent(ctx, repository, comment)
		if err != nil {
			return nil, time.Time{}, err
		}
		if isEvent {
			replies = append(replies, comment)
		}
	}
	return replies, askedAt, nil
}

// dispatchAnswer dispatches the task in asked or answered again. The dispatch event has the body of the issue and each
// comment of a trusted user, and each answer comment of the Mobius App, after the last question comment of the
// Mobius App. The event keeps the newest reply of a trusted user as its comment.
func (e *Engine) dispatchAnswer(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task) error {
	comments, err := repository.Comments(ctx, task.Issue)
	if err != nil {
		return err
	}
	trusted, askedAt, err := e.answerReplies(ctx, repository, comments)
	if err != nil {
		return err
	}
	var actor string
	var newest commentRef
	var newestAt time.Time
	var replies strings.Builder
	for _, comment := range comments {
		if !comment.GetCreatedAt().After(askedAt) {
			continue
		}
		if slices.Contains(trusted, comment) {
			actor = comment.GetUser().GetLogin()
			replies.WriteString("\n\n" + eventText(comment.GetCreatedAt().Time, "reply on", issue, actor, comment.GetBody()))
			if !comment.GetCreatedAt().Before(newestAt) {
				newest, newestAt = commentRef{id: comment.GetID()}, comment.GetCreatedAt().Time
			}
		} else if strings.EqualFold(comment.GetUser().GetLogin(), appLogin(repository.AppSlug)) && strings.HasPrefix(comment.GetBody(), chatAnswerPrefix) {
			if actor == "" {
				actor = comment.GetUser().GetLogin()
			}
			replies.WriteString("\n\n" + comment.GetBody())
		}
	}
	text := eventText(time.Now(), "answer of", issue, actor, issue.GetBody()) + replies.String()
	_, err = e.redispatch(ctx, repository, issue, task, text, newest)
	return err
}

// commentsWithoutTask acts on the new comments of an issue with no live task. Mobius does not act on a comment on a
// closed issue. Any other issue goes to workstreamCommentEvents.
func (e *Engine) commentsWithoutTask(ctx context.Context, repository github.Repository, issue *gh.Issue, comments []*gh.IssueComment) error {
	events, answered, err := e.replies(ctx, repository, comments)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	if issue.GetState() != "open" {
		return e.declineComments(ctx, repository, int64(issue.GetNumber()), events, closedIssueReply)
	}
	return e.workstreamCommentEvents(ctx, repository, issue, events, answered)
}

// workstreamCommentEvents adds a comment event for the Lead for each comment of events on an open Workstream issue, or
// on an open issue below a Workstream, that has no live task. The event of the Workstream issue has no issue. An issue
// in no Workstream with mobius:triage goes to retriage. Mobius does not act on a comment on another issue in no
// Workstream. When answered is true, a comment on the Workstream issue
// answers its question: mobius:question goes away.
func (e *Engine) workstreamCommentEvents(ctx context.Context, repository github.Repository, issue *gh.Issue, events []*gh.IssueComment, answered bool) error {
	number := int64(issue.GetNumber())
	workstream, err := e.commentWorkstream(ctx, repository, issue)
	if err != nil {
		return err
	}
	if workstream == 0 && hasLabel(issue, triageLabel) {
		return e.retriage(ctx, repository, issue, events)
	}
	if workstream == 0 {
		return e.declineComments(ctx, repository, number, events, noWorkstreamReply)
	}
	if err := acknowledge(ctx, repository, events); err != nil {
		return err
	}
	if answered && workstream == number && hasLabel(issue, questionLabel) {
		if err := repository.RemoveLabel(ctx, number, questionLabel); err != nil {
			return err
		}
		if err := e.releaseEvents(ctx, repository.FullName, workstream); err != nil {
			return err
		}
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
	events, _, err := e.replies(ctx, repository, comments)
	if err != nil {
		return err
	}
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

// newComments resets the counters of the task when comments have a comment that is an event. It gives the comments
// that are events, and true when the newest of them is newer than the last comment of the Mobius App.
func (e *Engine) newComments(ctx context.Context, repository github.Repository, task store.Task, comments []*gh.IssueComment) ([]*gh.IssueComment, bool, error) {
	replies, answered, err := e.replies(ctx, repository, comments)
	if err != nil {
		return nil, false, err
	}
	if len(replies) > 0 {
		if err := e.queries.ResetTaskCounters(ctx, task.ID); err != nil {
			return nil, false, err
		}
	}
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
// question of the Mobius App. A stop comment is no question.
func (e *Engine) replies(ctx context.Context, repository github.Repository, comments []*gh.IssueComment) ([]*gh.IssueComment, bool, error) {
	var askedAt, repliedAt time.Time
	var replies []*gh.IssueComment
	for _, comment := range comments {
		createdAt := comment.GetCreatedAt().Time
		if strings.EqualFold(comment.GetUser().GetLogin(), appLogin(repository.AppSlug)) && !isStopComment(comment.GetBody()) && createdAt.After(askedAt) {
			askedAt = createdAt
		}
		isEvent, err := e.commentIsEvent(ctx, repository, comment)
		if err != nil {
			return nil, false, err
		}
		if isEvent {
			replies = append(replies, comment)
			if createdAt.After(repliedAt) {
				repliedAt = createdAt
			}
		}
	}
	return replies, len(replies) > 0 && repliedAt.After(askedAt), nil
}

// commentIsEvent tells if the comment is an event for the Lead: a comment of a trusted user. A comment of the Lead
// session has a trusted user as its author and the Mobius App in performed_via_github_app, so it is no event. An
// answer of the Owner from the app has the same marks, and its id is in owner_answers.
func (e *Engine) commentIsEvent(ctx context.Context, repository github.Repository, comment *gh.IssueComment) (bool, error) {
	if !e.trustedUser(comment.GetUser().GetLogin()) {
		return false, nil
	}
	if comment.GetPerformedViaGithubApp().GetSlug() != repository.AppSlug {
		return true, nil
	}
	return e.queries.IsOwnerAnswer(ctx, store.IsOwnerAnswerParams{Repository: repository.FullName, CommentID: comment.GetID()})
}

// ask posts the question text on the issue of a live task of the Workstream or on the Workstream issue, adds
// mobius:question, and adds an Inbox item that replaces the stopped item of the issue. The reply comes later as a
// comment event. A dispatched task moves to asked and frees its Autopilot slot, and the reply dispatches it again. The
// task moves before mobius:working goes away, because setStateLabels adds mobius:working again to a dispatched task.
func (e *Engine) ask(ctx context.Context, c caller, repository github.Repository, input askInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	if err := refuseQuestions(input.Text, input.Questions); err != nil {
		return "", err
	}
	comment, questions, err := questionsComment(input)
	if err != nil {
		return "", err
	}
	var task store.Task
	if input.N != c.workstream {
		var err error
		if task, err = e.workstreamTask(ctx, repository, c.workstream, input.N); err != nil {
			return "", err
		}
	}
	issue, err := repository.Issue(ctx, input.N)
	if err != nil {
		return "", err
	}
	if issue == nil {
		return "", refuse("#%d does not exist.", input.N)
	}
	if _, err := repository.AddComment(ctx, input.N, comment); err != nil {
		return "", err
	}
	if err := repository.AddLabel(ctx, input.N, questionLabel); err != nil {
		return "", err
	}
	if err := e.dismissIssueItems(ctx, repository.FullName, input.N, questionKind); err != nil {
		return "", err
	}
	err = e.addInboxItem(ctx, store.AddInboxItemParams{
		Kind:         questionKind,
		Organization: repository.Owner(),
		Repository:   repository.FullName,
		Workstream:   c.workstream,
		Issue:        input.N,
		Title:        issue.GetTitle(),
		Reason:       "The Lead asks a question",
		Text:         input.Text,
		Questions:    questions,
		Link:         issue.GetHTMLURL(),
	})
	if err != nil {
		return "", err
	}
	if err := e.dismissIssueItems(ctx, repository.FullName, input.N, stoppedKind); err != nil {
		return "", err
	}
	if task.State == "dispatched" {
		if _, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "asked", ID: task.ID, FromState: "dispatched"}); err != nil {
			return "", err
		}
		if err := repository.RemoveLabel(ctx, input.N, workingLabel); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("Asked on #%d.", input.N), nil
}

// refuseQuestions refuses an empty text, a question with no text, a question with fewer than 2 options, and an option
// with no title.
func refuseQuestions(text string, questions []Question) error {
	if empty(text) {
		return refuse("text must not be empty.")
	}
	for _, question := range questions {
		if empty(question.Text) {
			return refuse("The text of a question must not be empty.")
		}
		if len(question.Options) < 2 {
			return refuse("A question needs 2 options or more.")
		}
		for _, option := range question.Options {
			if empty(option.Title) {
				return refuse("The title of an option must not be empty.")
			}
		}
	}
	return nil
}

// questionsComment gives the comment of ask and the JSON of the questions for the Inbox item. The JSON is empty for a
// call with no questions.
func questionsComment(input askInput) (string, string, error) {
	stored, err := questionsJSON(input.Questions)
	if err != nil || stored == "" {
		return input.Text, "", err
	}
	var comment strings.Builder
	comment.WriteString(input.Text)
	for i, question := range input.Questions {
		selection := "Select one."
		if question.Multiple {
			selection = "Select one or more."
		}
		fmt.Fprintf(&comment, "\n\n**%d. %s** %s\n", i+1, question.Text, selection)
		for j, option := range question.Options {
			if option.Details == "" {
				fmt.Fprintf(&comment, "\n%d. **%s**", j+1, option.Title)
			} else {
				fmt.Fprintf(&comment, "\n%d. **%s**: %s", j+1, option.Title, option.Details)
			}
		}
	}
	return comment.String(), stored, nil
}

// questionsJSON gives the JSON of the questions for the Inbox item, or an empty text for no questions.
func questionsJSON(questions []Question) (string, error) {
	if len(questions) == 0 {
		return "", nil
	}
	stored, err := json.Marshal(questions)
	return string(stored), err
}

// closeQuestion posts text on the issue as the Mobius App, continues a task in asked as after a reply on the issue,
// removes mobius:question, and dismisses the open question items of the issue. An issue whose task is in needs_human
// gets its stopped item back. mobius:question stays until the task continues, so a failed call can run again. A call
// posts no comment when the newest comment of the Mobius App already has text.
// task is the zero value for the Workstream issue.
func (e *Engine) closeQuestion(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task, text string) error {
	number := int64(issue.GetNumber())
	comments, err := repository.Comments(ctx, number)
	if err != nil {
		return err
	}
	var newest *gh.IssueComment
	for _, comment := range comments {
		if strings.EqualFold(comment.GetUser().GetLogin(), appLogin(repository.AppSlug)) && (newest == nil || comment.GetCreatedAt().After(newest.GetCreatedAt().Time)) {
			newest = comment
		}
	}
	if newest == nil || newest.GetBody() != text {
		if _, err := repository.AddComment(ctx, number, text); err != nil {
			return err
		}
	}
	if task.State == "asked" {
		if err := e.answerAsked(ctx, repository, issue, task); err != nil {
			return err
		}
	}
	if err := repository.RemoveLabel(ctx, number, questionLabel); err != nil {
		return err
	}
	if err := e.changeCopiedLabels(ctx, repository.FullName, number, nil, []string{questionLabel}); err != nil {
		return err
	}
	items, err := e.Inbox(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Repository != repository.FullName || item.Issue != number || item.Kind != questionKind {
			continue
		}
		if err := e.reopenStoppedItem(ctx, item); err != nil {
			return err
		}
		if err := e.Dismiss(ctx, item.ID); err != nil {
			return err
		}
	}
	return nil
}

// closeQuestionTool closes the question of the Lead on the Workstream issue or on the issue of a live task of the
// Workstream, after the Owner answered it in the chat.
func (e *Engine) closeQuestionTool(ctx context.Context, c caller, repository github.Repository, input answerInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	if empty(input.Answer) {
		return "", refuse("answer must not be empty.")
	}
	var task store.Task
	if input.N != c.workstream {
		var err error
		if task, err = e.workstreamTask(ctx, repository, c.workstream, input.N); err != nil {
			return "", err
		}
	}
	issue, err := existingIssue(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	if !hasLabel(issue, questionLabel) {
		return "", refuse("#%d has no open question.", input.N)
	}
	text := chatAnswerPrefix + "\n\n" + quote(input.Answer)
	if err := e.closeQuestion(ctx, repository, issue, task, text); err != nil {
		return "", err
	}
	return fmt.Sprintf("Closed the question on #%d.", input.N), nil
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
// trusted user starts it again. A task in answered in a Workstream with Autopilot off dispatches again at once. The
// drain holds each new dispatch. During the stop of a hard break of the CI, Autopilot starts only the issues in stop.
func (e *Engine) startAutopilot(ctx context.Context, repository github.Repository) error {
	if e.draining() {
		return nil
	}
	stop, err := e.ciStop(ctx, repository.FullName)
	if err != nil {
		return err
	}
	if err := e.dispatchAnsweredWithoutAutopilot(ctx, repository, stop); err != nil {
		return err
	}
	active, err := e.queries.CountActiveTasks(ctx)
	if err != nil {
		return err
	}
	if active < int64(e.config.Roles.Implementer.Max) {
		if err := e.queries.EndFullSlots(ctx); err != nil {
			return err
		}
	}
	if !e.copied[repository.FullName] {
		return nil
	}
	workstreams, err := e.queries.ListAutopilotWorkstreams(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, workstream := range workstreams {
		full, err := e.autopilotTree(ctx, repository, workstream, workstream, stop)
		if err != nil || full {
			return err
		}
	}
	return nil
}

// dispatchAnsweredWithoutAutopilot dispatches again each task in answered whose Workstream has Autopilot off, for
// example when the Owner turned Autopilot off after the reply. The task of an issue that is not in stop waits.
func (e *Engine) dispatchAnsweredWithoutAutopilot(ctx context.Context, repository github.Repository, stop map[int64]bool) error {
	tasks, err := e.queries.ListLiveTasks(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.State != "answered" || len(stop) > 0 && !stop[task.Issue] {
			continue
		}
		on, err := e.workstreamAutopilot(ctx, repository, task.Workstream)
		if err != nil {
			return err
		}
		if on {
			continue
		}
		issue, err := repository.Issue(ctx, task.Issue)
		if err != nil || issue == nil {
			return err
		}
		if err := e.dispatchAnswer(ctx, repository, issue, task); err != nil {
			return err
		}
	}
	return nil
}

// autopilotTree dispatches the tasks below parent in the Workstream, depth first. It gives true when as many tasks
// are active as roles.implementer.max. When stop has some issues, it starts only these.
func (e *Engine) autopilotTree(ctx context.Context, repository github.Repository, workstream, parent int64, stop map[int64]bool) (bool, error) {
	issues, err := repository.SubIssues(ctx, parent)
	if err != nil {
		return false, err
	}
	for _, issue := range issues {
		if hasLabel(issue, workstreamLabel) || inOtherRepository(issue, repository.FullName) {
			continue
		}
		number := int64(issue.GetNumber())
		if issue.GetState() == "open" && issue.GetIssueDependenciesSummary().GetBlockedBy() == 0 && !hasLabel(issue, triageLabel) {
			task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return false, err
			}
			answered := err == nil && task.State == "answered"
			start := answered
			if errors.Is(err, sql.ErrNoRows) && e.TrustedAuthor(repository.AppSlug, issue.GetUser().GetLogin()) {
				had, err := e.queries.HasTask(ctx, store.HasTaskParams{Repository: repository.FullName, Issue: number})
				if err != nil {
					return false, err
				}
				start = !had
			}
			if start && (len(stop) == 0 || stop[number]) {
				active, err := e.queries.CountActiveTasks(ctx)
				if err != nil {
					return false, err
				}
				if active >= int64(e.config.Roles.Implementer.Max) {
					return true, e.noteFullSlots(ctx, repository, issue, workstream)
				}
				if answered {
					err = e.dispatchAnswer(ctx, repository, issue, task)
				} else {
					err = e.dispatch(ctx, repository, issue, workstream, appLogin(repository.AppSlug))
				}
				if err != nil {
					return false, err
				}
			}
		}
		full, err := e.autopilotTree(ctx, repository, workstream, number, stop)
		if err != nil || full {
			return full, err
		}
	}
	return false, nil
}

// noteFullSlots keeps the time when Autopilot first could not start a task because all slots were full. When
// fullSlotsWait passed since then, it adds one activity on the issue that Autopilot could not start, with the tasks
// that hold the slots. It writes item_at after the activity, so a failed step runs again on the next poll.
func (e *Engine) noteFullSlots(ctx context.Context, repository github.Repository, issue *gh.Issue, workstream int64) error {
	current := e.timeNow().UTC()
	if err := e.queries.StartFullSlots(ctx, current.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	full, err := e.queries.GetFullSlots(ctx)
	if err != nil {
		return err
	}
	since, err := time.Parse(time.RFC3339Nano, full.Since)
	if err != nil {
		return err
	}
	if full.ItemAt.Valid || current.Sub(since) < fullSlotsWait {
		return nil
	}
	tasks, err := e.queries.ListActiveTasks(ctx)
	if err != nil {
		return err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "All %d Autopilot slots are full for %s. Autopilot could not start #%d \"%s\". These tasks hold the slots:", e.config.Roles.Implementer.Max, current.Sub(since).Round(time.Minute), issue.GetNumber(), issue.GetTitle())
	for _, task := range tasks {
		entered, err := time.Parse(time.RFC3339Nano, task.StateAt)
		if err != nil {
			return err
		}
		fmt.Fprintf(&text, "\n- %s#%d is %s for %s", task.Repository, task.Issue, task.State, current.Sub(entered).Round(time.Minute))
	}
	if err := e.addActivity(ctx, repository.FullName, workstream, issue, appLogin(repository.AppSlug), text.String()); err != nil {
		return err
	}
	return e.queries.SetFullSlotsItemAt(ctx, sql.NullString{String: current.Format(time.RFC3339Nano), Valid: true})
}

// resume continues the task of the issue that waits for a human, or the stopped task with a pull request, for the
// actor: a task with a pull request continues as continueTask says, and a task with no pull request goes back to the
// Lead with a dispatch event. The counters of the task go back to 0 when the actor is not the Mobius App.
// mobius:ready goes away last, so a failure before the round starts leaves the issue in the ready list.
func (e *Engine) resume(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task, actor string) error {
	if !strings.EqualFold(actor, appLogin(repository.AppSlug)) {
		if err := e.queries.ResetTaskCounters(ctx, task.ID); err != nil {
			return err
		}
	}
	var moved bool
	var err error
	if task.PullRequest.Valid {
		var pullRequest *gh.PullRequest
		if pullRequest, err = repository.PullRequest(ctx, task.PullRequest.Int64); err != nil {
			return err
		}
		moved, err = e.continueTask(ctx, repository, issue.GetTitle(), task, pullRequest, task.State)
	} else {
		moved, err = e.redispatch(ctx, repository, issue, task, eventText(time.Now(), "resume of", issue, actor, issue.GetBody()), commentRef{})
	}
	if err != nil || !moved {
		return err
	}
	if err := repository.RemoveLabel(ctx, int64(issue.GetNumber()), readyLabel); err != nil {
		return err
	}
	return e.addActivity(ctx, repository.FullName, task.Workstream, issue, actor, fmt.Sprintf("Continued \"%s\"", issue.GetTitle()))
}

// redispatch gives the task with no pull request back to the Lead with a dispatch event that has text and comment. It
// tells if the task moved.
func (e *Engine) redispatch(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task, text string, comment commentRef) (bool, error) {
	if err := e.unstop(ctx, repository, task); err != nil {
		return false, err
	}
	moved, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "dispatched", ID: task.ID, FromState: task.State})
	if err != nil || moved == 0 {
		return false, err
	}
	err = e.addCommentLeadEvent(ctx, repository.FullName, task.Workstream, sql.NullInt64{Int64: int64(issue.GetNumber()), Valid: true}, "dispatch", text, comment)
	if err != nil {
		_, stateErr := e.setTaskState(ctx, store.SetTaskStateParams{State: task.State, ID: task.ID, FromState: "dispatched"})
		return false, errors.Join(err, stateErr)
	}
	return true, nil
}

// unstop marks the task as working again: it adds mobius:working and removes mobius:needs-human.
func (e *Engine) unstop(ctx context.Context, repository github.Repository, task store.Task) error {
	if err := repository.AddLabel(ctx, task.Issue, workingLabel); err != nil {
		return err
	}
	return removeNeedsHuman(ctx, repository, task)
}

// continueTask continues the task with the pull request. A pull request with a merge conflict gets a conflict round.
// Another pull request gets a fix round with its open review threads and the failed check runs of its head. The task
// moves from the state from, and goes back to that state when the round fails to start. It tells if the task
// moved.
func (e *Engine) continueTask(ctx context.Context, repository github.Repository, title string, task store.Task, pullRequest *gh.PullRequest, from string) (bool, error) {
	if err := e.unstop(ctx, repository, task); err != nil {
		return false, err
	}
	conflict := pullRequest.Mergeable != nil && !pullRequest.GetMergeable() || behind(pullRequest)
	to := "working"
	if conflict {
		to = "checks"
	}
	parent, err := e.newestSession(ctx, task)
	if err != nil {
		return false, err
	}
	items, ci := "", ""
	if !conflict {
		head := pullRequest.GetHead().GetSHA()
		if ci, err = ciItems(ctx, repository, head); err != nil {
			return false, err
		}
		if ci != "" {
			diagnosis, err := e.endedDiagnosis(ctx, repository, int64(pullRequest.GetNumber()), head)
			if err != nil {
				return false, err
			}
			ci += diagnosis
		}
		if items, err = e.continueItems(ctx, repository, task, title, pullRequest.GetNumber(), ci); err != nil {
			return false, err
		}
	}
	moved, err := e.setTaskState(ctx, store.SetTaskStateParams{State: to, ID: task.ID, FromState: from})
	if err != nil || moved == 0 {
		return false, err
	}
	if conflict {
		task.State = to
		err = e.conflictRound(ctx, repository, task, pullRequest)
	} else {
		cause := reviewCause
		if ci != "" {
			cause = ciCause
		}
		err = e.fixRound(ctx, repository, round{task: task, title: title, pullRequest: pullRequest, counts: true, items: items, parent: parent, failedCheck: ci != "", cause: cause})
	}
	if err != nil {
		_, stateErr := e.setTaskState(ctx, store.SetTaskStateParams{State: from, ID: task.ID, FromState: to})
		return false, errors.Join(err, stateErr)
	}
	if ci != "" {
		head := sql.NullString{String: pullRequest.GetHead().GetSHA(), Valid: true}
		if err := e.queries.SetTaskCheckHead(ctx, store.SetTaskCheckHeadParams{CheckHead: head, ID: task.ID}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// continueItems gives the items of a fix round that continues the pull request number of the task: each open review
// thread, with the action fix, and then ci. It first sends the follow-up threads to the Lead. With no open thread and
// no ci, it tells the Implementer to finish the issue.
func (e *Engine) continueItems(ctx context.Context, repository github.Repository, task store.Task, title string, number int, ci string) (string, error) {
	trusted := func(login string) bool { return e.TrustedAuthor(repository.AppSlug, login) }
	threads, err := repository.ReviewThreads(ctx, int64(number))
	if err != nil {
		return "", err
	}
	if err := e.sendFollowUps(ctx, repository, task, title, number, threads, appLogin(repository.AppSlug), trusted); err != nil {
		return "", err
	}
	var open []int64
	for _, thread := range threads {
		if openThread(thread, trusted, appLogin(repository.AppSlug)) {
			open = append(open, thread.Comment)
		}
	}
	items, err := fixThreads(ctx, repository, int64(number), open, trusted)
	items += ci
	if items == "" && err == nil {
		return "\nThe human continued the task. Finish the issue.\n", nil
	}
	return items, err
}

// openThread tells if the review thread is open: it is not resolved, a trusted author started it, and its last trusted
// comment is not a reply of the Mobius App with the login app. The first comment of the Mobius App is a finding of the
// Reviewer, except in a follow-up thread with no reply of a trusted user, which is for the Lead.
func openThread(thread github.ReviewThread, trusted func(string) bool, app string) bool {
	var authors []string
	for _, author := range thread.Authors {
		if trusted(author) {
			authors = append(authors, author)
		}
	}
	if _, isFollowUp := followUpBody(thread, app, trusted); thread.Resolved || isFollowUp || len(authors) == 0 || thread.Authors[0] != authors[0] {
		return false
	}
	return len(authors) == 1 || !strings.EqualFold(authors[len(authors)-1], app)
}
