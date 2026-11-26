package runner

import (
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// markEnv is the environment variable that holds the mark of a session. Each process that the session starts inherits
// it, also a process in a new process group or session, and also after its parent ended. The mark is random, so a
// process of another session or of Mobius never has it.
const markEnv = "MOBIUS_PROCESS_MARK"

// termWait is the time that a marked process has to end after SIGTERM, for example to remove its temporary directory.
const termWait = 5 * time.Second

func addMark(cmd *exec.Cmd) {
	random := make([]byte, 16)
	// Reading random bytes does not fail on a supported system.
	_, _ = rand.Read(random)
	cmd.Env = append(cmd.Env, markEnv+"="+hex.EncodeToString(random))
}

func markOf(cmd *exec.Cmd) string {
	for _, entry := range cmd.Env {
		if mark, ok := strings.CutPrefix(entry, markEnv+"="); ok {
			return mark
		}
	}
	return ""
}

// stopMarked stops each process with the mark in its environment. It sends SIGTERM, and SIGKILL to each process that
// is still there after termWait. On macOS, ps does not show the environment of a system program, so such a program
// stays.
func stopMarked(mark string) error {
	pids, err := markedProcesses(mark)
	if err != nil {
		return err
	}
	sendAll(pids, syscall.SIGTERM)
	for deadline := time.Now().Add(termWait); len(pids) > 0 && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		if pids, err = markedProcesses(mark); err != nil {
			return err
		}
	}
	sendAll(pids, syscall.SIGKILL)
	return nil
}

func sendAll(pids []int, sig syscall.Signal) {
	for _, pid := range pids {
		_ = syscall.Kill(pid, sig)
	}
}
