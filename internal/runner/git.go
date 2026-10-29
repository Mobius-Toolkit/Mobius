package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// LowSpeedLimit and LowSpeedTime are the stall limit of a git transfer over HTTP: git stops the transfer when its
// speed stays below LowSpeedLimit bytes per second for LowSpeedTime seconds. The time is long, because a server can
// send no pack data while it counts the objects of a large repository or resolves the deltas of a push.
var (
	LowSpeedLimit = 1000
	LowSpeedTime  = 120
)

// git gives the git command with args in dir. The token goes to git only in the environment of the process, so no
// file and no process list shows it. No hook runs, so no code of the repository gets that environment. The command
// stays in the process group of the caller, so a kill of that group also stops the git processes that it started.
func git(ctx context.Context, dataDir, dir, token string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append(cmd.Args, "-c", "core.hooksPath=/dev/null")
	cmd.Args = append(cmd.Args, args...)
	cmd.Dir = dir
	// A child of git that the kill leaves can hold the output open.
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(agentEnv(dataDir), "gitconfig"), "GIT_TERMINAL_PROMPT=0")
	settings := [][2]string{
		{"http.lowSpeedLimit", strconv.Itoa(LowSpeedLimit)},
		{"http.lowSpeedTime", strconv.Itoa(LowSpeedTime)},
	}
	if token != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		settings = append(settings, [2]string{"http.extraHeader", "AUTHORIZATION: basic " + credentials})
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(settings)))
	for i, setting := range settings {
		cmd.Env = append(cmd.Env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, setting[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, setting[1]))
	}
	return cmd
}

