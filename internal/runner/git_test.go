package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
