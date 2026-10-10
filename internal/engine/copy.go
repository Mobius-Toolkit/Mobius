package engine

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The local copy holds the open Workstreams of each managed repository with their trees. The Workstream list reads
// only the copy. Mobius never writes to GitHub from the copy.

// copiedWorkstream is an open Workstream with the issues of its tree in depth-first order.
type copiedWorkstream struct {
	issue     *gh.Issue
	autopilot bool
	tree      []copiedIssue
}

type copiedIssue struct {
	issue    *gh.Issue
	parent   int64
	blockers []copiedBlocker
}

// copiedBlocker is an open blocker of an issue, with the Workstream of the blocker when it has one.
type copiedBlocker struct {
	number          int64
	workstream      sql.NullInt64
	workstreamTitle sql.NullString
}

// syncCopy replaces the copy of repository with the open Workstreams of GitHub.
func (e *Engine) syncCopy(ctx context.Context, repository github.Repository) error {
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	issues, err := repository.OpenIssuesWithLabel(ctx, workstreamLabel)
	if err != nil {
		return err
	}
	var workstreams []copiedWorkstream
	for _, issue := range issues {
		autopilot, err := e.issueAutopilot(ctx, repository, issue)
		if err != nil {
			return err
		}
		workstream, err := copyWorkstream(ctx, repository, issue, autopilot)
		if err != nil {
			return err
		}
		workstreams = append(workstreams, workstream)
	}
	if err := e.replaceCopy(ctx, repository.FullName, workstreams); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// forgetCopies removes the copy of each repository that is not in repositories.
func (e *Engine) forgetCopies(ctx context.Context, repositories []github.Repository) error {
	stored, err := e.queries.ListCopiedRepositories(ctx)
	if err != nil {
		return err
	}
	for _, name := range stored {
		if slices.ContainsFunc(repositories, func(repository github.Repository) bool { return repository.FullName == name }) {
			continue
		}
		if err := e.replaceCopy(ctx, name, nil); err != nil {
			return err
		}
		e.publish(Change{Workstreams: true})
	}
	return nil
}

func (e *Engine) replaceCopy(ctx context.Context, repository string, workstreams []copiedWorkstream) error {
	return e.inTx(ctx, func(q *store.Queries) error {
		for _, remove := range []func(context.Context, string) error{
			q.DeleteCopiedWorkstreamsOf, q.DeleteCopiedIssuesOf, q.DeleteCopiedIssueLabelsOf, q.DeleteCopiedBlockersOf,
		} {
			if err := remove(ctx, repository); err != nil {
				return err
			}
		}
		for _, workstream := range workstreams {
			if err := addWorkstream(ctx, q, repository, workstream); err != nil {
				return err
			}
		}
		return nil
	})
}

// updateCopy applies the change of one issue of repository to the copy, and tells if the copy changed. events are
// all events of the issue. With findParent, updateCopy reads the parent of an issue that the copy does not have,
// because its new link to a Workstream or a task changes the walk order of that Workstream.
//
// A task that gets or loses the Workstream label changes the walk order of its Workstream, and the Workstream of its
// blockers in the other trees. A new Workstream changes the Workstream of the blockers in its tree, in the other trees.
func (e *Engine) updateCopy(ctx context.Context, repository github.Repository, issue *gh.Issue, events []*gh.IssueEvent, findParent bool) (bool, error) {
	name := repository.FullName
	number := int64(issue.GetNumber())
	_, known, err := e.copiedWorkstreamOf(ctx, name, number, issue.GetRepositoryURL())
	if err != nil {
		return false, err
	}
	labelChanged, err := e.queries.ListCopiedWorkstreamsWithLabelChange(ctx, store.ListCopiedWorkstreamsWithLabelChangeParams{
		Repository:    name,
		Number:        number,
		RepositoryUrl: issue.GetRepositoryURL(),
		Name:          workstreamLabel,
		Present:       hasLabel(issue, workstreamLabel),
	})
	if err != nil {
		return false, err
	}
	stored, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: name, Number: number})
	if err != nil {
		return false, err
	}
	wanted := hasLabel(issue, workstreamLabel) && issue.GetState() == "open"
	if stored && !wanted {
		// The poll can give an issue that GitHub read before Mobius added the Workstream label.
		current, err := repository.Issue(ctx, number)
		if err != nil {
			return false, err
		}
		if current != nil {
			issue = current
			wanted = hasLabel(issue, workstreamLabel) && issue.GetState() == "open"
		}
	}
	autopilot := hasLabel(issue, autopilotLabel) && e.addedByTrustedUser(events)
	changed := false
	var added []copiedIssue
	switch {
	case stored && !wanted:
		changed = true
		err = e.removeCopiedWorkstream(ctx, name, number)
	case stored:
		changed, err = e.updateCopiedWorkstream(ctx, name, issue, autopilot)
	case wanted:
		var workstream copiedWorkstream
		workstream, err = copyWorkstream(ctx, repository, issue, autopilot)
		if err == nil {
			changed, added = true, workstream.tree
			err = e.inTx(ctx, func(q *store.Queries) error { return addWorkstream(ctx, q, name, workstream) })
		}
	}
	if err != nil {
		return false, err
	}
	issueChanged, err := e.updateCopiedIssue(ctx, issue)
	if err != nil {
		return false, err
	}
	changed = changed || issueChanged
	if issue.GetState() != "open" {
		removed, err := e.queries.DeleteCopiedBlocker(ctx, store.DeleteCopiedBlockerParams{Repository: name, Number: number})
		if err != nil {
			return false, err
		}
		changed = changed || removed > 0
	}
	stale := slices.Clone(labelChanged)
	if stored && !wanted {
		stale = append(stale, number)
	}
	rebuilt := labelChanged
	for _, workstream := range stale {
		blocked, err := e.queries.ListCopiedWorkstreamsWithBlockerIn(ctx, store.ListCopiedWorkstreamsWithBlockerInParams{
			Repository:        name,
			BlockerWorkstream: sql.NullInt64{Int64: workstream, Valid: true},
		})
		if err != nil {
			return false, err
		}
		rebuilt = appendNew(rebuilt, blocked...)
	}
	for _, task := range added {
		blocked, err := e.queries.ListCopiedWorkstreamsWithBlocker(ctx, store.ListCopiedWorkstreamsWithBlockerParams{Repository: name, Number: int64(task.issue.GetNumber())})
		if err != nil {
			return false, err
		}
		rebuilt = appendNew(rebuilt, slices.DeleteFunc(blocked, func(workstream int64) bool { return workstream == number })...)
	}
	for _, workstream := range rebuilt {
		if err := e.copyTree(ctx, repository, workstream); err != nil {
			return false, err
		}
		changed = true
	}
	if known || !findParent {
		return changed, nil
	}
	parent, err := repository.Parent(ctx, number)
	if err != nil || parent == nil {
		return changed, err
	}
	workstream, ok, err := e.copiedWorkstreamOf(ctx, name, int64(parent.GetNumber()), parent.GetRepositoryURL())
	if err != nil || !ok {
		return changed, err
	}
	return true, e.copyTree(ctx, repository, workstream)
}

