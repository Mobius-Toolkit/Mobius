package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"

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
	dshPackage     = "@deepseek-ai/dsh"
)

// NPMRegistry is the npm registry that gives the newest version of a package.
var NPMRegistry = "https://registry.npmjs.org"

// newestLife is the time that Mobius keeps the newest version of a package, also when the registry gave none.
const newestLife = time.Hour

// registryTimeout is the time that the registry has to answer.
const registryTimeout = 5 * time.Second

// upgradeTimeout is the time that an npm upgrade of a Harness package has.
const upgradeTimeout = 10 * time.Minute

// upgradeOutput is the longest text of a failed npm run that Mobius gives back.
const upgradeOutput = 4000

var versionText = regexp.MustCompile(`\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?`)

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
	// when Version has no version.
	Newest string
	// Outdated tells that Version has a lower Semantic Versioning 2.0.0 precedence than Newest.
	Outdated bool
	// Required tells that a role uses the Harness of the row, or that the program is git.
	Required bool
	// Package is the npm package of the row. It is empty for a program that is not in the node_modules of a Node
	// install, and for each program that Mobius does not upgrade.
	Package string
}

// Tools gives the path and the version of each program that Mobius runs for its agents and its work, in the PATH
// of the agent sessions.
func (e *Engine) Tools(ctx context.Context) []ToolCheck {
	path := runner.AgentPath(e.config.DataDir, e.agents.Path)
	used := map[config.Harness]bool{}
	roles := e.mergedRoles()
	for _, b := range roles.Bindings() {
		used[b.Binding.Harness] = true
	}
	var checks []ToolCheck
	for _, harness := range config.Harnesses {
		program := runner.Program(harness)
		check := checkVersion(ctx, program, runner.Find(program, path), path)
		check.Required = used[harness]
		if harness == config.ClaudeCode {
			e.compareNewest(ctx, &check, adapterPackage)
			check.Package = npmPackageOf(check.Path, adapterPackage)
			cli := e.claudeCodeCLI(ctx, check.Path, path)
			cli.Required = used[harness]
			checks = append(checks, check, cli)
			continue
		}
		if harness == config.DSH {
			e.compareNewest(ctx, &check, dshPackage)
			check.Package = npmPackageOf(check.Path, dshPackage)
		}
		checks = append(checks, check)
	}
	git := checkVersion(ctx, "git", runner.Find("git", path), path)
	git.Required = true
	return append(checks, git)
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
		check.Package = npmPackageOf(check.Path, cliPackage)
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
// the registry gave no version, or when the Version of check has no version.
func (e *Engine) compareNewest(ctx context.Context, check *ToolCheck, npmPackage string) {
	installed := versionText.FindString(check.Version)
	if installed == "" {
		return
	}
	newest := versionText.FindString(e.newestVersion(ctx, npmPackage))
	if newest == "" {
		return
	}
	check.Newest = newest
	check.Outdated = olderThan(installed, newest)
}

// olderThan tells if the version a has a lower Semantic Versioning 2.0.0 precedence than the version b.
func olderThan(a, b string) bool {
	return semver.Compare("v"+a, "v"+b) < 0
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

// npmPackageOf gives npmPackage when the real path of the program at programPath is inside
// <prefix>/lib/node_modules/npmPackage and <prefix>/bin/npm is a file. It gives "" for each other program.
func npmPackageOf(programPath, npmPackage string) string {
	prefix, name := nodeInstall(programPath)
	if prefix == "" || name != npmPackage {
		return ""
	}
	return name
}

// nodeInstall gives the prefix of the Node install and the npm package of the program at programPath, when the real
// path of the program is inside <prefix>/lib/node_modules/<package> and <prefix>/bin/npm is a file.
func nodeInstall(programPath string) (prefix, npmPackage string) {
	if programPath == "" {
		return "", ""
	}
	resolved, err := filepath.EvalSymlinks(programPath)
	if err != nil {
		return "", ""
	}
	parts := strings.Split(filepath.ToSlash(resolved), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] != "lib" || parts[i+1] != "node_modules" {
			continue
		}
		rest := parts[i+2:]
		name := rest[0]
		if strings.HasPrefix(name, "@") {
			if len(rest) < 2 || rest[1] == "" {
				return "", ""
			}
			name += "/" + rest[1]
		}
		prefix := strings.Join(parts[:i], "/")
		if prefix == "" {
			return "", ""
		}
		info, err := os.Stat(filepath.Join(prefix, "bin", "npm"))
		if err != nil || info.IsDir() {
			return "", ""
		}
		return prefix, name
	}
	return "", ""
}

// UpgradeTool installs the newest version of the npm package of a tool, and gives the new tool list. It refuses a
// package that no row has, a row with no newest version, and a package whose upgrade runs now.
func (e *Engine) UpgradeTool(ctx context.Context, npmPackage string) ([]ToolCheck, error) {
	checks := e.Tools(ctx)
	index := slices.IndexFunc(checks, func(check ToolCheck) bool { return check.Package == npmPackage })
	if index < 0 {
		return nil, refuse("Mobius does not upgrade the package \"%s\".", npmPackage)
	}
	check := checks[index]
	if check.Newest == "" {
		return nil, refuse("Mobius has no newest version of %s.", check.Name)
	}
	prefix, _ := nodeInstall(check.Path)
	if prefix == "" {
		return nil, refuse("Mobius cannot find the Node install of %s.", check.Name)
	}
	if !e.beginUpgrade(npmPackage) {
		return nil, refuse("An upgrade of %s runs now.", check.Name)
	}
	defer e.endUpgrade(npmPackage)
	output, err := runNPMUpgrade(ctx, filepath.Join(prefix, "bin", "npm"), prefix, npmPackage, check.Newest)
	if err != nil {
		return nil, refuse("The upgrade of %s failed: %v\n%s", check.Name, err, tail(string(output), upgradeOutput))
	}
	for _, organization := range e.github.Organizations() {
		e.addRecord(ctx, ChatKey{Organization: organization}, fmt.Sprintf("You upgraded %s to %s.", check.Name, check.Newest))
	}
	return e.Tools(ctx), nil
}

// beginUpgrade records that the upgrade of npmPackage starts. It gives false when an upgrade of the package runs.
func (e *Engine) beginUpgrade(npmPackage string) bool {
	e.upgradesMu.Lock()
	defer e.upgradesMu.Unlock()
	if e.upgrades[npmPackage] {
		return false
	}
	e.upgrades[npmPackage] = true
	return true
}

// endUpgrade records that the upgrade of npmPackage ends.
func (e *Engine) endUpgrade(npmPackage string) {
	e.upgradesMu.Lock()
	defer e.upgradesMu.Unlock()
	delete(e.upgrades, npmPackage)
}

// runNPMUpgrade runs npm to install npmPackage@version into the Node install at prefix. The PATH of the run starts
// with the bin directory of the Node install, so the npm and the node of that install run.
func runNPMUpgrade(ctx context.Context, npm, prefix, npmPackage, version string) ([]byte, error) {
	// Only the time limit may stop the run: a stopped HTTP request must not kill npm in the middle of a global
	// install, because that can leave the package of a Harness half written.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), upgradeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, npm)
	cmd.Args = append(cmd.Args, "install", "--global", "--prefix", prefix, npmPackage+"@"+version)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(prefix, "bin")+string(filepath.ListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("it did not end in %s", upgradeTimeout)
	}
	return output, err
}
