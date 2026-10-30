package engine

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

const maxSkillUsageDays = 90

type skillUsageInput struct {
	Days int `json:"days"`
}

type skillUsageKey struct {
	skill, harness, role string
}

type skillUsageSum struct {
	had, used map[int64]bool
	uses      int
	lastUse   time.Time
}

func (e *Engine) readSkillUsageTool(c caller) mcp.Tool {
	return toolOf(e, c, noRepository, "read_skill_usage",
		"Give the full list of the skills of the skill repository of the organization with their usage in a period: for each skill, its folder, description and roles, and for each Harness and role the sessions that got the skill (Had), the sessions that used it (Used), the uses and the last use. A skill with no copy and no use is also in the list. A separate part gives the skills of the source harness, which Mobius did not copy.",
		map[string]any{
			"days": map[string]any{"type": "integer", "minimum": 1, "maximum": maxSkillUsageDays, "description": "The length of the period in days. The period ends now."},
		},
		e.readSkillUsage)
}

func (e *Engine) readSkillUsage(ctx context.Context, c caller, _ github.Repository, input skillUsageInput) (string, error) {
	if input.Days < 1 || input.Days > maxSkillUsageDays {
		return "", refuse("days must be from 1 to %d.", maxSkillUsageDays)
	}
	to := e.timeNow().UTC()
	return e.skillUsageText(ctx, c.organization, to.AddDate(0, 0, -input.Days), to)
}

// skillUsage is the usage of the skills of one skill repository in a period.
type skillUsage struct {
	from, to        time.Time
	folders, texts  []string
	copied, harness map[skillUsageKey]*skillUsageSum
}

// skillUsageText gives the list of the skills of the default branch of the skill repository of organization with their
// usage from from, and before to.
func (e *Engine) skillUsageText(ctx context.Context, organization string, from, to time.Time) (string, error) {
	repository, ok := e.github.SkillRepository(organization)
	if !ok {
		return "The organization has no skill repository.", nil
	}
	usage, err := e.readSkillUsageSums(ctx, repository, organization, from, to)
	if err != nil {
		return "", err
	}
	return usage.text(), nil
}

// readSkillUsageSums reads the skills of repository, the skill repository of organization, and their usage from from,
// and before to.
func (e *Engine) readSkillUsageSums(ctx context.Context, repository github.Repository, organization string, from, to time.Time) (skillUsage, error) {
	rows, err := e.queries.ListSkillRowsBetween(ctx, from, to)
	if err != nil {
		return skillUsage{}, err
	}
	if err := e.fetchRepository(ctx, repository); err != nil {
		log.Printf("fetch of %s for the skill usage failed, use the last fetch: %v", repository.FullName, err)
	}
	folders, texts, err := e.skillTexts(ctx, repository)
	if err != nil {
		return skillUsage{}, err
	}

	copied := map[skillUsageKey]*skillUsageSum{}
	harness := map[skillUsageKey]*skillUsageSum{}
	for _, row := range rows {
		if row.Organization != organization {
			continue
		}
		sums, key := copied, skillUsageKey{skill: row.Skill, harness: row.Harness, role: roleConfigName(row.Role)}
		if row.Source == "harness" {
			sums = harness
		} else {
			_, name, _ := strings.Cut(row.Repository, "/")
			if own := path.Join("repos", name, row.Skill); slices.Contains(folders, own) {
				key.skill = own
			}
		}
		sum := sums[key]
		if sum == nil {
			sum = &skillUsageSum{had: map[int64]bool{}, used: map[int64]bool{}}
			sums[key] = sum
		}
		if !row.Used {
			sum.had[row.Session] = true
			continue
		}
		sum.used[row.Session] = true
		sum.uses++
		if row.At.After(sum.lastUse) {
			sum.lastUse = row.At
		}
	}
	return skillUsage{from: from, to: to, folders: folders, texts: texts, copied: copied, harness: harness}, nil
}

