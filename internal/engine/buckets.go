package engine

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"slices"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// GitHub allows 100 sub-issues for one issue. A Workstream stays far below that limit, so a tool can add many issues at
// one time.
const (
	maxDirectSubIssues    = 50
	directSubIssuesTarget = 30
	bucketCapacity        = 100
	maxBucketMoves        = 100
)

const bucketBody = "This bucket holds sub-issues of the Workstream #%d.\n\n" +
	"GitHub allows at most 100 sub-issues for one issue. Mobius moves sub-issues of a Workstream below buckets, " +
	"so the Workstream can get new sub-issues and each issue stays in the tree of its Workstream. " +
	"A closed bucket holds closed issues, and an open bucket holds open issues."

type bucket struct {
	number  int64
	closed  bool
	count   int
	counted bool
}

// bucketFill moves the sub-issues of one Workstream into buckets.
type bucketFill struct {
	engine     *Engine
	repository github.Repository
	workstream int64
	// buckets are the direct sub-issues with the bucket label, the highest number first.
	buckets []*bucket
	// direct is the number of direct sub-issues of the Workstream.
	direct int
	moves  *int
	// changed tells that a write changed the tree of the Workstream.
	changed bool
}

// fillBuckets moves the sub-issues of each Workstream of repository that has too many direct sub-issues into buckets.
// It decides from the copy and reads GitHub only for a Workstream that needs work. The sub-issues of a bucket count as
// no direct sub-issue of the Workstream. A failed write ends the work for its Workstream, and the next poll tries
// again.
func (e *Engine) fillBuckets(ctx context.Context, repository github.Repository) {
	if !e.copied[repository.FullName] {
		return
	}
	pending, err := e.queries.ListWorkstreamBuckets(ctx, repository.FullName)
	if err != nil {
		log.Printf("fill the buckets of %s: %v", repository.FullName, err)
		return
	}
	needing, err := e.queries.ListWorkstreamsNeedingBuckets(ctx, store.ListWorkstreamsNeedingBucketsParams{Repository: repository.FullName, Maximum: maxDirectSubIssues})
	if err != nil {
		log.Printf("fill the buckets of %s: %v", repository.FullName, err)
		return
	}
	workstreams := slices.Clone(needing)
	for _, row := range pending {
		workstreams = append(workstreams, row.Workstream)
	}
	slices.Sort(workstreams)
	moves := 0
	for _, workstream := range slices.Compact(workstreams) {
		fill := &bucketFill{engine: e, repository: repository, workstream: workstream, moves: &moves}
		err := fill.run(ctx, slices.DeleteFunc(slices.Clone(pending), func(row store.ListWorkstreamBucketsRow) bool { return row.Workstream != workstream }), slices.Contains(needing, workstream))
		if err != nil {
			log.Printf("fill the buckets of %s#%d: %v", repository.FullName, workstream, err)
		}
		if fill.changed {
			if err := e.recopyTrees(ctx, repository, workstream); err != nil {
				log.Printf("copy the tree of %s#%d again: %v", repository.FullName, workstream, err)
			}
		}
	}
}

func (f *bucketFill) run(ctx context.Context, pending []store.ListWorkstreamBucketsRow, needed bool) error {
	for _, row := range pending {
		issue, err := f.repository.Issue(ctx, row.Issue)
		if err != nil {
			return err
		}
		if issue == nil {
			err := f.engine.queries.DeleteWorkstreamBucket(ctx, store.DeleteWorkstreamBucketParams{Repository: f.repository.FullName, Issue: row.Issue})
			if err != nil {
				return err
			}
			continue
		}
		if err := f.complete(ctx, issue, row.Closed); err != nil {
			return err
		}
	}
	if !needed {
		return nil
	}
	direct, err := f.repository.SubIssues(ctx, f.workstream)
	if err != nil {
		return err
	}
	f.direct = len(direct)
	var closedIssues, openIssues []*gh.Issue
	for _, issue := range direct {
		switch {
		case hasLabel(issue, bucketLabel):
			f.buckets = append(f.buckets, &bucket{number: int64(issue.GetNumber()), closed: issue.GetState() == "closed"})
		case issue.GetState() == "closed":
			closedIssues = append(closedIssues, issue)
		default:
			openIssues = append(openIssues, issue)
		}
	}
	slices.SortFunc(f.buckets, func(a, b *bucket) int { return cmp.Compare(b.number, a.number) })
	if f.direct > maxDirectSubIssues {
		slices.SortStableFunc(closedIssues, func(a, b *gh.Issue) int { return a.GetClosedAt().Compare(b.GetClosedAt().Time) })
		for _, issue := range closedIssues {
			if _, err := f.move(ctx, issue, true, true); err != nil {
				return err
			}
		}
		slices.SortStableFunc(openIssues, func(a, b *gh.Issue) int { return a.GetCreatedAt().Compare(b.GetCreatedAt().Time) })
		for _, issue := range openIssues {
			if f.direct <= directSubIssuesTarget {
				break
			}
			if _, err := f.move(ctx, issue, false, true); err != nil {
				return err
			}
		}
	}
	return f.emptyOpenBuckets(ctx)
}

