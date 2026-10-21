package engine

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// copySkills writes the skills of the default branch of the skill repository of the organization of a into the skill
// folder of the Harness in the directory of a. A skill that is only on another branch never reaches the session.
func (a *Agent) copySkills(ctx context.Context) error {
	e := a.engine
	folder := a.harness.SkillDirectory()
	_, err := os.Stat(filepath.Join(a.spec.Dir, ".git"))
	worktree := err == nil
	if !worktree {
		if err := os.RemoveAll(filepath.Join(a.spec.Dir, folder)); err != nil {
			return err
		}
		_, err := a.exportSkills(ctx, folder, false)
		return err
	}
	earlier, err := runner.Excluded(ctx, e.config.DataDir, a.spec.Dir)
	if err != nil {
		return err
	}
	for _, pattern := range earlier {
		earlierFolder := strings.Trim(pattern, "/")
		tracked, err := runner.Tracked(ctx, e.config.DataDir, a.spec.Dir, earlierFolder)
		if err != nil {
			return err
		}
		if tracked {
			err = runner.CleanUntracked(ctx, e.config.DataDir, a.spec.Dir, earlierFolder)
		} else {
			err = os.RemoveAll(filepath.Join(a.spec.Dir, earlierFolder))
		}
		if err != nil {
			return err
		}
	}
	excluded, err := a.exportSkills(ctx, folder, true)
	if err != nil {
		return err
	}
	return runner.ExcludeFromStatus(ctx, e.config.DataDir, a.spec.Dir, excluded)
}

// exportSkills copies the skills into folder and gives the exclude pattern of each copied skill. In a worktree, it
// skips a skill whose folder exists.
func (a *Agent) exportSkills(ctx context.Context, folder string, worktree bool) ([]string, error) {
	e := a.engine
	repository, ok := e.github.SkillRepository(a.spec.Organization)
	if !ok {
		return nil, nil
	}
	if err := e.fetchRepository(ctx, repository); err != nil {
		log.Printf("fetch of %s for the session %d failed, use the last fetch: %v", repository.FullName, a.id, err)
	}
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	names, err := runner.SkillNames(ctx, e.config.DataDir, repository.FullName, repository.DefaultBranch)
	if err != nil {
		log.Printf("the skill repository %s has no skills for the session %d: %v", repository.FullName, a.id, err)
		return nil, nil
	}
	var copied, excluded []string
	for _, name := range names {
		_, err := os.Stat(filepath.Join(a.spec.Dir, folder, name))
		if worktree && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		copied = append(copied, name)
		excluded = append(excluded, "/"+path.Join(folder, name)+"/")
	}
	if len(copied) == 0 {
		return nil, nil
	}
	err = runner.Export(ctx, e.config.DataDir, repository.FullName, repository.DefaultBranch, filepath.Join(a.spec.Dir, folder), copied...)
	return excluded, err
}
