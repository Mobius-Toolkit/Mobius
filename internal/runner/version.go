package runner

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// versionTimeout is the time that a program has to give its version.
const versionTimeout = 10 * time.Second

// Version gives the first line of the output of file --version, with path as the PATH of the program.
func Version(ctx context.Context, file, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, file)
	cmd.Args = append(cmd.Args, "--version")
	cmd.Env = append(os.Environ(), "PATH="+path)
	// A program that the timeout kills can leave a child that holds the output open.
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	return strings.TrimSpace(line), nil
}
