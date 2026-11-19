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
	"strconv"
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

// copiedTree is the tree of a Workstream: its issues in depth-first order, the buckets of GitHub below it, and its
// nested Workstreams that are direct sub-issues. A nested Workstream and a bucket are no issues of the tree.
type copiedTree struct {
	issues  []copiedIssue
	buckets []copiedBucket
	nested  []copiedNested
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
	id         int64
	repository string
	issue      *gh.Issue
}

// copiedBucket is a bucket of a Workstream on GitHub. The key github of workstreams.metadata holds the buckets.
type copiedBucket struct {
	Number int64  `json:"number"`
	State  string `json:"state"`
	Title  string `json:"title"`
}

// copiedNested is a nested Workstream that is a direct sub-issue of a Workstream. The key github of workstreams.metadata
// holds them, so the count of the direct sub-issues is the count of GitHub.
type copiedNested struct {
	Number int64  `json:"number"`
	State  string `json:"state"`
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

const projectKind = "project"

func issueKind(labels []string) string {
	if slices.Contains(labels, projectLabel) {
		return projectKind
	}
	return "task"
}

// syncCopy copies the trees of the open Workstreams of repository. On the first poll of repository, when
// sync_cursors has no issues row, it also adds a row in workstreams for each open Workstream issue on GitHub.
// When the database already holds tree issues for repository, it refreshes only their title, body, state, author,
// kind and GitHub labels, and keeps the parent, Workstream and blockers of each existing row unchanged.
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
			return deleteUnusedIssues(ctx, q)
		})
		if err != nil {
			return err
		}
		e.publish(Change{Workstreams: true})
		return nil
	}
	hasTree, err := e.queries.HasRepositoryTreeIssues(ctx, repository.FullName)
	if err != nil {
		return err
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
	if hasTree {
		err = e.writeRead(ctx, before, trees, func(q *store.Queries) error {
			return refreshCopiedTrees(ctx, q, repository.FullName, numbers, trees)
		})
		if err != nil {
			return err
		}
		e.publish(Change{Workstreams: true})
		return nil
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
		return deleteUnusedIssues(ctx, q)
	})
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

