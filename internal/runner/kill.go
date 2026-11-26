package runner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killTree kills the process group of pid and each descendant of pid. A descendant can lead its own process group or
// session, as the web server of Playwright does, and the kill of the group does not reach it. killTree reads the
// descendants before the kill, because after the kill the survivors have a new parent.
func killTree(pid int) error {
	found, err := descendants(pid)
	killErr := syscall.Kill(-pid, syscall.SIGKILL)
	for _, descendant := range found {
		_ = syscall.Kill(descendant, syscall.SIGKILL)
	}
	return errors.Join(err, killErr)
}

func descendants(pid int) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=")
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	children := map[int][]int{}
	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		child, childErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if childErr == nil && parentErr == nil {
			children[parent] = append(children[parent], child)
		}
	}
	var found []int
	for queue := []int{pid}; len(queue) > 0; queue = queue[1:] {
		found = append(found, children[queue[0]]...)
		queue = append(queue, children[queue[0]]...)
	}
	return found, nil
}
