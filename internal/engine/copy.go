package engine

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The local copy holds the open Workstreams of each managed repository with their trees. The Workstream list reads
// only the copy. Mobius never writes to GitHub from the copy.

// copiedWorkstream is an open Workstream with the issues of its tree.
type copiedWorkstream struct {
	issue *gh.Issue
	tree  copiedTree
}

// copiedTree is the tree of a Workstream: its issues in depth-first order, and the buckets of GitHub below it. A
// nested Workstream and a bucket are no issues of the tree.
type copiedTree struct {
	issues  []copiedIssue
	buckets []copiedBucket
}

type copiedIssue struct {
	issue *gh.Issue
	// repository is the full name of the repository of the issue.
	repository string
	// body is the body of the issue with no link line of the Lead chat.
	body string
	// parent is the parent issue, or the zero value for a sub-issue of the Workstream issue or of a bucket.
	parent issueRef
	// bucket is the number of the bucket that holds the issue, or 0.
	bucket   int64
	blockers []copiedBlocker
}

// copiedBlocker is a blocker of an issue, open or closed.
type copiedBlocker struct {
	repository string
	issue      *gh.Issue
}

// copiedBucket is a bucket of a Workstream on GitHub. The key github of workstreams.metadata holds the buckets.
type copiedBucket struct {
	Number int64  `json:"number"`
	State  string `json:"state"`
	Title  string `json:"title"`
}

// issueGitHub is the key github of issues.metadata.
type issueGitHub struct {
	ID     int64    `json:"id"`
	Labels []string `json:"labels"`
	// Bucket is the number of the bucket that holds the issue, or 0.
	Bucket int64 `json:"bucket,omitempty"`
}

func githubMetadata(issue *gh.Issue, bucket int64) (string, error) {
	labels := labelNames(issue)
	slices.Sort(labels)
	text, err := json.Marshal(issueGitHub{ID: issue.GetID(), Labels: labels, Bucket: bucket})
	return string(text), err
}

// parseMetadata gives the key github of the metadata of an issue row.
func parseMetadata(metadata string) (issueGitHub, error) {
	var parsed struct {
		GitHub issueGitHub `json:"github"`
	}
	err := json.Unmarshal([]byte(metadata), &parsed)
	return parsed.GitHub, err
}

func issueKind(labels []string) string {
	if slices.Contains(labels, projectLabel) {
		return "project"
	}
	return "task"
}