func refreshCopiedTrees(ctx context.Context, q *store.Queries, repository string, numbers []int64, trees []copiedTree) error {
	for i, number := range numbers {
		wsID, err := q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: repository, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		for _, copied := range trees[i].issues {
			issue := copied.issue
			labels := labelNames(issue)
			slices.Sort(labels)
			labelsText, err := json.Marshal(labels)
			if err != nil {
				return err
			}
			if err := q.RefreshCopiedTreeIssue(ctx, store.RefreshCopiedTreeIssueParams{
				Title:      issue.GetTitle(),
				Body:       copied.body,
				State:      issue.GetState(),
				Author:     issue.GetUser().GetLogin(),
				Kind:       issueKind(labels),
				GithubID:   issue.GetID(),
				Labels:     string(labelsText),
				Repository: copied.repository,
				Number:     int64(issue.GetNumber()),
			}); err != nil {
				return err
			}
		}
		if err := setCopiedTreeData(ctx, q, wsID, trees[i]); err != nil {
			return err
		}
	}
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
			if err := detachAllTrees(ctx, q, name); err != nil {
				return err
			}
			if err := deleteUnusedIssues(ctx, q); err != nil {
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

func keepSince() string {
	return time.Now().Add(-addedCopyLifetime).UTC().Format(time.RFC3339Nano)
}

// detachTrees takes the issues out of the trees of the Workstreams of repository and removes their blockers. The tree
// of a Workstream with a pending close stays. An issue that stays in a tree gets its row back, with the same id, when
// addTree adds the tree again.
func detachTrees(ctx context.Context, q *store.Queries, repository string) error {
	since := keepSince()
	if err := q.DeleteCopiedBlockersOfSettledTrees(ctx, store.DeleteCopiedBlockersOfSettledTreesParams{Repository: repository, KeepSince: since}); err != nil {
		return err
	}
	return q.DetachCopiedSettledTrees(ctx, store.DetachCopiedSettledTreesParams{Repository: repository, KeepSince: since})
}

// detachAllTrees is detachTrees with the trees of the Workstreams with a pending close.
func detachAllTrees(ctx context.Context, q *store.Queries, repository string) error {
	if err := q.DeleteCopiedBlockersOfTrees(ctx, repository); err != nil {
		return err
	}
	return q.DetachCopiedTrees(ctx, repository)
}

// updateCopy applies the change of the title, body, state, author or labels of one issue of repository to its row in
// issues, and tells if the copy changed.
func (e *Engine) updateCopy(ctx context.Context, repository github.Repository, issue *gh.Issue, changes copyCounts) (bool, error) {
	return e.updateCopiedIssue(ctx, issue, repository.FullName, changes)
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
// records incoming changes for any tree or blocker differences from GitHub, syncs bucket and nested Workstream
// metadata, and consumes matching tree own_writes. It gives the repositories of any added incoming changes.
func (e *Engine) checkCopiedTrees(ctx context.Context, repository github.Repository, forceWalk map[issueRef]bool) []string {
	if !e.copied[repository.FullName] {
		return nil
	}
	workstreams, err := e.queries.ListCopiedWorkstreams(ctx)
	if err != nil {
		log.Printf("check the trees of %s: %v", repository.FullName, err)
		return nil
	}
	eventsCache := map[issueRef][]github.TreeEvent{}
	eventsOf := func(repo github.Repository, number int64) ([]github.TreeEvent, error) {
		key := lowerRef(repo.FullName, number)
		if cached, ok := eventsCache[key]; ok {
			return cached, nil
		}
		events, err := repo.TreeEvents(ctx, number)
		if err != nil {
			return nil, err
		}
		eventsCache[key] = events
		return events, nil
	}
	touched := map[string]bool{}
	var touchedRepos []string
	markTouched := func(repo string) {
		if !touched[repo] {
			touched[repo] = true
			touchedRepos = append(touchedRepos, repo)
		}
	}
	for _, workstream := range workstreams {
		if workstream.Repository != repository.FullName {
			continue
		}
		if err := e.checkWorkstreamTree(ctx, repository, workstream.Number, forceWalk, eventsOf, markTouched); err != nil {
			log.Printf("check the trees of %s: %v", repository.FullName, err)
			return touchedRepos
		}
	}
	if err := e.consumeTreeOwnWrites(ctx, repository, eventsOf); err != nil {
		log.Printf("consume the own writes of %s: %v", repository.FullName, err)
	}
	return touchedRepos
}

// subIssueAt is a sub-issue with the repository of the list that gave it and its GitHub bucket, or 0.
type subIssueAt struct {
	issue  *gh.Issue
	from   github.Repository
	bucket int64
}

// flatSubIssues gives the sub-issues of the issue parent of from, with the sub-issues of each bucket in place of the
// bucket. A nested Workstream is no sub-issue. It adds each bucket that it finds to leaves.buckets, and each nested
// Workstream to leaves.nested when direct is true.
func (e *Engine) flatSubIssues(ctx context.Context, from github.Repository, parent, bucket int64, direct bool, leaves *copiedTree) ([]subIssueAt, error) {
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
			if direct {
				leaves.nested = append(leaves.nested, nestedOf(issue))
			}
			continue
		}
		issueRepository, readable := e.issueRepository(issue, from)
		if !readable || !hasLabel(issue, bucketLabel) {
			found = append(found, subIssueAt{issue, from, bucket})
			continue
		}
		bucketNum := int64(issue.GetNumber())
		leaves.buckets = append(leaves.buckets, bucketOf(issue))
		inside, err := e.flatSubIssues(ctx, issueRepository, bucketNum, bucketNum, false, leaves)
		if err != nil {
			return nil, err
		}
		found = append(found, inside...)
	}
	return found, nil
}

func nestedOf(issue *gh.Issue) copiedNested {
	return copiedNested{Number: int64(issue.GetNumber()), State: issue.GetState()}
}

func bucketOf(issue *gh.Issue) copiedBucket {
	return copiedBucket{Number: int64(issue.GetNumber()), State: issue.GetState(), Title: issue.GetTitle()}
}

type eventFetcher func(repo github.Repository, number int64) ([]github.TreeEvent, error)

type dbBlockerInfo struct {
	repo            string
	number          int64
	title           string
	state           string
	author          string
	workstreamValid bool
}

func (e *Engine) checkWorkstreamTree(ctx context.Context, repository github.Repository, workstream int64, forceWalk map[issueRef]bool, eventsOf eventFetcher, markTouched func(string)) error {
	var rows []store.ListCopiedTreeRow
	var blockerRows []store.ListCopiedTreeAllBlockersRow
	var pendingBlockerIDs []int64
	var pendingParentRefs []store.ListPendingParentRefsRow
	var storedBuckets, storedNested string
	var workstreamID int64
	err := e.readTx(ctx, func(q *store.Queries) error {
		var err error
		workstreamID, err = q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: repository.FullName, Number: workstream})
		if err != nil {
			return err
		}
		rows, err = q.ListCopiedTree(ctx, store.ListCopiedTreeParams{Repository: repository.FullName, Number: workstream})
		if err != nil {
			return err
		}
		blockerRows, err = q.ListCopiedTreeAllBlockers(ctx, store.ListCopiedTreeAllBlockersParams{Repository: repository.FullName, Number: workstream})
		if err != nil {
			return err
		}
		pendingBlockerIDs, err = q.ListPendingBlockerIssues(ctx)
		if err != nil {
			return err
		}
		pendingParentRefs, err = q.ListPendingParentRefs(ctx)
		if err != nil {
			return err
		}
		storedBuckets, err = q.GetCopiedBuckets(ctx, store.GetCopiedBucketsParams{Repository: repository.FullName, Number: workstream})
		if err != nil {
			return err
		}
		storedNested, err = q.GetCopiedNested(ctx, store.GetCopiedNestedParams{Repository: repository.FullName, Number: workstream})
		return err
	})
	if err != nil {
		return err
	}
	pendingBlockers := map[int64]bool{}
	for _, id := range pendingBlockerIDs {
		pendingBlockers[id] = true
	}
	pendingParents := map[issueRef]bool{}
	for _, item := range pendingParentRefs {
		pendingParents[lowerRef(item.Repository, item.Number)] = true
	}
	root := lowerRef(repository.FullName, workstream)
	refs := map[int64]issueRef{}
	idByRef := map[issueRef]int64{}
	repoByRef := map[issueRef]string{}
	bucketByRef := map[issueRef]int64{}
	for _, row := range rows {
		if row.Number == 0 {
			continue
		}
		ref := lowerRef(row.IssueRepository, row.Number)
		refs[row.ID] = ref
		idByRef[ref] = row.ID
		repoByRef[ref] = row.IssueRepository
		if meta, err := parseMetadata(row.Metadata); err == nil {
			bucketByRef[ref] = meta.Bucket
		}
	}
	children := map[issueRef][]issueRef{}
	for _, row := range rows {
		ref, ok := refs[row.ID]
		if !ok || pendingParents[ref] {
			continue
		}
		parent := root
		if row.Parent.Valid {
			var found bool
			parent, found = refs[row.Parent.Int64]
			if !found {
				continue
			}
		}
		children[parent] = append(children[parent], ref)
	}
	dbBlockers := map[issueRef]map[issueRef]dbBlockerInfo{}
	for _, row := range blockerRows {
		node, ok := refs[row.Issue]
		if !ok {
			continue
		}
		if dbBlockers[node] == nil {
			dbBlockers[node] = map[issueRef]dbBlockerInfo{}
		}
		dbBlockers[node][lowerRef(row.BlockerRepository, row.Number)] = dbBlockerInfo{
			repo:            row.BlockerRepository,
			number:          row.Number,
			title:           row.Title,
			state:           row.State,
			author:          row.Author,
			workstreamValid: row.Workstream.Valid,
		}
	}
	var leaves copiedTree
	syncChanged := false
	visited := map[issueRef]bool{}
	var walk func(from github.Repository, parent int64, parentExact string, key issueRef) error
	walk = func(from github.Repository, parent int64, parentExact string, key issueRef) error {
		visited[key] = true
		issues, err := e.flatSubIssues(ctx, from, parent, 0, key == root, &leaves)
		if err != nil {
			return err
		}
		bucketRefs := map[issueRef]bool{}
		if key == root {
			for _, b := range leaves.buckets {
				bucketRefs[lowerRef(repository.FullName, b.Number)] = true
			}
		}
		wantSet := map[issueRef]bool{}
		for _, w := range children[key] {
			wantSet[w] = true
		}
		foundSet := map[issueRef]bool{}
		for _, sub := range issues {
			issue := sub.issue
			subRepoName := blockerRepository(issue, sub.from.FullName)
			subNum := int64(issue.GetNumber())
			ref := lowerRef(subRepoName, subNum)
			foundSet[ref] = true
			if pendingParents[ref] {
				continue
			}
			childRepo, readable := e.issueRepository(issue, sub.from)
			if !wantSet[ref] {
				ev, pRepo, pNum, ok, err := findAddedParentEvent(from, parent, parentExact, key, sub, subRepoName, subNum, childRepo, readable, bucketRefs, repository, workstream, eventsOf)
				if err != nil {
					return err
				}
				if ok {
					if err := e.recordTreeIncomingChange(ctx, repository, subRepoName, subNum, parentField, addedChange, ev, parentMetadata{pRepo, pNum}); err != nil {
						return err
					}
					markTouched(subRepoName)
				}
				continue
			}
			if sub.bucket != bucketByRef[ref] {
				if err := e.queries.SetIssueBucket(ctx, store.SetIssueBucketParams{Bucket: sub.bucket, ID: idByRef[ref]}); err != nil {
					return err
				}
				bucketByRef[ref] = sub.bucket
			}
			if (issue.GetState() != "open" && !forceWalk[ref]) || !readable {
				continue
			}
			if issue.GetState() == "open" && !pendingBlockers[idByRef[ref]] {
				var ghBlockers []*gh.Issue
				if issue.GetIssueDependenciesSummary().GetTotalBlockedBy() > 0 {
					ghBlockers, err = allBlockers(ctx, childRepo, subNum)
					if err != nil {
						return err
					}
				}
				ghMap := map[issueRef]*gh.Issue{}
				for _, b := range ghBlockers {
					bRepo := blockerRepository(b, childRepo.FullName)
					ghMap[lowerRef(bRepo, int64(b.GetNumber()))] = b
				}
				dbMap := dbBlockers[ref]
				for bRef, info := range dbMap {
					if ghIssue, ok := ghMap[bRef]; ok {
						if !info.workstreamValid && (info.state != ghIssue.GetState() || info.title != ghIssue.GetTitle() || info.author != ghIssue.GetUser().GetLogin()) {
							metadata, err := githubMetadata(ghIssue, 0)
							if err != nil {
								return err
							}
							if _, err := e.queries.AddCopiedBlockerIssue(ctx, store.AddCopiedBlockerIssueParams{
								Repository: info.repo,
								Number:     info.number,
								Title:      ghIssue.GetTitle(),
								Body:       ghIssue.GetBody(),
								State:      ghIssue.GetState(),
								Author:     ghIssue.GetUser().GetLogin(),
								Kind:       issueKind(labelNames(ghIssue)),
								Github:     metadata,
							}); err != nil {
								return err
							}
							syncChanged = true
						}
						continue
					}
					events, err := eventsOf(childRepo, subNum)
					if err != nil {
						return err
					}
					if ev, ok := newestBlockerEvent(events, "blocked_by_removed", childRepo.FullName, bRef); ok {
						if err := e.recordTreeIncomingChange(ctx, repository, childRepo.FullName, subNum, blockersField, removedChange, ev, blockerMetadata{info.repo, info.number}); err != nil {
							return err
						}
						markTouched(childRepo.FullName)
					}
				}
				for bRef, ghIssue := range ghMap {
					if _, ok := dbMap[bRef]; ok {
						continue
					}
					events, err := eventsOf(childRepo, subNum)
					if err != nil {
						return err
					}
					if ev, ok := newestBlockerEvent(events, "blocked_by_added", childRepo.FullName, bRef); ok {
						bRepo := blockerRepository(ghIssue, childRepo.FullName)
						if err := e.recordTreeIncomingChange(ctx, repository, childRepo.FullName, subNum, blockersField, addedChange, ev, blockerMetadata{bRepo, int64(ghIssue.GetNumber())}); err != nil {
							return err
						}
						markTouched(childRepo.FullName)
					}
				}
			}
			if !visited[ref] {
				if err := walk(childRepo, subNum, childRepo.FullName, ref); err != nil {
					return err
				}
			}
		}
		for _, want := range children[key] {
			if foundSet[want] || pendingParents[want] {
				continue
			}
			childRepoName := repoByRef[want]
			childRepo, readable := e.namedRepository(childRepoName, repository)
			ev, changeKind, pRepo, pNum, ok, err := e.findMissingChildEvent(ctx, from, parent, parentExact, key, want, childRepoName, childRepo, readable, bucketRefs, leaves.buckets, repository, workstream, eventsOf)
			if err != nil {
				return err
			}
			if ok {
				if err := e.recordTreeIncomingChange(ctx, repository, childRepoName, want.number, parentField, changeKind, ev, parentMetadata{pRepo, pNum}); err != nil {
					return err
				}
				markTouched(childRepoName)
			}
		}
		return nil
	}
	if err := walk(repository, workstream, repository.FullName, root); err != nil {
		return err
	}
	var stored copiedTree
	if err := json.Unmarshal([]byte(storedBuckets), &stored.buckets); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(storedNested), &stored.nested); err != nil {
		return err
	}
	compareBuckets := func(a, b copiedBucket) int { return cmp.Compare(a.Number, b.Number) }
	slices.SortFunc(leaves.buckets, compareBuckets)
	slices.SortFunc(stored.buckets, compareBuckets)
	compareNested := func(a, b copiedNested) int { return cmp.Compare(a.Number, b.Number) }
	slices.SortFunc(leaves.nested, compareNested)
	slices.SortFunc(stored.nested, compareNested)
	if !slices.EqualFunc(leaves.buckets, stored.buckets, func(a, b copiedBucket) bool { return a.Number == b.Number && a.State == b.State }) ||
		!slices.Equal(leaves.nested, stored.nested) {
		buckets, err := json.Marshal(append([]copiedBucket{}, leaves.buckets...))
		if err != nil {
			return err
		}
		nested, err := json.Marshal(append([]copiedNested{}, leaves.nested...))
		if err != nil {
			return err
		}
		if err := e.queries.SetCopiedTreeData(ctx, store.SetCopiedTreeDataParams{Buckets: string(buckets), Nested: string(nested), ID: workstreamID}); err != nil {
			return err
		}
		syncChanged = true
	}
	if syncChanged {
		e.publish(Change{Workstreams: true})
	}
	return nil
}

