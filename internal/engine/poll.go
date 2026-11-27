package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// issuesEndpoint is the endpoint of the issue list in the sync_cursors table.
const issuesEndpoint = "issues"

// poll reads the repositories of the Apps. When it read all Apps, it ends the tasks of each repository that no App
// gives, it removes the copy of each repository that it did not find, and it sends the changelog of a new release. Then, for each repository, it fixes the labels
// at the first sight in this run of the server, copies the open Workstreams until that step works one time, hands
// the lost tasks to a human and starts the Workers of the earlier run again until that step works one time, reads the
// changed issues, reads the CI of the default branch, dispatches the ready issues, starts the tasks of the Workstreams
// with Autopilot, checks the live tasks, and starts the Worker again of each task that has none. The work of a repository starts again at its first
// poll, because the work needs GitHub. At the end, it gives the Skills Lead the weekly skill review, it tells the Leads of the cycles of blockers, and the pull requests with work for an agent replace the ones of the
// last poll. With no App, the poll does nothing.
func (e *Engine) poll(ctx context.Context) {
	apps, err := e.queries.ListGitHubApps(ctx)
	if err != nil || len(apps) == 0 {
		if err != nil {
			log.Printf("read the GitHub Apps: %v", err)
		}
		return
	}
	e.expireKeptSessions()
	known := repositoryKeys(e.github.Repositories())
	var complete bool
	err = e.timeStep("refresh", true, func() error {
		var refreshErr error
		complete, refreshErr = e.github.Refresh(ctx)
		return refreshErr
	})
	if err != nil {
		log.Printf("read the repositories of the GitHub Apps: %v", err)
		return
	}
	repositories := e.github.Repositories()
	if !slices.Equal(known, repositoryKeys(repositories)) {
		e.publish(Change{Repositories: true})
	}
	if complete {
		if !e.oldSkillUsesMoved {
			e.oldSkillUsesMoved = true
			e.moveOldSkillUses(ctx)
		}
		if err := e.lostAccess(ctx, repositories); err != nil {
			log.Printf("end the tasks of the repositories with no access: %v", err)
		}
		if err := e.forgetCopies(ctx, repositories); err != nil {
			log.Printf("remove the copies of the repositories with no access: %v", err)
		}
		e.sendChangelog(ctx)
	}
	work := map[int64]Work{}
	for _, repository := range repositories {
		if !e.labelsFixed[repository.FullName] {
			e.labelsFixed[repository.FullName] = true
			if err := e.timeStep(repository.FullName+" FixLabels", true, func() error { return FixLabels(ctx, repository) }); err != nil {
				log.Printf("fix the labels of %s: %v", repository.FullName, err)
			}
		}
		if !e.copied[repository.FullName] {
			if err := e.timeStep(repository.FullName+" syncCopy", true, func() error { return e.syncCopy(ctx, repository) }); err != nil {
				log.Printf("copy the Workstreams of %s: %v", repository.FullName, err)
			} else {
				e.copied[repository.FullName] = true
			}
		}
		start := time.Now()
		err := e.pollRepository(ctx, repository, work)
		log.Printf("poll %s: %s", repository.FullName, time.Since(start).Round(time.Millisecond))
		if err != nil {
			log.Printf("poll %s: %v", repository.FullName, err)
			// A failed poll keeps the old work of the repository, so that it does not let a new ticket pass.
			for task, old := range e.currentWork() {
				if _, ok := work[task]; !ok && old.Repository == repository.FullName {
					work[task] = old
				}
			}
		}
	}
	if err := e.syncMissingWorkstreams(ctx, true); err != nil {
		log.Printf("check the missing Workstreams: %v", err)
	}
	e.reviewSkills(ctx, repositories)
	e.reportBlockerCycles(ctx)
	e.ReplaceWork(work)
}