// appendNew appends each value that numbers does not have.
func appendNew(numbers []int64, values ...int64) []int64 {
	for _, value := range values {
		if !slices.Contains(numbers, value) {
			numbers = append(numbers, value)
		}
	}
	return numbers
}

// recopyTrees copies again the tree of each Workstream that the copy has, and sends the change of the list.
func (e *Engine) recopyTrees(ctx context.Context, repository github.Repository, workstreams ...int64) error {
	for _, workstream := range workstreams {
		stored, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: repository.FullName, Number: workstream})
		if err != nil {
			return err
		}
		if !stored {
			continue
		}
		if err := e.copyTree(ctx, repository, workstream); err != nil {
			return err
		}
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// copiedWorkstreamOf gives the Workstream of the issue number of repositoryURL in the copy of repository.
// A copied Workstream is its own Workstream.
func (e *Engine) copiedWorkstreamOf(ctx context.Context, repository string, number int64, repositoryURL string) (int64, bool, error) {
	stored, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: repository, Number: number})
	if err != nil || stored {
		return number, stored, err
	}
	workstream, err := e.queries.GetCopiedIssueWorkstream(ctx, store.GetCopiedIssueWorkstreamParams{Repository: repository, Number: number, RepositoryUrl: repositoryURL})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return workstream, err == nil, err
}

func (e *Engine) removeCopiedWorkstream(ctx context.Context, repository string, number int64) error {
	return e.inTx(ctx, func(q *store.Queries) error {
		if err := q.DeleteCopiedWorkstream(ctx, store.DeleteCopiedWorkstreamParams{Repository: repository, Number: number}); err != nil {
			return err
		}
		return removeTree(ctx, q, repository, number)
	})
}

