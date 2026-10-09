package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// printNiceValue is the .mobius/check of TestCheckRunsWithTheNiceValueRaisedByTen.
func printNiceValue() int {
	output, err := exec.Command("sh", "-c", "ps -o nice= -p $PPID").Output()
	if err != nil {
		return 1
	}
	_, _ = os.Stdout.Write(output)
	return 0
}

func TestCheckRunsWithTheNiceValueRaisedByTen(t *testing.T) {
	worktree := t.TempDir()
	if err := os.Mkdir(filepath.Join(worktree, ".mobius"), 0o750); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(worktree, ".mobius", "check")); err != nil {
		t.Fatal(err)
	}
	// The test can run with a nice value above 0, and the kernel caps the nice value.
	want, err := exec.Command("nice", "-n", "10", "sh", "-c", "ps -o nice= -p $$").Output()
	if err != nil {
		t.Fatal(err)
	}

	got, passed, err := Check(context.Background(), t.TempDir(), worktree, os.Getenv("PATH"), time.Minute)

	if err != nil || !passed {
		t.Fatalf("Check = %q, %v, %v; want a pass", got, passed, err)
	}
	if got != string(want) {
		t.Errorf("nice value of the check = %q, want %q", got, want)
	}
	t.Logf("nice value of the check: %s", got)
}
