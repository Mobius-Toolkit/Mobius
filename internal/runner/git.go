package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// git gives the git command with args in dir. The token goes to git only in the environment of the process, so no
// file and no process list shows it. No hook runs, so no code of the repository gets that environment. The command
// leads its own process group, so the end of ctx also stops the git processes that it started, for example of a fetch.
func git(ctx context.Context, dataDir, dir, token string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-c", "core.hooksPath=/dev/null")
	cmd.Args = append(cmd.Args, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(agentEnv(dataDir), "gitconfig"), "GIT_TERMINAL_PROMPT=0")
	if token != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=AUTHORIZATION: basic "+credentials)
	}
	return cmd
}

// run runs cmd and gives its trimmed stdout. The error has the trimmed stderr.
func run(cmd *exec.Cmd) (string, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func bareDir(dataDir, repository string) string {
	return filepath.Join(dataDir, "repos", repository+".git")
}

// Fetch fetches origin into the bare clone of repository below dataDir, and makes the clone first when it does not
// exist. Two calls for the same repository must not run at the same time.
func Fetch(ctx context.Context, dataDir, repository, cloneURL, token string) error {
	bare := bareDir(dataDir, repository)
	if _, err := os.Stat(bare); errors.Is(err, fs.ErrNotExist) {
		// The clone gets its config in a temporary directory, so a failed start leaves no bare clone with part of its config.
		temporary := strings.TrimSuffix(bare, ".git") + ".new"
		if err := os.RemoveAll(temporary); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(temporary), 0o750); err != nil {
			return err
		}
		if _, err := run(git(ctx, dataDir, dataDir, token, "clone", "--bare", cloneURL, temporary)); err != nil {
			return err
		}
		if _, err := run(git(ctx, dataDir, temporary, "", "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")); err != nil {
			return err
		}
		if err := os.Rename(temporary, bare); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := worktreeConfig(ctx, dataDir, bare); err != nil {
		return err
	}
	_, err := run(git(ctx, dataDir, bare, token, "fetch", "--prune", "origin"))
	return err
}

// worktreeConfig moves core.bare of the bare clone from its shared config to the config of the clone itself. With
// extensions.worktreeConfig, a worktree of the clone then does not read core.bare. The steps can run again after a
// failure.
func worktreeConfig(ctx context.Context, dataDir, bare string) error {
	shared, err := run(git(ctx, dataDir, bare, "", "config", "--file", "config", "--default=", "--get", "core.bare"))
	if err != nil || shared == "" {
		return err
	}
	for _, args := range [][]string{
		{"config", "extensions.worktreeConfig", "true"},
		{"config", "--worktree", "core.bare", "true"},
		{"config", "--file", "config", "--unset", "core.bare"},
	} {
		if _, err := run(git(ctx, dataDir, bare, "", args...)); err != nil {
			return err
		}
	}
	return nil
}

// Show gives the text of path on origin/branch in the bare clone of repository below dataDir, as the last Fetch
// got it. It gives false when origin/branch has no such file.
func Show(ctx context.Context, dataDir, repository, branch, path string) (string, bool, error) {
	bare := bareDir(dataDir, repository)
	name := "origin/" + branch
	found, err := run(git(ctx, dataDir, bare, "", "ls-tree", "--name-only", name, "--", path))
	if err != nil || found == "" {
		return "", false, err
	}
	text, err := run(git(ctx, dataDir, bare, "", "show", name+":"+path))
	return text, err == nil, err
}

// TaskDir gives the worktree of the task of the issue number of repository below dataDir.
func TaskDir(dataDir, repository string, number int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "task-"+strconv.FormatInt(number, 10))
}

// ResearchDir gives the worktree of the Researcher session id of repository below dataDir.
func ResearchDir(dataDir, repository string, id int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "research-"+strconv.FormatInt(id, 10))
}

// ReviewDir gives the worktree of the Reviewer session id of repository below dataDir.
func ReviewDir(dataDir, repository string, id int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "review-"+strconv.FormatInt(id, 10))
}

// JudgeDir gives the worktree of the Judge session id of repository below dataDir.
func JudgeDir(dataDir, repository string, id int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "judge-"+strconv.FormatInt(id, 10))
}

// succeeds tells if cmd exits with status 0. Another exit status is no error.
func succeeds(cmd *exec.Cmd) (bool, error) {
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return err == nil, err
}

// hasRef tells if the ref name exists in dir.
func hasRef(ctx context.Context, dataDir, dir, name string) (bool, error) {
	return succeeds(git(ctx, dataDir, dir, "", "show-ref", "--verify", "--quiet", name))
}

