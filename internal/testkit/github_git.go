package testkit

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Remote gives the bare git repository of the repository fullName. Its file URL is the clone_url of the repository.
func (g *FakeGitHub) Remote(fullName string) string {
	return filepath.Join(g.remotes, fullName+".git")
}

// SetCloneURL makes url the clone URL of the repository fullName in the repository lists.
func (g *FakeGitHub) SetCloneURL(fullName, url string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cloneURLs[fullName] = url
}

// addEmptyRemote makes the bare git repository of fullName with no commit.
func (g *FakeGitHub) addEmptyRemote(fullName string) {
	remote := g.Remote(fullName)
	if err := os.MkdirAll(remote, 0o750); err != nil {
		g.t.Fatal(err)
	}
	g.git(remote, "init", "--bare", "--initial-branch=main")
}

// addRemote makes the bare git repository of fullName with one commit on main.
func (g *FakeGitHub) addRemote(fullName string) {
	g.addEmptyRemote(fullName)
	remote := g.Remote(fullName)
	work := g.t.TempDir()
	g.git(work, "init", "--initial-branch=main")
	g.git(work, "commit", "--allow-empty", "-m", "Start")
	g.git(work, "push", remote, "main:refs/heads/main")
}

// CommitFile commits the file path with content on main of the repository fullName.
func (g *FakeGitHub) CommitFile(fullName, path, content, message string) {
	g.commitFile(fullName, "main", path, content, message, time.Time{})
}

// CommitFileAt is CommitFile with at as the author date and the committer date of the commit.
func (g *FakeGitHub) CommitFileAt(fullName, path, content, message string, at time.Time) {
	g.commitFile(fullName, "main", path, content, message, at)
}

// CommitFileOnBranch commits the file path with content on a new branch of the repository fullName that starts at
// main, and pushes the branch.
func (g *FakeGitHub) CommitFileOnBranch(fullName, branch, path, content, message string) {
	g.commitFile(fullName, branch, path, content, message, time.Time{})
}

func (g *FakeGitHub) commitFile(fullName, branch, path, content, message string, at time.Time) {
	work := g.t.TempDir()
	g.git(work, "clone", "--branch=main", g.Remote(fullName), ".")
	file := filepath.Join(work, path)
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		g.t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		g.t.Fatal(err)
	}
	g.git(work, "add", path)
	commit := gitCommand(work, "commit", "-m", message)
	if !at.IsZero() {
		date := at.UTC().Format(time.RFC3339)
		commit.Env = append(commit.Env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	}
	if out, err := commit.CombinedOutput(); err != nil {
		g.t.Fatalf("git commit: %v: %s", err, out)
	}
	g.git(work, "push", "origin", "HEAD:refs/heads/"+branch)
}

// PushCommit pushes one new empty commit with message on top of main to branch of the repository fullName, as a
// person does.
func (g *FakeGitHub) PushCommit(fullName, branch, message string) {
	work := g.t.TempDir()
	g.git(work, "clone", "--branch=main", g.Remote(fullName), ".")
	g.git(work, "commit", "--allow-empty", "-m", message)
	g.git(work, "push", "origin", "HEAD:refs/heads/"+branch)
}

// getBranchRef gives the head commit of the branch in the git repository of the repository.
func (g *FakeGitHub) getBranchRef(w http.ResponseWriter, r *http.Request) {
	ref := "refs/heads/" + r.PathValue("branch")
	sha, err := gitCommand(g.Remote(repository(r)), "rev-parse", "--verify", "--quiet", ref).Output()
	if err != nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ref":    ref,
		"object": map[string]string{"type": "commit", "sha": strings.TrimSpace(string(sha))},
	})
}

type branchKey struct {
	repository, branch string
}

// RefuseBranchDeletion makes the deletion of the branch refuse with reason, for example for a rule of the repository.
// An empty reason lifts the refusal.
func (g *FakeGitHub) RefuseBranchDeletion(repository, branch, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.branchDeletionRefusals[branchKey{repository, branch}] = reason
}

// deleteBranchRef deletes the branch in the git repository of the repository. It gives status 422 for a branch that
// does not exist and for a branch set to refuse by RefuseBranchDeletion.
func (g *FakeGitHub) deleteBranchRef(w http.ResponseWriter, r *http.Request) {
	branch := r.PathValue("branch")
	g.mu.Lock()
	defer g.mu.Unlock()
	if reason := g.branchDeletionRefusals[branchKey{repository(r), branch}]; reason != "" {
		message(w, http.StatusUnprocessableEntity, reason)
		return
	}
	remote := g.Remote(repository(r))
	ref := "refs/heads/" + branch
	if err := gitCommand(remote, "rev-parse", "--verify", "--quiet", ref).Run(); err != nil {
		message(w, http.StatusUnprocessableEntity, "Reference does not exist")
		return
	}
	g.git(remote, "update-ref", "-d", ref)
	w.WriteHeader(http.StatusNoContent)
}

// UnlistPullRequests makes the list of the issues not show the pull requests, as GitHub does for a short time after a
// push, or shows them again.
func (g *FakeGitHub) UnlistPullRequests(unlisted bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pullRequestsUnlisted = unlisted
}

// touchPushedPullRequests sets the update time of each pull request of the repository whose head commit is not the
// head at the last call. The caller must hold the lock.
func (g *FakeGitHub) touchPushedPullRequests(repository string) {
	for _, pull := range g.pullRequests {
		if pull.repository != repository {
			continue
		}
		key := issueKey{repository, pull.Number}
		head, _ := gitCommand(g.Remote(repository), "rev-parse", "--verify", "--quiet", pull.Head).Output()
		sha := strings.TrimSpace(string(head))
		if last, listed := g.pushedHeads[key]; listed && last != sha {
			g.issues[key].updatedAt = g.tick()
		}
		g.pushedHeads[key] = sha
	}
}

// SetCheck commits script as an executable .mobius/check on main of the repository fullName.
func (g *FakeGitHub) SetCheck(fullName, script string) {
	work := g.t.TempDir()
	g.git(work, "clone", "--branch=main", g.Remote(fullName), ".")
	check := filepath.Join(work, ".mobius", "check")
	if err := os.MkdirAll(filepath.Dir(check), 0o750); err != nil {
		g.t.Fatal(err)
	}
	if err := os.WriteFile(check, []byte("#!/bin/sh\n"+script+"\n"), 0o600); err != nil {
		g.t.Fatal(err)
	}
	g.git(work, "add", "--chmod=+x", ".mobius/check")
	g.git(work, "commit", "-m", "Add the local check")
	g.git(work, "push", "origin", "HEAD:refs/heads/main")
}

func (g *FakeGitHub) git(dir string, args ...string) {
	Git(g.t, dir, args...)
}

// Git runs git with args in dir, and gives its trimmed stdout. The git config of the system and of the user does not
// apply. The test fails when git fails.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := gitCommand(dir, args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("git %v: %v: %s", args, err, exit.Stderr)
		}
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// gitCommand gives the command of git with args in dir, with the author owner and no git config of the system and
// of the user.
func gitCommand(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", "-c", "user.name=owner", "-c", "user.email=owner@example.com")
	cmd.Args = append(cmd.Args, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	return cmd
}