func (u skillUsage) text() string {
	var text strings.Builder
	fmt.Fprintf(&text, "Skill usage from %s to %s (UTC).\n", u.from.UTC().Format(time.RFC3339), u.to.UTC().Format(time.RFC3339))
	var unrecorded []string
	for _, h := range config.Harnesses {
		if !slices.Contains(recordedSkillHarnesses, h) {
			unrecorded = append(unrecorded, string(h))
		}
	}
	fmt.Fprintf(&text, "Mobius records no skill use for: %s.\n", strings.Join(unrecorded, ", "))
	for i, folder := range u.folders {
		description, roles := skillFrontmatter(u.texts[i])
		rolesText := "all roles"
		if roles != nil {
			rolesText = *roles
		}
		fmt.Fprintf(&text, "\n## %s\n\nFolder: %s/\nDescription: %s\nRoles: %s\n\n", path.Base(folder), folder, strings.Join(strings.Fields(description), " "), rolesText)
		keys := usageKeys(u.copied, func(key skillUsageKey) bool { return key.skill == folder })
		if len(keys) == 0 {
			text.WriteString("No copy and no use\n")
			continue
		}
		text.WriteString("| Harness | Role | Had | Used | Uses | Last use |\n|---|---|---|---|---|---|\n")
		for _, key := range keys {
			sum := u.copied[key]
			fmt.Fprintf(&text, "| %s | %s | %d | %d | %d | %s |\n", key.harness, key.role, len(sum.had), len(sum.used), sum.uses, lastUse(sum))
		}
	}
	text.WriteString("\n## Skills of the source harness\n\n")
	keys := usageKeys(u.harness, func(skillUsageKey) bool { return true })
	if len(keys) == 0 {
		text.WriteString("No use\n")
		return text.String()
	}
	text.WriteString("| Skill | Harness | Role | Used | Uses | Last use |\n|---|---|---|---|---|---|\n")
	for _, key := range keys {
		sum := u.harness[key]
		fmt.Fprintf(&text, "| %s | %s | %s | %d | %d | %s |\n", key.skill, key.harness, key.role, len(sum.used), sum.uses, lastUse(sum))
	}
	return text.String()
}

// skillTexts gives the skill folders of the default branch of repository, and the text of the SKILL.md of each.
func (e *Engine) skillTexts(ctx context.Context, repository github.Repository) ([]string, []string, error) {
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	folders, err := runner.SkillFolders(ctx, e.config.DataDir, repository.FullName, repository.DefaultBranch)
	if err != nil {
		return nil, nil, err
	}
	texts := make([]string, 0, len(folders))
	for _, folder := range folders {
		text, _, err := runner.Show(ctx, e.config.DataDir, repository.FullName, repository.DefaultBranch, path.Join(folder, "SKILL.md"))
		if err != nil {
			return nil, nil, err
		}
		texts = append(texts, text)
	}
	return folders, texts, nil
}

// usageKeys gives the keys of sums that pass keep, in the order of the skill, the Harness and the role.
func usageKeys(sums map[skillUsageKey]*skillUsageSum, keep func(skillUsageKey) bool) []skillUsageKey {
	var keys []skillUsageKey
	for key := range sums {
		if keep(key) {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b skillUsageKey) int {
		return cmp.Or(cmp.Compare(a.skill, b.skill), cmp.Compare(a.harness, b.harness), cmp.Compare(a.role, b.role))
	})
	return keys
}

func lastUse(sum *skillUsageSum) string {
	if sum.uses == 0 {
		return "none"
	}
	return sum.lastUse.UTC().Format(time.RFC3339)
}

// roleConfigName gives the name of role in the config, which metadata.roles of a skill uses.
func roleConfigName(role string) string {
	index := slices.IndexFunc(groups, func(g group) bool { return g.role == role })
	if index < 0 {
		return role
	}
	return groups[index].name
}
