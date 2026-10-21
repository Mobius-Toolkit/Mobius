package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// minFreeDisk is the free disk space in bytes that a check on a full disk waits for.
const minFreeDisk = 20 << 30

// maxBranchChecks is the number of branches that one run of the Housekeeper reads from GitHub. All calls share the rate
// limit of GitHub.
const maxBranchChecks = 100

// keepHouse removes the directories that nothing owns, lets the checks that wait for free disk space run again when the
// disk has minFreeDisk of free space, and deletes the branches of the merged pull requests of the ended tasks.
func (e *Engine) keepHouse(ctx context.Context) {
	if err := e.clean(ctx); err != nil {
		log.Printf("Housekeeper: %v", err)
	}
	if err := e.freeDisk(ctx); err != nil {
		log.Printf("Housekeeper: %v", err)
	}
	if err := e.deleteMergedBranches(ctx); err != nil {
		log.Printf("Housekeeper: %v", err)
	}
}

// deleteMergedBranches deletes the branch of the merged pull request of each ended task. A branch stays when a live task
// of the same issue has it, when its pull request closed with no merge, and when it has a commit that is not in the pull
// request. A later task can merge origin/mobius/<issue> to keep the work of such a branch. A branch of an open pull
// request waits. A row is done after its delete, after a 4xx answer of GitHub and after a decision to keep the branch.
func (e *Engine) deleteMergedBranches(ctx context.Context) error {
	rows, err := e.queries.ListBranchesToCheck(ctx)
	if err != nil {
		return err
	}
	calls := 0
	for _, row := range rows {
		if calls == maxBranchChecks {
			break
		}
		repository, err := e.repository(row.Repository)
		if err != nil {
			continue
		}
		live, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: row.Repository, Issue: row.Issue})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && live.Branch == row.Branch {
			continue
		}
		calls++
		checked, err := deleteMergedBranch(ctx, repository, row)
		if err != nil {
			log.Printf("Housekeeper: the branch %s of %s: %v", row.Branch.String, row.Repository, err)
		}
		if checked {
			if err := e.queries.SetTaskBranchChecked(ctx, row.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// deleteMergedBranch deletes the branch of the pull request of the ended task row, if the pull request merged and the
// branch has the head of the pull request. It tells if the row needs no more work.
func deleteMergedBranch(ctx context.Context, repository github.Repository, row store.Task) (bool, error) {
	pull, err := repository.PullRequest(ctx, row.PullRequest.Int64)
	if err != nil {
		return refusedByGitHub(err), err
	}
	if pull.GetState() == "open" {
		return false, nil
	}
	if !pull.GetMerged() {
		return true, nil
	}
	head, err := repository.BranchHead(ctx, row.Branch.String)
	if err != nil {
		return refusedByGitHub(err), err
	}
	if head != pull.GetHead().GetSHA() {
		return true, nil
	}
	err = repository.DeleteBranch(ctx, row.Branch.String)
	return err == nil || refusedByGitHub(err), err
}

// freeDisk dismisses the Inbox items of a full disk and wakes the checks that wait for free disk space, when the disk
// has minFreeDisk of free space.
func (e *Engine) freeDisk(ctx context.Context) error {
	free, err := runner.FreeSpace(ctx, e.config.DataDir, e.agents.Path)
	if err != nil || free < minFreeDisk {
		return err
	}
	items, err := e.Inbox(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Kind == fullDiskKind {
			if err := e.Dismiss(ctx, item.ID); err != nil {
				return err
			}
		}
	}
	e.diskFreed.notify()
	return nil
}

// waitForDisk holds until the Housekeeper finds minFreeDisk of free space. The check of the issue of the task with
// title waits for it. All checks on a full disk share one Inbox item.
func (e *Engine) waitForDisk(ctx context.Context, task store.Task, title string) error {
	freed := e.diskFreed.wait()
	if err := e.addFullDiskItem(ctx, task, title); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-freed:
		return nil
	}
}

// addFullDiskItem adds the Inbox item of a full disk, unless the Inbox has one.
func (e *Engine) addFullDiskItem(ctx context.Context, task store.Task, title string) error {
	e.pausing.Lock()
	defer e.pausing.Unlock()
	items, err := e.Inbox(ctx)
	if err != nil || slices.ContainsFunc(items, func(item store.InboxItem) bool { return item.Kind == fullDiskKind }) {
		return err
	}
	free, err := runner.FreeSpace(ctx, e.config.DataDir, e.agents.Path)
	if err != nil {
		return err
	}
	repository, err := e.repository(task.Repository)
	if err != nil {
		return err
	}
	issue, err := existingIssue(ctx, repository, task.Issue)
	if err != nil {
		return err
	}
	err = e.addInboxItem(ctx, store.AddInboxItemParams{
		Kind:                 fullDiskKind,
		Organization:         repository.Owner(),
		Repository:           task.Repository,
		WorkstreamRepository: task.WorkstreamRepository,
		Workstream:           task.Workstream,
		Issue:                task.Issue,
		Title:                title,
		Reason:               "The disk is full",
		Text: fmt.Sprintf("The disk of the Mobius server is full. The .mobius/check of #%d \"%s\" waits for %d GiB of free space. The disk has %d GiB of free space.",
			task.Issue, title, minFreeDisk>>30, free>>30),
		Link: issue.GetHTMLURL(),
	})
	return err
}

// workDir is a directory of a task or a session below worktrees/<repository>.
type workDir struct {
	repository, name string
}

// clean removes each directory of a task or a session that ended. The name of a directory tells its owner:
// worktrees/<repository>/task-<issue> is a live task, and review-<id>, judge-<id>, research-<id>, curator-<id> and diagnoser-<id> below
// worktrees/<repository> and scratch/<id> are open sessions. The other directories stay.
func (e *Engine) clean(ctx context.Context) error {
	worktrees := filepath.Join(e.config.DataDir, "worktrees")
	scratch := filepath.Join(e.config.DataDir, "scratch")
	// The directories are read before the sessions and the tasks, so a new session or task owns each directory that it made.
	var dirs []workDir
	owners, err := subdirectories(worktrees)
	if err != nil {
		return err
	}
	for _, owner := range owners {
		repositories, err := subdirectories(filepath.Join(worktrees, owner))
		if err != nil {
			return err
		}
		for _, repository := range repositories {
			names, err := subdirectories(filepath.Join(worktrees, owner, repository))
			if err != nil {
				return err
			}
			for _, name := range names {
				dirs = append(dirs, workDir{owner + "/" + repository, name})
			}
		}
	}
	sessions, err := subdirectories(scratch)
	if err != nil {
		return err
	}
	ids, err := e.queries.ListOpenSessionIDs(ctx)
	if err != nil {
		return err
	}
	open := map[string]bool{}
	for _, id := range ids {
		open[strconv.FormatInt(id, 10)] = true
	}
	for _, dir := range dirs {
		owned := true
		switch kind, id, _ := strings.Cut(dir.name, "-"); kind {
		case "task":
			if owned, err = e.liveTask(ctx, dir.repository, id); err != nil {
				return err
			}
		case "review", "judge", "research", "curator", "diagnoser":
			owned = open[id]
		}
		if !owned {
			if err := os.RemoveAll(filepath.Join(worktrees, dir.repository, dir.name)); err != nil {
				return err
			}
		}
	}
	for _, id := range sessions {
		if !open[id] {
			if err := os.RemoveAll(filepath.Join(scratch, id)); err != nil {
				return err
			}
		}
	}
	// A worktree directory that went away keeps its record in the bare clone until the prune.
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	for _, owner := range owners {
		repositories, err := subdirectories(filepath.Join(worktrees, owner))
		if err != nil {
			return err
		}
		for _, repository := range repositories {
			if err := runner.Prune(ctx, e.config.DataDir, owner+"/"+repository); err != nil {
				return err
			}
		}
	}
	return nil
}

// liveTask tells if repository has a live task of the issue number in text.
func (e *Engine) liveTask(ctx context.Context, repository, text string) (bool, error) {
	tasks, err := e.queries.ListLiveTasks(ctx, repository)
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if strconv.FormatInt(task.Issue, 10) == text {
			return true, nil
		}
	}
	return false, nil
}

// subdirectories gives the names of the directories in dir. A dir that does not exist has none.
func subdirectories(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}
