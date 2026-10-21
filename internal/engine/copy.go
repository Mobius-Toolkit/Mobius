package engine

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"log"
	"maps"
	"slices"
	"strings"

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
	issue            *gh.Issue
	parent           int64
	parentRepository string
	blockers         []copiedBlocker
}

// copiedBlocker is an open blocker of an issue, with the Workstream of the blocker when it has one.
// Only a blocker of the repository of the copy has a Workstream.
type copiedBlocker struct {
	repository      string
	number          int64
	workstream      sql.NullInt64
	workstreamTitle sql.NullString
}

// syncCopy replaces the copy of repository with the open Workstreams of GitHub.
func (e *Engine) syncCopy(ctx context.Context, repository github.Repository) error {
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	before := e.labelChangesNow()
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
		workstream, err := e.copyWorkstream(ctx, repository, issue, autopilot)
		if err != nil {
			return err
		}
		workstreams = append(workstreams, workstream)
	}
	var trees [][]copiedIssue
	for _, workstream := range workstreams {
		trees = append(trees, workstream.tree)
	}
	err = e.writeRead(ctx, repository.FullName, before, trees, func(q *store.Queries) error {
		return replaceCopy(ctx, q, repository.FullName, workstreams)
	})
	if err != nil {
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
		err := e.inTx(ctx, func(q *store.Queries) error { return replaceCopy(ctx, q, name, nil) })
		if err != nil {
			return err
		}
		e.publish(Change{Workstreams: true})
	}
	return nil
}

func replaceCopy(ctx context.Context, q *store.Queries, repository string, workstreams []copiedWorkstream) error {
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
}

