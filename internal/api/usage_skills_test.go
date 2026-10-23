package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type skillRowBody struct {
	Skill  string `json:"skill"`
	Source string `json:"source"`
	Group  string `json:"group"`
	Uses   int64  `json:"uses"`
	Had    int64  `json:"had"`
	Used   int64  `json:"used"`
}

type usageSkillsBody struct {
	Totals struct {
		Uses     int64 `json:"uses"`
		Skills   int64 `json:"skills"`
		Sessions int64 `json:"sessions"`
	} `json:"totals"`
	Rows    []skillRowBody `json:"rows"`
	Options struct {
		Harnesses    []string `json:"harnesses"`
		Roles        []string `json:"roles"`
		Repositories []string `json:"repositories"`
		Sources      []string `json:"sources"`
	} `json:"options"`
}

func seedUsageSkills(t *testing.T) func(query string) (int, usageSkillsBody) {
	t.Helper()
	mux, db := testMux(t)
	exec(t, db, `
		INSERT INTO sessions (id, role, harness, model, repository, workstream, started_at, organization) VALUES
			(1, 'Implementer', 'claude-code', 'opus', 'o/a', 1, '2026-10-04T23:00:00Z', 'o'),
			(2, 'Reviewer', 'claude-code', 'opus', 'o/a', 1, '2026-10-05T12:00:00Z', 'o'),
			(3, 'Reviewer', 'devin', 'swe', 'o/b', 1, '2026-10-05T13:00:00Z', 'o'),
			(4, 'Judge', 'other', 'swe', 'o/other', 1, '2026-10-03T13:00:00Z', 'o');
		INSERT INTO session_skills (session, organization, repository, role, harness, skill, copied_at) VALUES
			(1, 'o', 'o/a', 'Implementer', 'claude-code', 'review-diff', '2026-10-04T23:00:00.5Z'),
			(1, 'o', 'o/a', 'Implementer', 'claude-code', 'commit', '2026-10-04T23:00:00.5Z'),
			(2, 'o', 'o/a', 'Reviewer', 'claude-code', 'review-diff', '2026-10-05T12:00:00Z'),
			(4, 'o', 'o/other', 'Judge', 'other', 'old-copy', '2026-10-03T23:59:59.9Z'),
			(4, 'o', 'o/other', 'Judge', 'other', 'edge-copy', '2026-10-06T00:00:00Z');
		INSERT INTO skill_uses (session, organization, repository, role, harness, skill, source, used_at) VALUES
			(1, 'o', 'o/a', 'Implementer', 'claude-code', 'review-diff', 'mobius', '2026-10-05T00:10:00Z'),
			(1, 'o', 'o/a', 'Implementer', 'claude-code', 'review-diff', 'mobius', '2026-10-05T00:20:00Z'),
			(2, 'o', 'o/a', 'Reviewer', 'claude-code', 'review-diff', 'mobius', '2026-10-05T23:59:59.9Z'),
			(3, 'o', 'o/b', 'Reviewer', 'devin', 'frontend-design', 'harness', '2026-10-05T13:30:00Z'),
			(4, 'o', 'o/other', 'Judge', 'other', 'old', 'harness', '2026-10-03T23:59:59.9Z'),
			(4, 'o', 'o/other', 'Judge', 'other', 'edge', 'harness', '2026-10-06T00:00:00Z');
	`)
	return func(query string) (int, usageSkillsBody) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usage/skills?"+query, nil))
		var body struct {
			Data usageSkillsBody `json:"data"`
		}
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
		}
		return rec.Code, body.Data
	}
}

func getUsageSkills(t *testing.T, query string) usageSkillsBody {
	t.Helper()
	get := seedUsageSkills(t)
	code, body := get(query)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	return body
}

func TestGetUsageSkillsCountsEachSkillInThePeriod(t *testing.T) {
	body := getUsageSkills(t, usagePeriod)

	want := []skillRowBody{
		{Skill: "review-diff", Source: "mobius", Uses: 3, Had: 2, Used: 2},
		{Skill: "frontend-design", Source: "harness", Uses: 1, Had: 0, Used: 1},
		{Skill: "commit", Source: "mobius", Uses: 0, Had: 1, Used: 0},
	}
	if !reflect.DeepEqual(body.Rows, want) {
		t.Errorf("rows = %+v, want %+v", body.Rows, want)
	}
}

