package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type usageValues struct {
	Sessions         int64    `json:"sessions"`
	InputTokens      *int64   `json:"inputTokens"`
	OutputTokens     *int64   `json:"outputTokens"`
	CacheReadTokens  *int64   `json:"cacheReadTokens"`
	CacheWriteTokens *int64   `json:"cacheWriteTokens"`
	CostUSD          *float64 `json:"costUsd"`
}

type usageBody struct {
	Totals usageValues `json:"totals"`
	Days   []struct {
		Day    string      `json:"day"`
		Group  string      `json:"group"`
		Values usageValues `json:"values"`
	} `json:"days"`
	Groups []struct {
		Group  string      `json:"group"`
		Values usageValues `json:"values"`
	} `json:"groups"`
	Options struct {
		Harnesses    []string `json:"harnesses"`
		Models       []string `json:"models"`
		Efforts      []string `json:"efforts"`
		Roles        []string `json:"roles"`
		Repositories []string `json:"repositories"`
	} `json:"options"`
}

const usagePeriod = "from=2026-10-04T00:00:00Z&to=2026-10-06T00:00:00Z"

func int64Ptr(n int64) *int64 { return &n }

func float64Ptr(f float64) *float64 { return &f }

func seedUsage(t *testing.T) func(query string) (int, usageBody) {
	t.Helper()
	mux, db := testMux(t)
	exec(t, db, `
		INSERT INTO sessions (id, role, harness, model, repository, workstream, started_at, organization) VALUES
			(1, 'Implementer', 'claude-code', 'opus', 'o/a', 1, '2026-10-04T23:00:00Z', 'o'),
			(2, 'Reviewer', 'devin', 'swe', 'o/b', 1, '2026-10-05T12:00:00Z', 'o'),
			(3, 'Reviewer', 'claude-code', 'sonnet', 'o/a', 1, '2026-10-05T13:00:00Z', 'o');
		INSERT INTO turn_usage (session, workstream, organization, repository, role, harness, model, reported_model, effort,
			started_at, ended_at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd) VALUES
			(1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'claude-opus-5', 'high',
				'2026-10-04T23:30:00.5Z', '2026-10-04T23:40:00Z', 100, 10, 1000, 5, 1.5),
			(1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'claude-opus-5', 'high',
				'2026-10-05T00:10:00Z', '2026-10-05T00:20:00Z', 50, 5, NULL, NULL, 0.5),
			(2, 1, 'o', 'o/b', 'Reviewer', 'devin', 'swe', NULL, NULL,
				'2026-10-05T12:00:00Z', '2026-10-05T12:10:00Z', 20, 2, NULL, NULL, NULL),
			(3, 1, 'o', 'o/a', 'Reviewer', 'claude-code', 'sonnet', NULL, 'low',
				'2026-10-05T23:59:59.9Z', '2026-10-06T00:10:00Z', 7, 1, NULL, NULL, 0.25),
			(3, 1, 'o', 'o/a', 'Reviewer', 'claude-code', 'sonnet', NULL, 'low',
				'2026-10-06T00:00:00Z', '2026-10-06T00:10:00Z', 1000, 1000, NULL, NULL, 100),
			(3, 1, 'o', 'o/a', 'Reviewer', 'claude-code', 'sonnet', NULL, 'low',
				'2026-10-03T23:59:59.9Z', '2026-10-04T00:10:00Z', 1000, 1000, NULL, NULL, 100);
	`)
	return func(query string) (int, usageBody) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usage?"+query, nil))
		var body struct {
			Data usageBody `json:"data"`
		}
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
		}
		return rec.Code, body.Data
	}
}

func getUsage(t *testing.T, query string) usageBody {
	t.Helper()
	get := seedUsage(t)
	code, body := get(query)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	return body
}

func TestGetUsageTotalsCountDistinctSessionsInThePeriod(t *testing.T) {
	body := getUsage(t, usagePeriod)

	want := usageValues{
		Sessions:         3,
		InputTokens:      int64Ptr(177),
		OutputTokens:     int64Ptr(18),
		CacheReadTokens:  int64Ptr(1000),
		CacheWriteTokens: int64Ptr(5),
		CostUSD:          float64Ptr(2.25),
	}
	if !reflect.DeepEqual(body.Totals, want) {
		t.Errorf("totals = %+v, want %+v", body.Totals, want)
	}
}

func TestGetUsageFiltersTakeSeveralValues(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		sessions int64
		input    int64
	}{
		{"two harnesses", "harness=devin&harness=claude-code", 3, 177},
		{"one harness", "harness=devin", 1, 20},
		{"reported model", "model=claude-opus-5", 1, 150},
		{"config model when none is reported", "model=swe&model=sonnet", 2, 27},
		{"effort", "effort=low", 1, 7},
		{"role", "role=Reviewer", 2, 27},
		{"repository", "repository=o/b", 1, 20},
		{"filters together", "role=Reviewer&repository=o/a", 1, 7},
		{"no match", "harness=antigravity", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := getUsage(t, usagePeriod+"&"+tt.query)

			if body.Totals.Sessions != tt.sessions {
				t.Errorf("sessions = %d, want %d", body.Totals.Sessions, tt.sessions)
			}
			var input int64
			if body.Totals.InputTokens != nil {
				input = *body.Totals.InputTokens
			}
			if input != tt.input {
				t.Errorf("input tokens = %d, want %d", input, tt.input)
			}
		})
	}
}