// run runs cmd and gives its trimmed stdout. The error has the trimmed stderr.
func run(cmd *exec.Cmd) (string, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func bareDir(dataDir, repository string) string {
	return filepath.Join(dataDir, "repos", repository+".git")
}

// Fetch fetches origin into the bare clone of repository below dataDir, and makes the clone first when it does not
// exist. Two calls for the same repository must not run at the same time.
func Fetch(ctx context.Context, dataDir, repository, cloneURL, token string) error {
	bare := bareDir(dataDir, repository)
	if _, err := os.Stat(bare); errors.Is(err, fs.ErrNotExist) {
		// The clone gets its config in a temporary directory, so a failed start leaves no bare clone with part of its config.
		temporary := strings.TrimSuffix(bare, ".git") + ".new"
		if err := os.RemoveAll(temporary); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(temporary), 0o750); err != nil {
			return err
		}
		if _, err := run(git(ctx, dataDir, dataDir, token, "clone", "--bare", cloneURL, temporary)); err != nil {
			return err
		}
		if _, err := run(git(ctx, dataDir, temporary, "", "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")); err != nil {
			return err
		}
		if err := os.Rename(temporary, bare); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := worktreeConfig(ctx, dataDir, bare); err != nil {
		return err
	}
	_, err := run(git(ctx, dataDir, bare, token, "fetch", "--prune", "origin"))
	return err
}

// HasRemoteBranches tells if the bare clone of repository below dataDir has at least one origin branch, as the last
// Fetch got it.
func HasRemoteBranches(ctx context.Context, dataDir, repository string) (bool, error) {
	refs, err := run(git(ctx, dataDir, bareDir(dataDir, repository), "", "for-each-ref", "--format=%(refname)", "refs/remotes/origin"))
	if err != nil {
		return false, err
	}
	for _, ref := range strings.Fields(refs) {
		if ref != "refs/remotes/origin/HEAD" {
			return true, nil
		}
	}
	return false, nil
}

// emptyTree is the id of the tree with no entry. Git knows it in each repository.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// StartBranch pushes a commit of the empty tree, with the author name and email, to branch of the empty remote of
// the bare clone of repository below dataDir, and fetches again. A failed push is no error when origin/branch exists
// after the fetch.
func StartBranch(ctx context.Context, dataDir, repository, cloneURL, token, branch, name, email string) error {
	bare := bareDir(dataDir, repository)
	commit := git(ctx, dataDir, bare, "", "commit-tree", emptyTree, "-m", "Start")
	commit.Env = append(commit.Env,
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	id, err := run(commit)
	if err != nil {
		return err
	}
	_, pushErr := run(git(ctx, dataDir, bare, token, "push", "origin", id+":refs/heads/"+branch))
	if err := Fetch(ctx, dataDir, repository, cloneURL, token); err != nil {
		return err
	}
	if pushErr == nil {
		return nil
	}
	exists, err := hasRef(ctx, dataDir, bare, "refs/remotes/origin/"+branch)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return pushErr
}

// worktreeConfig moves core.bare of the bare clone from its shared config to the config of the clone itself. With
// extensions.worktreeConfig, a worktree of the clone then does not read core.bare. The steps can run again after a
// failure.
func worktreeConfig(ctx context.Context, dataDir, bare string) error {
	shared, err := run(git(ctx, dataDir, bare, "", "config", "--file", "config", "--default=", "--get", "core.bare"))
	if err != nil || shared == "" {
		return err
	}
	for _, args := range [][]string{
		{"config", "extensions.worktreeConfig", "true"},
		{"config", "--worktree", "core.bare", "true"},
		{"config", "--file", "config", "--unset", "core.bare"},
	} {
		if _, err := run(git(ctx, dataDir, bare, "", args...)); err != nil {
			return err
		}
	}
	return nil
}

// Show gives the text of path on origin/branch in the bare clone of repository below dataDir, as the last Fetch
// got it. It gives false when origin/branch has no such file.
func Show(ctx context.Context, dataDir, repository, branch, path string) (string, bool, error) {
	bare := bareDir(dataDir, repository)
	name := "origin/" + branch
	found, err := run(git(ctx, dataDir, bare, "", "ls-tree", "--name-only", name, "--", path))
	if err != nil || found == "" {
		return "", false, err
	}
	text, err := run(git(ctx, dataDir, bare, "", "show", name+":"+path))
	return text, err == nil, err
}

// SkillNames gives the names of the folders directly below folder in origin/branch in the bare clone of repository
// below dataDir, as the last Fetch got them, that have a SKILL.md file. An empty folder is the root.
func SkillNames(ctx context.Context, dataDir, repository, branch, folder string) ([]string, error) {
	files, err := run(git(ctx, dataDir, bareDir(dataDir, repository), "", "ls-tree", "-r", "--name-only", "origin/"+branch))
	if err != nil {
		return nil, err
	}
	var names []string
	for file := range strings.SplitSeq(files, "\n") {
		below := file
		if folder != "" {
			var found bool
			if below, found = strings.CutPrefix(file, folder+"/"); !found {
				continue
			}
		}
		if name, found := strings.CutSuffix(below, "/SKILL.md"); found && !strings.Contains(name, "/") {
			names = append(names, name)
		}
	}
	return names, nil
}

// SkillFolders gives the folders in origin/branch in the bare clone of repository below dataDir, as the last Fetch got
// them, that have a SKILL.md file. A skill is a folder in the root, or a folder below repos/<repository name>. The
// folders are in name order.
func SkillFolders(ctx context.Context, dataDir, repository, branch string) ([]string, error) {
	files, err := run(git(ctx, dataDir, bareDir(dataDir, repository), "", "ls-tree", "-r", "--name-only", "origin/"+branch))
	if err != nil {
		return nil, err
	}
	var folders []string
	for file := range strings.SplitSeq(files, "\n") {
		folder, found := strings.CutSuffix(file, "/SKILL.md")
		if !found {
			continue
		}
		parts := strings.Split(folder, "/")
		if len(parts) == 1 || len(parts) == 3 && parts[0] == "repos" {
			folders = append(folders, folder)
		}
	}
	slices.Sort(folders)
	return folders, nil
}

// SkillNamesEver gives each skill name that had a SKILL.md file in a commit of the history of origin/branch in the bare
// clone of repository below dataDir, as the last Fetch got it, also a skill that a later commit removed. A skill is a
// folder in the root, or a folder below repos/<repository name>. Each name is in the list one time.
func SkillNamesEver(ctx context.Context, dataDir, repository, branch string) ([]string, error) {
	files, err := run(git(ctx, dataDir, bareDir(dataDir, repository), "", "log", "--format=", "--name-only", "origin/"+branch,
		"--", ":(glob)*/SKILL.md", ":(glob)repos/*/*/SKILL.md"))
	if err != nil {
		return nil, err
	}
	var names []string
	for file := range strings.SplitSeq(files, "\n") {
		parts := strings.Split(file, "/")
		switch {
		case len(parts) == 2:
			names = append(names, parts[0])
		case len(parts) == 4 && parts[0] == "repos":
			names = append(names, parts[2])
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// Export writes the paths below folder of origin/branch in the bare clone of repository below dataDir, as the last
// Fetch got them, into the directory dir. An empty folder is the root. The files have no .git and no folder prefix.
func Export(ctx context.Context, dataDir, repository, branch, folder, dir string, paths ...string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tree := "origin/" + branch
	if folder != "" {
		tree += ":" + folder
	}
	archive := git(ctx, dataDir, bareDir(dataDir, repository), "", append([]string{"archive", "--format=tar", tree, "--"}, paths...)...)
	var tarball bytes.Buffer
	archive.Stdout = &tarball
	var stderr bytes.Buffer
	archive.Stderr = &stderr
	if err := archive.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(archive.Args, " "), err, strings.TrimSpace(stderr.String()))
	}
	extract := exec.CommandContext(ctx, "tar", "-x")
	extract.Dir = dir
	extract.Stdin = &tarball
	_, err := run(extract)
	return err
}

// ExcludeFromStatus makes git status and git add -A of the worktree ignore patterns, which are in the format of a
// .gitignore file. It sets core.excludesFile of the worktree to a file in its git directory. That key replaces the
// default user ignore file of git, so the file also has the lines of that file.
func ExcludeFromStatus(ctx context.Context, dataDir, worktree string, patterns []string) error {
	user, err := userIgnoreLines()
	if err != nil {
		return err
	}
	list := strings.Join(patterns, "\n") + "\n"
	copies, err := copiesFile(ctx, dataDir, worktree)
	if err != nil {
		return err
	}
	if err := os.WriteFile(copies, []byte(list), 0o600); err != nil {
		return err
	}
	file, err := excludeFile(ctx, dataDir, worktree)
	if err != nil {
		return err
	}
	if err := os.WriteFile(file, append(bytes.TrimRight(user, "\n"), []byte("\n"+list)...), 0o600); err != nil {
		return err
	}
	_, err = run(git(ctx, dataDir, worktree, "", "config", "--worktree", "core.excludesFile", file))
	return err
}

// userIgnoreLines gives the content of the default user ignore file of git, or nothing when it does not exist.
func userIgnoreLines() ([]byte, error) {
	lines, err := os.ReadFile(filepath.Clean(userIgnoreFile()))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return lines, err
}

// userIgnoreFile gives the file that git reads for the ignored files of the user when core.excludesFile is not set.
func userIgnoreFile() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "git", "ignore")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "git", "ignore")
}