// repositoryKeys gives the sorted full name and App of each repository.
func repositoryKeys(repositories []github.Repository) []string {
	keys := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		keys = append(keys, fmt.Sprintf("%s %d", repository.FullName, repository.AppID))
	}
	slices.Sort(keys)
	return keys
}

// recover hands the lost tasks of repository to a human, gives the messages of the Owner and the events that wait
// from the earlier run of the server to the agents of the chats, starts a Curator for the requests of the Owner that wait, starts each Triager turn of the earlier run again, and starts each Worker of the earlier
// run again.
func (e *Engine) recover(ctx context.Context, repository github.Repository) error {
	if err := e.handLostTasks(ctx, repository); err != nil {
		return err
	}
	organization, _, _ := strings.Cut(repository.FullName, "/")
	if err := e.giveEarlierMessages(ctx, organization, repository.FullName); err != nil {
		return err
	}
	if err := e.giveEarlierMessages(ctx, organization, ""); err != nil {
		return err
	}
	waiting, err := e.queries.ListWaitingLeadWorkstreams(ctx)
	if err != nil {
		return err
	}
	for _, workstream := range waiting {
		if workstream.Repository != repository.FullName {
			continue
		}
		if err := e.wakeEvents(ctx, workstream.Repository, workstream.Workstream); err != nil {
			return err
		}
	}
	requests, err := e.queries.ListCuratorRequests(ctx, repository.FullName)
	if err != nil {
		return err
	}
	if len(requests) > 0 {
		if err := e.startCurator(ctx, repository.FullName); err != nil {
			return err
		}
	}
	if err := e.startHeldTriagers(ctx, repository); err != nil {
		return err
	}
	e.recovered[repository.FullName] = true
	return e.restartLost(ctx, repository, true)
}

// pollRepository recovers the work of the earlier run until that works one time, and then acts on the changes of the
// repository. It adds the pull requests with work for an agent to work.
func (e *Engine) pollRepository(ctx context.Context, repository github.Repository, work map[int64]Work) error {
	steps := []pollStep{
		{"addTriageRows", func() error { return e.addTriageRows(ctx, repository) }},
		{"recover", func() error {
			if e.recovered[repository.FullName] {
				return nil
			}
			return e.recover(ctx, repository)
		}},
		{"upgradeReadyStarts", func() error { return e.upgradeReadyStarts(ctx, repository) }},
		{"changedIssues", func() error { return e.changedIssues(ctx, repository) }},
		{"applyIncomingChanges", func() error { return e.applyIncomingChanges(ctx, repository) }},
		{"retryWorkstreamCloses", func() error { return e.retryWorkstreamCloses(ctx, repository) }},
		{"checkCopiedTrees", func() error { return e.checkCopiedTreesLoop(ctx, repository) }},
		{"startHeldTriagers", func() error { return e.startHeldTriagers(ctx, repository) }},
		{"readDefaultBranchCI", func() error { return e.readDefaultBranchCI(ctx, repository) }},
		{"createCheckTimeoutIssue", func() error { return e.createCheckTimeoutIssue(ctx, repository) }},
		{"createInitFailureIssue", func() error { return e.createInitFailureIssue(ctx, repository) }},
		{"createCIFailureIssues", func() error { return e.createCIFailureIssues(ctx, repository) }},
		{"endCIFailures", func() error { return e.endCIFailures(ctx, repository) }},
		{"dispatchReady", func() error { return e.dispatchReady(ctx, repository) }},
		{"startAutopilot", func() error { return e.startAutopilot(ctx, repository) }},
		{"closeInboxItems", func() error { return e.closeInboxItems(ctx, repository) }},
		{"continuePausedMerges", func() error { return e.continuePausedMerges(ctx, repository) }},
		{"freeClosedQuestionHolds", func() error { return e.freeClosedQuestionHolds(ctx, repository.FullName) }},
		{"retryFailedChats", func() error { return e.retryFailedChats(ctx, repository.FullName, repository.Owner()) }},
		{"startIdleLeads", func() error { return e.startIdleLeads(ctx, repository.FullName) }},
		{"sendOutgoingChanges", func() error { return e.sendOutgoingChanges(ctx, repository) }},
		{"checkTasks", func() error {
			start := time.Now()
			err := e.checkTasks(ctx, repository, work)
			log.Printf("check the tasks of %s: %s", repository.FullName, time.Since(start).Round(time.Millisecond))
			return err
		}},
		{"sendStopNotices", func() error { return e.sendStopNotices(ctx, repository.FullName, 0) }},
		{"restartLost", func() error { return e.restartLost(ctx, repository, false) }},
	}
	for _, step := range steps {
		if err := e.timeStep(repository.FullName+" "+step.name, false, step.run); err != nil {
			return err
		}
	}
	return nil
}

