package engine

import (
	"context"
	"database/sql"
	"errors"
	"slices"

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
//   - The pull request of the newest task of the issue has mobius:needs-human only when that task is in needs_human.
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
	task, err := e.queries.GetNewestTask(ctx, store.GetNewestTaskParams{Repository: change.Repository, Issue: number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	found := err == nil
	live := found && task.State != "ended"
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