// Excluded gives the patterns that ExcludeFromStatus got for the worktree, or none when it got nothing.
func Excluded(ctx context.Context, dataDir, worktree string) ([]string, error) {
	copies, err := copiesFile(ctx, dataDir, worktree)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(filepath.Clean(copies))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var patterns []string
	for line := range strings.SplitSeq(string(content), "\n") {
		if line != "" {
			patterns = append(patterns, line)
		}
	}
	return patterns, nil
}

// Tracked tells if the worktree tracks a file in folder.
func Tracked(ctx context.Context, dataDir, worktree, folder string) (bool, error) {
	files, err := run(git(ctx, dataDir, worktree, "", "ls-files", "--", folder))
	return files != "", err
}

// CleanUntracked removes the untracked and the ignored files in folder of the worktree.
func CleanUntracked(ctx context.Context, dataDir, worktree, folder string) error {
	_, err := run(git(ctx, dataDir, worktree, "", "clean", "-fdx", "--", folder))
	return err
}

func excludeFile(ctx context.Context, dataDir, worktree string) (string, error) {
	return gitDirFile(ctx, dataDir, worktree, "mobius-exclude")
}

func copiesFile(ctx context.Context, dataDir, worktree string) (string, error) {
	return gitDirFile(ctx, dataDir, worktree, "mobius-copies")
}

func gitDirFile(ctx context.Context, dataDir, worktree, name string) (string, error) {
	gitDir, err := run(git(ctx, dataDir, worktree, "", "rev-parse", "--absolute-git-dir"))
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDir, name), nil
}

// TaskDir gives the worktree of the task of the issue number of repository below dataDir.
func TaskDir(dataDir, repository string, number int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "task-"+strconv.FormatInt(number, 10))
}

// ResearchDir gives the worktree of the Researcher session id of repository below dataDir.
func ResearchDir(dataDir, repository string, id int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "research-"+strconv.FormatInt(id, 10))
}

// CuratorDir gives the worktree of the Curator session id of repository below dataDir.
func CuratorDir(dataDir, repository string, id int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "curator-"+strconv.FormatInt(id, 10))
}

// DiagnoserDir gives the worktree of the Diagnoser session id of repository below dataDir.
func DiagnoserDir(dataDir, repository string, id int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "diagnoser-"+strconv.FormatInt(id, 10))
}

