package engine

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"gopkg.in/yaml.v3"
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
	general, err := runner.SkillNames(ctx, e.config.DataDir, repository.FullName, repository.DefaultBranch, "")
	if err != nil {
		log.Printf("the skill repository %s has no skills for the session %d: %v", repository.FullName, a.id, err)
		return nil, nil
	}
	var own []string
	repositoryFolder := ""
	if _, name, found := strings.Cut(a.spec.Repository, "/"); found {
		repositoryFolder = path.Join("repos", name)
		own, err = runner.SkillNames(ctx, e.config.DataDir, repository.FullName, repository.DefaultBranch, repositoryFolder)
		if err != nil {
			return nil, err
		}
	}
	var excluded []string
	for _, group := range []struct {
		folder string
		names  []string
	}{{"", slices.DeleteFunc(general, func(name string) bool { return slices.Contains(own, name) })}, {repositoryFolder, own}} {
		var copied []string
		for _, name := range group.names {
			text, _, err := runner.Show(ctx, e.config.DataDir, repository.FullName, repository.DefaultBranch, path.Join(group.folder, name, "SKILL.md"))
			if err != nil {
				return excluded, err
			}
			if !skillGoesToRole(text, a.roleName()) {
				continue
			}
			_, err = os.Stat(filepath.Join(a.spec.Dir, folder, name))
			if worktree && !errors.Is(err, fs.ErrNotExist) {
				continue
			}
			copied = append(copied, name)
			excluded = append(excluded, "/"+path.Join(folder, name)+"/")
		}
		if len(copied) == 0 {
			continue
		}
		err := runner.Export(ctx, e.config.DataDir, repository.FullName, repository.DefaultBranch, group.folder, filepath.Join(a.spec.Dir, folder), copied...)
		if err != nil {
			return excluded, err
		}
	}
	return excluded, nil
}

// roleName gives the name of the Role of a in the config.
func (a *Agent) roleName() string {
	index := slices.IndexFunc(groups, func(g group) bool { return g.role == a.spec.Role })
	return groups[index].name
}

// skillGoesToRole tells whether a skill with the text of its SKILL.md goes to the Role with the config name role. A
// skill goes to each Role when it has no metadata.roles key in the frontmatter, or when the frontmatter is not valid.
func skillGoesToRole(text, role string) bool {
	rest, found := strings.CutPrefix(text, "---\n")
	if !found {
		return true
	}
	frontmatter, _, found := strings.Cut(rest, "\n---")
	if !found {
		return true
	}
	var parsed struct {
		Metadata struct {
			Roles *string `yaml:"roles"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &parsed); err != nil || parsed.Metadata.Roles == nil {
		return true
	}
	for name := range strings.SplitSeq(*parsed.Metadata.Roles, ",") {
		if strings.TrimSpace(name) == role {
			return true
		}
	}
	return false
}
