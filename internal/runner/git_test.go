package runner

import (
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", "-c", "user.name=Test", "-c", "user.email=test@example.com")
	cmd.Args = append(cmd.Args, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveWorktreeRemovesTheWorktreeThatAKilledAddLeft(t *testing.T) {
	tests := []struct {
		name  string
		leave func(t *testing.T, bare, dir string)
	}{
		{"a locked worktree", func(t *testing.T, bare, dir string) {
			gitIn(t, bare, "worktree", "add", "--lock", "--reason", "initializing", "--no-checkout", dir, "main")
		}},
		{"a locked record with no commondir", func(t *testing.T, bare, dir string) {
			gitIn(t, bare, "worktree", "add", "--lock", "--reason", "initializing", "--detach", dir, "main")
			remove(t, filepath.Join(bare, "worktrees", filepath.Base(dir), "commondir"))
		}},
		{"a locked worktree with no .git file", func(t *testing.T, bare, dir string) {
			gitIn(t, bare, "worktree", "add", "--lock", "--reason", "initializing", "--detach", dir, "main")
			remove(t, filepath.Join(dir, ".git"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			source := filepath.Join(dataDir, "source")
			if err := os.Mkdir(source, 0o750); err != nil {
				t.Fatal(err)
			}
			gitIn(t, source, "init", "--initial-branch=main")
			gitIn(t, source, "commit", "--allow-empty", "-m", "first")
			bare := bareDir(dataDir, "acme/shop")
			gitIn(t, dataDir, "clone", "--bare", source, bare)
			dir := filepath.Join(dataDir, "worktree")
			tt.leave(t, bare, dir)

			if err := RemoveWorktree(context.Background(), dataDir, "acme/shop", dir); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("stat dir = %v, want not exist", err)
			}
			records, err := os.ReadDir(filepath.Join(bare, "worktrees"))
			if err == nil && len(records) != 0 {
				t.Errorf("worktree records = %v", records)
			}
			if err := AddDetachedWorktree(context.Background(), dataDir, "acme/shop", dir, "main"); err != nil {
				t.Errorf("add the worktree again: %v", err)
			}
		})
	}
}

func shortStall(t *testing.T) {
	t.Helper()
	limit, seconds := LowSpeedLimit, LowSpeedTime
	LowSpeedLimit, LowSpeedTime = 1000, 1
	t.Cleanup(func() { LowSpeedLimit, LowSpeedTime = limit, seconds })
}

// stalledServer gives the URL of a repository whose server reads each request and then sends nothing.
func stalledServer(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server.URL + "/acme/shop.git"
}

func TestFetchFailsAfterTheStallLimitOfAStalledClone(t *testing.T) {
	shortStall(t)
	dataDir := t.TempDir()

	start := time.Now()
	err := Fetch(context.Background(), dataDir, "acme/shop", stalledServer(t), "token")

	if err == nil || !strings.Contains(err.Error(), "too slow") {
		t.Fatalf("err = %v, want the stall error of git", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("elapsed = %v", elapsed)
	}
	if _, err := os.Stat(bareDir(dataDir, "acme/shop")); !os.IsNotExist(err) {
		t.Errorf("stat bare = %v, want not exist", err)
	}
}

func TestFetchFailsAfterTheStallLimitOfAStalledFetchAndLeavesNoLock(t *testing.T) {
	dataDir := t.TempDir()
	source := filepath.Join(dataDir, "source")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	gitIn(t, source, "init", "--initial-branch=main")
	gitIn(t, source, "commit", "--allow-empty", "-m", "first")
	if err := Fetch(context.Background(), dataDir, "acme/shop", source, ""); err != nil {
		t.Fatal(err)
	}
	bare := bareDir(dataDir, "acme/shop")
	gitIn(t, bare, "remote", "set-url", "origin", stalledServer(t))
	shortStall(t)

	start := time.Now()
	err := Fetch(context.Background(), dataDir, "acme/shop", "", "token")

	if err == nil || !strings.Contains(err.Error(), "too slow") {
		t.Fatalf("err = %v, want the stall error of git", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("elapsed = %v", elapsed)
	}
	err = filepath.WalkDir(bare, func(path string, _ fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(path, ".lock") {
			t.Errorf("lock file %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGitSendsTheTokenHeaderAndTheStallLimit(t *testing.T) {
	cmd := git(context.Background(), t.TempDir(), t.TempDir(), "secret", "fetch")

	want := []string{
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=http.lowSpeedLimit", "GIT_CONFIG_VALUE_0=" + strconv.Itoa(LowSpeedLimit),
		"GIT_CONFIG_KEY_1=http.lowSpeedTime", "GIT_CONFIG_VALUE_1=" + strconv.Itoa(LowSpeedTime),
		"GIT_CONFIG_KEY_2=http.extraHeader",
		"GIT_CONFIG_VALUE_2=AUTHORIZATION: basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:secret")),
	}
	for _, entry := range want {
		if !slices.Contains(cmd.Env, entry) {
			t.Errorf("env has no %q", entry)
		}
	}
	if without := git(context.Background(), t.TempDir(), t.TempDir(), "", "fetch"); !slices.Contains(without.Env, "GIT_CONFIG_COUNT=2") {
		t.Errorf("env with no token = %q", without.Env)
	}
}

func TestFetchSendsTheTokenHeader(t *testing.T) {
	var header atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header.Store(r.Header.Get("Authorization"))
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	if err := Fetch(context.Background(), t.TempDir(), "acme/shop", server.URL+"/acme/shop.git", "secret"); err == nil {
		t.Fatal("fetch from a missing repository succeeded")
	}

	want := "basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:secret"))
	if got, _ := header.Load().(string); !strings.EqualFold(got, want) {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

func TestPullAbortsAMergeWithConflicts(t *testing.T) {
	dataDir := t.TempDir()
	source := filepath.Join(dataDir, "source")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(dir, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, source, "init", "--initial-branch=main")
	write(source, "base\n")
	gitIn(t, source, "add", ".")
	gitIn(t, source, "commit", "-m", "first")
	gitIn(t, dataDir, "clone", "--bare", source, bareDir(dataDir, "acme/shop"))
	worktree := filepath.Join(dataDir, "worktree")
	if err := AddDetachedWorktree(context.Background(), dataDir, "acme/shop", worktree, "main"); err != nil {
		t.Fatal(err)
	}
	gitIn(t, worktree, "checkout", "-b", "pushed")
	write(worktree, "pushed\n")
	gitIn(t, worktree, "commit", "-am", "pushed")
	gitIn(t, worktree, "update-ref", "refs/remotes/origin/feature", "HEAD")
	gitIn(t, worktree, "checkout", "-b", "feature", "main")
	write(worktree, "rewritten\n")
	gitIn(t, worktree, "commit", "-am", "rewritten")

	err := Pull(context.Background(), dataDir, worktree, "feature")

	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("Pull = %v, want ErrMergeConflict", err)
	}
	if status, err := run(git(context.Background(), dataDir, worktree, "", "status", "--porcelain")); err != nil || status != "" {
		t.Errorf("status = %q, %v, want clean", status, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "repos", "acme", "shop.git", "worktrees", "worktree", "MERGE_HEAD")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat MERGE_HEAD = %v, want not exist", err)
	}
}
