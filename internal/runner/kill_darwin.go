package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// markedProcesses gives the pids of the processes with the mark in their environment. ps -E writes the environment
// after the command.
func markedProcesses(mark string) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-Eww", "-A", "-o", "pid=,command=")
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var pids []int
	for line := range strings.SplitSeq(string(output), "\n") {
		pid, command, _ := strings.Cut(strings.TrimSpace(line), " ")
		if !strings.Contains(command, " "+markEnv+"="+mark) {
			continue
		}
		if pid, err := strconv.Atoi(pid); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
