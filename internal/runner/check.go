package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Check runs .mobius/check of the worktree in the agent environment with the Harness commands on path. It gives the
// output, with stdout and stderr in the order of the writes, and true when the check passes. A worktree with no
// .mobius/check passes. A check that does not end in timeout fails, and its output ends with a line about the timeout.
// When ctx ends, Check stops the check and gives the error of ctx.
func Check(ctx context.Context, dataDir, worktree, path string, timeout time.Duration) (string, bool, error) {
	if _, err := os.Stat(filepath.Join(worktree, ".mobius", "check")); errors.Is(err, fs.ErrNotExist) {
		return "", true, nil
	}
	cmd := command(context.Background(), "/bin/sh", worktree, dataDir, path, "")
	cmd.Args = append(cmd.Args, "-c", "exec ./.mobius/check 2>&1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	if err := cmd.Start(); err != nil {
		return "", false, fmt.Errorf(".mobius/check: %w", err)
	}
	var output []byte
	read := make(chan error, 1)
	go func() {
		var err error
		output, err = io.ReadAll(stdout)
		// Check does not call cmd.Wait, so the pipe needs its own close.
		read <- errors.Join(err, stdout.Close())
	}()
	exited := make(chan *os.ProcessState, 1)
	go func() {
		// A failed wait leaves no exit status, and the check then fails.
		state, _ := cmd.Process.Wait()
		exited <- state
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var state *os.ProcessState
	select {
	case state = <-exited:
	case <-timer.C:
	case <-ctx.Done():
	}
	// The check leads its own process group, so the kill also ends each process that it started. Until then, such a
	// process can hold the pipe open.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if state == nil {
		<-exited
	}
	if err := <-read; err != nil {
		return "", false, fmt.Errorf(".mobius/check: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	log := string(output)
	switch {
	case state == nil:
		log += fmt.Sprintf("\n.mobius/check did not end in %s.\n", timeout)
	case state.Success():
		return log, true, nil
	}
	return log, false, nil
}

// FreeSpace gives the free space of the file system of dataDir in bytes. The program df comes from path.
func FreeSpace(ctx context.Context, dataDir, path string) (uint64, error) {
	df := find("df", path)
	if df == "" {
		return 0, errors.New("df is not on PATH")
	}
	cmd := command(ctx, df, dataDir, dataDir, path, "")
	cmd.Args = append(cmd.Args, "-Pk", dataDir)
	output, err := run(cmd)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(output, "\n")
	if len(lines) < 2 || len(strings.Fields(lines[1])) < 4 {
		return 0, fmt.Errorf("df -Pk gave no free space: %s", output)
	}
	kibibytes, err := strconv.ParseUint(strings.Fields(lines[1])[3], 10, 64)
	return kibibytes * 1024, err
}
