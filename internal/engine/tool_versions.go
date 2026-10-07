package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// The status values of a tool check.
const (
	// NotFound is a program that Mobius cannot find.
	NotFound = "not-found"
	// NoVersion is a program that gives no version.
	NoVersion = "no-version"
)

// claudeCodeEnv is the environment variable that gives the Claude Code CLI to claude-agent-acp.
const claudeCodeEnv = "CLAUDE_CODE_EXECUTABLE"

// ToolCheck is the path and the version of a program that Mobius runs.
type ToolCheck struct {
	Name string
	// Path is empty for a program that Mobius cannot find, and for the Claude Code CLI that is built in the adapter.
	Path string
	// Version is empty when Status is not empty.
	Version string
	// Status is NotFound, NoVersion or empty.
	Status string
}

// Tools gives the path and the version of each program that Mobius runs for its agents and its work, in the PATH
// of the agent sessions.
func (e *Engine) Tools(ctx context.Context) []ToolCheck {
	path := runner.AgentPath(e.config.DataDir, e.agents.Path)
	harnesses := map[config.Harness]bool{}
	for _, b := range e.config.Roles.Bindings() {
		harnesses[b.Binding.Harness] = true
	}
	var checks []ToolCheck
	for _, harness := range config.Harnesses {
		if !harnesses[harness] {
			continue
		}
		program := runner.Program(harness)
		adapter := checkVersion(ctx, program, runner.Find(program, path), path)
		checks = append(checks, adapter)
		if harness == config.ClaudeCode {
			checks = append(checks, claudeCodeCLI(ctx, adapter.Path, path))
		}
	}
	for _, program := range []string{"git", "curl", "tar"} {
		checks = append(checks, checkVersion(ctx, program, runner.Find(program, path), path))
	}
	return checks
}

// claudeCodeCLI checks the Claude Code CLI that the adapter at adapterPath runs.
func claudeCodeCLI(ctx context.Context, adapterPath, path string) ToolCheck {
	const name = "Claude Code CLI"
	if executable := os.Getenv(claudeCodeEnv); executable != "" {
		dir, program := filepath.Split(executable)
		if dir == "" {
			dir = path
		}
		return checkVersion(ctx, name, runner.Find(program, dir), path)
	}
	check := ToolCheck{Name: name, Version: builtInClaudeCodeVersion(adapterPath)}
	if check.Version == "" {
		check.Status = NoVersion
	}
	return check
}

// builtInClaudeCodeVersion reads the version of the Claude Code CLI in the npm package of the adapter at adapterPath.
// It gives "" when the package has no such version.
func builtInClaudeCodeVersion(adapterPath string) string {
	file, err := filepath.EvalSymlinks(adapterPath)
	if err != nil {
		return ""
	}
	for dir := filepath.Dir(file); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
			continue
		}
		sdk, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-agent-sdk", "package.json")))
		if err != nil {
			return ""
		}
		var manifest struct {
			ClaudeCodeVersion string `json:"claudeCodeVersion"`
		}
		if json.Unmarshal(sdk, &manifest) != nil {
			return ""
		}
		return manifest.ClaudeCodeVersion
	}
	return ""
}

// checkVersion gives the version of file. An empty file is a program that is not found.
func checkVersion(ctx context.Context, name, file, path string) ToolCheck {
	check := ToolCheck{Name: name, Path: file}
	if file == "" {
		check.Status = NotFound
		return check
	}
	version, err := runner.Version(ctx, file, path)
	if err != nil || version == "" {
		check.Status = NoVersion
		return check
	}
	check.Version = version
	return check
}
