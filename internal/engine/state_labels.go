package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// taskLabels are the labels that the state of the task of an issue gives. mobius:question is not one of them: the open
// question Inbox items of the issue give it.
var taskLabels = []string{workingLabel, reviewLabel, needsHumanLabel}

// addStateLabelsChange adds a pending outgoing change that writes the state labels of the issue number from the
// database, with q, so the caller can add it in the transaction of the change of the state.
func addStateLabelsChange(ctx context.Context, q *store.Queries, repository string, number int64) (int64, error) {
	return addFieldChange(ctx, q, repository, number, stateLabelsField)
}

// addTaskLabelsChange adds the change of addStateLabelsChange for the issue of the task id.
func addTaskLabelsChange(ctx context.Context, q *store.Queries, id int64) (store.Task, int64, error) {
	task, err := q.GetTask(ctx, id)
	if err != nil {
		return task, 0, err
	}
	change, err := addStateLabelsChange(ctx, q, task.Repository, task.Issue)
	return task, change, err
}

// sendLabelsNow sends the outgoing change of the state labels after its caller added it. A repository that the Mobius
// App cannot reach keeps the change pending.
func (e *Engine) sendLabelsNow(ctx context.Context, repository string, change int64) error {
	source, ok := e.github.Repository(repository)
	if !ok {
		return nil
	}
	return e.sendNow(ctx, source, change)
}

// stateLabelsPending tells if an outgoing change of the state labels of the issue number is pending.
func (e *Engine) stateLabelsPending(ctx context.Context, repository string, number int64) (bool, error) {
	pending, err := e.pendingChanges(ctx, repository, number)
	return slices.ContainsFunc(pending, func(change store.OutgoingChange) bool { return change.Field == stateLabelsField }), err
}

// isStateLabelEvent tells if the event is an add or a removal of one of the state labels.
func isStateLabelEvent(event *gh.IssueEvent) bool {
	field, _, ok := eventField(event)
	return ok && field == stateLabelsField
}

