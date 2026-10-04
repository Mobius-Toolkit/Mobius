package engine

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// keepHouse removes the directories that nothing owns.
func (e *Engine) keepHouse(ctx context.Context) {
	if err := e.clean(ctx); err != nil {
		log.Printf("Housekeeper: %v", err)
	}
}

// workDir is a directory of a task or a session below worktrees/<repository>.
type workDir struct {
	repository, name string
}

// clean removes each directory of a task or a session that ended. The name of a directory tells its owner:
// worktrees/<repository>/task-<issue> is a live task, and review-<id>, judge-<id> and research-<id> below
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
		case "review", "judge", "research":
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
