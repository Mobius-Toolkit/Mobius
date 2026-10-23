package api

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// SkillTotals are the counts of all skill rows that pass the filters.
type SkillTotals struct {
	// Uses is the number of skill uses
	Uses int64 `gork:"uses"`
	// Skills is the number of distinct skill names of the skill uses and the copied skills
	Skills int64 `gork:"skills"`
	// Sessions is the number of distinct sessions that used a skill
	Sessions int64 `gork:"sessions"`
}

// SkillUsageRow is the count of one skill of one source in one group.
type SkillUsageRow struct {
	Skill string `gork:"skill"`
	// Source is "mobius" for a skill that Mobius copied into a session, and "harness" for each other skill
	Source string `gork:"source" validate:"oneof=mobius harness"`
	// Group is the value of the group, or empty when the request has no group
	Group string `gork:"group"`
	// Uses is the number of skill uses
	Uses int64 `gork:"uses"`
	// Had is the number of distinct sessions that Mobius gave the skill to. It is 0 for a harness skill
	Had int64 `gork:"had"`
	// Used is the number of distinct sessions that used the skill
	Used int64 `gork:"used"`
}

// SkillUsageOptions are the values that each filter can take in the period.
type SkillUsageOptions struct {
	// Harnesses are the Harnesses of the rows
	Harnesses []string `gork:"harnesses"`
	// Roles are the roles of the rows
	Roles []string `gork:"roles"`
	// Repositories are the repositories of the rows as "owner/name"
	Repositories []string `gork:"repositories"`
	// Sources are the sources of the rows
	Sources []string `gork:"sources"`
}

// SkillUsage is the skill statistics of a period.
type SkillUsage struct {
	// Totals are the counts of all rows that pass the filters
	Totals SkillTotals `gork:"totals"`
	// Rows are the counts of each skill, source and group, the most used skill first
	Rows []SkillUsageRow `gork:"rows"`
	// Options are the values that each filter can take, before the filters
	Options SkillUsageOptions `gork:"options"`
}

// SkillUsageQuery is the query of GetUsageSkills.
type SkillUsageQuery struct {
	// From is the start of the period. A row is in the period when its time is From or later
	From time.Time `gork:"from" validate:"required"`
	// To is the end of the period. A row is in the period when its time is before To
	To time.Time `gork:"to" validate:"required"`
	// Harness limits the rows to these Harnesses
	Harness []string `gork:"harness"`
	// Role limits the rows to these roles
	Role []string `gork:"role"`
	// Repository limits the rows to these repositories as "owner/name"
	Repository []string `gork:"repository"`
	// Source limits the rows to these sources. A copied skill has the source "mobius"
	Source []string `gork:"source"`
	// Group is the value that splits the rows. With no Group, all rows are in one group
	Group string `gork:"group" validate:"omitempty,oneof=harness role repository source"`
}

// GetUsageSkillsRequest is the request of GetUsageSkills.
type GetUsageSkillsRequest struct {
	Query SkillUsageQuery
}

// GetUsageSkillsResponse is the response of GetUsageSkills.
type GetUsageSkillsResponse struct {
	Body Envelope[SkillUsage]
}

type skillRow struct {
	session    int64
	repository string
	role       string
	harness    string
	skill      string
	source     string
	used       bool
}

type skillKey struct {
	skill  string
	source string
	group  string
}

type skillSum struct {
	uses int64
	had  map[int64]bool
	used map[int64]bool
}

// GetUsageSkills returns the skills that the sessions used or got in the period.
func (h *handlers) GetUsageSkills(ctx context.Context, req GetUsageSkillsRequest) (*GetUsageSkillsResponse, error) {
	query := req.Query
	after := query.From.Add(-time.Second).UTC().Format(utcSecond)
	before := query.To.Add(time.Second).UTC().Format(utcSecond)
	uses, err := h.queries.ListSkillUsesBetween(ctx, store.ListSkillUsesBetweenParams{After: after, Before: before})
	if err != nil {
		return nil, err
	}
	copied, err := h.queries.ListSessionSkillsBetween(ctx, store.ListSessionSkillsBetweenParams{After: after, Before: before})
	if err != nil {
		return nil, err
	}

	rows := make([]skillRow, 0, len(uses)+len(copied))
	times := make([]string, 0, len(uses)+len(copied))
	for _, use := range uses {
		rows = append(rows, skillRow{
			session: use.Session, repository: use.Repository, role: use.Role, harness: use.Harness, skill: use.Skill,
			source: use.Source, used: true,
		})
		times = append(times, use.UsedAt)
	}
	for _, skill := range copied {
		rows = append(rows, skillRow{
			session: skill.Session, repository: skill.Repository, role: skill.Role, harness: skill.Harness,
			skill: skill.Skill, source: "mobius",
		})
		times = append(times, skill.CopiedAt)
	}

	var (
		options  SkillUsageOptions
		skills   = map[string]bool{}
		sessions = map[int64]bool{}
		totals   SkillTotals
		sums     = map[skillKey]*skillSum{}
	)
	for i, row := range rows {
		at, err := time.Parse(time.RFC3339Nano, times[i])
		if err != nil {
			return nil, err
		}
		if at.Before(query.From) || !at.Before(query.To) {
			continue
		}
		options.Harnesses = append(options.Harnesses, row.harness)
		options.Roles = append(options.Roles, row.role)
		options.Repositories = append(options.Repositories, row.repository)
		options.Sources = append(options.Sources, row.source)
		if !passes(query.Harness, row.harness) || !passes(query.Role, row.role) ||
			!passes(query.Repository, row.repository) || !passes(query.Source, row.source) {
			continue
		}
		key := skillKey{skill: row.skill, source: row.source, group: skillGroup(row, query.Group)}
		if sums[key] == nil {
			sums[key] = &skillSum{had: map[int64]bool{}, used: map[int64]bool{}}
		}
		skills[row.skill] = true
		if row.used {
			totals.Uses++
			sessions[row.session] = true
			sums[key].uses++
			sums[key].used[row.session] = true
		} else {
			sums[key].had[row.session] = true
		}
	}

	totals.Skills = int64(len(skills))
	totals.Sessions = int64(len(sessions))
	usage := SkillUsage{Totals: totals, Rows: []SkillUsageRow{}, Options: SkillUsageOptions{
		Harnesses:    distinct(options.Harnesses),
		Roles:        distinct(options.Roles),
		Repositories: distinct(options.Repositories),
		Sources:      distinct(options.Sources),
	}}
	for key, sum := range sums {
		usage.Rows = append(usage.Rows, SkillUsageRow{
			Skill: key.skill, Source: key.source, Group: key.group, Uses: sum.uses, Had: int64(len(sum.had)),
			Used: int64(len(sum.used)),
		})
	}
	slices.SortFunc(usage.Rows, func(a, b SkillUsageRow) int {
		return cmp.Or(
			cmp.Compare(b.Uses, a.Uses), cmp.Compare(b.Had, a.Had), cmp.Compare(a.Skill, b.Skill),
			cmp.Compare(a.Source, b.Source), cmp.Compare(a.Group, b.Group),
		)
	})
	return &GetUsageSkillsResponse{Body: Envelope[SkillUsage]{Data: usage}}, nil
}

func skillGroup(row skillRow, group string) string {
	switch group {
	case "harness":
		return row.harness
	case "role":
		return row.role
	case "repository":
		return row.repository
	case "source":
		return row.source
	default:
		return ""
	}
}
