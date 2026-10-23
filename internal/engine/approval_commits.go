package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// newerCommitsText gives the lines that follow the first line of the event ready_for_approval. They list each commit of
// the default branch with a commit date after started that changes a file of the pull request. A 4xx answer of GitHub
// gives a line about it, and any other error returns.
func (e *Engine) newerCommitsText(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest, started time.Time) (string, error) {
	branch := repository.DefaultBranch
	if started.IsZero() {
		return fmt.Sprintf("\nThe head has no CI run, so Mobius lists no commit of %s.", branch), nil
	}
	text, err := sharedCommitLines(ctx, repository, pullRequest, started)
	var refused *gh.ErrorResponse
	if errors.As(err, &refused) && refused.Response.StatusCode < 500 {
		return fmt.Sprintf("\nMobius could not read the commits of %s after the CI: %d.", branch, refused.Response.StatusCode), nil
	}
	if err != nil {
		return "", err
	}
	if text == "" {
		return fmt.Sprintf("\nNo commit of %s after the start of the CI of the head (%s) changes a file of the pull request.", branch, started.UTC().Format(timeFormat)), nil
	}
	return fmt.Sprintf("\nCommits of %s after the start of the CI of the head (%s) that change a file of the pull request:%s\nReview rounds: %d of %d.", branch, started.UTC().Format(timeFormat), text, task.ReviewRounds, e.config.MaxFixRounds), nil
}

// sharedCommitLines gives one line for each commit of the default branch after started that changes a file of the pull
// request, the oldest first, or "" with no such commit.
func sharedCommitLines(ctx context.Context, repository github.Repository, pullRequest *gh.PullRequest, started time.Time) (string, error) {
	commits, err := repository.CommitsSince(ctx, repository.DefaultBranch, started)
	if err != nil {
		return "", err
	}
	commits = slices.DeleteFunc(commits, func(commit *gh.RepositoryCommit) bool {
		return !commit.GetCommit().GetCommitter().GetDate().After(started)
	})
	if len(commits) == 0 {
		return "", nil
	}
	pullFiles, err := repository.PullRequestFiles(ctx, int64(pullRequest.GetNumber()))
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, commit := range slices.Backward(commits) {
		files, err := repository.CommitFiles(ctx, commit.GetSHA())
		if err != nil {
			return "", err
		}
		shared := slices.DeleteFunc(files, func(file string) bool { return !slices.Contains(pullFiles, file) })
		if len(shared) == 0 {
			continue
		}
		subject, _, _ := strings.Cut(commit.GetCommit().GetMessage(), "\n")
		fmt.Fprintf(&text, "\n- %.8s %s %s. Shared files: %s.", commit.GetSHA(), commit.GetCommit().GetCommitter().GetDate().UTC().Format(timeFormat), subject, strings.Join(shared, ", "))
	}
	return text.String(), nil
}
