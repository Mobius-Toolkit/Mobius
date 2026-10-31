package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	"github.com/Mobius-Toolkit/Mobius/internal/store"
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

// errUpgradeRuns is the error of an upgrade that waits for the end of an upgrade of the same npm package.
var errUpgradeRuns = errors.New("an upgrade of the package runs now")

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
	// AutoUpgrade tells that Mobius installs the newest version of Package by itself.
	AutoUpgrade bool
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
	auto := e.autoUpgradePackages(ctx)
	var checks []ToolCheck
	for _, harness := range config.Harnesses {
		program := runner.Program(harness)
		check := checkVersion(ctx, program, runner.Find(program, path), path)
		check.Required = used[harness]
		if harness == config.ClaudeCode {
			e.compareNewest(ctx, &check, adapterPackage)
			check.Package = npmPackageOf(check.Path, adapterPackage)
			check.AutoUpgrade = auto[check.Package]
			cli := e.claudeCodeCLI(ctx, check.Path, path)
			cli.Required = used[harness]
			cli.AutoUpgrade = auto[cli.Package]
			checks = append(checks, check, cli)
			continue
		}
		if harness == config.DSH {
			e.compareNewest(ctx, &check, dshPackage)
			check.Package = npmPackageOf(check.Path, dshPackage)
			check.AutoUpgrade = auto[check.Package]
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

// autoUpgradePackages gives each npm package whose auto-upgrade the Owner turned on. It gives no package when the
// database read fails, so a failed read starts no upgrade.
func (e *Engine) autoUpgradePackages(ctx context.Context) map[string]bool {
	rows, err := e.queries.ListToolAutoUpgrades(ctx)
	if err != nil {
		log.Printf("auto-upgrade packages: %v", err)
		return nil
	}
	on := make(map[string]bool, len(rows))
	for _, row := range rows {
		on[row.Package] = true
	}
	return on
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
	// The npm run and the steps after it continue when the request ends, so a closed connection cannot leave the
	// package of the Harness half written and without its record.
	ctx = context.WithoutCancel(ctx)
	if err := e.installNewest(ctx, check); err != nil {
		if errors.Is(err, errUpgradeRuns) {
			return nil, refuse("An upgrade of %s runs now.", check.Name)
		}
		return nil, err
	}
	e.clearToolUpgradeFailure(ctx, npmPackage)
	for _, organization := range e.github.Organizations() {
		e.addRecord(ctx, ChatKey{Organization: organization}, fmt.Sprintf("You upgraded %s to %s.", check.Name, check.Newest))
	}
	return e.Tools(ctx), nil
}

// installNewest runs npm to install the newest version of check in the Node install of its program.
func (e *Engine) installNewest(ctx context.Context, check ToolCheck) error {
	prefix, _ := nodeInstall(check.Path)
	if prefix == "" {
		return refuse("Mobius cannot find the Node install of %s.", check.Name)
	}
	if !e.beginUpgrade(check.Package) {
		return errUpgradeRuns
	}
	defer e.endUpgrade(check.Package)
	output, err := runNPMUpgrade(ctx, filepath.Join(prefix, "bin", "npm"), prefix, check.Package, check.Newest)
	if err != nil {
		return refuse("The upgrade of %s failed: %v\n%s", check.Name, err, tail(string(output), upgradeOutput))
	}
	return nil
}

// SetToolAutoUpgrade turns the auto-upgrade of the npm package of a tool on or off, and gives the new tool list. It
// refuses a package that no row has.
func (e *Engine) SetToolAutoUpgrade(ctx context.Context, npmPackage string, on bool) ([]ToolCheck, error) {
	checks := e.Tools(ctx)
	index := slices.IndexFunc(checks, func(check ToolCheck) bool { return check.Package == npmPackage })
	if index < 0 {
		return nil, refuse("Mobius does not upgrade the package \"%s\".", npmPackage)
	}
	if on {
		if err := e.queries.AddToolAutoUpgrade(ctx, npmPackage); err != nil {
			return nil, err
		}
	} else if err := e.queries.DeleteToolAutoUpgrade(ctx, npmPackage); err != nil {
		return nil, err
	}
	checks[index].AutoUpgrade = on
	state := "off"
	if on {
		state = "on"
	}
	for _, organization := range e.github.Organizations() {
		e.addRecord(ctx, ChatKey{Organization: organization}, fmt.Sprintf("You turned Auto-upgrade of %s %s.", checks[index].Name, state))
	}
	return checks, nil
}

// clearToolUpgradeFailure records that npmPackage has no failed version, so a later auto-upgrade of the same version
// can run again.
func (e *Engine) clearToolUpgradeFailure(ctx context.Context, npmPackage string) {
	err := e.queries.SetToolAutoUpgradeFailure(ctx, store.SetToolAutoUpgradeFailureParams{FailedVersion: "", Package: npmPackage})
	if err != nil {
		log.Printf("clear the failed version of %s: %v", npmPackage, err)
	}
}

// autoUpgradeTools upgrades each package whose auto-upgrade is on when the newest version differs from the version that
// failed last. It checks each poll, and it acts at the start and then each newestLife.
func (e *Engine) autoUpgradeTools(ctx context.Context) {
	rows, err := e.queries.ListToolAutoUpgrades(ctx)
	if err != nil {
		log.Printf("auto-upgrade tools: %v", err)
		return
	}
	if len(rows) == 0 || !e.autoUpgradeDue() {
		return
	}
	checks := e.Tools(ctx)
	for _, row := range rows {
		index := slices.IndexFunc(checks, func(check ToolCheck) bool { return check.Package == row.Package })
		if index < 0 {
			continue
		}
		check := checks[index]
		if !check.Outdated || check.Newest == row.FailedVersion {
			continue
		}
		err := e.installNewest(ctx, check)
		if errors.Is(err, errUpgradeRuns) {
			continue
		}
		if err != nil {
			if failErr := e.failAutoUpgrade(ctx, check, err.Error()); failErr != nil {
				log.Printf("auto-upgrade of %s: %v", check.Package, failErr)
			}
			continue
		}
		e.clearToolUpgradeFailure(ctx, check.Package)
	}
}

// autoUpgradeDue tells if the auto-upgrade check runs now, and records the time of the run.
func (e *Engine) autoUpgradeDue() bool {
	at := e.timeNow()
	if !e.autoUpgradeAt.IsZero() && at.Sub(e.autoUpgradeAt) < newestLife {
		return false
	}
	e.autoUpgradeAt = at
	return true
}

// failAutoUpgrade records that the auto-upgrade of check to check.Newest failed, and adds one Inbox item for each
// organization. It writes the record and the items in one transaction, so a failed item does not keep the error from
// the next check.
func (e *Engine) failAutoUpgrade(ctx context.Context, check ToolCheck, reason string) error {
	var items []store.InboxItem
	err := e.inTx(ctx, func(q *store.Queries) error {
		err := q.SetToolAutoUpgradeFailure(ctx, store.SetToolAutoUpgradeFailureParams{FailedVersion: check.Newest, Package: check.Package})
		if err != nil {
			return err
		}
		for _, organization := range e.github.Organizations() {
			item, err := q.AddInboxItem(ctx, store.AddInboxItemParams{
				Kind:         toolUpgradeKind,
				Organization: organization,
				Title:        fmt.Sprintf("Auto-upgrade of %s failed", check.Name),
				Reason:       reason,
				Time:         now(),
			})
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, item := range items {
		e.publish(Change{Inbox: &item})
	}
	return nil
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
	ctx, cancel := context.WithTimeout(ctx, upgradeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, npm)
	// A program that the time limit kills can leave a child that holds the output open.
	cmd.WaitDelay = time.Second
	cmd.Args = append(cmd.Args, "install", "--global", "--prefix", prefix, npmPackage+"@"+version)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(prefix, "bin")+string(filepath.ListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("it did not end in %s", upgradeTimeout)
	}
	return output, err
}