// updateCopy applies the change of one issue of repository to the copy, and tells if the copy changed. events are
// all events of the issue. With findParent, updateCopy reads the parent of an issue that no copy has,
// because its new link to a Workstream or a task changes the walk order of that Workstream. changes is the
// labelChangeCount of repository from before the read of issue.
//
// A task that gets or loses the Workstream label changes the walk order of its Workstream, and the Workstream of its
// blockers in the other trees. A new Workstream changes the Workstream of the blockers in its tree, in the other trees.
// The copy of another repository can hold the issue in its tree, so these changes go to the Workstreams of all copies.
func (e *Engine) updateCopy(ctx context.Context, repository github.Repository, issue *gh.Issue, events []*gh.IssueEvent, findParent bool, changes int) (bool, error) {
	name := repository.FullName
	number := int64(issue.GetNumber())
	holding, err := e.copiedWorkstreamsHolding(ctx, number, issue.GetRepositoryURL())
	if err != nil {
		return false, err
	}
	labelChanged, err := e.queries.ListCopiedWorkstreamsWithLabelChange(ctx, store.ListCopiedWorkstreamsWithLabelChangeParams{
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
		before := e.labelChangesNow()
		workstream, err = e.copyWorkstream(ctx, repository, issue, autopilot)
		if err == nil {
			changed, added = true, workstream.tree
			err = e.writeRead(ctx, name, before, [][]copiedIssue{workstream.tree}, func(q *store.Queries) error { return addWorkstream(ctx, q, name, workstream) })
		}
	}
	if err != nil {
		return false, err
	}
	issueChanged, err := e.updateCopiedIssue(ctx, issue, name, changes)
	if err != nil {
		return false, err
	}
	changed = changed || issueChanged
	if issue.GetState() != "open" {
		removed, err := e.queries.DeleteCopiedBlocker(ctx, store.DeleteCopiedBlockerParams{BlockerRepository: name, Number: number})
		if err != nil {
			return false, err
		}
		changed = changed || removed > 0
	}
	var stale []int64
	var rebuilt []issueRef
	for _, row := range labelChanged {
		rebuilt = appendNew(rebuilt, issueRef{row.Repository, row.Workstream})
		if row.Repository == name {
			stale = append(stale, row.Workstream)
		}
	}
	if stored && !wanted {
		stale = append(stale, number)
	}
	for _, workstream := range stale {
		blocked, err := e.queries.ListCopiedWorkstreamsWithBlockerIn(ctx, store.ListCopiedWorkstreamsWithBlockerInParams{
			Repository:        name,
			BlockerWorkstream: sql.NullInt64{Int64: workstream, Valid: true},
		})
		if err != nil {
			return false, err
		}
		for _, blockedWorkstream := range blocked {
			rebuilt = appendNew(rebuilt, issueRef{name, blockedWorkstream})
		}
	}
	for _, task := range added {
		blocked, err := e.queries.ListCopiedWorkstreamsWithBlocker(ctx, store.ListCopiedWorkstreamsWithBlockerParams{Repository: name, BlockerRepository: blockerRepository(task.issue, name), Number: int64(task.issue.GetNumber())})
		if err != nil {
			return false, err
		}
		for _, blockedWorkstream := range blocked {
			if blockedWorkstream != number {
				rebuilt = appendNew(rebuilt, issueRef{name, blockedWorkstream})
			}
		}
	}
	for _, workstream := range rebuilt {
		if err := e.copyTreeOf(ctx, workstream); err != nil {
			return false, err
		}
		changed = true
	}
	if len(holding) > 0 || !findParent {
		return changed, nil
	}
	parent, err := repository.Parent(ctx, number)
	if err != nil || parent == nil {
		return changed, err
	}
	workstreams, err := e.copiedWorkstreamsHolding(ctx, int64(parent.GetNumber()), parent.GetRepositoryURL())
	if err != nil {
		return changed, err
	}
	for _, workstream := range workstreams {
		if err := e.copyTreeOf(ctx, workstream); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// appendNew appends each value that refs does not have.
func appendNew(refs []issueRef, values ...issueRef) []issueRef {
	for _, value := range values {
		if !slices.Contains(refs, value) {
			refs = append(refs, value)
		}
	}
	return refs
}

// copyTreeOf copies again the tree of the Workstream of the copy of another repository.
func (e *Engine) copyTreeOf(ctx context.Context, workstream issueRef) error {
	repository, err := e.repository(workstream.repository)
	if err != nil {
		return err
	}
	return e.copyTree(ctx, repository, workstream.number)
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

// checkCopiedTrees reads the sub-issues and the blockers of the open nodes of each copied Workstream of repository,
// and copies again each tree that differs from GitHub. GitHub does not change `updated_at` of an issue when a
// sub-issue or a blocker changes, so the issue list does not show these changes. A failed read ends the check, and the
// next poll checks again.
func (e *Engine) checkCopiedTrees(ctx context.Context, repository github.Repository) {
	if !e.copied[repository.FullName] {
		return
	}
	workstreams, err := e.queries.ListCopiedWorkstreams(ctx)
	if err != nil {
		log.Printf("check the trees of %s: %v", repository.FullName, err)
		return
	}
	var differing []int64
	for _, workstream := range workstreams {
		if workstream.Repository != repository.FullName {
			continue
		}
		differs, err := e.treeDiffers(ctx, repository, workstream.Number)
		if err != nil {
			log.Printf("check the trees of %s: %v", repository.FullName, err)
			return
		}
		if differs {
			differing = append(differing, workstream.Number)
		}
	}
	if len(differing) == 0 {
		return
	}
	if err := e.recopyTrees(ctx, repository, differing...); err != nil {
		log.Printf("copy the trees of %s again: %v", repository.FullName, err)
	}
}

type blockerKey struct {
	repository string
	number     int64
}

type subIssue struct {
	number        int64
	repositoryURL string
}

// treeDiffers tells if the sub-issues or the open blockers of the Workstream on GitHub differ from its copied tree.
// It reads only the open nodes. A nested Workstream and an issue of a repository that the App cannot read are leaves,
// as in readTree.
func (e *Engine) treeDiffers(ctx context.Context, repository github.Repository, workstream int64) (bool, error) {
	params := store.ListCopiedTreeParams{Repository: repository.FullName, Workstream: workstream}
	rows, err := e.queries.ListCopiedTree(ctx, params)
	if err != nil {
		return false, err
	}
	blockerRows, err := e.queries.ListCopiedTreeBlockers(ctx, store.ListCopiedTreeBlockersParams(params))
	if err != nil {
		return false, err
	}
	children := map[issueRef][]subIssue{}
	nodes := map[int64]issueRef{}
	for _, row := range rows {
		parent := lowerRef(row.ParentRepository, row.Parent)
		children[parent] = append(children[parent], subIssue{row.Number, row.RepositoryUrl})
		nodes[row.Position] = lowerRef(repositoryOfURL(row.RepositoryUrl), row.Number)
	}
	blockers := map[issueRef][]blockerKey{}
	for _, row := range blockerRows {
		node := nodes[row.Position]
		blockers[node] = append(blockers[node], blockerKey{row.BlockerRepository, row.Number})
	}
	var differs func(from github.Repository, parent int64) (bool, error)
	differs = func(from github.Repository, parent int64) (bool, error) {
		issues, err := from.SubIssues(ctx, parent)
		if err != nil {
			return false, err
		}
		found := make([]subIssue, 0, len(issues))
		for _, issue := range issues {
			found = append(found, subIssue{int64(issue.GetNumber()), issue.GetRepositoryURL()})
		}
		if !slices.Equal(found, children[lowerRef(from.FullName, parent)]) {
			return true, nil
		}
		for _, issue := range issues {
			issueRepository, readable := e.issueRepository(issue, from)
			if issue.GetState() != "open" || hasLabel(issue, workstreamLabel) || !readable {
				continue
			}
			number := int64(issue.GetNumber())
			var open []blockerKey
			if issue.GetIssueDependenciesSummary().GetTotalBlockedBy() > 0 {
				blocking, err := openBlockers(ctx, issueRepository, number)
				if err != nil {
					return false, err
				}
				for _, blocker := range blocking {
					open = append(open, blockerKey{blockerRepository(blocker, issueRepository.FullName), int64(blocker.GetNumber())})
				}
				slices.SortFunc(open, func(a, b blockerKey) int {
					return cmp.Or(strings.Compare(a.repository, b.repository), cmp.Compare(a.number, b.number))
				})
			}
			if !slices.Equal(open, blockers[lowerRef(issueRepository.FullName, number)]) {
				return true, nil
			}
			if changed, err := differs(issueRepository, number); err != nil || changed {
				return changed, err
			}
		}
		return false, nil
	}
	return differs(repository, workstream)
}

// issueRepository gives the repository of the issue, which is a sub-issue or a blocker of an issue of from, and tells
// if the App can read that repository.
func (e *Engine) issueRepository(issue *gh.Issue, from github.Repository) (github.Repository, bool) {
	return e.urlRepository(issue.GetRepositoryURL(), from)
}

// urlRepository gives the repository of repositoryURL, the API URL of a repository, and tells if the App can read it.
// It gives from for the URL of from.
func (e *Engine) urlRepository(repositoryURL string, from github.Repository) (github.Repository, bool) {
	if !otherRepository(repositoryURL, from.FullName) {
		return from, true
	}
	return e.github.Repository(repositoryOfURL(repositoryURL))
}

// lowerRef gives the key of the issue number of repository in a map. GitHub does not tell the case of a name.
func lowerRef(repository string, number int64) issueRef {
	return issueRef{strings.ToLower(repository), number}
}

// repositoryOfURL gives the full name of the repository of repositoryURL, the API URL of a repository.
func repositoryOfURL(repositoryURL string) string {
	_, name, _ := strings.Cut(repositoryURL, "/repos/")
	return name
}

// copiedWorkstreamOf gives the Workstream of the issue number of repositoryURL in the copy of repository.
// A copied Workstream is its own Workstream.
func (e *Engine) copiedWorkstreamOf(ctx context.Context, repository string, number int64, repositoryURL string) (int64, bool, error) {
	if !otherRepository(repositoryURL, repository) {
		stored, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: repository, Number: number})
		if err != nil || stored {
			return number, stored, err
		}
	}
	workstream, err := e.queries.GetCopiedIssueWorkstream(ctx, store.GetCopiedIssueWorkstreamParams{Repository: repository, Number: number, RepositoryUrl: repositoryURL})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return workstream, err == nil, err
}

// copiedWorkstreamsHolding gives the Workstreams of all copies that have the issue number of repositoryURL in their tree,
// and the Workstream that is this issue. The copy of another repository can hold the issue.
func (e *Engine) copiedWorkstreamsHolding(ctx context.Context, number int64, repositoryURL string) ([]issueRef, error) {
	rows, err := e.queries.ListCopiedWorkstreamsWithIssue(ctx, store.ListCopiedWorkstreamsWithIssueParams{Number: number, RepositoryUrl: repositoryURL})
	if err != nil {
		return nil, err
	}
	var found []issueRef
	for _, row := range rows {
		found = append(found, issueRef{row.Repository, row.Workstream})
	}
	repository := repositoryOfURL(repositoryURL)
	stored, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: repository, Number: number})
	if err != nil {
		return nil, err
	}
	if stored {
		found = appendNew(found, issueRef{repository, number})
	}
	return found, nil
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
			Evergreen:  hasLabel(issue, evergreenLabel),
			Skills:     hasLabel(issue, skillsLabel),
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
// It keeps the labels of the copy when changeCopiedLabels changed a label of this issue after labelChangeCount gave changes,
// because the issue can have the old labels. In this case, a closed issue of the copy also stays closed, because the issue
// can be open on GitHub from before the close that Mobius made.
func (e *Engine) updateCopiedIssue(ctx context.Context, issue *gh.Issue, repository string, changes int) (bool, error) {
	changed := false
	labels := labelNames(issue)
	slices.Sort(labels)
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	keepLabels := e.labelChangedAt[issueRef{repository, int64(issue.GetNumber())}] > changes
	return changed, e.inTx(ctx, func(q *store.Queries) error {
		state := issue.GetState()
		if keepLabels && state == "open" {
			stored, err := q.GetCopiedIssueState(ctx, store.GetCopiedIssueStateParams{Number: int64(issue.GetNumber()), RepositoryUrl: issue.GetRepositoryURL()})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if stored == "closed" {
				state = stored
			}
		}
		updated, err := q.UpdateCopiedIssue(ctx, store.UpdateCopiedIssueParams{
			Title:         issue.GetTitle(),
			Body:          issue.GetBody(),
			State:         state,
			Author:        issue.GetUser().GetLogin(),
			Number:        int64(issue.GetNumber()),
			RepositoryUrl: issue.GetRepositoryURL(),
		})
		if err != nil {
			return err
		}
		changed = updated > 0
		if keepLabels {
			return nil
		}
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
// A copy of another repository can hold the issue, so the change adds 1 to the counter of each repository that has a changed row.
func (e *Engine) changeCopiedLabels(ctx context.Context, repositoryName string, number int64, add, remove []string) error {
	changed := map[string]bool{repositoryName: true}
	e.labelChangeMu.Lock()
	err := e.inTx(ctx, func(q *store.Queries) error {
		rows, err := q.ListCopiedIssueRowsByNumber(ctx, number)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if otherRepository(row.RepositoryUrl, repositoryName) {
				continue
			}
			changed[row.Repository] = true
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
	if err == nil {
		for repository := range changed {
			e.labelChanges[repository]++
		}
		e.labelChangedAt[issueRef{repositoryName, number}] = e.labelChanges[repositoryName]
	}
	e.labelChangeMu.Unlock()
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

type issueRef struct {
	repository string
	number     int64
}

func (e *Engine) labelChangeCount(repository string) int {
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	return e.labelChanges[repository]
}

// labelChangesNow gives the labelChangedAt of all issues. A read of GitHub that starts after it gives the labels of
// at least this state.
func (e *Engine) labelChangesNow() map[issueRef]int {
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	return maps.Clone(e.labelChangedAt)
}

// writeRead runs write in a transaction, to store the trees that GitHub gave. An issue that changeCopiedLabels changed
// after labelChangesNow gave before keeps its copied labels, because the read can have the old labels. It also keeps a
// closed state of the copy, because the issue can be open on GitHub from before the close that Mobius made.
func (e *Engine) writeRead(ctx context.Context, repository string, before map[issueRef]int, trees [][]copiedIssue, write func(*store.Queries) error) error {
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	return e.inTx(ctx, func(q *store.Queries) error {
		for _, tree := range trees {
			for _, copied := range tree {
				issue := copied.issue
				ref := issueRef{blockerRepository(issue, repository), int64(issue.GetNumber())}
				if e.labelChangedAt[ref] == before[ref] {
					continue
				}
				rows, err := q.ListCopiedIssueRows(ctx, store.ListCopiedIssueRowsParams{Number: int64(issue.GetNumber()), RepositoryUrl: issue.GetRepositoryURL()})
				if err != nil {
					return err
				}
				if len(rows) == 0 {
					continue
				}
				names, err := q.ListCopiedIssueLabels(ctx, store.ListCopiedIssueLabelsParams(rows[0]))
				if err != nil {
					return err
				}
				issue.Labels = nil
				for _, name := range names {
					issue.Labels = append(issue.Labels, &gh.Label{Name: name})
				}
				if issue.GetState() != "open" {
					continue
				}
				stored, err := q.GetCopiedIssueState(ctx, store.GetCopiedIssueStateParams{Number: int64(issue.GetNumber()), RepositoryUrl: issue.GetRepositoryURL()})
				if err != nil {
					return err
				}
				if stored == "closed" {
					issue.State = &stored
				}
			}
		}
		return write(q)
	})
}

// copyTree replaces the copied tree of the Workstream with the tree on GitHub.
func (e *Engine) copyTree(ctx context.Context, repository github.Repository, workstream int64) error {
	before := e.labelChangesNow()
	tree, err := e.readTree(ctx, repository, workstream)
	if err != nil {
		return err
	}
	return e.writeRead(ctx, repository.FullName, before, [][]copiedIssue{tree}, func(q *store.Queries) error {
		if err := removeTree(ctx, q, repository.FullName, workstream); err != nil {
			return err
		}
		return addTree(ctx, q, repository.FullName, workstream, tree)
	})
}

func (e *Engine) copyWorkstream(ctx context.Context, repository github.Repository, issue *gh.Issue, autopilot bool) (copiedWorkstream, error) {
	tree, err := e.readTree(ctx, repository, int64(issue.GetNumber()))
	return copiedWorkstream{issue: issue, autopilot: autopilot, tree: tree}, err
}

// readTree gives the issues below the Workstream, depth first, with the rules of list_tasks, but with each issue.
// A sub-issue of another repository has its blockers and sub-issues. A nested Workstream and an issue of a repository
// that the App cannot read are leaves.
func (e *Engine) readTree(ctx context.Context, repository github.Repository, workstream int64) ([]copiedIssue, error) {
	var tree []copiedIssue
	return tree, e.addSubtree(ctx, repository, repository, workstream, &tree)
}

// addSubtree adds the sub-issues of the issue parent of repository. home is the repository of the Workstream.
func (e *Engine) addSubtree(ctx context.Context, home, repository github.Repository, parent int64, tree *[]copiedIssue) error {
	issues, err := repository.SubIssues(ctx, parent)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		copied := copiedIssue{issue: issue, parent: parent, parentRepository: repository.FullName}
		issueRepository, readable := e.issueRepository(issue, repository)
		leaf := hasLabel(issue, workstreamLabel) || !readable
		if !leaf && issue.GetIssueDependenciesSummary().GetBlockedBy() > 0 {
			if copied.blockers, err = e.copyBlockers(ctx, home, issueRepository, int64(issue.GetNumber())); err != nil {
				return err
			}
		}
		*tree = append(*tree, copied)
		if leaf {
			continue
		}
		if err := e.addSubtree(ctx, home, issueRepository, int64(issue.GetNumber()), tree); err != nil {
			return err
		}
	}
	return nil
}

// openBlockers gives the open blockers of the issue number, also in other repositories.
func openBlockers(ctx context.Context, repository github.Repository, number int64) ([]*gh.Issue, error) {
	issues, err := repository.BlockedBy(ctx, number)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(issues, func(issue *gh.Issue) bool { return issue.GetState() != "open" }), nil
}

// blockerRepository gives the full name of the repository of the issue. It gives repository for an issue of repository.
func blockerRepository(issue *gh.Issue, repository string) string {
	if !inOtherRepository(issue, repository) {
		return repository
	}
	return repositoryOfURL(issue.GetRepositoryURL())
}

// copyBlockers gives the open blockers of the issue number in repository. home is the repository of the Workstream.
// Only a blocker in home has a Workstream.
func (e *Engine) copyBlockers(ctx context.Context, home, repository github.Repository, number int64) ([]copiedBlocker, error) {
	issues, err := openBlockers(ctx, repository, number)
	if err != nil {
		return nil, err
	}
	var found []copiedBlocker
	for _, issue := range issues {
		blocker := copiedBlocker{repository: blockerRepository(issue, repository.FullName), number: int64(issue.GetNumber())}
		if inOtherRepository(issue, home.FullName) {
			found = append(found, blocker)
			continue
		}
		workstreamRepository, workstream, err := e.workstreamOf(ctx, home, blocker.number)
		if err != nil {
			return nil, err
		}
		if workstream != 0 && workstreamRepository == home.FullName {
			blocker.workstream = sql.NullInt64{Int64: workstream, Valid: true}
			workstreamIssue, err := home.Issue(ctx, workstream)
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
		Evergreen:  hasLabel(issue, evergreenLabel),
		Skills:     hasLabel(issue, skillsLabel),
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
			Repository:       repository,
			Workstream:       workstream,
			Position:         position,
			Number:           int64(issue.GetNumber()),
			Parent:           copied.parent,
			ParentRepository: copied.parentRepository,
			Title:            issue.GetTitle(),
			Body:             issue.GetBody(),
			State:            issue.GetState(),
			Author:           issue.GetUser().GetLogin(),
			HtmlUrl:          issue.GetHTMLURL(),
			RepositoryUrl:    issue.GetRepositoryURL(),
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
				BlockerRepository:      blocker.repository,
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
