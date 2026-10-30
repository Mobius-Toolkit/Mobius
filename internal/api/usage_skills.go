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
	// Group is the value of each selected field, in the order of the fields of the route. With no field it is empty
	Group []string `gork:"group"`
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
	// Group is the list of fields that splits the rows. With no Group, all rows are in one group
	Group []string `gork:"group" validate:"omitempty,dive,oneof=harness role repository source"`
}

// GetUsageSkillsRequest is the request of GetUsageSkills.
type GetUsageSkillsRequest struct {
	Query SkillUsageQuery
}

// GetUsageSkillsResponse is the response of GetUsageSkills.
type GetUsageSkillsResponse struct {
	Body Envelope[SkillUsage]
}

type skillKey struct {
	skill  string
	source string
	group  groupKey
}

type skillSum struct {
	uses int64
	had  map[int64]bool
	used map[int64]bool
}

// GetUsageSkills returns the skills that the sessions used or got in the period.
func (h *handlers) GetUsageSkills(ctx context.Context, req GetUsageSkillsRequest) (*GetUsageSkillsResponse, error) {
	query := req.Query
	rows, err := h.queries.ListSkillRowsBetween(ctx, query.From, query.To)
	if err != nil {
		return nil, err
	}

	var (
		options     SkillUsageOptions
		skills      = map[string]bool{}
		sessions    = map[int64]bool{}
		totals      SkillTotals
		sums        = map[skillKey]*skillSum{}
		groupValues = map[groupKey][]string{}
	)
	for _, row := range rows {
		options.Harnesses = append(options.Harnesses, row.Harness)
		options.Roles = append(options.Roles, row.Role)
		options.Repositories = append(options.Repositories, row.Repository)
		options.Sources = append(options.Sources, row.Source)
		if !passes(query.Harness, row.Harness) || !passes(query.Role, row.Role) ||
			!passes(query.Repository, row.Repository) || !passes(query.Source, row.Source) {
			continue
		}
		group := skillGroupValues(row, query.Group)
		encoded := keyOfGroup(group)
		key := skillKey{skill: row.Skill, source: row.Source, group: encoded}
		if sums[key] == nil {
			sums[key] = &skillSum{had: map[int64]bool{}, used: map[int64]bool{}}
		}
		groupValues[encoded] = group
		skills[row.Skill] = true
		if row.Used {
			totals.Uses++
			sessions[row.Session] = true
			sums[key].uses++
			sums[key].used[row.Session] = true
		} else {
			sums[key].had[row.Session] = true
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
			Skill: key.skill, Source: key.source, Group: groupValues[key.group], Uses: sum.uses,
			Had: int64(len(sum.had)), Used: int64(len(sum.used)),
		})
	}
	slices.SortFunc(usage.Rows, func(a, b SkillUsageRow) int {
		return cmp.Or(
			cmp.Compare(b.Uses, a.Uses), cmp.Compare(b.Had, a.Had), cmp.Compare(a.Skill, b.Skill),
			cmp.Compare(a.Source, b.Source), slices.Compare(a.Group, b.Group),
		)
	})
	return &GetUsageSkillsResponse{Body: Envelope[SkillUsage]{Data: usage}}, nil
}

var skillGroupFields = []string{"harness", "role", "repository", "source"}

func skillGroupValues(row store.SkillRow, fields []string) []string {
	values := make([]string, 0, len(fields))
	for _, field := range selectedFields(fields, skillGroupFields) {
		switch field {
		case "harness":
			values = append(values, row.Harness)
		case "role":
			values = append(values, row.Role)
		case "repository":
			values = append(values, row.Repository)
		case "source":
			values = append(values, row.Source)
		}
	}
	return values
}
