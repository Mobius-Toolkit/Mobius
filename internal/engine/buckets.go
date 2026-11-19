package engine

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

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
	// placed holds the ids of the issues that makeRoom already moved below a new bucket.
	placed map[int64]bool
	// changed tells that a write changed the tree of the Workstream.
	changed bool
}

// fillBuckets moves the sub-issues of each Workstream of repository that has too many direct sub-issues into buckets.
// It decides from the issues table and from workstreams.metadata, and reads GitHub only for a Workstream that needs
// work. The sub-issues of a bucket count as no direct sub-issue of the Workstream. A failed write ends the work for its
// Workstream, and the next poll tries again.
func (e *Engine) fillBuckets(ctx context.Context, repository github.Repository) {
	if !e.copied[repository.FullName] {
		return
	}
	pending, err := e.queries.ListPendingBuckets(ctx, repository.FullName)
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
	changed := false
	for _, workstream := range slices.Compact(workstreams) {
		fill := &bucketFill{engine: e, repository: repository, workstream: workstream, moves: &moves, placed: map[int64]bool{}}
		err := fill.run(ctx, slices.DeleteFunc(slices.Clone(pending), func(row store.ListPendingBucketsRow) bool { return row.Workstream != workstream }), slices.Contains(needing, workstream))
		if err != nil {
			log.Printf("fill the buckets of %s#%d: %v", repository.FullName, workstream, err)
		}
		changed = changed || fill.changed
	}
	if changed {
		e.publish(Change{Workstreams: true})
	}
}