func findAddedParentEvent(from github.Repository, parent int64, parentExact string, key issueRef, sub subIssueAt, subRepoName string, subNum int64, childRepo github.Repository, readable bool, bucketRefs map[issueRef]bool, workstreamRepo github.Repository, workstream int64, eventsOf eventFetcher) (github.TreeEvent, string, int64, bool, error) {
	if readable {
		events, err := eventsOf(childRepo, subNum)
		if err != nil {
			return github.TreeEvent{}, "", 0, false, err
		}
		for i := len(events) - 1; i >= 0; i-- {
			ev := events[i]
			if ev.Event != "parent_issue_added" || ev.ParentIssue == nil {
				continue
			}
			pRepo := ev.ParentIssue.RepositoryName(childRepo.FullName)
			pNum := ev.ParentIssue.Number
			pRef := lowerRef(pRepo, pNum)
			if pRef == key || bucketRefs[pRef] {
				if bucketRefs[pRef] {
					pRepo, pNum = workstreamRepo.FullName, workstream
				} else {
					pRepo = parentExact
				}
				return ev, pRepo, pNum, true, nil
			}
		}
	}
	listRepo, listParent := from, parent
	if sub.bucket != 0 {
		listParent = sub.bucket
	}
	events, err := eventsOf(listRepo, listParent)
	if err != nil {
		return github.TreeEvent{}, "", 0, false, err
	}
	childRef := lowerRef(subRepoName, subNum)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Event == "sub_issue_added" && ev.SubIssue != nil && lowerRef(ev.SubIssue.RepositoryName(listRepo.FullName), ev.SubIssue.Number) == childRef {
			if sub.bucket != 0 {
				return ev, workstreamRepo.FullName, workstream, true, nil
			}
			return ev, parentExact, parent, true, nil
		}
	}
	return github.TreeEvent{}, "", 0, false, nil
}

