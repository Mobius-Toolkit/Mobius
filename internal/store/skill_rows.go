package store

import (
	"context"
	"time"
)

// utcSecond is the time layout of a whole second. A time of the layout time.RFC3339Nano has a variable number of
// fraction digits, so a text compare of two times in one second can give the wrong order.
const utcSecond = "2006-01-02T15:04:05Z"

// SkillRow is one skill use, or one copy of a skill into a session.
type SkillRow struct {
	Session      int64
	Organization string
	Repository   string
	Role         string
	Harness      string
	Skill        string
	// Source is "mobius" for a copied skill, and "harness" for each other skill
	Source string
	// Used is true for a skill use, and false for a copy
	Used bool
	At   time.Time
}

// ListSkillRowsBetween gives the skill uses and the skill copies with a time from from, and before to.
func (q *Queries) ListSkillRowsBetween(ctx context.Context, from, to time.Time) ([]SkillRow, error) {
	after := from.Add(-time.Second).UTC().Format(utcSecond)
	before := to.Add(time.Second).UTC().Format(utcSecond)
	uses, err := q.ListSkillUsesBetween(ctx, ListSkillUsesBetweenParams{After: after, Before: before})
	if err != nil {
		return nil, err
	}
	copied, err := q.ListSessionSkillsBetween(ctx, ListSessionSkillsBetweenParams{After: after, Before: before})
	if err != nil {
		return nil, err
	}
	var rows []SkillRow
	for _, use := range uses {
		at, err := time.Parse(time.RFC3339Nano, use.UsedAt)
		if err != nil {
			return nil, err
		}
		if at.Before(from) || !at.Before(to) {
			continue
		}
		rows = append(rows, SkillRow{
			Session: use.Session, Organization: use.Organization, Repository: use.Repository, Role: use.Role,
			Harness: use.Harness, Skill: use.Skill, Source: use.Source, Used: true, At: at,
		})
	}
	for _, skill := range copied {
		at, err := time.Parse(time.RFC3339Nano, skill.CopiedAt)
		if err != nil {
			return nil, err
		}
		if at.Before(from) || !at.Before(to) {
			continue
		}
		rows = append(rows, SkillRow{
			Session: skill.Session, Organization: skill.Organization, Repository: skill.Repository, Role: skill.Role,
			Harness: skill.Harness, Skill: skill.Skill, Source: "mobius", At: at,
		})
	}
	return rows, nil
}