// pollStep is a step of pollRepository. Its name is the name of its function.
type pollStep struct {
	name string
	run  func() error
}

// slowStep is the time from which timeStep logs a step that is not always logged.
const slowStep = time.Second

// timeStep runs step and logs its time under name when always is true or the step takes slowStep or more.
func (e *Engine) timeStep(name string, always bool, step func() error) error {
	start := e.timeNow()
	err := step()
	if elapsed := e.timeNow().Sub(start); always || elapsed >= slowStep {
		log.Printf("poll step %s: %s", name, elapsed.Round(time.Millisecond))
	}
	return err
}

// checkCopiedTreesLoop checks the copied trees of repository and acts on the incoming changes of the Workstreams that
// they touch, until a check touches none or no change enters a Workstream. It stops after ten rounds.
func (e *Engine) checkCopiedTreesLoop(ctx context.Context, repository github.Repository) error {
	forceWalk := map[issueRef]bool{}
	for range 10 {
		touched := e.checkCopiedTrees(ctx, repository, forceWalk)
		if len(touched) == 0 {
			return nil
		}
		anyEntered := false
		for _, name := range touched {
			entered, err := e.applyIncomingChangesOf(ctx, name, e.changeRepository(name, repository))
			if err != nil {
				return err
			}
			for _, ref := range entered {
				forceWalk[ref] = true
				anyEntered = true
			}
		}
		if !anyEntered {
			return nil
		}
	}
	return nil
}