func (e *Engine) findMissingChildEvent(ctx context.Context, from github.Repository, parent int64, parentExact string, key, childRef issueRef, childRepoName string, childRepo github.Repository, readable bool, bucketRefs map[issueRef]bool, buckets []copiedBucket, workstreamRepo github.Repository, workstream int64, eventsOf eventFetcher) (github.TreeEvent, string, string, int64, bool, error) {
	if readable {
		events, err := eventsOf(childRepo, childRef.number)
		if err != nil {
			return github.TreeEvent{}, "", "", 0, false, err
		}
		for i := len(events) - 1; i >= 0; i-- {
			ev := events[i]
			switch ev.Event {
			case "parent_issue_added":
				if ev.ParentIssue == nil {
					continue
				}
				pRepo := ev.ParentIssue.RepositoryName(childRepo.FullName)
				pNum := ev.ParentIssue.Number
				pRef := lowerRef(pRepo, pNum)
				if pRef == key || bucketRefs[pRef] {
					return github.TreeEvent{}, "", "", 0, false, nil
				}
				if bws, err := e.queries.FindBucketWorkstream(ctx, store.FindBucketWorkstreamParams{Repository: pRepo, Bucket: pNum}); err == nil {
					pRepo, pNum = bws.Repository, bws.Number
				} else if !errors.Is(err, sql.ErrNoRows) {
					return github.TreeEvent{}, "", "", 0, false, err
				}
				return ev, addedChange, pRepo, pNum, true, nil
			case "parent_issue_removed":
				if ev.ParentIssue == nil {
					continue
				}
				pRepo := ev.ParentIssue.RepositoryName(childRepo.FullName)
				pNum := ev.ParentIssue.Number
				pRef := lowerRef(pRepo, pNum)
				if pRef == key || bucketRefs[pRef] {
					if bucketRefs[pRef] {
						pRepo, pNum = workstreamRepo.FullName, workstream
					} else {
						pRepo = parentExact
					}
					return ev, removedChange, pRepo, pNum, true, nil
				}
				return ev, removedChange, pRepo, pNum, true, nil
			}
		}
		return github.TreeEvent{}, "", "", 0, false, nil
	}
	_ = childRepoName
	parentEvents, err := eventsOf(from, parent)
	if err != nil {
		return github.TreeEvent{}, "", "", 0, false, err
	}
	for i := len(parentEvents) - 1; i >= 0; i-- {
		ev := parentEvents[i]
		if ev.Event == "sub_issue_removed" && ev.SubIssue != nil && lowerRef(ev.SubIssue.RepositoryName(from.FullName), ev.SubIssue.Number) == childRef {
			return ev, removedChange, parentExact, parent, true, nil
		}
	}
	for _, bucket := range buckets {
		bucketEvents, err := eventsOf(workstreamRepo, bucket.Number)
		if err != nil {
			return github.TreeEvent{}, "", "", 0, false, err
		}
		for i := len(bucketEvents) - 1; i >= 0; i-- {
			ev := bucketEvents[i]
			if ev.Event == "sub_issue_removed" && ev.SubIssue != nil && lowerRef(ev.SubIssue.RepositoryName(workstreamRepo.FullName), ev.SubIssue.Number) == childRef {
				return ev, removedChange, workstreamRepo.FullName, workstream, true, nil
			}
		}
	}
	return github.TreeEvent{}, "", "", 0, false, nil
}

func newestBlockerEvent(events []github.TreeEvent, kind, issueRepo string, blockerRef issueRef) (github.TreeEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Event == kind && ev.BlockedBy != nil && lowerRef(ev.BlockedBy.RepositoryName(issueRepo), ev.BlockedBy.Number) == blockerRef {
			return ev, true
		}
	}
	return github.TreeEvent{}, false
}

