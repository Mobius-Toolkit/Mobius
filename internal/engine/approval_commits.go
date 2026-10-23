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

const maxBroughtInLines = 30

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

// approvedHeadText gives the lines that follow the first line of the event ready_for_approval when the Lead approved
// an earlier head of the pull request. A 4xx answer of GitHub gives a line about it, and any other error returns.
func approvedHeadText(ctx context.Context, repository github.Repository, task store.Task, pullRequest *gh.PullRequest) (string, error) {
	approved := task.ApprovedHead.String
	if approved == "" || approved == pullRequest.GetHead().GetSHA() {
		return "", nil
	}
	text, err := changesAfterApproval(ctx, repository, pullRequest, approved)
	var refused *gh.ErrorResponse
	if errors.As(err, &refused) && refused.Response.StatusCode < 500 {
		return fmt.Sprintf("\nMobius could not compare the approved head %.8s with the head: %d.", approved, refused.Response.StatusCode), nil
	}
	return text, err
}

// commitAfterApproval is a commit of the first-parent chain from the approved head to the head. Brought holds the
// commits of the compare that only this commit adds to the history.
type commitAfterApproval struct {
	commit  *gh.RepositoryCommit
	merge   bool
	brought []*gh.RepositoryCommit
}

// changesAfterApproval lists the commits after the approved head and tells if the change of the pull request is the
// same as at the approved head.
func changesAfterApproval(ctx context.Context, repository github.Repository, pullRequest *gh.PullRequest, approved string) (string, error) {
	branch := repository.DefaultBranch
	comparison, err := repository.Compare(ctx, approved, pullRequest.GetHead().GetSHA())
	if err != nil {
		return "", err
	}
	notInHistory := fmt.Sprintf("\nThe Lead approved the head %.8s before. It is not in the history of the head, so the change can differ.", approved)
	if comparison.GetStatus() != "ahead" {
		return notInHistory, nil
	}
	commits := map[string]*gh.RepositoryCommit{}
	for _, commit := range comparison.Commits {
		commits[commit.GetSHA()] = commit
	}
	var chain []commitAfterApproval
	for sha := pullRequest.GetHead().GetSHA(); sha != approved; {
		commit, found := commits[sha]
		if !found || len(commit.Parents) == 0 {
			return notInHistory, nil
		}
		chain = append(chain, commitAfterApproval{commit: commit})
		sha = commit.Parents[0].GetSHA()
	}
	slices.Reverse(chain)
	listed := map[string]bool{}
	for _, link := range chain {
		listed[link.commit.GetSHA()] = true
	}
	for i := range chain {
		parents := chain[i].commit.Parents
		if len(parents) == 2 {
			second, err := repository.Compare(ctx, branch, parents[1].GetSHA())
			if err != nil {
				return "", err
			}
			chain[i].merge = second.GetStatus() == "behind" || second.GetStatus() == "identical"
		}
		var pending []string
		for _, parent := range parents[1:] {
			pending = append(pending, parent.GetSHA())
		}
		for len(pending) > 0 {
			sha := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			commit, found := commits[sha]
			if !found || listed[sha] {
				continue
			}
			listed[sha] = true
			chain[i].brought = append(chain[i].brought, commit)
			for _, parent := range commit.Parents {
				pending = append(pending, parent.GetSHA())
			}
		}
	}
	var text strings.Builder
	fmt.Fprintf(&text, "\nThe Lead approved the head %.8s before. Commits after it:", approved)
	for _, link := range chain {
		mark := ""
		if link.merge {
			mark = fmt.Sprintf(" (merge of %s)", branch)
		}
		fmt.Fprintf(&text, "\n- %.8s %s%s", link.commit.GetSHA(), commitSubject(link.commit), mark)
	}
	var brought []*gh.RepositoryCommit
	for _, commit := range comparison.Commits {
		if !slices.ContainsFunc(chain, func(link commitAfterApproval) bool { return link.commit.GetSHA() == commit.GetSHA() }) {
			brought = append(brought, commit)
		}
	}
	if len(brought) > 0 {
		fmt.Fprintf(&text, "\nCommits of %s that these merges brought in:", branch)
	}
	for i, commit := range brought {
		if i == maxBroughtInLines {
			fmt.Fprintf(&text, "\n… and %d more.", len(brought)-i)
			break
		}
		fmt.Fprintf(&text, "\n- %.8s %s", commit.GetSHA(), commitSubject(commit))
	}
	reasons, err := differences(ctx, repository, pullRequest, approved, chain)
	if err != nil {
		return "", err
	}
	if len(reasons) == 0 {
		fmt.Fprintf(&text, "\nThe change of the pull request is the same as at the approved head: only merges of %s came after it.", branch)
		return text.String(), nil
	}
	text.WriteString("\nThe change of the pull request can differ from the approved head:")
	for _, reason := range reasons {
		fmt.Fprintf(&text, "\n- %s", reason)
	}
	return text.String(), nil
}

// differences gives the reasons why the change of the pull request can differ from the change at the approved head.
func differences(ctx context.Context, repository github.Repository, pullRequest *gh.PullRequest, approved string, chain []commitAfterApproval) ([]string, error) {
	branch := repository.DefaultBranch
	var reasons []string
	for _, link := range chain {
		if !link.merge {
			reasons = append(reasons, fmt.Sprintf("%.8s is not a merge of %s", link.commit.GetSHA(), branch))
		}
	}
	oldComparison, err := repository.Compare(ctx, branch, approved)
	if err != nil {
		return nil, err
	}
	var oldFiles []string
	for _, file := range oldComparison.Files {
		oldFiles = append(oldFiles, file.GetFilename())
	}
	files, err := repository.PullRequestFiles(ctx, int64(pullRequest.GetNumber()))
	if err != nil {
		return nil, err
	}
	added := slices.DeleteFunc(slices.Clone(files), func(file string) bool { return slices.Contains(oldFiles, file) })
	removed := slices.DeleteFunc(oldFiles, func(file string) bool { return slices.Contains(files, file) })
	if len(added) > 0 || len(removed) > 0 {
		reasons = append(reasons, fmt.Sprintf("the changed files differ: added %s, removed %s", fileNames(added), fileNames(removed)))
	}
	for _, link := range chain {
		var shared []string
		for _, commit := range link.brought {
			changed, err := repository.CommitFiles(ctx, commit.GetSHA())
			if err != nil {
				return nil, err
			}
			for _, file := range changed {
				if slices.Contains(files, file) && !slices.Contains(shared, file) {
					shared = append(shared, file)
				}
			}
		}
		if len(shared) > 0 {
			reasons = append(reasons, fmt.Sprintf("%.8s merged a change of %s, so it can hold a conflict resolution", link.commit.GetSHA(), strings.Join(shared, ", ")))
		}
	}
	return reasons, nil
}

func fileNames(files []string) string {
	if len(files) == 0 {
		return "none"
	}
	return strings.Join(files, ", ")
}

func commitSubject(commit *gh.RepositoryCommit) string {
	subject, _, _ := strings.Cut(commit.GetCommit().GetMessage(), "\n")
	return subject
}