// changedIssues reads the issues and pull requests that changed at or after the `since` cursor, reads the new comments
// of the repository with two calls, and reads the reviews and the review threads of the open pull requests with one
// call. Then it starts the Triager of an issue, acts on the comments, updates the copy, and
// moves the cursors to the last change. It adds the incoming changes of the new events of the fields of a Workstream
// (the labels, the state and the title), and of a new edit of the Brief of a Workstream, in the transaction
// of the issue cursor. The first poll of a repository has no issues row in the cursors, so it reads
// all issues, and it cannot see which event is new. It reads no comment: the next poll starts each comment list at the
// issue cursor. A repository with no issue has a row with no time, so its next poll is not a first poll.
// When treeBodyChange asks for a new read, the poll keeps the cursor and stores no ETag, so the next poll reads the
// same issues, and no status 304 hides the edit.
func (e *Engine) changedIssues(ctx context.Context, repository github.Repository) error {
	cursor, err := e.queries.GetSyncCursor(ctx, store.GetSyncCursorParams{Repository: repository.FullName, Endpoint: issuesEndpoint})
	firstPoll := errors.Is(err, sql.ErrNoRows)
	if err != nil && !firstPoll {
		return err
	}
	if firstPoll && !e.copied[repository.FullName] {
		return nil
	}
	var since time.Time
	if cursor.Since.Valid {
		if since, err = time.Parse(time.RFC3339, cursor.Since.String); err != nil {
			return err
		}
	}
	labelChanges := e.labelChangeCount(repository.FullName)
	page, changed, err := repository.IssuesSince(ctx, since, cursor.Etag.String)
	if err != nil || !changed {
		return err
	}
	e.markChangedPullRequests(repository, page.Issues)
	var conversation map[int64][]*gh.IssueComment
	var review map[int64][]*gh.PullRequestComment
	var commentCursors []store.SetSyncCursorParams
	if !firstPoll {
		var conversationCursor, reviewCursor store.SetSyncCursorParams
		e.ownerAnswerMu.Lock()
		conversation, conversationCursor, err = newComments(ctx, e.queries, repository, issueCommentsEndpoint, since, repository.CommentsSince, issueCommentNumber)
		if err == nil {
			err = e.dropHandledAnswers(ctx, repository, conversation)
		}
		e.ownerAnswerMu.Unlock()
		if err != nil {
			return err
		}
		review, reviewCursor, err = newComments(ctx, e.queries, repository, reviewCommentsEndpoint, since, repository.ReviewCommentsSince, reviewCommentNumber)
		if err != nil {
			return err
		}
		commentCursors = []store.SetSyncCursorParams{conversationCursor, reviewCursor}
	}
	unrouted := map[int64]bool{}
	for number := range conversation {
		unrouted[number] = true
	}
	for number := range review {
		unrouted[number] = true
	}
	for _, issue := range page.Issues {
		delete(unrouted, int64(issue.GetNumber()))
	}
	// A comment that GitHub wrote after it gave the page has its issue or pull request outside the page.
	var outside []*gh.Issue
	for _, number := range slices.Sorted(maps.Keys(unrouted)) {
		issue, err := repository.Issue(ctx, number)
		if err != nil {
			return err
		}
		if issue != nil {
			outside = append(outside, issue)
		}
	}
	if err := e.readPullRequests(ctx, repository, openPullRequests(slices.Concat(page.Issues, outside))); err != nil {
		return err
	}
	listChanged := false
	var changeEvents []changeEvent
	var bodyChanges []store.AddIncomingChangeParams
	readAgain := false
	stateIssues, statePulls, err := e.stateLabelItems(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, issue := range page.Issues {
		number := int64(issue.GetNumber())
		triaged, err := e.startTriage(ctx, repository, issue, conversation[number], since, firstPoll)
		if err != nil {
			return err
		}
		if !triaged {
			if err := e.routeComments(ctx, repository, issue, conversation[number], review[number]); err != nil {
				return err
			}
		}
		if issue.IsPullRequest() {
			if !firstPoll {
				pullEvents, err := e.pullStateLabelEvents(ctx, repository, number, since, statePulls[number])
				if err != nil {
					return err
				}
				changeEvents = append(changeEvents, pullEvents...)
			}
			continue
		}
		labeled := hasLabel(issue, workstreamLabel)
		work, err := e.hasWork(ctx, repository.FullName, number)
		if err != nil {
			return err
		}
		stored, err := e.queries.HasWorkstream(ctx, store.HasWorkstreamParams{Repository: repository.FullName, Number: number})
		if err != nil {
			return err
		}
		treeRow, inTree, err := e.treeIssueOf(ctx, repository.FullName, number)
		if err != nil {
			return err
		}
		// An issue with an own write has the event of that write, also when a write removed its Workstream label.
		writing, err := e.queries.HasOwnWrite(ctx, store.HasOwnWriteParams{Repository: repository.FullName, Number: number})
		if err != nil {
			return err
		}
		changing := labeled || stored || inTree || writing
		triaging, err := e.hasTriageState(ctx, repository.FullName, issue)
		if err != nil {
			return err
		}
		hasReady := hasLabel(issue, readyLabel)
		var events []*gh.IssueEvent
		if changing || work || triaging || stateIssues[number] || !firstPoll && hasReady {
			if events, err = repository.IssueEvents(ctx, number); err != nil {
				return err
			}
		}
		for _, event := range events {
			if event.Actor == nil || event.GetCreatedAt().Before(since) {
				continue
			}
			// The cursor has a precision of one second, so an event in the second of the cursor can be new. An incoming
			// change keeps the id of each event that the poll read, so a second read does nothing.
			if !firstPoll && (changing || triaging && isTriageEvent(event) || hasReady && isStartEvent(event) || stateIssues[number] && isStateLabelEvent(event)) {
				changeEvents = append(changeEvents, changeEvent{number, event})
			}
		}
		if !firstPoll {
			var change *store.AddIncomingChangeParams
			var waiting bool
			if stored {
				change, err = e.bodyChange(ctx, repository, issue, since)
			} else if inTree {
				change, waiting, err = e.treeBodyChange(ctx, repository, treeRow, issue, since)
			}
			if err != nil {
				return err
			}
			readAgain = readAgain || waiting
			if change != nil {
				bodyChanges = append(bodyChanges, *change)
			}
		}
		// After a failed update, the next poll copies the repository again.
		if e.copied[repository.FullName] {
			copyChanged, err := e.updateCopy(ctx, repository, issue, labelChanges)
			if err != nil {
				log.Printf("update the copy of %s: %v", repository.FullName, err)
				delete(e.copied, repository.FullName)
			}
			listChanged = listChanged || copyChanged
		}
	}
	for _, issue := range outside {
		number := int64(issue.GetNumber())
		if err := e.routeComments(ctx, repository, issue, conversation[number], review[number]); err != nil {
			return err
		}
	}
	if listChanged {
		e.publish(Change{Workstreams: true})
	}
	for _, cursor := range commentCursors {
		if err := e.queries.SetSyncCursor(ctx, cursor); err != nil {
			return err
		}
	}
	if !readAgain {
		for _, issue := range page.Issues {
			if issue.GetUpdatedAt().After(since) {
				since = issue.GetUpdatedAt().Time
			}
		}
	}
	app := appLogin(repository.AppSlug)
	return e.inTx(ctx, func(q *store.Queries) error {
		if err := addEventChanges(ctx, q, repository, changeEvents); err != nil {
			return err
		}
		for _, change := range bodyChanges {
			if change.Metadata == "{}" {
				known, err := q.HasIncomingChange(ctx, change.ExternalID)
				if err != nil {
					return err
				}
				if known {
					continue
				}
				if strings.EqualFold(change.Actor, app) {
					own, err := q.DeleteOldestOwnWrite(ctx, store.DeleteOldestOwnWriteParams{
						Repository: repository.FullName,
						Number:     change.Number,
						Field:      bodyField,
						Label:      "",
						Change:     editedChange,
					})
					if err != nil {
						return err
					}
					if own > 0 {
						change.Status = "done"
					}
				}
			}
			if err := q.AddIncomingChange(ctx, change); err != nil {
				return err
			}
		}
		return q.SetSyncCursor(ctx, store.SetSyncCursorParams{
			Repository: repository.FullName,
			Endpoint:   issuesEndpoint,
			// The rows of the Rust version have the same format.
			Since: sql.NullString{String: since.UTC().Format(time.RFC3339), Valid: !since.IsZero()},
			Etag:  sql.NullString{String: page.ETag, Valid: page.ETag != "" && !readAgain},
		})
	})
}

// routeComments gives the new comments of the issue or the pull request to the code that acts on them.
func (e *Engine) routeComments(ctx context.Context, repository github.Repository, issue *gh.Issue, conversation []*gh.IssueComment, review []*gh.PullRequestComment) error {
	if issue.IsPullRequest() {
		return e.pullRequestComments(ctx, repository, issue, conversation, review)
	}
	return e.commentEvents(ctx, repository, issue, conversation)
}

// bodyChange gives the incoming change of an edit of the body of the Workstream issue that the issue cursor since did
// not pass, or nil when the body of the issue with no link line of the Lead chat equals the Brief in workstreams, when an
// outgoing body change of the issue waits, when the newest edit is older than since, or when the Mobius App made the
// newest edit. The body of the issue differs from the row while that change waits, so the edit is no change of a person.
// A poll that read the issue before the send of the body change also reads the edit of that send.
func (e *Engine) bodyChange(ctx context.Context, repository github.Repository, issue *gh.Issue, since time.Time) (*store.AddIncomingChangeParams, error) {
	number := int64(issue.GetNumber())
	row, err := e.queries.GetWorkstream(ctx, store.GetWorkstreamParams{Repository: repository.FullName, Number: number})
	if err != nil {
		return nil, err
	}
	brief := e.WithoutChatLink(repository.FullName, number, issue.GetBody())
	if brief == row.Body {
		return nil, nil
	}
	pending, err := e.pendingChanges(ctx, repository.FullName, number)
	if err != nil || slices.ContainsFunc(pending, func(change store.OutgoingChange) bool { return change.Field == bodyField }) {
		return nil, err
	}
	edit, err := repository.IssueNewestBodyEdit(ctx, number)
	if err != nil || edit == nil || edit.EditedAt.Before(since) || strings.EqualFold(edit.Editor, appLogin(repository.AppSlug)) {
		return nil, err
	}
	metadata, err := json.Marshal(map[string]string{"body": brief})
	if err != nil {
		return nil, err
	}
	return &store.AddIncomingChangeParams{
		ExternalID: edit.ID,
		Repository: repository.FullName,
		Number:     number,
		Field:      bodyField,
		Change:     editedChange,
		Actor:      edit.Editor,
		Metadata:   string(metadata),
		Status:     "pending",
		CreatedAt:  now(),
	}, nil
}

// treeBodyChange gives the incoming change of an edit of the body of the tree issue row that the issue cursor since did
// not pass, or nil when the cleaned body equals the body of the row with no pending own write, when an outgoing body
// change of the issue waits, or when the newest edit is older than since. It tells true as the second value when an
// outgoing body change waits and the body of the issue equals the body of the row: the send can have written the body
// before it marked the change sent, so the poll must read the issue again to see the edit of that send.
func (e *Engine) treeBodyChange(ctx context.Context, repository github.Repository, row store.GetTreeIssueRow, issue *gh.Issue, since time.Time) (*store.AddIncomingChangeParams, bool, error) {
	number := int64(issue.GetNumber())
	body := e.WithoutChatLink(row.WorkstreamRepository.String, row.WorkstreamNumber.Int64, withoutWriteMarker(issue.GetBody()))
	pending, err := e.pendingItemChanges(ctx, repository.FullName, changeItem{kind: "issue", id: row.ID})
	if err != nil {
		return nil, false, err
	}
	if slices.ContainsFunc(pending, func(change store.OutgoingChange) bool { return change.Field == bodyField }) {
		return nil, body == row.Body, nil
	}
	ownBody, err := e.queries.HasFieldOwnWrite(ctx, store.HasFieldOwnWriteParams{Repository: repository.FullName, Number: number, Field: bodyField})
	if err != nil {
		return nil, false, err
	}
	if body == row.Body && !ownBody {
		return nil, false, nil
	}
	edit, err := repository.IssueNewestBodyEdit(ctx, number)
	if err != nil || edit == nil || edit.EditedAt.Before(since) {
		return nil, false, err
	}
	if body == row.Body && !strings.EqualFold(edit.Editor, appLogin(repository.AppSlug)) {
		return nil, false, nil
	}
	return &store.AddIncomingChangeParams{
		ExternalID: edit.ID,
		Repository: repository.FullName,
		Number:     number,
		Field:      bodyField,
		Change:     editedChange,
		Actor:      edit.Editor,
		Metadata:   "{}",
		Status:     "pending",
		CreatedAt:  now(),
	}, false, nil
}