// FreeBranch gives the first branch of mobius/<number>, mobius/<number>-2, ... that the bare clone of repository
// does not have, locally and on origin.
func FreeBranch(ctx context.Context, dataDir, repository string, number int64) (string, error) {
	bare := bareDir(dataDir, repository)
	branch := fmt.Sprintf("mobius/%d", number)
	for next := 2; ; next++ {
		local, err := hasRef(ctx, dataDir, bare, "refs/heads/"+branch)
		if err != nil {
			return "", err
		}
		remote, err := hasRef(ctx, dataDir, bare, "refs/remotes/origin/"+branch)
		if err != nil {
			return "", err
		}
		if !local && !remote {
			return branch, nil
		}
		branch = fmt.Sprintf("mobius/%d-%d", number, next)
	}
}

// AddWorktree makes the worktree dir of the bare clone of repository on branch, with the commit author name and
// email. A branch that the clone does not have starts at start. A worktree at dir goes away first.
func AddWorktree(ctx context.Context, dataDir, repository, dir, branch, start, name, email string) error {
	bare := bareDir(dataDir, repository)
	if _, err := os.Stat(dir); err == nil {
		if _, err := run(git(ctx, dataDir, bare, "", "worktree", "remove", "--force", dir)); err != nil {
			return err
		}
	}
	local, err := hasRef(ctx, dataDir, bare, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	args := []string{"worktree", "add", dir, branch}
	if !local {
		args = []string{"worktree", "add", "--no-track", "-b", branch, dir, start}
	}
	if _, err := run(git(ctx, dataDir, bare, "", args...)); err != nil {
		return err
	}
	if _, err := run(git(ctx, dataDir, dir, "", "config", "--worktree", "user.name", name)); err != nil {
		return err
	}
	_, err = run(git(ctx, dataDir, dir, "", "config", "--worktree", "user.email", email))
	return err
}

// AddDetachedWorktree makes the worktree dir of the bare clone of repository, detached at commit.
func AddDetachedWorktree(ctx context.Context, dataDir, repository, dir, commit string) error {
	_, err := run(git(ctx, dataDir, bareDir(dataDir, repository), "", "worktree", "add", "--detach", dir, commit))
	return err
}

// RemoveWorktree removes the worktree dir of the bare clone of repository, with its changes. It also removes a worktree
// that a killed "git worktree add" left: git cannot remove it, and "git worktree prune" keeps its record because the
// record is locked. Thus the fallback deletes the record that points to dir, so a new worktree can use dir again.
func RemoveWorktree(ctx context.Context, dataDir, repository, dir string) error {
	bare := bareDir(dataDir, repository)
	if _, err := run(git(ctx, dataDir, bare, "", "worktree", "remove", "--force", "--force", dir)); err == nil {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		return err
	}
	gitdirs, err := filepath.Glob(filepath.Join(bare, "worktrees", "*", "gitdir"))
	if err != nil {
		return err
	}
	for _, gitdir := range gitdirs {
		content, err := os.ReadFile(filepath.Clean(gitdir))
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(content)) == filepath.Join(parent, filepath.Base(dir), ".git") {
			if err := os.RemoveAll(filepath.Dir(gitdir)); err != nil {
				return err
			}
		}
	}
	_, err = run(git(ctx, dataDir, bare, "", "worktree", "prune"))
	return err
}

// Prune removes the records of the worktrees whose directories are gone from the bare clone of repository. A
// repository with no bare clone is no error.
func Prune(ctx context.Context, dataDir, repository string) error {
	bare := bareDir(dataDir, repository)
	if _, err := os.Stat(bare); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	_, err := run(git(ctx, dataDir, bare, "", "worktree", "prune"))
	return err
}

// Pull merges origin/branch of the last Fetch into the worktree, also when the two branches diverged.
func Pull(ctx context.Context, dataDir, worktree, branch string) error {
	remote := "refs/remotes/origin/" + branch
	found, err := hasRef(ctx, dataDir, worktree, remote)
	if err != nil || !found {
		return err
	}
	_, err = run(git(ctx, dataDir, worktree, "", "merge", "--no-edit", remote))
	return err
}

// Push pushes HEAD of the worktree to branch on origin with token, and gives the pushed commit.
func Push(ctx context.Context, dataDir, worktree, token, branch string) (string, error) {
	if _, err := run(git(ctx, dataDir, worktree, token, "push", "origin", "HEAD:refs/heads/"+branch)); err != nil {
		return "", err
	}
	return RevParse(ctx, dataDir, worktree, "HEAD")
}

// MergeBase gives the best common ancestor of the commits one and other in the bare clone of repository.
func MergeBase(ctx context.Context, dataDir, repository, one, other string) (string, error) {
	return run(git(ctx, dataDir, bareDir(dataDir, repository), "", "merge-base", one, other))
}

// RevParse gives the commit of name in the worktree.
func RevParse(ctx context.Context, dataDir, worktree, name string) (string, error) {
	return run(git(ctx, dataDir, worktree, "", "rev-parse", name))
}

// HeadContains tells if HEAD of the worktree contains commit.
func HeadContains(ctx context.Context, dataDir, worktree, commit string) (bool, error) {
	return succeeds(git(ctx, dataDir, worktree, "", "merge-base", "--is-ancestor", commit, "HEAD"))
}