func (f *bucketFill) run(ctx context.Context, pending []store.ListPendingBucketsRow, needed bool) error {
	for _, row := range pending {
		if err := f.finish(ctx, row); err != nil {
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
// has no sub-issues. While the Workstream has too many direct sub-issues, an empty open bucket stays open: GitHub
// refused the moves into it, and a closed bucket would make the next poll create a new open bucket.
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
		if remaining > 0 || f.direct > maxDirectSubIssues {
			continue
		}
		if err := f.closeBucket(ctx, open.number); err != nil {
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
	if f.placed[issue.GetID()] {
		return true, nil
	}
	err = f.add(ctx, target.number, issue)
	if refusedByGitHub(err) {
		log.Printf("move %s#%d below %s#%d: %v", f.repository.FullName, issue.GetNumber(), f.repository.FullName, target.number, err)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := f.storeBucketOf(ctx, issue, target.number); err != nil {
		return false, err
	}
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

// create makes a bucket below the Workstream. It stores the entry of the bucket in workstreams.metadata before the call
// to GitHub, with the marker of the bucket issue. The entry stays pending until the bucket is closed, when it has to be
// closed, and below the Workstream. Thus a failed call, also a call that GitHub did but did not answer, makes no
// second bucket.
func (f *bucketFill) create(ctx context.Context, closed bool) (*bucket, error) {
	state := "open"
	if closed {
		state = "closed"
	}
	title := fmt.Sprintf("Bucket %d of #%d", len(f.buckets)+1, f.workstream)
	marker := fmt.Sprintf("<!-- mobius:bucket %d -->", time.Now().UnixNano())
	entry, err := json.Marshal(copiedBucket{State: state, Title: title, Pending: true, Marker: marker})
	if err != nil {
		return nil, err
	}
	err = f.engine.queries.AddPendingBucket(ctx, store.AddPendingBucketParams{Bucket: string(entry), Repository: f.repository.FullName, Workstream: f.workstream})
	if err != nil {
		return nil, err
	}
	issue, err := f.makeIssue(ctx, marker, title)
	if err != nil {
		return nil, err
	}
	if err := f.complete(ctx, issue, closed); err != nil {
		return nil, err
	}
	created := &bucket{number: int64(issue.GetNumber()), closed: closed}
	f.buckets = slices.Insert(f.buckets, 0, created)
	f.direct++
	return created, nil
}

// makeIssue gives the bucket issue that has the marker, or makes it, and stores its number in the entry of the bucket.
func (f *bucketFill) makeIssue(ctx context.Context, marker, title string) (*gh.Issue, error) {
	issue, err := f.engine.findOrCreateIssue(ctx, f.repository, marker, title, fmt.Sprintf(bucketBody, f.workstream), []string{bucketLabel})
	if err != nil {
		return nil, err
	}
	f.changed = true
	if _, err := f.own(ctx, int64(issue.GetNumber()), labeledChange); err != nil {
		return nil, err
	}
	err = f.engine.queries.SetBucketNumber(ctx, store.SetBucketNumberParams{Marker: marker, Number: int64(issue.GetNumber()), Repository: f.repository.FullName, Workstream: f.workstream})
	return issue, err
}

// finish completes a bucket that an earlier poll did not complete. An entry with no number lacks its issue. An entry
// whose issue is gone is deleted.
func (f *bucketFill) finish(ctx context.Context, row store.ListPendingBucketsRow) error {
	var issue *gh.Issue
	var err error
	if row.Number == 0 {
		issue, err = f.makeIssue(ctx, row.Marker, row.Title)
	} else {
		issue, err = f.repository.Issue(ctx, row.Number)
	}
	if err != nil {
		return err
	}
	if issue == nil {
		return f.engine.queries.DeleteBucket(ctx, store.DeleteBucketParams{Number: row.Number, Repository: f.repository.FullName, Workstream: f.workstream})
	}
	return f.complete(ctx, issue, row.Closed)
}

// complete closes the bucket when it has to be closed, adds it below the Workstream when it is not a sub-issue yet,
// and ends the pending state of its entry.
func (f *bucketFill) complete(ctx context.Context, issue *gh.Issue, closed bool) error {
	number := int64(issue.GetNumber())
	state := issue.GetState()
	if closed && state == "open" {
		if err := f.closeBucket(ctx, number); err != nil {
			return err
		}
		state = "closed"
	}
	parent, err := f.repository.Parent(ctx, number)
	if err != nil {
		return err
	}
	if int64(parent.GetNumber()) != f.workstream {
		if err := f.makeRoom(ctx, number, closed); err != nil {
			return err
		}
		if err := f.add(ctx, f.workstream, issue); err != nil {
			return err
		}
		f.changed = true
	}
	return f.engine.queries.SetBucketState(ctx, store.SetBucketStateParams{Number: number, State: state, Repository: f.repository.FullName, Workstream: f.workstream})
}

// closeBucket closes the bucket issue number and stores the state in its entry.
func (f *bucketFill) closeBucket(ctx context.Context, number int64) error {
	own, err := f.own(ctx, number, closedChange)
	if err != nil {
		return err
	}
	if _, err := f.repository.CloseIssue(ctx, number, "completed"); err != nil {
		if delErr := f.engine.queries.DeleteOwnWrite(ctx, own); delErr != nil {
			return delErr
		}
		return err
	}
	return f.engine.queries.SetBucketState(ctx, store.SetBucketStateParams{Number: number, State: "closed", Repository: f.repository.FullName, Workstream: f.workstream})
}

// makeRoom moves the first direct sub-issue of the Workstream with the state of the bucket below the bucket, when the
// Workstream has no room for the bucket. GitHub refuses the add of the bucket below a Workstream with 100 sub-issues.
func (f *bucketFill) makeRoom(ctx context.Context, bucket int64, closed bool) error {
	direct, err := f.repository.SubIssues(ctx, f.workstream)
	if err != nil {
		return err
	}
	if len(direct) < bucketCapacity || *f.moves >= maxBucketMoves {
		return nil
	}
	var candidates []*gh.Issue
	for _, issue := range direct {
		if !hasLabel(issue, bucketLabel) && (issue.GetState() == "closed") == closed {
			candidates = append(candidates, issue)
		}
	}
	if closed {
		slices.SortStableFunc(candidates, func(a, b *gh.Issue) int { return a.GetClosedAt().Compare(b.GetClosedAt().Time) })
	} else {
		slices.SortStableFunc(candidates, func(a, b *gh.Issue) int { return a.GetCreatedAt().Compare(b.GetCreatedAt().Time) })
	}
	for _, issue := range candidates {
		if *f.moves >= maxBucketMoves {
			return nil
		}
		err := f.add(ctx, bucket, issue)
		if refusedByGitHub(err) {
			log.Printf("move %s#%d below %s#%d: %v", f.repository.FullName, issue.GetNumber(), f.repository.FullName, bucket, err)
			continue
		}
		if err != nil {
			return err
		}
		if err := f.storeBucketOf(ctx, issue, bucket); err != nil {
			return err
		}
		f.placed[issue.GetID()] = true
		f.direct--
		f.changed = true
		return nil
	}
	return nil
}

// add counts each call toward the limit of moves, also a refused one, because it uses the rate limit of GitHub. It
// stores the own write of the call, and deletes it when GitHub refuses the call, because GitHub makes no event then.
func (f *bucketFill) add(ctx context.Context, parent int64, issue *gh.Issue) error {
	*f.moves++
	own, err := f.own(ctx, int64(issue.GetNumber()), addedChange)
	if err != nil {
		return err
	}
	_, _, err = f.repository.Client.SubIssue.Add(ctx, f.repository.Owner(), f.repository.Name(), parent, gh.SubIssueRequest{SubIssueID: issue.GetID(), ReplaceParent: new(true)})
	if err != nil {
		if delErr := f.engine.queries.DeleteOwnWrite(ctx, own); delErr != nil {
			return delErr
		}
	}
	return err
}

// own stores that the call of the bucket machinery makes the change kind on the issue number, so the poll makes no
// incoming change for the event of the call.
func (f *bucketFill) own(ctx context.Context, number int64, kind string) (int64, error) {
	return addOwnWrite(ctx, f.engine.queries, f.repository.FullName, number, bucketField, "", kind)
}

// storeBucketOf stores in the row of issue that it is below the bucket. An issue with no row has nothing to store.
func (f *bucketFill) storeBucketOf(ctx context.Context, issue *gh.Issue, bucket int64) error {
	node, err := f.engine.queries.GetIssueTreeNode(ctx, store.GetIssueTreeNodeParams{Repository: f.repository.FullName, Number: int64(issue.GetNumber())})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.engine.queries.SetIssueBucket(ctx, store.SetIssueBucketParams{Bucket: bucket, ID: node.ID})
}
