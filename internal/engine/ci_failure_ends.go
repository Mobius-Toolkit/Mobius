package engine

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// endCIFailures ends each open failure record of repository whose issue is closed. When a merged pull request closed
// the issue, it updates the branch of each open pull request of the record whose head is the head of its place. Then,
// until the head of the pull request changes or branchUpdateWait passes, it makes the next poll read the pull request
// from GitHub again, because GitHub adds the merge commit after its answer to the update. A 5xx answer of GitHub is an
// error, and the next poll tries again.
func (e *Engine) endCIFailures(ctx context.Context, repository github.Repository) error {
	failures, err := e.queries.ListCIFailuresWithIssue(ctx, repository.FullName)
	if err != nil {
		return err
	}
	var errs []error
	for _, failure := range failures {
		errs = append(errs, e.endCIFailure(ctx, repository, failure))
	}
	places, err := e.queries.ListEndedCIFailurePullRequestPlaces(ctx, store.ListEndedCIFailurePullRequestPlacesParams{
		Repository:   repository.FullName,
		UpdatedAfter: e.timeNow().Add(-2 * branchUpdateWait).UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, place := range places {
		if place.BranchUpdatedAt == "" {
			errs = append(errs, e.updateFailureBranch(ctx, repository, place))
		} else {
			errs = append(errs, e.awaitMergeCommit(repository, place))
		}
	}
	return errors.Join(errs...)
}

// endCIFailure ends failure when its issue is closed. A close with no merged pull request ends the record with no update
// of a branch.
func (e *Engine) endCIFailure(ctx context.Context, repository github.Repository, failure store.CiFailure) error {
	issue := max(failure.Issue, -failure.Issue)
	closed, err := e.ciBreakIssueClosed(ctx, repository, issue)
	if err != nil || !closed {
		return err
	}
	merged, err := e.fixMerged(ctx, repository, issue)
	if err != nil {
		return err
	}
	if !merged {
		if err := e.queries.SetCIFailurePlacesUpdated(ctx, store.SetCIFailurePlacesUpdatedParams{BranchUpdatedAt: noBranchUpdate, Failure: failure.ID}); err != nil {
			return err
		}
	}
	return e.queries.EndCIFailure(ctx, store.EndCIFailureParams{EndedAt: now(), ID: failure.ID})
}

// fixMerged tells whether the newest task of the issue has a merged pull request.
func (e *Engine) fixMerged(ctx context.Context, repository github.Repository, issue int64) (bool, error) {
	number, err := e.queries.GetNewestTaskPullRequest(ctx, store.GetNewestTaskPullRequestParams{Repository: repository.FullName, Issue: issue})
	if errors.Is(err, sql.ErrNoRows) || err == nil && !number.Valid {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	pullRequest, err := repository.PullRequest(ctx, number.Int64)
	if refusedByGitHub(err) {
		log.Printf("read the pull request #%d of %s: %v", number.Int64, repository.FullName, err)
		return false, nil
	}
	return pullRequest.GetMerged(), err
}

// updateFailureBranch updates the branch of the pull request of place when the pull request is open and its head is the
// head of place, and stores the time of the update. Else it stores noBranchUpdate, also after a 4xx answer of GitHub.
func (e *Engine) updateFailureBranch(ctx context.Context, repository github.Repository, place store.CiFailurePlace) error {
	pullRequest, err := repository.PullRequest(ctx, place.PullRequest)
	if err != nil && !refusedByGitHub(err) {
		return err
	}
	updatedAt := noBranchUpdate
	if err == nil && pullRequest.GetState() == "open" && pullRequest.GetHead().GetSHA() == place.Head {
		err = repository.UpdatePullRequestBranch(ctx, place.PullRequest, place.Head)
		if err != nil && !refusedByGitHub(err) {
			return err
		}
		if err == nil {
			e.pull(repository, place.PullRequest).changed = true
			updatedAt = e.updatedAt()
		}
	}
	return e.queries.SetCIFailurePlaceUpdated(ctx, store.SetCIFailurePlaceUpdatedParams{BranchUpdatedAt: updatedAt, ID: place.ID})
}

// awaitMergeCommit makes the next poll read the pull request of place from GitHub while the earlier read still has the
// head of place and branchUpdateWait has not passed since the update.
func (e *Engine) awaitMergeCommit(repository github.Repository, place store.CiFailurePlace) error {
	updatedAt, err := time.Parse(time.RFC3339Nano, place.BranchUpdatedAt)
	if err != nil {
		return err
	}
	state := e.pull(repository, place.PullRequest)
	if state.pullRequest.GetState() == "open" && state.pullRequest.GetHead().GetSHA() == place.Head && e.timeNow().Sub(updatedAt) < branchUpdateWait {
		state.changed = true
	}
	return nil
}

// noBranchUpdate is the branch_updated_at of a place that got no update. It is older than the window of
// ListEndedCIFailurePullRequestPlaces, so awaitMergeCommit never sees the place.
var noBranchUpdate = time.Time{}.Format(time.RFC3339Nano)

func (e *Engine) updatedAt() string {
	return e.timeNow().UTC().Format(time.RFC3339Nano)
}
