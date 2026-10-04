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
	"strings"
)

// git gives the git command with args in dir. The token goes to git only in the environment of the process, so no
// file and no process list shows it. No hook runs, so no code of the repository gets that environment.
func git(ctx context.Context, dataDir, dir, token string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-c", "core.hooksPath=/dev/null")
	cmd.Args = append(cmd.Args, args...)
	cmd.Dir = dir
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
	_, err := run(git(ctx, dataDir, bare, token, "fetch", "--prune", "origin"))
	return err
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