// updateCopiedWorkstream changes the copied Workstream of the issue, and the title of each blocker in that Workstream.
// It gives false when the copy has the same values.
func (e *Engine) updateCopiedWorkstream(ctx context.Context, repository string, issue *gh.Issue, autopilot bool) (bool, error) {
	changed := false
	return changed, e.inTx(ctx, func(q *store.Queries) error {
		updated, err := q.UpdateCopiedWorkstream(ctx, store.UpdateCopiedWorkstreamParams{
			Title:      issue.GetTitle(),
			Body:       issue.GetBody(),
			Autopilot:  autopilot,
			Repository: repository,
			Number:     int64(issue.GetNumber()),
		})
		if err != nil {
			return err
		}
		titled, err := q.UpdateCopiedBlockerTitles(ctx, store.UpdateCopiedBlockerTitlesParams{
			Title:      sql.NullString{String: issue.GetTitle(), Valid: true},
			Repository: repository,
			Workstream: sql.NullInt64{Int64: int64(issue.GetNumber()), Valid: true},
		})
		changed = updated > 0 || titled > 0
		return err
	})
}

// updateCopiedIssue changes each copied row of the issue in all repositories. It gives false when the copy has the same values.
func (e *Engine) updateCopiedIssue(ctx context.Context, issue *gh.Issue) (bool, error) {
	changed := false
	labels := labelNames(issue)
	slices.Sort(labels)
	return changed, e.inTx(ctx, func(q *store.Queries) error {
		updated, err := q.UpdateCopiedIssue(ctx, store.UpdateCopiedIssueParams{
			Title:         issue.GetTitle(),
			Body:          issue.GetBody(),
			State:         issue.GetState(),
			Author:        issue.GetUser().GetLogin(),
			Number:        int64(issue.GetNumber()),
			RepositoryUrl: issue.GetRepositoryURL(),
		})
		if err != nil {
			return err
		}
		changed = updated > 0
		rows, err := q.ListCopiedIssueRows(ctx, store.ListCopiedIssueRowsParams{Number: int64(issue.GetNumber()), RepositoryUrl: issue.GetRepositoryURL()})
		if err != nil {
			return err
		}
		for _, row := range rows {
			stored, err := q.ListCopiedIssueLabels(ctx, store.ListCopiedIssueLabelsParams(row))
			if err != nil {
				return err
			}
			if slices.Equal(stored, labels) {
				continue
			}
			changed = true
			if err := q.DeleteCopiedIssueLabelsAt(ctx, store.DeleteCopiedIssueLabelsAtParams(row)); err != nil {
				return err
			}
			if err := addLabels(ctx, q, row.Repository, row.Workstream, row.Position, labels); err != nil {
				return err
			}
		}
		return nil
	})
}

