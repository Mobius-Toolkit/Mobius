package engine

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The kind of the Lead event that carries the weekly review of the skills.
const skillReviewKind = "skill_review"

const (
	skillReviewInterval = 7 * 24 * time.Hour
	skillReviewDays     = 30
	manyCopies          = 20
	fewUsesPercent      = 10
	oftenUsed           = 3
)

// skillRole is the sessions of one role, for one skill.
type skillRole struct {
	skill, role string
}

type skillRoleCount struct {
	had, used int
}

// reviewSkills adds the event skill review to the Skills Workstream of each organization of repositories that has
// no such event in the last interval. A failure ends the step for the organization, and the next poll runs it again.
func (e *Engine) reviewSkills(ctx context.Context, repositories []github.Repository) {
	var organizations []string
	for _, repository := range repositories {
		organization, _, _ := strings.Cut(repository.FullName, "/")
		if slices.Contains(organizations, organization) {
			continue
		}
		organizations = append(organizations, organization)
		if err := e.reviewOrganizationSkills(ctx, organization); err != nil {
			log.Printf("review the skills of %s: %v", organization, err)
		}
	}
}

func (e *Engine) reviewOrganizationSkills(ctx context.Context, organization string) error {
	workstream, err := e.queries.GetSkillsWorkstream(ctx, organization)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	repository, ok := e.github.SkillRepository(organization)
	if !ok {
		return nil
	}
	to := e.timeNow().UTC()
	last, err := e.queries.GetLastLeadEventTime(ctx, store.GetLastLeadEventTimeParams{Repository: workstream.Repository, Workstream: workstream.Number, Kind: skillReviewKind})
	if err == nil {
		at, err := time.Parse(time.RFC3339Nano, last)
		if err != nil {
			return err
		}
		if to.Sub(at) < skillReviewInterval {
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	usage, err := e.readSkillUsageSums(ctx, repository, organization, to.AddDate(0, 0, -skillReviewDays), to)
	if err != nil {
		return err
	}
	return e.addLeadEvent(ctx, workstream.Repository, workstream.Number, "", sql.NullInt64{}, skillReviewKind, usage.reviewText())
}

// reviewText gives the text of the event skill review. The rules count only the Harnesses for which Mobius records a
// skill use.
func (u skillUsage) reviewText() string {
	copied, harness := u.roleCounts(u.copied), u.roleCounts(u.harness)
	var text strings.Builder
	fmt.Fprintf(&text, "Weekly review of the skills for the last %d days.\n\n%s", skillReviewDays, u.text())

	fmt.Fprintf(&text, "\n## Many copies, few uses\n\nThe role got the skill in %d or more sessions, and used it in less than %d%% of them.\n\n", manyCopies, fewUsesPercent)
	count := 0
	for _, key := range sortedSkillRoles(copied) {
		c := copied[key]
		if c.had >= manyCopies && c.used*100 < c.had*fewUsesPercent {
			fmt.Fprintf(&text, "- %s/, role %s: Had %d, Used %d\n", key.skill, key.role, c.had, c.used)
			count++
		}
	}
	if count == 0 {
		text.WriteString("None\n")
	}

	fmt.Fprintf(&text, "\n## No use in %d days\n\nNo role used the skill.\n\n", skillReviewDays)
	used := map[string]int{}
	for key, c := range copied {
		used[key.skill] += c.used
	}
	count = 0
	for _, folder := range u.folders {
		if used[folder] == 0 {
			fmt.Fprintf(&text, "- %s/\n", folder)
			count++
		}
	}
	if count == 0 {
		text.WriteString("None\n")
	}

	fmt.Fprintf(&text, "\n## Used with no copy\n\nThe role used the skill of the source harness in %d or more sessions.\n\n", oftenUsed)
	count = 0
	for _, key := range sortedSkillRoles(harness) {
		if c := harness[key]; c.used >= oftenUsed {
			fmt.Fprintf(&text, "- %s, role %s: Used %d\n", key.skill, key.role, c.used)
			count++
		}
	}
	if count == 0 {
		text.WriteString("None\n")
	}
	return text.String()
}

// roleCounts adds the sessions of each skill and role over the Harnesses for which Mobius records a skill use.
func (u skillUsage) roleCounts(sums map[skillUsageKey]*skillUsageSum) map[skillRole]skillRoleCount {
	counts := map[skillRole]skillRoleCount{}
	for key, sum := range sums {
		if !slices.Contains(recordedSkillHarnesses, config.Harness(key.harness)) {
			continue
		}
		role := skillRole{key.skill, key.role}
		c := counts[role]
		c.had += len(sum.had)
		c.used += len(sum.used)
		counts[role] = c
	}
	return counts
}

func sortedSkillRoles(counts map[skillRole]skillRoleCount) []skillRole {
	keys := slices.Collect(maps.Keys(counts))
	slices.SortFunc(keys, func(a, b skillRole) int {
		return cmp.Or(cmp.Compare(a.skill, b.skill), cmp.Compare(a.role, b.role))
	})
	return keys
}