// emptyOpenBuckets moves the closed issues of each open bucket into closed buckets, and closes each open bucket that
// has no sub-issues.
func (f *bucketFill) emptyOpenBuckets(ctx context.Context) error {
	for _, open := range slices.Clone(f.buckets) {
		if open.closed {
			continue
		}
		issues, err := f.repository.SubIssues(ctx, open.number)
		if err != nil {
			return err
		}
		remaining := len(issues)
		for _, issue := range issues {
			if issue.GetState() != "closed" {
				continue
			}
			moved, err := f.move(ctx, issue, true, false)
			if err != nil {
				return err
			}
			if moved {
				remaining--
			}
		}
		if remaining > 0 {
			continue
		}
		if _, err := f.repository.CloseIssue(ctx, open.number, "completed"); err != nil {
			return err
		}
		open.closed = true
		f.changed = true
	}
	return nil
}

// move adds issue below a bucket for closed or open issues, and tells if it moved the issue. GitHub can refuse the add
// for one issue for good, for example at the depth limit. Then the issue stays. The move stops at the limit of moves
// of a poll.
func (f *bucketFill) move(ctx context.Context, issue *gh.Issue, closed, fromDirect bool) (bool, error) {
	if *f.moves >= maxBucketMoves {
		return false, nil
	}
	target, err := f.target(ctx, closed)
	if err != nil {
		return false, err
	}
	err = f.add(ctx, target.number, issue.GetID())
	if refusedByGitHub(err) {
		log.Printf("move %s#%d below %s#%d: %v", f.repository.FullName, issue.GetNumber(), f.repository.FullName, target.number, err)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	*f.moves++
	target.count++
	f.changed = true
	if fromDirect {
		f.direct--
	}
	return true, nil
}

// target gives the bucket of the state with the highest number that has fewer than 100 sub-issues, or a new bucket.
// It reads the number of sub-issues of a bucket from GitHub, because the summary of an issue can be late.
func (f *bucketFill) target(ctx context.Context, closed bool) (*bucket, error) {
	for _, candidate := range f.buckets {
		if candidate.closed != closed {
			continue
		}
		if !candidate.counted {
			issues, err := f.repository.SubIssues(ctx, candidate.number)
			if err != nil {
				return nil, err
			}
			candidate.count = len(issues)
			candidate.counted = true
		}
		if candidate.count < bucketCapacity {
			return candidate, nil
		}
	}
	return f.create(ctx, closed)
}

// create makes a bucket below the Workstream. The row of the bucket stays until the bucket is closed, when it has to be
// closed, and below the Workstream. Thus a failed call makes no second bucket.
func (f *bucketFill) create(ctx context.Context, closed bool) (*bucket, error) {
	body := fmt.Sprintf(bucketBody, f.workstream)
	title := fmt.Sprintf("Bucket %d of #%d", len(f.buckets)+1, f.workstream)
	issue, _, err := f.repository.Client.Issues.Create(ctx, f.repository.Owner(), f.repository.Name(), gh.CreateIssueRequest{Title: title, Body: &body, Labels: []string{bucketLabel}})
	if err != nil {
		return nil, err
	}
	f.changed = true
	err = f.engine.queries.AddWorkstreamBucket(ctx, store.AddWorkstreamBucketParams{Repository: f.repository.FullName, Workstream: f.workstream, Issue: int64(issue.GetNumber()), Closed: closed})
	if err != nil {
		return nil, err
	}
	if err := f.complete(ctx, issue, closed); err != nil {
		return nil, err
	}
	created := &bucket{number: int64(issue.GetNumber()), closed: closed, counted: true}
	f.buckets = slices.Insert(f.buckets, 0, created)
	f.direct++
	return created, nil
}

// complete closes the bucket when it has to be closed, adds it below the Workstream when it is not a sub-issue yet,
// and deletes its row.
func (f *bucketFill) complete(ctx context.Context, issue *gh.Issue, closed bool) error {
	number := int64(issue.GetNumber())
	if closed && issue.GetState() == "open" {
		if _, err := f.repository.CloseIssue(ctx, number, "completed"); err != nil {
			return err
		}
	}
	parent, err := f.repository.Parent(ctx, number)
	if err != nil {
		return err
	}
	if int64(parent.GetNumber()) != f.workstream {
		if err := f.add(ctx, f.workstream, issue.GetID()); err != nil {
			return err
		}
		f.changed = true
	}
	return f.engine.queries.DeleteWorkstreamBucket(ctx, store.DeleteWorkstreamBucketParams{Repository: f.repository.FullName, Issue: number})
}

func (f *bucketFill) add(ctx context.Context, parent, id int64) error {
	_, _, err := f.repository.Client.SubIssue.Add(ctx, f.repository.Owner(), f.repository.Name(), parent, gh.SubIssueRequest{SubIssueID: id, ReplaceParent: new(true)})
	return err
}