// stateLabelItems gives the numbers of the issues, and of the pull requests, of repository whose events of the state
// labels the poll reads: each issue with a live task or with an open question or lost Inbox item, and each pull
// request of a live task.
func (e *Engine) stateLabelItems(ctx context.Context, repository string) (map[int64]bool, map[int64]bool, error) {
	tasks, err := e.queries.ListLiveTasks(ctx, repository)
	if err != nil {
		return nil, nil, err
	}
	issues, pulls := map[int64]bool{}, map[int64]bool{}
	for _, task := range tasks {
		issues[task.Issue] = true
		if task.PullRequest.Valid {
			pulls[task.PullRequest.Int64] = true
		}
	}
	items, err := e.Inbox(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, item := range items {
		if item.Repository == repository && (item.Kind == questionKind || item.Kind == lostKind) {
			issues[item.Issue] = true
		}
	}
	return issues, pulls, nil
}

// pullStateLabelEvents gives the events of the state labels of the pull request number that are not older than since.
// It reads the events of a pull request of a live task, and of a pull request with an own write that waits for its event.
func (e *Engine) pullStateLabelEvents(ctx context.Context, repository github.Repository, number int64, since time.Time, live bool) ([]changeEvent, error) {
	writing, err := e.queries.HasOwnWrite(ctx, store.HasOwnWriteParams{Repository: repository.FullName, Number: number})
	if err != nil || !live && !writing {
		return nil, err
	}
	events, err := repository.IssueEvents(ctx, number)
	if err != nil {
		return nil, err
	}
	var found []changeEvent
	for _, event := range events {
		if event.Actor != nil && !event.GetCreatedAt().Before(since) && isStateLabelEvent(event) {
			found = append(found, changeEvent{number, event})
		}
	}
	return found, nil
}

// applyStateLabelsChange applies the incoming change of a state label of an issue, or of the pull request of a live
// task. A removal by a trusted user acts like the UI, when Mobius needs the label no more than the person says:
//   - mobius:working or mobius:review of the issue of a live task that needs that label stops the task (StopIssue).
//   - mobius:needs-human of the issue or of the pull request of a task in needs_human, or of an issue with no live task
//     and an open lost Inbox item, resumes it (ResumeIssue).
//   - mobius:question of an issue with an open question Inbox item closes the question.
//
// Each other change, also each change of another actor and of the Mobius App, adds and sends the outgoing change that
// writes the state labels of the issue back from the database. A label of a pull request other than mobius:needs-human
// is no state label of Mobius.
func (e *Engine) applyStateLabelsChange(ctx context.Context, repository github.Repository, change store.IncomingChange) error {
	repository = e.changeRepository(change.Repository, repository)
	var meta struct{ Label string }
	if err := json.Unmarshal([]byte(change.Metadata), &meta); err != nil {
		return err
	}
	number := change.Number
	task, err := e.queries.GetLiveTaskByPullRequest(ctx, store.GetLiveTaskByPullRequestParams{Repository: change.Repository, PullRequest: sql.NullInt64{Int64: number, Valid: true}})
	onPull := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if onPull {
		if meta.Label != needsHumanLabel {
			return nil
		}
		number = task.Issue
	}
	if e.trustedUser(change.Actor) && change.Change == unlabeledChange {
		acted, err := e.removeStateLabel(ctx, repository, number, meta.Label, change.Actor)
		if err != nil || acted {
			return err
		}
	}
	id, err := addStateLabelsChange(ctx, e.queries, change.Repository, number)
	if err != nil {
		return err
	}
	return e.sendNow(ctx, repository, id)
}

// removeStateLabel runs the action of the removal of label from the issue number by the actor, and tells if it ran.
func (e *Engine) removeStateLabel(ctx context.Context, repository github.Repository, number int64, label, actor string) (bool, error) {
	switch label {
	case workingLabel, reviewLabel:
		return e.stopForLabel(ctx, repository, number, label, actor)
	case needsHumanLabel:
		return e.resumeForLabel(ctx, repository, number, actor)
	case questionLabel:
		asked, err := e.hasOpenQuestion(ctx, repository.FullName, number)
		if err != nil || !asked {
			return false, err
		}
		return true, e.closeQuestionItems(ctx, repository.FullName, number)
	}
	return false, nil
}

// stopForLabel stops the live task of the issue number as StopIssue does, when the task needs label.
func (e *Engine) stopForLabel(ctx context.Context, repository github.Repository, number int64, label, actor string) (bool, error) {
	e.stopMu.Lock()
	defer e.stopMu.Unlock()
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
	if errors.Is(err, sql.ErrNoRows) || err == nil && neededLabel(task) != label {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	record, err := e.stopLiveTask(ctx, repository, task, "Stopped by a label removal.", "@"+actor+" stopped")
	if err != nil {
		return true, err
	}
	e.AddRecord(ctx, record)
	return true, nil
}

// resumeForLabel resumes the issue number as ResumeIssue does, when its live task is in needs_human, or when it has no
// live task and an open lost Inbox item.
func (e *Engine) resumeForLabel(ctx context.Context, repository github.Repository, number int64, actor string) (bool, error) {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && task.State != "needs_human" {
		return false, nil
	}
	if err != nil {
		lost, err := e.openIssueItems(ctx, repository.FullName, number, lostKind)
		if err != nil || len(lost) == 0 {
			return false, err
		}
	}
	record, err := e.resumeIssue(ctx, repository, number, actor, "@"+actor+" resumed")
	if Refused(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	e.AddRecord(ctx, record)
	return true, nil
}

type labelWrite struct {
	number int64
	label  string
	add    bool
}

// sendStateLabels writes the state labels of the issue from the database to GitHub, and the label of its pull request.
//   - The issue of a live task has the label that neededLabel gives, and none of the other labels of taskLabels. The
//     issue and the pull request of a Judge that runs from needs_human stay as they are.
//   - The issue with no live task has mobius:needs-human when it has an open lost Inbox item, and none of the labels of
//     taskLabels else.
//   - The issue has mobius:question when it has an open question Inbox item.
//   - The pull request of the live task, else of the newest task, has mobius:needs-human only when that task is in
//     needs_human.
//
// It writes only a wrong label, adds before it removes, and stores an own write for each write.
func (e *Engine) sendStateLabels(ctx context.Context, repository github.Repository, change store.OutgoingChange) (bool, error) {
	number := change.Number.Int64
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return e.failed(ctx, repository, change, err)
	}
	if issue == nil {
		return false, e.refuse(ctx, change)
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: change.Repository, Issue: number})
	live := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		task, err = e.queries.GetNewestTask(ctx, store.GetNewestTaskParams{Repository: change.Repository, Issue: number})
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	found := err == nil
	kept := live && judgesHuman(task)
	needed := ""
	if live {
		needed = neededLabel(task)
	} else {
		lost, err := e.openIssueItems(ctx, change.Repository, number, lostKind)
		if err != nil {
			return false, err
		}
		if len(lost) > 0 {
			needed = needsHumanLabel
		}
	}
	asked, err := e.hasOpenQuestion(ctx, change.Repository, number)
	if err != nil {
		return false, err
	}
	var writes []labelWrite
	if !kept {
		for _, label := range taskLabels {
			if hasLabel(issue, label) != (label == needed) {
				writes = append(writes, labelWrite{number, label, label == needed})
			}
		}
	}
	if hasLabel(issue, questionLabel) != asked {
		writes = append(writes, labelWrite{number, questionLabel, asked})
	}
	if found && task.PullRequest.Valid && !kept {
		pullRequest, err := repository.Issue(ctx, task.PullRequest.Int64)
		if err != nil {
			return e.failed(ctx, repository, change, err)
		}
		if pullRequest != nil && hasLabel(pullRequest, needsHumanLabel) != (task.State == "needs_human") {
			writes = append(writes, labelWrite{task.PullRequest.Int64, needsHumanLabel, task.State == "needs_human"})
		}
	}
	slices.SortStableFunc(writes, func(a, b labelWrite) int {
		switch {
		case a.add == b.add:
			return 0
		case a.add:
			return -1
		}
		return 1
	})
	var added, removed []string
	for _, write := range writes {
		if pending, err := e.writeStateLabel(ctx, repository, change, write); pending || err != nil {
			return pending, err
		}
		switch {
		case write.number != number:
		case write.add:
			added = append(added, write.label)
		default:
			removed = append(removed, write.label)
		}
	}
	if len(added)+len(removed) > 0 {
		if err := e.changeCopiedLabels(ctx, change.Repository, number, added, removed); err != nil {
			return false, err
		}
	}
	return false, e.markSent(ctx, change)
}

// writeStateLabel adds or removes the label of the write with an own write. A failed write deletes the own write,
// because GitHub made no event.
func (e *Engine) writeStateLabel(ctx context.Context, repository github.Repository, change store.OutgoingChange, write labelWrite) (bool, error) {
	kind := unlabeledChange
	if write.add {
		kind = labeledChange
	}
	ownWrite, err := addOwnWrite(ctx, e.queries, change.Repository, write.number, stateLabelsField, write.label, kind)
	if err != nil {
		return false, err
	}
	if write.add {
		err = repository.AddLabel(ctx, write.number, write.label)
	} else {
		err = repository.RemoveLabel(ctx, write.number, write.label)
	}
	if err == nil {
		return false, nil
	}
	if delErr := e.queries.DeleteOwnWrite(ctx, ownWrite); delErr != nil {
		return false, delErr
	}
	return e.failed(ctx, repository, change, err)
}
