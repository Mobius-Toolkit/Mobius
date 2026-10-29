package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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

// The npm packages of the critical tools.
const (
	adapterPackage = "@agentclientprotocol/claude-agent-acp"
	cliPackage     = "@anthropic-ai/claude-code"
)

// NPMRegistry is the npm registry that gives the newest version of a package.
var NPMRegistry = "https://registry.npmjs.org"

// newestLife is the time that Mobius keeps the newest version of a package, also when the registry gave none.
const newestLife = time.Hour

// registryTimeout is the time that the registry has to answer.
const registryTimeout = 5 * time.Second

var versionNumber = regexp.MustCompile(`\d+\.\d+\.\d+`)

// newestVersion is the newest version of an npm package that Mobius read at read. It is empty when the registry gave
// none.
type newestVersion struct {
	version string
	read    time.Time
}

// ToolCheck is the path and the version of a program that Mobius runs.
type ToolCheck struct {
	Name string
	// Path is empty for a program that Mobius cannot find, and for the Claude Code CLI that is built in the adapter.
	Path string
	// Version is empty when Status is not empty.
	Version string
	// Status is NotFound, NoVersion or empty.
	Status string
	// Newest is the newest version of the npm package of a critical tool. It is empty when the registry gave none, or
	// when Version has no X.Y.Z.
	Newest string
	// Outdated tells that the X.Y.Z of Version is older than Newest.
	Outdated bool
}

// Tools gives the path and the version of each program that Mobius runs for its agents and its work, in the PATH
// of the agent sessions.
func (e *Engine) Tools(ctx context.Context) []ToolCheck {
	path := runner.AgentPath(e.config.DataDir, e.agents.Path)
	harnesses := map[config.Harness]bool{}
	roles := e.mergedRoles()
	for _, b := range roles.Bindings() {
		harnesses[b.Binding.Harness] = true
	}
	var checks []ToolCheck
	for _, harness := range config.Harnesses {
		if !harnesses[harness] {
			continue
		}
		program := runner.Program(harness)
		adapter := checkVersion(ctx, program, runner.Find(program, path), path)
		if harness == config.ClaudeCode {
			e.compareNewest(ctx, &adapter, adapterPackage)
			checks = append(checks, adapter, e.claudeCodeCLI(ctx, adapter.Path, path))
		} else {
			checks = append(checks, adapter)
		}
	}
	for _, program := range []string{"git", "curl", "tar"} {
		checks = append(checks, checkVersion(ctx, program, runner.Find(program, path), path))
	}
	return checks
}

// claudeCodeCLI checks the Claude Code CLI that the adapter at adapterPath runs.
func (e *Engine) claudeCodeCLI(ctx context.Context, adapterPath, path string) ToolCheck {
	const name = "Claude Code CLI"
	if executable := os.Getenv(claudeCodeEnv); executable != "" {
		dir, program := filepath.Split(executable)
		if dir == "" {
			dir = path
		}
		check := checkVersion(ctx, name, runner.Find(program, dir), path)
		e.compareNewest(ctx, &check, cliPackage)
		return check
	}
	check := ToolCheck{Name: name, Version: builtInClaudeCodeVersion(adapterPath)}
	if check.Version == "" {
		check.Status = NoVersion
	}
	return check
}

// builtInClaudeCodeVersion reads the version of the Claude Code CLI in the SDK that the adapter at adapterPath loads.
// It looks in the node_modules of each parent directory, as Node does. It gives "" when it finds no such version.
func builtInClaudeCodeVersion(adapterPath string) string {
	file, err := filepath.EvalSymlinks(adapterPath)
	if err != nil {
		return ""
	}
	for dir := filepath.Dir(file); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		sdk, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-agent-sdk", "package.json")))
		if err != nil {
			continue
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

// compareNewest sets Newest and Outdated of check from the newest version of the npm package. It does nothing when
// the registry gave no version, or when the Version of check has no X.Y.Z.
func (e *Engine) compareNewest(ctx context.Context, check *ToolCheck, npmPackage string) {
	installed := versionNumber.FindString(check.Version)
	if installed == "" {
		return
	}
	newest := versionNumber.FindString(e.newestVersion(ctx, npmPackage))
	if newest == "" {
		return
	}
	check.Newest = newest
	check.Outdated = olderThan(installed, newest)
}

// olderThan tells if the X.Y.Z version a is older than the X.Y.Z version b. It compares the numbers.
func olderThan(a, b string) bool {
	x, y := versionNumbers(a), versionNumbers(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

func versionNumbers(version string) [3]int {
	var numbers [3]int
	for i, text := range strings.SplitN(version, ".", 3) {
		numbers[i], _ = strconv.Atoi(text)
	}
	return numbers
}

// newestVersion gives the newest version of the npm package, or "" when the registry gives none. It reads the
// registry at most one time in newestLife for each package.
func (e *Engine) newestVersion(ctx context.Context, npmPackage string) string {
	e.newestMu.Lock()
	defer e.newestMu.Unlock()
	if known, ok := e.newest[npmPackage]; ok && e.timeNow().Sub(known.read) < newestLife {
		return known.version
	}
	version := readNewestVersion(ctx, npmPackage)
	if ctx.Err() != nil {
		return ""
	}
	e.newest[npmPackage] = newestVersion{version: version, read: e.timeNow()}
	return version
}

func readNewestVersion(ctx context.Context, npmPackage string) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, NPMRegistry+"/"+npmPackage+"/latest", nil)
	if err != nil {
		return ""
	}
	response, err := (&http.Client{Timeout: registryTimeout}).Do(request)
	if err != nil {
		return ""
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var latest struct {
		Version string `json:"version"`
	}
	if json.NewDecoder(response.Body).Decode(&latest) != nil {
		return ""
	}
	return latest.Version
}