// ReviewDir gives the worktree of the Reviewer session id of repository below dataDir.
func ReviewDir(dataDir, repository string, id int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "review-"+strconv.FormatInt(id, 10))
}

// JudgeDir gives the worktree of the Judge session id of repository below dataDir.
func JudgeDir(dataDir, repository string, id int64) string {
	return filepath.Join(dataDir, "worktrees", repository, "judge-"+strconv.FormatInt(id, 10))
}

// succeeds tells if cmd exits with status 0. Another exit status is no error.
func succeeds(cmd *exec.Cmd) (bool, error) {
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return err == nil, err
}

// hasRef tells if the ref name exists in dir.
func hasRef(ctx context.Context, dataDir, dir, name string) (bool, error) {
	return succeeds(git(ctx, dataDir, dir, "", "show-ref", "--verify", "--quiet", name))
}

// FreeBranch gives the first branch of mobius/<number>, mobius/<number>-2, ... that the bare clone of repository
// does not have, locally and on origin.
func FreeBranch(ctx context.Context, dataDir, repository string, number int64) (string, error) {
	bare := bareDir(dataDir, repository)
	branch := fmt.Sprintf("mobius/%d", number)
	for next := 2; ; next++ {
		local, err := hasRef(ctx, dataDir, bare, "refs/heads/"+branch)
		if err != nil {
			return "", err
		}
		remote, err := hasRef(ctx, dataDir, bare, "refs/remotes/origin/"+branch)
		if err != nil {
			return "", err
		}
		if !local && !remote {
			return branch, nil
		}
		branch = fmt.Sprintf("mobius/%d-%d", number, next)
	}
}

// AddWorktree makes the worktree dir of the bare clone of repository on branch, with the commit author name and
// email. A branch that the clone does not have starts at start. A worktree at dir goes away first.
func AddWorktree(ctx context.Context, dataDir, repository, dir, branch, start, name, email string) error {
	bare := bareDir(dataDir, repository)
	if _, err := os.Stat(dir); err == nil {
		if _, err := run(git(ctx, dataDir, bare, "", "worktree", "remove", "--force", dir)); err != nil {
			return err
		}
	}
	local, err := hasRef(ctx, dataDir, bare, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	args := []string{"worktree", "add", dir, branch}
	if !local {
		args = []string{"worktree", "add", "--no-track", "-b", branch, dir, start}
	}
	if _, err := run(git(ctx, dataDir, bare, "", args...)); err != nil {
		return err
	}
	if _, err := run(git(ctx, dataDir, dir, "", "config", "--worktree", "user.name", name)); err != nil {
		return err
	}
	_, err = run(git(ctx, dataDir, dir, "", "config", "--worktree", "user.email", email))
	return err
}

// AddDetachedWorktree makes the worktree dir of the bare clone of repository, detached at commit.
func AddDetachedWorktree(ctx context.Context, dataDir, repository, dir, commit string) error {
	_, err := run(git(ctx, dataDir, bareDir(dataDir, repository), "", "worktree", "add", "--detach", dir, commit))
	return err
}

// RemoveWorktree removes the worktree dir of the bare clone of repository, with its changes. It also removes a worktree
// that a killed "git worktree add" left: git cannot remove it, and "git worktree prune" keeps its record because the
// record is locked. Thus the fallback deletes the record that points to dir, so a new worktree can use dir again.
func RemoveWorktree(ctx context.Context, dataDir, repository, dir string) error {
	bare := bareDir(dataDir, repository)
	if _, err := run(git(ctx, dataDir, bare, "", "worktree", "remove", "--force", "--force", dir)); err == nil {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		return err
	}
	gitdirs, err := filepath.Glob(filepath.Join(bare, "worktrees", "*", "gitdir"))
	if err != nil {
		return err
	}
	for _, gitdir := range gitdirs {
		content, err := os.ReadFile(filepath.Clean(gitdir))
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(content)) == filepath.Join(parent, filepath.Base(dir), ".git") {
			if err := os.RemoveAll(filepath.Dir(gitdir)); err != nil {
				return err
			}
		}
	}
	_, err = run(git(ctx, dataDir, bare, "", "worktree", "prune"))
	return err
}

// Prune removes the records of the worktrees whose directories are gone from the bare clone of repository. A
// repository with no bare clone is no error.
func Prune(ctx context.Context, dataDir, repository string) error {
	bare := bareDir(dataDir, repository)
	if _, err := os.Stat(bare); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	_, err := run(git(ctx, dataDir, bare, "", "worktree", "prune"))
	return err
}