// syncCopy copies the trees of the open Workstreams of repository. On the first poll of repository, when
// sync_cursors has no issues row, it also adds a row in workstreams for each open Workstream issue on GitHub.
func (e *Engine) syncCopy(ctx context.Context, repository github.Repository) error {
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	_, err := e.queries.GetSyncCursor(ctx, store.GetSyncCursorParams{Repository: repository.FullName, Endpoint: issuesEndpoint})
	firstPoll := errors.Is(err, sql.ErrNoRows)
	if err != nil && !firstPoll {
		return err
	}
	before := e.labelChangesNow()
	if firstPoll {
		issues, err := repository.OpenIssuesWithLabel(ctx, workstreamLabel)
		if err != nil {
			return err
		}
		var workstreams []copiedWorkstream
		for _, issue := range issues {
			workstream, err := e.copyWorkstream(ctx, repository, issue)
			if err != nil {
				return err
			}
			workstreams = append(workstreams, workstream)
		}
		trees := make([]copiedTree, len(workstreams))
		for i, workstream := range workstreams {
			trees[i] = workstream.tree
		}
		err = e.writeRead(ctx, before, trees, func(q *store.Queries) error {
			if err := detachTrees(ctx, q, repository.FullName); err != nil {
				return err
			}
			for _, workstream := range workstreams {
				if _, err := e.addWorkstreamRow(ctx, q, repository.FullName, workstream.issue); err != nil {
					return err
				}
				if err := addTree(ctx, q, repository.FullName, int64(workstream.issue.GetNumber()), workstream.tree); err != nil {
					return err
				}
			}
			return q.DeleteUnusedIssues(ctx)
		})
		if err != nil {
			return err
		}
		e.publish(Change{Workstreams: true})
		return nil
	}
	numbers, err := e.queries.ListOpenWorkstreamNumbers(ctx, repository.FullName)
	if err != nil {
		return err
	}
	trees := make([]copiedTree, len(numbers))
	for i, number := range numbers {
		trees[i], err = e.readTree(ctx, repository, number)
		if err != nil {
			return err
		}
	}
	err = e.writeRead(ctx, before, trees, func(q *store.Queries) error {
		if err := detachTrees(ctx, q, repository.FullName); err != nil {
			return err
		}
		for i, number := range numbers {
			if err := addTree(ctx, q, repository.FullName, number, trees[i]); err != nil {
				return err
			}
		}
		return q.DeleteUnusedIssues(ctx)
	})
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// forgetCopies removes the copy and the issues cursor of each repository that is not in repositories.
func (e *Engine) forgetCopies(ctx context.Context, repositories []github.Repository) error {
	stored, err := e.queries.ListCopiedRepositories(ctx)
	if err != nil {
		return err
	}
	for _, name := range stored {
		if slices.ContainsFunc(repositories, func(repository github.Repository) bool { return repository.FullName == name }) {
			continue
		}
		err := e.inTx(ctx, func(q *store.Queries) error {
			if err := detachTrees(ctx, q, name); err != nil {
				return err
			}
			if err := q.DeleteUnusedIssues(ctx); err != nil {
				return err
			}
			if err := q.DeleteCopiedWorkstreamsOf(ctx, name); err != nil {
				return err
			}
			return q.DeleteSyncCursor(ctx, store.DeleteSyncCursorParams{Repository: name, Endpoint: issuesEndpoint})
		})
		if err != nil {
			return err
		}
		delete(e.copied, name)
		e.publish(Change{Workstreams: true})
	}
	return nil
}

// detachTrees takes the issues out of the trees of all Workstreams of repository and removes their blockers. An issue
// that stays in a tree gets its row back, with the same id, when addTree adds the tree again.
func detachTrees(ctx context.Context, q *store.Queries, repository string) error {
	if err := q.DeleteCopiedBlockersOfTrees(ctx, repository); err != nil {
		return err
	}
	return q.DetachCopiedTrees(ctx, repository)
}

// updateCopy applies the change of one issue of repository to the copied trees, and tells if the copy changed. With
// findParent, updateCopy reads the parent of an issue that no copy has, because its new link to a Workstream or a task
// changes the walk order of that Workstream. changes is the labelChangeCount of repository from before the read of
// issue. It changes no row of workstreams.
func (e *Engine) updateCopy(ctx context.Context, repository github.Repository, issue *gh.Issue, findParent bool, changes int) (bool, error) {
	name := repository.FullName
	number := int64(issue.GetNumber())
	holding, err := e.copiedWorkstreamsHolding(ctx, name, number)
	if err != nil {
		return false, err
	}
	labelChanged, err := e.queries.ListCopiedWorkstreamsWithLabelChange(ctx, store.ListCopiedWorkstreamsWithLabelChangeParams{
		Repository: name,
		Number:     number,
		Name:       workstreamLabel,
		Present:    hasLabel(issue, workstreamLabel),
	})
	if err != nil {
		return false, err
	}
	changed, err := e.updateCopiedIssue(ctx, issue, name, changes)
	if err != nil {
		return false, err
	}
	var rebuilt []issueRef
	for _, row := range labelChanged {
		rebuilt = appendNew(rebuilt, issueRef{row.Repository, row.Workstream})
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
	workstreams, err := e.copiedWorkstreamsHolding(ctx, blockerRepository(parent, name), int64(parent.GetNumber()))
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

// beforeRecopyTrees is a hook of the tests. recopyTrees calls it before copyTree.
var beforeRecopyTrees func()

// recopyTrees copies again the tree of each Workstream that the copy has, and sends the change of the list.
func (e *Engine) recopyTrees(ctx context.Context, repository github.Repository, workstreams ...int64) error {
	if beforeRecopyTrees != nil {
		beforeRecopyTrees()
	}
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

// subIssueAt is a sub-issue with the repository of the list that gave it.
type subIssueAt struct {
	issue *gh.Issue
	from  github.Repository
}

// flatSubIssues gives the sub-issues of the issue parent of from, with the sub-issues of each bucket in place of the
// bucket. A nested Workstream is no sub-issue. It adds each bucket that it finds to buckets.
func (e *Engine) flatSubIssues(ctx context.Context, from github.Repository, parent int64, buckets *[]copiedBucket) ([]subIssueAt, error) {
	issues, err := from.SubIssues(ctx, parent)
	if github.NotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var found []subIssueAt
	for _, issue := range issues {
		if hasLabel(issue, workstreamLabel) {
			continue
		}
		issueRepository, readable := e.issueRepository(issue, from)
		if !readable || !hasLabel(issue, bucketLabel) {
			found = append(found, subIssueAt{issue, from})
			continue
		}
		*buckets = append(*buckets, bucketOf(issue))
		inside, err := e.flatSubIssues(ctx, issueRepository, int64(issue.GetNumber()), buckets)
		if err != nil {
			return nil, err
		}
		found = append(found, inside...)
	}
	return found, nil
}

func bucketOf(issue *gh.Issue) copiedBucket {
	return copiedBucket{Number: int64(issue.GetNumber()), State: issue.GetState(), Title: issue.GetTitle()}
}

// treeDiffers tells if the sub-issues, the open blockers or the buckets of the Workstream on GitHub differ from its
// copied tree. It reads only the open nodes. A nested Workstream and an issue of a repository that the App cannot read
// are leaves, as in readTree.
func (e *Engine) treeDiffers(ctx context.Context, repository github.Repository, workstream int64) (bool, error) {
	var rows []store.ListCopiedTreeRow
	var blockerRows []store.ListCopiedTreeBlockersRow
	var storedBuckets string
	err := e.readTx(ctx, func(q *store.Queries) error {
		var err error
		rows, err = q.ListCopiedTree(ctx, store.ListCopiedTreeParams{Repository: repository.FullName, Number: workstream})
		if err != nil {
			return err
		}
		blockerRows, err = q.ListCopiedTreeBlockers(ctx, store.ListCopiedTreeBlockersParams{Repository: repository.FullName, Number: workstream})
		if err != nil {
			return err
		}
		storedBuckets, err = q.GetCopiedBuckets(ctx, store.GetCopiedBucketsParams{Repository: repository.FullName, Number: workstream})
		return err
	})
	if err != nil {
		return false, err
	}
	root := lowerRef(repository.FullName, workstream)
	refs := map[int64]issueRef{}
	for _, row := range rows {
		refs[row.ID] = lowerRef(row.IssueRepository, row.Number)
	}
	children := map[issueRef][]issueRef{}
	for _, row := range rows {
		parent := root
		if row.Parent.Valid {
			parent = refs[row.Parent.Int64]
		}
		children[parent] = append(children[parent], refs[row.ID])
	}
	blockers := map[issueRef][]blockerKey{}
	for _, row := range blockerRows {
		node := refs[row.Issue]
		blockers[node] = append(blockers[node], blockerKey{row.BlockerRepository, row.Number})
	}
	var buckets []copiedBucket
	var differs func(from github.Repository, parent int64, key issueRef) (bool, error)
	differs = func(from github.Repository, parent int64, key issueRef) (bool, error) {
		issues, err := e.flatSubIssues(ctx, from, parent, &buckets)
		if err != nil {
			return false, err
		}
		found := make([]issueRef, 0, len(issues))
		for _, sub := range issues {
			found = append(found, lowerRef(blockerRepository(sub.issue, sub.from.FullName), int64(sub.issue.GetNumber())))
		}
		want := slices.Clone(children[key])
		compareRefs := func(a, b issueRef) int {
			return cmp.Or(cmp.Compare(a.number, b.number), strings.Compare(a.repository, b.repository))
		}
		slices.SortFunc(found, compareRefs)
		slices.SortFunc(want, compareRefs)
		if !slices.Equal(found, want) {
			return true, nil
		}
		for _, sub := range issues {
			issue := sub.issue
			issueRepository, readable := e.issueRepository(issue, sub.from)
			if issue.GetState() != "open" || !readable {
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
			itemKey := lowerRef(issueRepository.FullName, number)
			if !slices.Equal(open, blockers[itemKey]) {
				return true, nil
			}
			if changed, err := differs(issueRepository, number, itemKey); err != nil || changed {
				return changed, err
			}
		}
		return false, nil
	}
	if changed, err := differs(repository, workstream, root); err != nil || changed {
		return changed, err
	}
	var stored []copiedBucket
	if err := json.Unmarshal([]byte(storedBuckets), &stored); err != nil {
		return false, err
	}
	compare := func(a, b copiedBucket) int { return cmp.Compare(a.Number, b.Number) }
	slices.SortFunc(buckets, compare)
	slices.SortFunc(stored, compare)
	return !slices.EqualFunc(buckets, stored, func(a, b copiedBucket) bool { return a.Number == b.Number && a.State == b.State }), nil
}

type blockerKey struct {
	repository string
	number     int64
}

// issueRepository gives the repository of the issue, which is a sub-issue or a blocker of an issue of from, and tells
// if the App can read that repository.
func (e *Engine) issueRepository(issue *gh.Issue, from github.Repository) (github.Repository, bool) {
	return e.urlRepository(issue.GetRepositoryURL(), from)
}

// urlRepository gives the repository of repositoryURL, the API URL of a repository, and tells if the App can read it.
// It gives from for the URL of from.
func (e *Engine) urlRepository(repositoryURL string, from github.Repository) (github.Repository, bool) {
	return e.namedRepository(repositoryOfURL(repositoryURL), from)
}

// namedRepository gives the repository name, and tells if the App can read it. It gives from for the name of from.
func (e *Engine) namedRepository(name string, from github.Repository) (github.Repository, bool) {
	if strings.EqualFold(name, from.FullName) {
		return from, true
	}
	return e.github.Repository(name)
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
	workstream, err := e.queries.GetCopiedIssueWorkstream(ctx, store.GetCopiedIssueWorkstreamParams{WorkstreamRepository: repository, Repository: repositoryOfURL(repositoryURL), Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return workstream, err == nil, err
}

// copiedWorkstreamsHolding gives the Workstream that has the issue number of repository in its tree, and the
// Workstream that is this issue. The copy of another repository can hold the issue.
func (e *Engine) copiedWorkstreamsHolding(ctx context.Context, repository string, number int64) ([]issueRef, error) {
	rows, err := e.queries.ListCopiedWorkstreamsWithIssue(ctx, store.ListCopiedWorkstreamsWithIssueParams{Repository: repository, Number: number})
	if err != nil {
		return nil, err
	}
	var found []issueRef
	for _, row := range rows {
		found = append(found, issueRef{row.Repository, row.Workstream})
	}
	stored, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: repository, Number: number})
	if err != nil {
		return nil, err
	}
	if stored {
		found = appendNew(found, issueRef{repository, number})
	}
	return found, nil
}

// updateCopiedIssue changes the copied row of the issue of repository. It gives false when the copy has the same values.
// It keeps the labels of the copy when changeCopiedLabels changed a label of this issue after labelChangeCount gave changes,
// because the issue can have the old labels. In this case, a closed issue of the copy also stays closed, because the issue
// can be open on GitHub from before the close that Mobius made.
func (e *Engine) updateCopiedIssue(ctx context.Context, issue *gh.Issue, repository string, changes int) (bool, error) {
	changed := false
	number := int64(issue.GetNumber())
	labels := labelNames(issue)
	slices.Sort(labels)
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	keepLabels := e.labelChangedAt[issueRef{repository, number}] > changes
	return changed, e.inTx(ctx, func(q *store.Queries) error {
		row, err := q.GetCopiedIssue(ctx, store.GetCopiedIssueParams{Repository: repository, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		state := issue.GetState()
		if keepLabels && state == "open" && row.State == "closed" {
			state = row.State
		}
		body := issue.GetBody()
		workstream, err := q.GetCopiedIssueWorkstreamOf(ctx, store.GetCopiedIssueWorkstreamOfParams{Repository: repository, Number: number})
		if err == nil {
			body = e.WithoutChatLink(workstream.Repository, workstream.Number, body)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		updated, err := q.UpdateCopiedIssue(ctx, store.UpdateCopiedIssueParams{
			Title:      issue.GetTitle(),
			Body:       body,
			State:      state,
			Author:     issue.GetUser().GetLogin(),
			Repository: repository,
			Number:     number,
		})
		if err != nil {
			return err
		}
		changed = updated > 0
		if keepLabels {
			return nil
		}
		stored, err := parseMetadata(row.Metadata)
		if err != nil {
			return err
		}
		if slices.Equal(stored.Labels, labels) {
			return nil
		}
		changed = true
		return setIssueLabels(ctx, q, repository, number, labels)
	})
}

func setIssueLabels(ctx context.Context, q *store.Queries, repository string, number int64, labels []string) error {
	text, err := json.Marshal(labels)
	if err != nil {
		return err
	}
	return q.SetCopiedIssueLabels(ctx, store.SetCopiedIssueLabelsParams{Labels: string(text), Kind: issueKind(labels), Repository: repository, Number: number})
}

// changeCopiedLabels adds and removes labels on the copied row of the issue number of repositoryName, and publishes the change.
func (e *Engine) changeCopiedLabels(ctx context.Context, repositoryName string, number int64, add, remove []string) error {
	e.labelChangeMu.Lock()
	err := e.inTx(ctx, func(q *store.Queries) error {
		row, err := q.GetCopiedIssue(ctx, store.GetCopiedIssueParams{Repository: repositoryName, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		stored, err := parseMetadata(row.Metadata)
		if err != nil {
			return err
		}
		labels := slices.DeleteFunc(slices.Clone(stored.Labels), func(name string) bool { return slices.Contains(remove, name) })
		for _, name := range add {
			if !slices.Contains(labels, name) {
				labels = append(labels, name)
			}
		}
		slices.Sort(labels)
		return setIssueLabels(ctx, q, repositoryName, number, labels)
	})
	if err == nil {
		e.labelChanges[repositoryName]++
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
func (e *Engine) writeRead(ctx context.Context, before map[issueRef]int, trees []copiedTree, write func(*store.Queries) error) error {
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	var pending []addedCopy
	err := e.inTx(ctx, func(q *store.Queries) error {
		for _, tree := range trees {
			for _, copied := range tree.issues {
				issue := copied.issue
				ref := issueRef{copied.repository, int64(issue.GetNumber())}
				if e.labelChangedAt[ref] == before[ref] {
					continue
				}
				row, err := q.GetCopiedIssue(ctx, store.GetCopiedIssueParams{Repository: copied.repository, Number: int64(issue.GetNumber())})
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				stored, err := parseMetadata(row.Metadata)
				if err != nil {
					return err
				}
				issue.Labels = nil
				for _, name := range stored.Labels {
					issue.Labels = append(issue.Labels, &gh.Label{Name: name})
				}
				if issue.GetState() == "open" && row.State == "closed" {
					issue.State = &row.State
				}
			}
		}
		if err := write(q); err != nil {
			return err
		}
		pending = e.pendingCopies(trees)
		for _, added := range pending {
			if err := putAddedIssue(ctx, q, added); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		e.addedCopies = pending
	}
	return err
}

// addedCopyLifetime is the time that writeRead puts an added issue back into the tree. A list of GitHub can lack a new
// issue for some time, and an issue that GitHub never lists must not stay in the copy.
const addedCopyLifetime = 10 * time.Minute

// addedCopy is an issue that addIssueToCopy added to the copied tree of workstream. The copy keeps it until a tree of
// GitHub has it, or until the time until.
type addedCopy struct {
	workstream issueRef
	issue      copiedIssue
	until      time.Time
}

// pendingCopies gives the added issues that none of trees has, and that have not expired. The caller holds labelChangeMu.
func (e *Engine) pendingCopies(trees []copiedTree) []addedCopy {
	now := e.timeNow()
	var pending []addedCopy
	for _, added := range e.addedCopies {
		listed := slices.ContainsFunc(trees, func(tree copiedTree) bool {
			return slices.ContainsFunc(tree.issues, func(copied copiedIssue) bool { return sameIssue(copied.issue, added.issue.issue) })
		})
		if !listed && now.Before(added.until) {
			pending = append(pending, added)
		}
	}
	return pending
}

func sameIssue(a, b *gh.Issue) bool {
	return a.GetNumber() == b.GetNumber() && strings.EqualFold(a.GetRepositoryURL(), b.GetRepositoryURL())
}

// addIssueToCopy adds the issue, which createIssue created below parent with blockers, to the copied tree of the
// Workstream workstream. Each write of a tree of GitHub that lacks the issue puts it back (writeRead).
func (e *Engine) addIssueToCopy(ctx context.Context, workstream issueRef, issue *gh.Issue, parent issueAt, blockers []issueAt) error {
	copied := copiedIssue{
		issue:      issue,
		repository: repositoryOfURL(issue.GetRepositoryURL()),
		body:       e.WithoutChatLink(workstream.repository, workstream.number, issue.GetBody()),
	}
	if !strings.EqualFold(parent.repository.FullName, workstream.repository) || parent.number != workstream.number {
		copied.parent = issueRef{parent.repository.FullName, parent.number}
	}
	keys := make([]blockerKey, len(blockers))
	for i, blocker := range blockers {
		keys[i] = blockerKey{blocker.repository.FullName, blocker.number}
	}
	var err error
	if copied.blockers, err = e.readBlockers(ctx, keys); err != nil {
		return err
	}
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	added := addedCopy{workstream: workstream, issue: copied, until: e.timeNow().Add(addedCopyLifetime)}
	if err := e.inTx(ctx, func(q *store.Queries) error { return putAddedIssue(ctx, q, added) }); err != nil {
		return err
	}
	e.addedCopies = append(e.addedCopies, added)
	e.publish(Change{Workstreams: true})
	return nil
}

// setCopiedBlockers replaces the open blockers of the copied row of the issue number of the repository URL with
// blockers, and publishes the change. A closed blocker stays.
func (e *Engine) setCopiedBlockers(ctx context.Context, repositoryURL string, number int64, blockers []blockerKey) error {
	found, err := e.readBlockers(ctx, blockers)
	if err != nil {
		return err
	}
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	err = e.inTx(ctx, func(q *store.Queries) error {
		row, err := q.GetCopiedIssue(ctx, store.GetCopiedIssueParams{Repository: repositoryOfURL(repositoryURL), Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.DeleteCopiedOpenBlockersOfIssue(ctx, row.ID); err != nil {
			return err
		}
		return addBlockers(ctx, q, row.ID, found)
	})
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// readBlockers reads the issues of blockers from GitHub. It leaves out an issue that is gone.
func (e *Engine) readBlockers(ctx context.Context, blockers []blockerKey) ([]copiedBlocker, error) {
	var found []copiedBlocker
	for _, blocker := range blockers {
		repository, err := e.repository(blocker.repository)
		if err != nil {
			return nil, err
		}
		issue, err := repository.Issue(ctx, blocker.number)
		if err != nil {
			return nil, err
		}
		if issue != nil {
			found = append(found, copiedBlocker{repository: blocker.repository, issue: issue})
		}
	}
	return found, nil
}

// putAddedIssue adds the issue to its Workstream, with the labels and blockers. It adds nothing when the copy has no
// such open Workstream or the tree has the issue. The next copy of the tree puts the issue in its depth-first place.
func putAddedIssue(ctx context.Context, q *store.Queries, added addedCopy) error {
	workstream := added.workstream
	stored, err := q.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: workstream.repository, Number: workstream.number})
	if err != nil || !stored {
		return err
	}
	rows, err := q.ListCopiedTree(ctx, store.ListCopiedTreeParams{Repository: workstream.repository, Number: workstream.number})
	if err != nil {
		return err
	}
	number := int64(added.issue.issue.GetNumber())
	if slices.ContainsFunc(rows, func(row store.ListCopiedTreeRow) bool {
		return row.Number == number && strings.EqualFold(row.IssueRepository, added.issue.repository)
	}) {
		return nil
	}
	parent := sql.NullInt64{}
	if added.issue.parent != (issueRef{}) {
		for _, row := range rows {
			if row.Number == added.issue.parent.number && strings.EqualFold(row.IssueRepository, added.issue.parent.repository) {
				parent = sql.NullInt64{Int64: row.ID, Valid: true}
			}
		}
	}
	id, err := q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: workstream.repository, Number: workstream.number})
	if err != nil {
		return err
	}
	_, err = addCopiedIssue(ctx, q, id, parent, added.issue)
	return err
}

// copyTree replaces the copied tree of the Workstream with the tree on GitHub.
func (e *Engine) copyTree(ctx context.Context, repository github.Repository, workstream int64) error {
	before := e.labelChangesNow()
	tree, err := e.readTree(ctx, repository, workstream)
	if err != nil {
		return err
	}
	return e.writeRead(ctx, before, []copiedTree{tree}, func(q *store.Queries) error {
		return replaceTree(ctx, q, repository.FullName, workstream, tree)
	})
}

func (e *Engine) copyWorkstream(ctx context.Context, repository github.Repository, issue *gh.Issue) (copiedWorkstream, error) {
	tree, err := e.readTree(ctx, repository, int64(issue.GetNumber()))
	return copiedWorkstream{issue: issue, tree: tree}, err
}

// readTree gives the issues below the Workstream, depth first, with the rules of list_tasks, but with each issue.
// A sub-issue of another repository has its blockers and sub-issues. A nested Workstream and an issue of a repository
// that the App cannot read are leaves. A bucket is no issue of the tree: its sub-issues have the parent of the bucket.
// When GitHub answers 404 or 410 for an issue, it skips that subtree.
func (e *Engine) readTree(ctx context.Context, repository github.Repository, workstream int64) (copiedTree, error) {
	var tree copiedTree
	return tree, e.addSubtree(ctx, repository, repository, workstream, workstream, issueRef{}, 0, &tree)
}

// addSubtree adds the sub-issues of the issue parent of repository. home is the repository of the Workstream.
// parentRef is the parent of the sub-issues, and bucket is the bucket that holds them, or 0.
func (e *Engine) addSubtree(ctx context.Context, home, repository github.Repository, workstream, parent int64, parentRef issueRef, bucket int64, tree *copiedTree) error {
	issues, err := repository.SubIssues(ctx, parent)
	if github.NotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, issue := range issues {
		if hasLabel(issue, workstreamLabel) {
			continue
		}
		issueRepository, readable := e.issueRepository(issue, repository)
		number := int64(issue.GetNumber())
		if readable && hasLabel(issue, bucketLabel) {
			tree.buckets = append(tree.buckets, bucketOf(issue))
			if err := e.addSubtree(ctx, home, issueRepository, workstream, number, parentRef, number, tree); err != nil {
				return err
			}
			continue
		}
		name := blockerRepository(issue, repository.FullName)
		copied := copiedIssue{
			issue:      issue,
			repository: name,
			body:       e.WithoutChatLink(home.FullName, workstream, issue.GetBody()),
			parent:     parentRef,
			bucket:     bucket,
		}
		if readable && issue.GetIssueDependenciesSummary().GetTotalBlockedBy() > 0 {
			if copied.blockers, err = copyBlockers(ctx, issueRepository, number); err != nil {
				return err
			}
		}
		tree.issues = append(tree.issues, copied)
		if !readable {
			continue
		}
		if err := e.addSubtree(ctx, home, issueRepository, workstream, number, issueRef{name, number}, 0, tree); err != nil {
			return err
		}
	}
	return nil
}

// allBlockers gives the blockers of the issue number, open and closed, also in other repositories.
func allBlockers(ctx context.Context, repository github.Repository, number int64) ([]*gh.Issue, error) {
	issues, err := repository.BlockedBy(ctx, number)
	if github.NotFound(err) {
		return nil, nil
	}
	return issues, err
}

// openBlockers gives the open blockers of the issue number, also in other repositories.
func openBlockers(ctx context.Context, repository github.Repository, number int64) ([]*gh.Issue, error) {
	issues, err := allBlockers(ctx, repository, number)
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

// copyBlockers gives the blockers of the issue number in repository, open and closed.
func copyBlockers(ctx context.Context, repository github.Repository, number int64) ([]copiedBlocker, error) {
	issues, err := allBlockers(ctx, repository, number)
	if err != nil {
		return nil, err
	}
	var found []copiedBlocker
	for _, issue := range issues {
		found = append(found, copiedBlocker{repository: blockerRepository(issue, repository.FullName), issue: issue})
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

// addWorkstreamRow inserts the row of the Workstream issue when it has none, with the title, the Brief with no link
// line of the Lead chat, the open state, the creation time of the issue, the Autopilot level Off, and the other
// settings from the labels. A maintenance Workstream is evergreen, because Mobius manages it. A row that exists stays
// as it is. When it adds the row, it also adds an outgoing change for each level label that the issue has, so the issue
// loses the label, and for the evergreen label that a maintenance Workstream lacks. It gives 1 when it added the row.
func (e *Engine) addWorkstreamRow(ctx context.Context, q *store.Queries, repository string, issue *gh.Issue) (int64, error) {
	number := int64(issue.GetNumber())
	maintenance := hasLabel(issue, maintenanceLabel)
	added, err := q.AddWorkstream(ctx, store.AddWorkstreamParams{
		Repository:  repository,
		Number:      number,
		Title:       issue.GetTitle(),
		Body:        e.WithoutChatLink(repository, number, issue.GetBody()),
		State:       "open",
		CreatedAt:   issue.GetCreatedAt().UTC().Format(time.RFC3339Nano),
		Evergreen:   hasLabel(issue, evergreenLabel) || maintenance,
		Maintenance: maintenance,
		Skills:      hasLabel(issue, skillsLabel),
	})
	if err != nil || added == 0 {
		return added, err
	}
	var fields []string
	if hasLabel(issue, autopilotLabel) {
		fields = append(fields, autopilotField)
	}
	if hasLabel(issue, autoMergeLabel) {
		fields = append(fields, autoMergeField)
	}
	if maintenance && !hasLabel(issue, evergreenLabel) {
		fields = append(fields, evergreenField)
	}
	for _, field := range fields {
		if _, err := addSettingChange(ctx, q, repository, number, field); err != nil {
			return 0, err
		}
	}
	return added, nil
}

// closeCopiedWorkstream closes the row of the Workstream number of repository, deletes its recurring tasks, MCP servers,
// and copied tree, and publishes the Workstreams change.
func (e *Engine) closeCopiedWorkstream(ctx context.Context, repository github.Repository, number int64) error {
	closed := false
	err := e.inTx(ctx, func(q *store.Queries) error {
		updated, err := q.CloseWorkstream(ctx, store.CloseWorkstreamParams{Repository: repository.FullName, Number: number})
		if err != nil || updated == 0 {
			return err
		}
		closed = true
		if err := deleteWorkstreamRecurringTasks(ctx, q, repository.FullName, number); err != nil {
			return err
		}
		if err := q.DeleteWorkstreamMcpServers(ctx, store.DeleteWorkstreamMcpServersParams{Repository: repository.FullName, Workstream: number}); err != nil {
			return err
		}
		return removeTree(ctx, q, repository.FullName, number)
	})
	if err != nil || !closed {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// recopyAfterRemoval copies again the tree of the Workstream above the issue number of repository, because the issue
// is no Workstream, so it is a task of that Workstream.
func (e *Engine) recopyAfterRemoval(ctx context.Context, repository github.Repository, number int64) error {
	parentRepository, parent, err := e.workstreamOf(ctx, repository, number)
	if err != nil || parent == 0 {
		return err
	}
	stored, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: parentRepository, Number: parent})
	if err != nil {
		return err
	}
	if stored {
		if err := e.copyTreeOf(ctx, issueRef{parentRepository, parent}); err != nil {
			return err
		}
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// removeTree takes the issues out of the tree of the Workstream. An issue that blocks an issue of another tree stays
// as a row with no Workstream, and any other issue goes away.
func removeTree(ctx context.Context, q *store.Queries, repository string, workstream int64) error {
	if err := detachTree(ctx, q, repository, workstream); err != nil {
		return err
	}
	return q.DeleteUnusedIssues(ctx)
}

func detachTree(ctx context.Context, q *store.Queries, repository string, workstream int64) error {
	id, err := q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: repository, Number: workstream})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := q.DeleteCopiedBlockersOfTree(ctx, sql.NullInt64{Int64: id, Valid: true}); err != nil {
		return err
	}
	return q.DetachCopiedTree(ctx, sql.NullInt64{Int64: id, Valid: true})
}

// replaceTree writes the tree as the tree of the Workstream. An issue that was in the tree keeps its row and its id.
func replaceTree(ctx context.Context, q *store.Queries, repository string, workstream int64, tree copiedTree) error {
	if err := detachTree(ctx, q, repository, workstream); err != nil {
		return err
	}
	if err := addTree(ctx, q, repository, workstream, tree); err != nil {
		return err
	}
	return q.DeleteUnusedIssues(ctx)
}

// addTree writes the issues and the buckets of tree as the tree of the Workstream. The caller removes the issues that
// leave the tree, and the rows that no tree and no blocker holds (DeleteUnusedIssues).
func addTree(ctx context.Context, q *store.Queries, repository string, workstream int64, tree copiedTree) error {
	id, err := q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: repository, Number: workstream})
	if err != nil {
		return err
	}
	ids := map[issueRef]int64{}
	for _, copied := range tree.issues {
		parent := sql.NullInt64{}
		if copied.parent != (issueRef{}) {
			if parentID, found := ids[lowerRef(copied.parent.repository, copied.parent.number)]; found {
				parent = sql.NullInt64{Int64: parentID, Valid: true}
			}
		}
		issueID, err := addCopiedIssue(ctx, q, id, parent, copied)
		if err != nil {
			return err
		}
		ids[lowerRef(copied.repository, int64(copied.issue.GetNumber()))] = issueID
	}
	buckets, err := json.Marshal(append([]copiedBucket{}, tree.buckets...))
	if err != nil {
		return err
	}
	return q.SetCopiedBuckets(ctx, store.SetCopiedBucketsParams{Buckets: string(buckets), ID: id})
}

// addCopiedIssue writes the row of the issue in the tree of the Workstream with the id workstream, and its blockers.
func addCopiedIssue(ctx context.Context, q *store.Queries, workstream int64, parent sql.NullInt64, copied copiedIssue) (int64, error) {
	issue := copied.issue
	metadata, err := githubMetadata(issue, copied.bucket)
	if err != nil {
		return 0, err
	}
	id, err := q.AddCopiedIssue(ctx, store.AddCopiedIssueParams{
		Repository: copied.repository,
		Number:     int64(issue.GetNumber()),
		Title:      issue.GetTitle(),
		Body:       copied.body,
		State:      issue.GetState(),
		Author:     issue.GetUser().GetLogin(),
		Workstream: sql.NullInt64{Int64: workstream, Valid: true},
		Parent:     parent,
		Kind:       issueKind(labelNames(issue)),
		Github:     metadata,
	})
	if err != nil {
		return 0, err
	}
	if err := q.DeleteCopiedBlockersOfIssue(ctx, id); err != nil {
		return 0, err
	}
	return id, addBlockers(ctx, q, id, copied.blockers)
}

// addBlockers adds the blockers of the issue with the id issue. A blocker with no row gets a row with no Workstream.
func addBlockers(ctx context.Context, q *store.Queries, issue int64, blockers []copiedBlocker) error {
	for _, blocker := range blockers {
		metadata, err := githubMetadata(blocker.issue, 0)
		if err != nil {
			return err
		}
		id, err := q.AddCopiedBlockerIssue(ctx, store.AddCopiedBlockerIssueParams{
			Repository: blocker.repository,
			Number:     int64(blocker.issue.GetNumber()),
			Title:      blocker.issue.GetTitle(),
			Body:       blocker.issue.GetBody(),
			State:      blocker.issue.GetState(),
			Author:     blocker.issue.GetUser().GetLogin(),
			Kind:       issueKind(labelNames(blocker.issue)),
			Github:     metadata,
		})
		if err != nil {
			return err
		}
		if err := q.AddCopiedBlocker(ctx, store.AddCopiedBlockerParams{Issue: issue, Blocker: id}); err != nil {
			return err
		}
	}
	return nil
}

// treeRow is a row of a copied tree with the labels of its metadata.
type treeRow struct {
	store.ListCopiedTreeRow
	labels []string
}

// orderTree gives the rows of a copied tree depth first, with the siblings in the order of number. rows must be in the
// order of number.
func orderTree(rows []store.ListCopiedTreeRow) ([]treeRow, error) {
	children := map[int64][]store.ListCopiedTreeRow{}
	for _, row := range rows {
		children[row.Parent.Int64] = append(children[row.Parent.Int64], row)
	}
	ordered := make([]treeRow, 0, len(rows))
	var walk func(parent int64) error
	walk = func(parent int64) error {
		for _, row := range children[parent] {
			stored, err := parseMetadata(row.Metadata)
			if err != nil {
				return err
			}
			ordered = append(ordered, treeRow{row, stored.Labels})
			if err := walk(row.ID); err != nil {
				return err
			}
		}
		return nil
	}
	return ordered, walk(0)
}
