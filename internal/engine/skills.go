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

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"gopkg.in/yaml.v3"
)

// copySkills writes the skills of the default branch of the skill repository of the organization of a into the skill
// folder of the Harness in the directory of a. A skill that is only on another branch never reaches the session.
func (a *Agent) copySkills(ctx context.Context) error {
	e := a.engine
	folder := a.binding.Harness.SkillDirectory()
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
		a.addSessionSkills(ctx, copied)
	}
	return excluded, nil
}

// addSessionSkills adds a session_skills row for each name and keeps the names for the source of a skill use.
func (a *Agent) addSessionSkills(ctx context.Context, names []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	spec := a.spec
	for _, name := range names {
		a.copiedSkills = append(a.copiedSkills, name)
		err := a.engine.queries.AddSessionSkill(ctx, store.AddSessionSkillParams{
			Session:      a.id,
			Organization: spec.Organization,
			Repository:   spec.Repository,
			Role:         spec.Role,
			Harness:      string(a.binding.Harness),
			Skill:        name,
			CopiedAt:     now(),
		})
		if err != nil {
			log.Printf("add the skill %s of the session %d: %v", name, a.id, err)
		}
	}
}

// recordedSkillHarnesses are the Harnesses for which recordSkillUse adds a skill_uses row.
var recordedSkillHarnesses = []config.Harness{config.ClaudeCode, config.Devin, config.DSH}

// recordSkillUse adds a skill_uses row for the first update of a skill tool call. The caller holds a.mu.
func (a *Agent) recordSkillUse(ctx context.Context, notification map[string]any, kind string) {
	update := notification["update"]
	switch a.binding.Harness {
	case config.ClaudeCode:
		if (kind != "tool_call" && kind != "tool_call_update") || stringField(update, "_meta", "claudeCode", "toolName") != "Skill" {
			return
		}
	case config.Devin:
		if kind != "tool_call" || stringField(update, "_meta", "cognition.ai/inferenceToolName") != "skill" {
			return
		}
	case config.DSH:
		if kind != "tool_call" || stringField(update, "title") != "skill" {
			return
		}
	default:
		return
	}
	skillField := "skill"
	if a.binding.Harness == config.DSH {
		skillField = "name"
	}
	skill := stringField(update, "rawInput", skillField)
	call := stringField(update, "toolCallId")
	if skill == "" || a.skillCalls[call] {
		return
	}
	source := "harness"
	if slices.Contains(a.copiedSkills, skill) {
		source = "mobius"
	}
	spec := a.spec
	err := a.engine.queries.AddSkillUse(ctx, store.AddSkillUseParams{
		Session:      a.id,
		Organization: spec.Organization,
		Repository:   spec.Repository,
		Role:         spec.Role,
		Harness:      string(a.binding.Harness),
		Skill:        skill,
		Source:       source,
		UsedAt:       now(),
	})
	if err != nil {
		log.Printf("add the skill use %s of the session %d: %v", skill, a.id, err)
		return
	}
	if a.skillCalls == nil {
		a.skillCalls = map[string]bool{}
	}
	a.skillCalls[call] = true
}

// roleName gives the name of the Role of a in the config.
func (a *Agent) roleName() string {
	index := slices.IndexFunc(groups, func(g group) bool { return g.role == a.spec.Role })
	return groups[index].name
}

// skillFrontmatter gives the description and the metadata.roles value of the frontmatter of the text of a SKILL.md. The
// roles are nil when the frontmatter has no metadata.roles key or is not valid.
func skillFrontmatter(text string) (description string, roles *string) {
	rest, found := strings.CutPrefix(text, "---\n")
	if !found {
		return "", nil
	}
	frontmatter, _, found := strings.Cut(rest, "\n---")
	if !found {
		return "", nil
	}
	var parsed struct {
		Description any `yaml:"description"`
		Metadata    struct {
			Roles *string `yaml:"roles"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &parsed); err != nil {
		return "", nil
	}
	description, _ = parsed.Description.(string)
	return description, parsed.Metadata.Roles
}

// skillGoesToRole tells whether a skill with the text of its SKILL.md goes to the Role with the config name role. A
// skill goes to each Role when it has no metadata.roles key in the frontmatter, or when the frontmatter is not valid.
func skillGoesToRole(text, role string) bool {
	_, roles := skillFrontmatter(text)
	if roles == nil {
		return true
	}
	for name := range strings.SplitSeq(*roles, ",") {
		if strings.TrimSpace(name) == role {
			return true
		}
	}
	return false
}