func TestGetUsageSkillsTotals(t *testing.T) {
	body := getUsageSkills(t, usagePeriod)

	if body.Totals.Uses != 4 || body.Totals.Skills != 3 || body.Totals.Sessions != 3 {
		t.Errorf("totals = %+v, want uses 4, skills 3, sessions 3", body.Totals)
	}
}

func TestGetUsageSkillsFilters(t *testing.T) {
	tests := []struct {
		name   string
		filter string
		want   []string
	}{
		{"harness", "harness=devin", []string{"frontend-design"}},
		{"role", "role=Implementer", []string{"review-diff", "commit"}},
		{"repository", "repository=o/b", []string{"frontend-design"}},
		{"source mobius", "source=mobius", []string{"review-diff", "commit"}},
		{"several values", "harness=devin&harness=claude-code", []string{"review-diff", "frontend-design", "commit"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := getUsageSkills(t, usagePeriod+"&"+tt.filter)

			var got []string
			for _, row := range body.Rows {
				got = append(got, row.Skill)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("skills = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetUsageSkillsHarnessSourceRemovesTheCopiedSkills(t *testing.T) {
	body := getUsageSkills(t, usagePeriod+"&source=harness")

	want := []skillRowBody{{Skill: "frontend-design", Source: "harness", Uses: 1, Had: 0, Used: 1}}
	if !reflect.DeepEqual(body.Rows, want) {
		t.Errorf("rows = %+v, want %+v", body.Rows, want)
	}
	if body.Totals.Uses != 1 || body.Totals.Skills != 1 || body.Totals.Sessions != 1 {
		t.Errorf("totals = %+v, want 1 use, 1 skill, 1 session", body.Totals)
	}
}

func TestGetUsageSkillsGroupByRoleSplitsASkill(t *testing.T) {
	body := getUsageSkills(t, usagePeriod+"&group=role")

	want := []skillRowBody{
		{Skill: "review-diff", Source: "mobius", Group: "Implementer", Uses: 2, Had: 1, Used: 1},
		{Skill: "review-diff", Source: "mobius", Group: "Reviewer", Uses: 1, Had: 1, Used: 1},
		{Skill: "frontend-design", Source: "harness", Group: "Reviewer", Uses: 1, Had: 0, Used: 1},
		{Skill: "commit", Source: "mobius", Group: "Implementer", Uses: 0, Had: 1, Used: 0},
	}
	if !reflect.DeepEqual(body.Rows, want) {
		t.Errorf("rows = %+v, want %+v", body.Rows, want)
	}
}

func TestGetUsageSkillsGroupBySourceGivesTheSource(t *testing.T) {
	body := getUsageSkills(t, usagePeriod+"&group=source")

	for _, row := range body.Rows {
		if row.Group != row.Source {
			t.Errorf("row %+v: group is not the source", row)
		}
	}
}

func TestGetUsageSkillsOptionsIgnoreTheFiltersAndTheOtherPeriods(t *testing.T) {
	body := getUsageSkills(t, usagePeriod+"&harness=devin&source=harness")

	got := body.Options
	if !reflect.DeepEqual(got.Harnesses, []string{"claude-code", "devin"}) ||
		!reflect.DeepEqual(got.Roles, []string{"Implementer", "Reviewer"}) ||
		!reflect.DeepEqual(got.Repositories, []string{"o/a", "o/b"}) ||
		!reflect.DeepEqual(got.Sources, []string{"harness", "mobius"}) {
		t.Errorf("options = %+v", got)
	}
}

func TestGetUsageSkillsRejectsBadRequests(t *testing.T) {
	get := seedUsageSkills(t)
	tests := []struct {
		name  string
		query string
	}{
		{"unknown group", usagePeriod + "&group=skill"},
		{"no period", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _ := get(tt.query); code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
			}
		})
	}
}
