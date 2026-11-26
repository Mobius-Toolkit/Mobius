package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
)

// markedProcesses gives the pids of the processes with the mark in their environment.
func markedProcesses(mark string) ([]int, error) {
	files, err := filepath.Glob("/proc/[0-9]*/environ")
	if err != nil {
		return nil, err
	}
	entry := []byte(markEnv + "=" + mark)
	var pids []int
	for _, file := range files {
		// A process of another user, or one that ended, has no readable environment.
		environ, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			continue
		}
		for _, variable := range bytes.Split(environ, []byte{0}) {
			if bytes.Equal(variable, entry) {
				pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(file)))
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids, nil
}
