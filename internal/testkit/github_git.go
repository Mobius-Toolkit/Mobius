package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
)

// remote gives the bare git repository of the repository fullName. It is the clone_url of the repository.
func (g *FakeGitHub) remote(fullName string) string {
	return filepath.Join(g.remotes, fullName+".git")
}

// addRemote makes the bare git repository of fullName with one commit on main.
func (g *FakeGitHub) addRemote(fullName string) {
	remote := g.remote(fullName)
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
	g.git(work, "clone", "--branch=main", g.remote(fullName), ".")
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

// git runs git with args in dir. The git config of the system and of the user does not apply.
func (g *FakeGitHub) git(dir string, args ...string) {
	cmd := exec.Command("git", "-c", "user.name=owner", "-c", "user.email=owner@example.com")
	cmd.Args = append(cmd.Args, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		g.t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