func TestGetUsageGroups(t *testing.T) {
	tests := []struct {
		name  string
		group string
		want  []string
	}{
		{"harness", "harness", []string{"claude-code", "devin"}},
		{"model", "model", []string{"claude-opus-5", "sonnet", "swe"}},
		{"effort", "effort", []string{"", "high", "low"}},
		{"role", "role", []string{"Implementer", "Reviewer"}},
		{"repository", "repository", []string{"o/a", "o/b"}},
		{"no group", "", []string{""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := getUsage(t, usagePeriod+"&group="+tt.group)

			var got []string
			for _, group := range body.Groups {
				got = append(got, group.Group)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("groups = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetUsageGroupValues(t *testing.T) {
	body := getUsage(t, usagePeriod+"&group=harness")

	want := map[string]usageValues{
		"claude-code": {Sessions: 2, InputTokens: int64Ptr(157), OutputTokens: int64Ptr(16), CacheReadTokens: int64Ptr(1000), CacheWriteTokens: int64Ptr(5), CostUSD: float64Ptr(2.25)},
		"devin":       {Sessions: 1, InputTokens: int64Ptr(20), OutputTokens: int64Ptr(2)},
	}
	for _, group := range body.Groups {
		if !reflect.DeepEqual(group.Values, want[group.Group]) {
			t.Errorf("group %s = %+v, want %+v", group.Group, group.Values, want[group.Group])
		}
	}
}

func TestGetUsageDaysInUTC(t *testing.T) {
	body := getUsage(t, usagePeriod+"&group=harness")

	type day struct{ day, group string }
	var got []day
	for _, item := range body.Days {
		got = append(got, day{item.Day, item.Group})
	}
	want := []day{
		{"2026-10-04", "claude-code"},
		{"2026-10-05", "claude-code"},
		{"2026-10-05", "devin"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("days = %v, want %v", got, want)
	}
	if first := body.Days[0].Values; first.Sessions != 1 || *first.InputTokens != 100 {
		t.Errorf("first day = %+v, want 1 session and 100 input tokens", first)
	}
}

func TestGetUsageDaysInATimeZone(t *testing.T) {
	body := getUsage(t, usagePeriod+"&tz=Europe/Berlin&group=harness")

	type day struct {
		day, group string
		input      int64
	}
	var got []day
	for _, item := range body.Days {
		got = append(got, day{item.Day, item.Group, *item.Values.InputTokens})
	}
	want := []day{
		{"2026-10-05", "claude-code", 150},
		{"2026-10-05", "devin", 20},
		{"2026-10-06", "claude-code", 7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("days = %v, want %v", got, want)
	}
}

func TestGetUsageSumWithOnlyNullValuesIsNull(t *testing.T) {
	body := getUsage(t, usagePeriod+"&harness=devin")

	if body.Totals.CostUSD != nil || body.Totals.CacheReadTokens != nil || body.Totals.CacheWriteTokens != nil {
		t.Errorf("totals = %+v, want a null cost and null cache tokens", body.Totals)
	}
	if body.Days[0].Values.CostUSD != nil || body.Groups[0].Values.CostUSD != nil {
		t.Errorf("day %+v and group %+v, want a null cost", body.Days[0].Values, body.Groups[0].Values)
	}
}

func TestGetUsageOptionsIgnoreTheFilters(t *testing.T) {
	body := getUsage(t, usagePeriod+"&harness=devin")

	want := map[string][]string{
		"harnesses":    {"claude-code", "devin"},
		"models":       {"claude-opus-5", "sonnet", "swe"},
		"efforts":      {"high", "low"},
		"roles":        {"Implementer", "Reviewer"},
		"repositories": {"o/a", "o/b"},
	}
	got := map[string][]string{
		"harnesses":    body.Options.Harnesses,
		"models":       body.Options.Models,
		"efforts":      body.Options.Efforts,
		"roles":        body.Options.Roles,
		"repositories": body.Options.Repositories,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("options = %v, want %v", got, want)
	}
}

func TestGetUsageRejectsBadRequests(t *testing.T) {
	get := seedUsage(t)
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"unknown time zone", usagePeriod + "&tz=Mars/Base", http.StatusBadRequest},
		{"unknown group", usagePeriod + "&group=session", http.StatusBadRequest},
		{"no period", "", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _ := get(tt.query); code != tt.want {
				t.Errorf("status = %d, want %d", code, tt.want)
			}
		})
	}
}