// changeCopiedLabels adds and removes labels on each copied row of the issue number of repositoryName, and publishes the change.
func (e *Engine) changeCopiedLabels(ctx context.Context, repositoryName string, number int64, add, remove []string) error {
	err := e.inTx(ctx, func(q *store.Queries) error {
		rows, err := q.ListCopiedIssueRowsByNumber(ctx, number)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if otherRepository(row.RepositoryUrl, repositoryName) {
				continue
			}
			for _, name := range remove {
				if err := q.DeleteCopiedIssueLabel(ctx, store.DeleteCopiedIssueLabelParams{Repository: row.Repository, Workstream: row.Workstream, Position: row.Position, Name: name}); err != nil {
					return err
				}
			}
			for _, name := range add {
				if err := q.AddCopiedIssueLabelIfMissing(ctx, store.AddCopiedIssueLabelIfMissingParams{Repository: row.Repository, Workstream: row.Workstream, Position: row.Position, Name: name}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// copyTree replaces the copied tree of the Workstream with the tree on GitHub.
func (e *Engine) copyTree(ctx context.Context, repository github.Repository, workstream int64) error {
	tree, err := readTree(ctx, repository, workstream)
	if err != nil {
		return err
	}
	return e.inTx(ctx, func(q *store.Queries) error {
		if err := removeTree(ctx, q, repository.FullName, workstream); err != nil {
			return err
		}
		return addTree(ctx, q, repository.FullName, workstream, tree)
	})
}

func copyWorkstream(ctx context.Context, repository github.Repository, issue *gh.Issue, autopilot bool) (copiedWorkstream, error) {
	tree, err := readTree(ctx, repository, int64(issue.GetNumber()))
	return copiedWorkstream{issue: issue, autopilot: autopilot, tree: tree}, err
}

// readTree gives the issues below the Workstream, depth first, with the rules of list_tasks, but with each issue.
// A nested Workstream and an issue of another repository are leaves.
func readTree(ctx context.Context, repository github.Repository, workstream int64) ([]copiedIssue, error) {
	var tree []copiedIssue
	return tree, addSubtree(ctx, repository, workstream, &tree)
}

func addSubtree(ctx context.Context, repository github.Repository, parent int64, tree *[]copiedIssue) error {
	issues, err := repository.SubIssues(ctx, parent)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		copied := copiedIssue{issue: issue, parent: parent}
		leaf := hasLabel(issue, workstreamLabel) || inOtherRepository(issue, repository.FullName)
		if !leaf && issue.GetIssueDependenciesSummary().GetBlockedBy() > 0 {
			if copied.blockers, err = copyBlockers(ctx, repository, int64(issue.GetNumber())); err != nil {
				return err
			}
		}
		*tree = append(*tree, copied)
		if leaf {
			continue
		}
		if err := addSubtree(ctx, repository, int64(issue.GetNumber()), tree); err != nil {
			return err
		}
	}
	return nil
}

// copyBlockers gives the open blockers of the issue number in repository.
func copyBlockers(ctx context.Context, repository github.Repository, number int64) ([]copiedBlocker, error) {
	issues, err := repository.BlockedBy(ctx, number)
	if err != nil {
		return nil, err
	}
	var found []copiedBlocker
	for _, issue := range issues {
		if issue.GetState() != "open" || inOtherRepository(issue, repository.FullName) {
			continue
		}
		blocker := copiedBlocker{number: int64(issue.GetNumber())}
		workstream, err := workstreamOf(ctx, repository, blocker.number)
		if err != nil {
			return nil, err
		}
		if workstream != 0 {
			blocker.workstream = sql.NullInt64{Int64: workstream, Valid: true}
			workstreamIssue, err := repository.Issue(ctx, workstream)
			if err != nil {
				return nil, err
			}
			if workstreamIssue != nil {
				blocker.workstreamTitle = sql.NullString{String: workstreamIssue.GetTitle(), Valid: true}
			}
		}
		found = append(found, blocker)
	}
	return found, nil
}

func labelNames(issue *gh.Issue) []string {
	names := []string{}
	for _, label := range issue.Labels {
		names = append(names, label.GetName())
	}
	return names
}

func addWorkstream(ctx context.Context, q *store.Queries, repository string, workstream copiedWorkstream) error {
	issue := workstream.issue
	err := q.AddCopiedWorkstream(ctx, store.AddCopiedWorkstreamParams{
		Repository: repository,
		Number:     int64(issue.GetNumber()),
		Title:      issue.GetTitle(),
		Body:       issue.GetBody(),
		Autopilot:  workstream.autopilot,
	})
	if err != nil {
		return err
	}
	return addTree(ctx, q, repository, int64(issue.GetNumber()), workstream.tree)
}

func removeTree(ctx context.Context, q *store.Queries, repository string, workstream int64) error {
	if err := q.DeleteCopiedIssues(ctx, store.DeleteCopiedIssuesParams{Repository: repository, Workstream: workstream}); err != nil {
		return err
	}
	if err := q.DeleteCopiedIssueLabels(ctx, store.DeleteCopiedIssueLabelsParams{Repository: repository, Workstream: workstream}); err != nil {
		return err
	}
	return q.DeleteCopiedBlockers(ctx, store.DeleteCopiedBlockersParams{Repository: repository, Workstream: workstream})
}

func addTree(ctx context.Context, q *store.Queries, repository string, workstream int64, tree []copiedIssue) error {
	for i, copied := range tree {
		position := int64(i)
		issue := copied.issue
		err := q.AddCopiedIssue(ctx, store.AddCopiedIssueParams{
			Repository:    repository,
			Workstream:    workstream,
			Position:      position,
			Number:        int64(issue.GetNumber()),
			Parent:        copied.parent,
			Title:         issue.GetTitle(),
			Body:          issue.GetBody(),
			State:         issue.GetState(),
			Author:        issue.GetUser().GetLogin(),
			HtmlUrl:       issue.GetHTMLURL(),
			RepositoryUrl: issue.GetRepositoryURL(),
		})
		if err != nil {
			return err
		}
		if err := addLabels(ctx, q, repository, workstream, position, labelNames(issue)); err != nil {
			return err
		}
		for _, blocker := range copied.blockers {
			err := q.AddCopiedBlocker(ctx, store.AddCopiedBlockerParams{
				Repository:             repository,
				Workstream:             workstream,
				Position:               position,
				Number:                 blocker.number,
				BlockerWorkstream:      blocker.workstream,
				BlockerWorkstreamTitle: blocker.workstreamTitle,
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func addLabels(ctx context.Context, q *store.Queries, repository string, workstream, position int64, labels []string) error {
	for _, label := range labels {
		err := q.AddCopiedIssueLabel(ctx, store.AddCopiedIssueLabelParams{Repository: repository, Workstream: workstream, Position: position, Name: label})
		if err != nil {
			return err
		}
	}
	return nil
}