func (e *Engine) isTreeAppActor(repository github.Repository, actor, targetRepo string, meta any) bool {
	if strings.EqualFold(actor, appLogin(repository.AppSlug)) {
		return true
	}
	if repo, ok := e.namedRepository(targetRepo, repository); ok && strings.EqualFold(actor, appLogin(repo.AppSlug)) {
		return true
	}
	if m, ok := meta.(parentMetadata); ok && m.ParentRepository != "" {
		if repo, ok := e.namedRepository(m.ParentRepository, repository); ok && strings.EqualFold(actor, appLogin(repo.AppSlug)) {
			return true
		}
	}
	return false
}

func (e *Engine) recordTreeIncomingChange(ctx context.Context, repository github.Repository, targetRepo string, number int64, field, change string, event github.TreeEvent, meta any) error {
	externalID := strconv.FormatInt(event.ID, 10)
	known, err := e.queries.HasIncomingChange(ctx, externalID)
	if err != nil || known {
		return err
	}
	actor := event.Actor.Login
	status := "pending"
	if e.isTreeAppActor(repository, actor, targetRepo, meta) {
		var otherRepo string
		var otherNumber int64
		switch m := meta.(type) {
		case parentMetadata:
			otherRepo, otherNumber = m.ParentRepository, m.Parent
		case blockerMetadata:
			otherRepo, otherNumber = m.BlockerRepository, m.Blocker
		}
		own, err := e.queries.DeleteOldestTreeOwnWrite(ctx, store.DeleteOldestTreeOwnWriteParams{
			Repository:      targetRepo,
			Number:          number,
			Field:           field,
			Change:          change,
			OtherRepository: otherRepo,
			OtherNumber:     otherNumber,
		})
		if err != nil {
			return err
		}
		addMissingChild := false
		if own == 0 && field == parentField && change == addedChange {
			node, err := e.queries.GetIssueTreeNode(ctx, store.GetIssueTreeNodeParams{Repository: targetRepo, Number: number})
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !node.Workstream.Valid) {
				addMissingChild = true
			} else if err != nil {
				return err
			}
		}
		if !addMissingChild {
			status = "done"
		}
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return e.queries.AddIncomingChange(ctx, store.AddIncomingChangeParams{
		ExternalID: externalID,
		Repository: targetRepo,
		Number:     number,
		Field:      field,
		Change:     change,
		Actor:      actor,
		Metadata:   string(encoded),
		Status:     status,
		CreatedAt:  now(),
	})
}