// ErrMergeConflict is the error of a Pull whose merge has conflicts.
var ErrMergeConflict = errors.New("merge conflict")

// Pull merges origin/branch of the last Fetch into the worktree, also when the two branches diverged. A failed merge
// is aborted, so the worktree has no unmerged files. A merge with conflicts gives ErrMergeConflict.
func Pull(ctx context.Context, dataDir, worktree, branch string) error {
	remote := "refs/remotes/origin/" + branch
	found, err := hasRef(ctx, dataDir, worktree, remote)
	if err != nil || !found {
		return err
	}
	_, err = run(git(ctx, dataDir, worktree, "", "merge", "--no-edit", remote))
	if err == nil {
		return nil
	}
	conflicts, conflictsErr := run(git(ctx, dataDir, worktree, "", "diff", "--name-only", "--diff-filter=U"))
	if _, abortErr := run(git(ctx, dataDir, worktree, "", "merge", "--abort")); abortErr != nil {
		return errors.Join(err, conflictsErr, abortErr)
	}
	if conflictsErr == nil && conflicts != "" {
		return fmt.Errorf("%w in %s", ErrMergeConflict, strings.ReplaceAll(conflicts, "\n", ", "))
	}
	return err
}

// Push pushes HEAD of the worktree to branch on origin with token, and gives the pushed commit.
func Push(ctx context.Context, dataDir, worktree, token, branch string) (string, error) {
	if _, err := run(git(ctx, dataDir, worktree, token, "push", "origin", "HEAD:refs/heads/"+branch)); err != nil {
		return "", err
	}
	return RevParse(ctx, dataDir, worktree, "HEAD")
}

// MergeBase gives the best common ancestor of the commits one and other in the bare clone of repository.
func MergeBase(ctx context.Context, dataDir, repository, one, other string) (string, error) {
	return run(git(ctx, dataDir, bareDir(dataDir, repository), "", "merge-base", one, other))
}

// MergeTree gives the tree of the automatic merge of the commits first and second in the bare clone of repository. A
// merge with a conflict exits with status 1 and still writes the tree, so MergeTree gives that tree with no error.
func MergeTree(ctx context.Context, dataDir, repository, first, second string) (string, error) {
	cmd := git(ctx, dataDir, bareDir(dataDir, repository), "", "merge-tree", "--write-tree", first, second)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return "", fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, strings.TrimSpace(stderr.String()))
	}
	tree, _, _ := strings.Cut(string(out), "\n")
	return tree, nil
}

// DiffFiles gives the names of the files that differ between the trees or commits from and to in the bare clone of
// repository.
func DiffFiles(ctx context.Context, dataDir, repository, from, to string) ([]string, error) {
	out, err := run(git(ctx, dataDir, bareDir(dataDir, repository), "", "diff", "--name-only", from, to))
	return strings.Fields(out), err
}

// RevParse gives the commit of name in the worktree.
func RevParse(ctx context.Context, dataDir, worktree, name string) (string, error) {
	return run(git(ctx, dataDir, worktree, "", "rev-parse", name))
}

// HeadContains tells if HEAD of the worktree contains commit.
func HeadContains(ctx context.Context, dataDir, worktree, commit string) (bool, error) {
	return succeeds(git(ctx, dataDir, worktree, "", "merge-base", "--is-ancestor", commit, "HEAD"))
}

// HasUncommittedChanges tells if the worktree has a change with no commit, also a new file.
func HasUncommittedChanges(ctx context.Context, dataDir, worktree string) (bool, error) {
	status, err := run(git(ctx, dataDir, worktree, "", "--no-optional-locks", "status", "--porcelain"))
	return status != "", err
}

// HasUnpushedWork tells if the worktree has uncommitted changes, or commits that origin/branch does not have. Before
// the first push, origin/branch does not exist, and the worktree is compared with origin/base.
func HasUnpushedWork(ctx context.Context, dataDir, worktree, branch, base string) (bool, error) {
	uncommitted, err := HasUncommittedChanges(ctx, dataDir, worktree)
	if err != nil || uncommitted {
		return uncommitted, err
	}
	remote := "origin/" + branch
	found, err := hasRef(ctx, dataDir, worktree, "refs/remotes/"+remote)
	if err != nil {
		return false, err
	}
	if !found {
		remote = "origin/" + base
	}
	commits, err := run(git(ctx, dataDir, worktree, "", "rev-list", "--count", remote+"..HEAD"))
	return commits != "0", err
}
