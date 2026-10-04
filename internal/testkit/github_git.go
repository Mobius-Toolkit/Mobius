package testkit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Remote gives the bare git repository of the repository fullName. Its file URL is the clone_url of the repository.
func (g *FakeGitHub) Remote(fullName string) string {
	return filepath.Join(g.remotes, fullName+".git")
}

// addRemote makes the bare git repository of fullName with one commit on main.
func (g *FakeGitHub) addRemote(fullName string) {
	remote := g.Remote(fullName)
	if err := os.MkdirAll(remote, 0o750); err != nil {
		g.t.Fatal(err)
	}
	g.git(remote, "init", "--bare", "--initial-branch=main")
	work := g.t.TempDir()
	g.git(work, "init", "--initial-branch=main")
	g.git(work, "commit", "--allow-empty", "-m", "Start")
	g.git(work, "push", remote, "main:refs/heads/main")
}

// CommitFile commits the file path with content on main of the repository fullName.
func (g *FakeGitHub) CommitFile(fullName, path, content, message string) {
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
	g.git(work, "commit", "-m", message)
	g.git(work, "push", "origin", "HEAD:refs/heads/main")
}

// PushCommit pushes one new empty commit with message on top of main to branch of the repository fullName, as a
// person does.
func (g *FakeGitHub) PushCommit(fullName, branch, message string) {
	work := g.t.TempDir()
	g.git(work, "clone", "--branch=main", g.Remote(fullName), ".")
	g.git(work, "commit", "--allow-empty", "-m", message)
	g.git(work, "push", "origin", "HEAD:refs/heads/"+branch)
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