func (e *Engine) consumeTreeOwnWrites(ctx context.Context, repository github.Repository, eventsOf eventFetcher) error {
	rows, err := e.queries.ListTreeOwnWrites(ctx)
	if err != nil {
		return err
	}
	app := appLogin(repository.AppSlug)
	for _, row := range rows {
		if !strings.EqualFold(row.Repository, repository.FullName) {
			continue
		}
		events, err := eventsOf(repository, row.Number)
		if err != nil {
			return err
		}
		for _, ev := range events {
			if !strings.EqualFold(ev.Actor.Login, app) {
				continue
			}
			externalID := strconv.FormatInt(ev.ID, 10)
			known, err := e.queries.HasIncomingChange(ctx, externalID)
			if err != nil {
				return err
			}
			if known {
				continue
			}
			var field, change, otherRepo string
			var otherNum int64
			var meta any
			switch ev.Event {
			case "parent_issue_added":
				if ev.ParentIssue == nil {
					continue
				}
				field, change = parentField, addedChange
				otherRepo, otherNum = ev.ParentIssue.RepositoryName(repository.FullName), ev.ParentIssue.Number
				meta = parentMetadata{otherRepo, otherNum}
			case "parent_issue_removed":
				if ev.ParentIssue == nil {
					continue
				}
				field, change = parentField, removedChange
				otherRepo, otherNum = ev.ParentIssue.RepositoryName(repository.FullName), ev.ParentIssue.Number
				meta = parentMetadata{otherRepo, otherNum}
			case "blocked_by_added":
				if ev.BlockedBy == nil {
					continue
				}
				field, change = blockersField, addedChange
				otherRepo, otherNum = ev.BlockedBy.RepositoryName(repository.FullName), ev.BlockedBy.Number
				meta = blockerMetadata{otherRepo, otherNum}
			case "blocked_by_removed":
				if ev.BlockedBy == nil {
					continue
				}
				field, change = blockersField, removedChange
				otherRepo, otherNum = ev.BlockedBy.RepositoryName(repository.FullName), ev.BlockedBy.Number
				meta = blockerMetadata{otherRepo, otherNum}
			default:
				continue
			}
			own, err := e.queries.DeleteOldestTreeOwnWrite(ctx, store.DeleteOldestTreeOwnWriteParams{
				Repository:      row.Repository,
				Number:          row.Number,
				Field:           field,
				Change:          change,
				OtherRepository: otherRepo,
				OtherNumber:     otherNum,
			})
			if err != nil {
				return err
			}
			if own == 0 {
				continue
			}
			encoded, err := json.Marshal(meta)
			if err != nil {
				return err
			}
			if err := e.queries.AddIncomingChange(ctx, store.AddIncomingChangeParams{
				ExternalID: externalID,
				Repository: row.Repository,
				Number:     row.Number,
				Field:      field,
				Change:     change,
				Actor:      ev.Actor.Login,
				Metadata:   string(encoded),
				Status:     "done",
				CreatedAt:  now(),
			}); err != nil {
				return err
			}
		}
	}
	return nil
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

// updateCopiedIssue changes the copied row of the issue of repository. It gives false when the copy has the same values.
// It keeps the labels or the body of the copy when a local change changed them after labelChangeCount gave changes,
// because the issue can have the old values. In the label case, a closed issue of the copy also stays closed, because
// the issue can be open on GitHub from before the close that Mobius made.
func (e *Engine) updateCopiedIssue(ctx context.Context, issue *gh.Issue, repository string, changes copyCounts) (bool, error) {
	changed := false
	number := int64(issue.GetNumber())
	labels := labelNames(issue)
	slices.Sort(labels)
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	ref := lowerRef(repository, number)
	keepLabels := e.labelChangedAt[ref] > changes.labels
	keepBody := e.bodyChangedAt[ref] > changes.body
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
		body := withoutWriteMarker(issue.GetBody())
		workstream, err := q.GetCopiedIssueWorkstreamOf(ctx, store.GetCopiedIssueWorkstreamOfParams{Repository: repository, Number: number})
		if err == nil {
			body = e.WithoutChatLink(workstream.Repository, workstream.Number, body)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		updated, err := q.UpdateCopiedIssue(ctx, store.UpdateCopiedIssueParams{
			Title:      issue.GetTitle(),
			KeepBody:   keepBody,
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
		repo := strings.ToLower(repositoryName)
		e.labelChanges[repo]++
		e.labelChangedAt[lowerRef(repositoryName, number)] = e.labelChanges[repo]
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

type copyCounts struct {
	labels int
	body   int
}

type copySnapshots struct {
	labels   map[issueRef]int
	body     map[issueRef]int
	blockers map[issueRef]int
}

func (e *Engine) labelChangeCount(repository string) copyCounts {
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	repo := strings.ToLower(repository)
	return copyCounts{labels: e.labelChanges[repo], body: e.bodyChanges[repo]}
}

// labelChangesNow gives the change counts of all issues. A read of GitHub that starts after it gives at least these
// states.
func (e *Engine) labelChangesNow() copySnapshots {
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	return copySnapshots{
		labels:   maps.Clone(e.labelChangedAt),
		body:     maps.Clone(e.bodyChangedAt),
		blockers: maps.Clone(e.blockerChangedAt),
	}
}

// writeRead runs write in a transaction, to store the trees that GitHub gave. An issue that a local change changed
// after labelChangesNow gave before keeps its copied labels, closed state, body, or blockers, because the read can have
// the old values.
func (e *Engine) writeRead(ctx context.Context, before copySnapshots, trees []copiedTree, write func(*store.Queries) error) error {
	e.labelChangeMu.Lock()
	defer e.labelChangeMu.Unlock()
	return e.inTx(ctx, func(q *store.Queries) error {
		for t := range trees {
			for i := range trees[t].issues {
				copied := &trees[t].issues[i]
				issue := copied.issue
				ref := lowerRef(copied.repository, int64(issue.GetNumber()))
				labelsChanged := e.labelChangedAt[ref] != before.labels[ref]
				bodyChanged := e.bodyChangedAt[ref] != before.body[ref]
				blockersChanged := e.blockerChangedAt[ref] != before.blockers[ref]
				if !labelsChanged && !bodyChanged && !blockersChanged {
					continue
				}
				row, err := q.GetCopiedIssue(ctx, store.GetCopiedIssueParams{Repository: copied.repository, Number: int64(issue.GetNumber())})
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				if labelsChanged {
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
				if bodyChanged {
					copied.body = row.Body
				}
				if blockersChanged {
					saved, err := q.ListBlockersOfIssue(ctx, row.ID)
					if err != nil {
						return err
					}
					restored := make([]copiedBlocker, 0, len(saved))
					for _, b := range saved {
						restored = append(restored, copiedBlocker{id: b.ID})
					}
					copied.blockers = restored
				}
			}
		}
		return write(q)
	})
}

// addedCopyLifetime is the time that a copied tree keeps an issue with a recent parent outgoing change.
const addedCopyLifetime = 10 * time.Minute

// copyTree updates the bucket metadata and adds only new issues that the Mobius App created for the Workstream,
// keeping the parent, Workstream and blockers of each existing row unchanged.
func (e *Engine) copyTree(ctx context.Context, repository github.Repository, workstream int64) error {
	before := e.labelChangesNow()
	tree, err := e.readTree(ctx, repository, workstream)
	if err != nil {
		return err
	}
	appAdded, err := e.appAddedIssues(ctx, repository, workstream, tree)
	if err != nil {
		return err
	}
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	return e.writeRead(ctx, before, []copiedTree{tree}, func(q *store.Queries) error {
		return addCreatedTreeIssues(ctx, q, repository.FullName, workstream, tree, appAdded)
	})
}

func (e *Engine) appAddedIssues(ctx context.Context, repository github.Repository, workstream int64, tree copiedTree) (map[issueRef]bool, error) {
	appAdded := map[issueRef]bool{}
	wsApp := appLogin(repository.AppSlug)
	eventsCache := map[issueRef][]github.TreeEvent{}
	eventsOf := func(repo github.Repository, num int64) ([]github.TreeEvent, error) {
		key := lowerRef(repo.FullName, num)
		if cached, ok := eventsCache[key]; ok {
			return cached, nil
		}
		events, err := repo.TreeEvents(ctx, num)
		if err != nil {
			return nil, err
		}
		eventsCache[key] = events
		return events, nil
	}
	for _, copied := range tree.issues {
		num := int64(copied.issue.GetNumber())
		_, err := e.queries.GetIssueTreeNode(ctx, store.GetIssueTreeNodeParams{Repository: copied.repository, Number: num})
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		author := copied.issue.GetUser().GetLogin()
		childRepo, readable := e.namedRepository(copied.repository, repository)
		childApp := wsApp
		if readable && childRepo.AppSlug != "" {
			childApp = appLogin(childRepo.AppSlug)
		}
		if !strings.EqualFold(author, wsApp) && !strings.EqualFold(author, childApp) {
			continue
		}
		ok, err := e.addedByApp(repository, workstream, copied, num, childRepo, readable, wsApp, childApp, eventsOf)
		if err != nil {
			return nil, err
		}
		if ok {
			appAdded[lowerRef(copied.repository, num)] = true
		}
	}
	return appAdded, nil
}

func (e *Engine) addedByApp(repository github.Repository, workstream int64, copied copiedIssue, num int64, childRepo github.Repository, readable bool, wsApp, childApp string, eventsOf eventFetcher) (bool, error) {
	parentRepoName, parentNum := repository.FullName, workstream
	if copied.parent != (issueRef{}) {
		parentRepoName, parentNum = copied.parent.repository, copied.parent.number
	}
	parentKey := lowerRef(parentRepoName, parentNum)
	bucketKey := issueRef{}
	if copied.bucket != 0 {
		bucketKey = lowerRef(repository.FullName, copied.bucket)
	}
	if readable {
		events, err := eventsOf(childRepo, num)
		if err != nil {
			return false, err
		}
		for i := len(events) - 1; i >= 0; i-- {
			ev := events[i]
			if ev.Event != "parent_issue_added" || ev.ParentIssue == nil {
				continue
			}
			pRef := lowerRef(ev.ParentIssue.RepositoryName(childRepo.FullName), ev.ParentIssue.Number)
			if pRef == parentKey || (copied.bucket != 0 && pRef == bucketKey) {
				return strings.EqualFold(ev.Actor.Login, wsApp) || strings.EqualFold(ev.Actor.Login, childApp), nil
			}
		}
	}
	listRepo, ok := e.namedRepository(parentRepoName, repository)
	if !ok {
		return false, nil
	}
	listParent := parentNum
	if copied.bucket != 0 {
		listRepo = repository
		listParent = copied.bucket
	}
	events, err := eventsOf(listRepo, listParent)
	if err != nil {
		return false, err
	}
	listApp := appLogin(listRepo.AppSlug)
	childRef := lowerRef(copied.repository, num)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Event == "sub_issue_added" && ev.SubIssue != nil && lowerRef(ev.SubIssue.RepositoryName(listRepo.FullName), ev.SubIssue.Number) == childRef {
			return strings.EqualFold(ev.Actor.Login, wsApp) || strings.EqualFold(ev.Actor.Login, listApp), nil
		}
	}
	return false, nil
}

func addCreatedTreeIssues(ctx context.Context, q *store.Queries, repository string, workstream int64, tree copiedTree, appAdded map[issueRef]bool) error {
	wsID, err := q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: repository, Number: workstream})
	if err != nil {
		return err
	}
	ids := map[issueRef]int64{}
	for _, copied := range tree.issues {
		num := int64(copied.issue.GetNumber())
		ref := lowerRef(copied.repository, num)
		existing, err := q.GetIssueTreeNode(ctx, store.GetIssueTreeNodeParams{Repository: copied.repository, Number: num})
		if err == nil {
			if existing.Workstream.Valid && existing.Workstream.Int64 == wsID {
				ids[ref] = existing.ID
				if !existing.Parent.Valid {
					if meta, err := parseMetadata(existing.Metadata); err == nil && meta.Bucket != copied.bucket {
						if err := q.SetIssueBucket(ctx, store.SetIssueBucketParams{Bucket: copied.bucket, ID: existing.ID}); err != nil {
							return err
						}
					}
				}
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !appAdded[ref] {
			continue
		}
		var parent sql.NullInt64
		if copied.parent != (issueRef{}) {
			parentID, found := ids[lowerRef(copied.parent.repository, copied.parent.number)]
			if !found {
				continue
			}
			parent = sql.NullInt64{Int64: parentID, Valid: true}
		}
		id, err := addNewCopiedIssue(ctx, q, wsID, parent, copied)
		if err != nil {
			return err
		}
		ids[ref] = id
	}
	return setCopiedTreeData(ctx, q, wsID, tree)
}

func setCopiedTreeData(ctx context.Context, q *store.Queries, wsID int64, tree copiedTree) error {
	buckets, err := json.Marshal(append([]copiedBucket{}, tree.buckets...))
	if err != nil {
		return err
	}
	nested, err := json.Marshal(append([]copiedNested{}, tree.nested...))
	if err != nil {
		return err
	}
	return q.SetCopiedTreeData(ctx, store.SetCopiedTreeDataParams{Buckets: string(buckets), Nested: string(nested), ID: wsID})
}

func addNewCopiedIssue(ctx context.Context, q *store.Queries, workstream int64, parent sql.NullInt64, copied copiedIssue) (int64, error) {
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
	return id, addMissingBlockers(ctx, q, id, copied.blockers)
}

func addMissingBlockers(ctx context.Context, q *store.Queries, issue int64, blockers []copiedBlocker) error {
	for _, blocker := range blockers {
		id := blocker.id
		if id == 0 {
			existing, err := q.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: blocker.repository, Number: int64(blocker.issue.GetNumber())})
			switch {
			case err == nil:
				id = existing.ID
			case errors.Is(err, sql.ErrNoRows):
				metadata, err := githubMetadata(blocker.issue, 0)
				if err != nil {
					return err
				}
				id, err = q.AddCopiedBlockerIssue(ctx, store.AddCopiedBlockerIssueParams{
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
			default:
				return err
			}
		}
		if err := q.AddCopiedBlocker(ctx, store.AddCopiedBlockerParams{Issue: issue, Blocker: id}); err != nil {
			return err
		}
	}
	return nil
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
			if parent == workstream && repository.FullName == home.FullName {
				tree.nested = append(tree.nested, nestedOf(issue))
			}
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
			body:       e.WithoutChatLink(home.FullName, workstream, withoutWriteMarker(issue.GetBody())),
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

// closeCopiedWorkstream closes the row of the Workstream number of repository, deletes its recurring tasks and MCP
// servers, and publishes the Workstreams change. The tree stays until removeClosedWorkstreamTree.
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
		return q.DeleteWorkstreamMcpServers(ctx, store.DeleteWorkstreamMcpServersParams{Repository: repository.FullName, Workstream: number})
	})
	if err != nil || !closed {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// removeClosedWorkstreamTree takes the tree of the closed Workstream number of repository out of issues. closeWorkstream
// reads the tree, so it runs first, and a retry of a failed closeWorkstream still finds the tree.
func (e *Engine) removeClosedWorkstreamTree(ctx context.Context, repository string, number int64) error {
	err := e.inTx(ctx, func(q *store.Queries) error {
		return removeClosedTree(ctx, q, repository, number)
	})
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// recopyAfterRemoval copies the subtree rooted at number into the Workstream above it, because the issue is no
// Workstream, so it is a task of that Workstream. Existing rows of other issues stay unchanged.
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
		if err := e.copySubtreeIntoWorkstream(ctx, issueRef{parentRepository, parent}, repository.FullName, number); err != nil {
			return err
		}
	}
	e.publish(Change{Workstreams: true})
	return nil
}

func (e *Engine) copySubtreeIntoWorkstream(ctx context.Context, workstream issueRef, rootRepo string, rootNum int64) error {
	repository, err := e.repository(workstream.repository)
	if err != nil {
		return err
	}
	before := e.labelChangesNow()
	tree, err := e.readTree(ctx, repository, workstream.number)
	if err != nil {
		return err
	}
	target := lowerRef(rootRepo, rootNum)
	inSubtree := map[issueRef]bool{}
	for _, copied := range tree.issues {
		ref := lowerRef(copied.repository, int64(copied.issue.GetNumber()))
		if ref == target || (copied.parent != (issueRef{}) && inSubtree[lowerRef(copied.parent.repository, copied.parent.number)]) {
			inSubtree[ref] = true
		}
	}
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	return e.writeRead(ctx, before, []copiedTree{tree}, func(q *store.Queries) error {
		wsID, err := q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: repository.FullName, Number: workstream.number})
		if err != nil {
			return err
		}
		ids := map[issueRef]int64{}
		for _, copied := range tree.issues {
			num := int64(copied.issue.GetNumber())
			ref := lowerRef(copied.repository, num)
			existing, err := q.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: copied.repository, Number: num})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			stored := err == nil
			if !inSubtree[ref] {
				if stored && existing.Workstream.Valid && existing.Workstream.Int64 == wsID {
					ids[ref] = existing.ID
				}
				continue
			}
			if stored && existing.Workstream.Valid {
				ids[ref] = existing.ID
				continue
			}
			var parent sql.NullInt64
			if copied.parent != (issueRef{}) {
				parentID, found := ids[lowerRef(copied.parent.repository, copied.parent.number)]
				if !found {
					continue
				}
				parent = sql.NullInt64{Int64: parentID, Valid: true}
			}
			if stored {
				if err := q.SetIssueParentAndWorkstream(ctx, store.SetIssueParentAndWorkstreamParams{
					Workstream: sql.NullInt64{Int64: wsID, Valid: true},
					Parent:     parent,
					ID:         existing.ID,
				}); err != nil {
					return err
				}
				if copied.bucket != 0 {
					if err := q.SetIssueBucket(ctx, store.SetIssueBucketParams{Bucket: copied.bucket, ID: existing.ID}); err != nil {
						return err
					}
				}
				if err := addMissingBlockers(ctx, q, existing.ID, copied.blockers); err != nil {
					return err
				}
				ids[ref] = existing.ID
				continue
			}
			id, err := addNewCopiedIssue(ctx, q, wsID, parent, copied)
			if err != nil {
				return err
			}
			ids[ref] = id
		}
		return setCopiedTreeData(ctx, q, wsID, tree)
	})
}

// removeTree takes the issues out of the tree of the Workstream. An issue that blocks an issue of another tree stays
// as a row with no Workstream, and any other issue goes away.
func removeTree(ctx context.Context, q *store.Queries, repository string, workstream int64) error {
	if err := detachTree(ctx, q, repository, workstream); err != nil {
		return err
	}
	return deleteUnusedIssues(ctx, q)
}

// removeClosedTree is removeTree for a Workstream that stays as a closed row. An issue that stays as a blocker keeps the
// id of that Workstream, so the blocked tasks still show its title.
func removeClosedTree(ctx context.Context, q *store.Queries, repository string, workstream int64) error {
	id, err := q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: repository, Number: workstream})
	if err != nil {
		return err
	}
	if err := q.KeepWorkstreamOfBlockers(ctx, sql.NullInt64{Int64: id, Valid: true}); err != nil {
		return err
	}
	return removeTree(ctx, q, repository, workstream)
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

func detachOpenTree(ctx context.Context, q *store.Queries, repository string, workstream int64) error {
	id, err := q.GetWorkstreamID(ctx, store.GetWorkstreamIDParams{Repository: repository, Number: workstream})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	since := keepSince()
	if err := q.DeleteCopiedBlockersOfOpenTree(ctx, store.DeleteCopiedBlockersOfOpenTreeParams{Workstream: sql.NullInt64{Int64: id, Valid: true}, KeepSince: since}); err != nil {
		return err
	}
	return q.DetachCopiedOpenTree(ctx, store.DetachCopiedOpenTreeParams{Workstream: sql.NullInt64{Int64: id, Valid: true}, KeepSince: since})
}

// replaceTree writes the tree as the tree of the Workstream. An issue that was in the tree keeps its row and its id.
func replaceTree(ctx context.Context, q *store.Queries, repository string, workstream int64, tree copiedTree) error {
	if err := detachOpenTree(ctx, q, repository, workstream); err != nil {
		return err
	}
	if err := addTree(ctx, q, repository, workstream, tree); err != nil {
		return err
	}
	return deleteUnusedIssues(ctx, q)
}

// deleteUnusedIssues refuses pending changes and removes blockers of detached issues before deleting unused issue rows,
// so SQLite id reuse cannot inherit orphan blockers or pending outgoing changes.
func deleteUnusedIssues(ctx context.Context, q *store.Queries) error {
	if err := q.RefuseDetachedIssueChanges(ctx); err != nil {
		return err
	}
	if err := q.DeleteBlockersOfDetachedIssues(ctx); err != nil {
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
	nested, err := json.Marshal(append([]copiedNested{}, tree.nested...))
	if err != nil {
		return err
	}
	return q.SetCopiedTreeData(ctx, store.SetCopiedTreeDataParams{Buckets: string(buckets), Nested: string(nested), ID: id})
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
	pending, err := q.HasPendingIssueChange(ctx, store.HasPendingIssueChangeParams{
		Issue: sql.NullInt64{Int64: id, Valid: true},
		Field: blockersField,
	})
	if err != nil || pending {
		return id, err
	}
	if err := q.DeleteCopiedBlockersOfIssue(ctx, id); err != nil {
		return 0, err
	}
	return id, addBlockers(ctx, q, id, copied.blockers)
}

// addBlockers adds the blockers of the issue with the id issue. A blocker with no row gets a row with no Workstream.
func addBlockers(ctx context.Context, q *store.Queries, issue int64, blockers []copiedBlocker) error {
	for _, blocker := range blockers {
		id := blocker.id
		if id == 0 {
			metadata, err := githubMetadata(blocker.issue, 0)
			if err != nil {
				return err
			}
			id, err = q.AddCopiedBlockerIssue(ctx, store.AddCopiedBlockerIssueParams{
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
