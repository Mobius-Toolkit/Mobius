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
		Harnesses     []string `json:"harnesses"`
		Models        []string `json:"models"`
		ModelVersions []string `json:"modelVersions"`
		Efforts       []string `json:"efforts"`
		Roles         []string `json:"roles"`
		Repositories  []string `json:"repositories"`
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
		{"model", "model=opus", 1, 150},
		{"two models", "model=swe&model=sonnet", 2, 27},
		{"model does not match the reported model", "model=claude-opus-5", 0, 0},
		{"model version", "modelVersion=claude-opus-5", 1, 150},
		{"model version does not match the model", "modelVersion=opus", 0, 0},
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
		{"model", "model", []string{"opus", "sonnet", "swe"}},
		{"model version", "modelVersion", []string{"", "claude-opus-5"}},
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
		"harnesses":     {"claude-code", "devin"},
		"models":        {"opus", "sonnet", "swe"},
		"modelVersions": {"claude-opus-5"},
		"efforts":       {"high", "low"},
		"roles":         {"Implementer", "Reviewer"},
		"repositories":  {"o/a", "o/b"},
	}
	got := map[string][]string{
		"harnesses":     body.Options.Harnesses,
		"models":        body.Options.Models,
		"modelVersions": body.Options.ModelVersions,
		"efforts":       body.Options.Efforts,
		"roles":         body.Options.Roles,
		"repositories":  body.Options.Repositories,
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

type timeStat struct {
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	Count        int64   `json:"count"`
	TotalSeconds float64 `json:"totalSeconds"`
	MeanSeconds  float64 `json:"meanSeconds"`
	Results      []struct {
		Result string `json:"result"`
		Count  int64  `json:"count"`
	} `json:"results"`
}

type usageTimesBody struct {
	Totals []timeStat `json:"totals"`
	Days   []struct {
		Day   string     `json:"day"`
		Group string     `json:"group"`
		Stats []timeStat `json:"stats"`
	} `json:"days"`
	Groups []struct {
		Group string     `json:"group"`
		Stats []timeStat `json:"stats"`
	} `json:"groups"`
	Options struct {
		Harnesses     []string `json:"harnesses"`
		Models        []string `json:"models"`
		ModelVersions []string `json:"modelVersions"`
		Efforts       []string `json:"efforts"`
		Roles         []string `json:"roles"`
		Repositories  []string `json:"repositories"`
	} `json:"options"`
}

func seedUsageTimes(t *testing.T) func(query string) (int, usageTimesBody) {
	t.Helper()
	mux, db := testMux(t)
	exec(t, db, `
		INSERT INTO sessions (id, role, harness, model, repository, workstream, started_at, organization) VALUES
			(1, 'Implementer', 'claude-code', 'opus', 'o/a', 1, '2026-10-04T23:00:00Z', 'o'),
			(2, 'Reviewer', 'devin', 'swe', 'o/b', 1, '2026-10-05T12:00:00Z', 'o'),
			(3, 'Reviewer', 'claude-code', 'sonnet', 'o/a', 1, '2026-10-05T13:00:00Z', 'o');
		INSERT INTO turn_usage (session, workstream, organization, repository, role, harness, model, reported_model,
			started_at, ended_at) VALUES
			(1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'claude-opus-4', '2026-10-04T23:30:00Z', '2026-10-04T23:40:00Z'),
			(1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'claude-opus-5', '2026-10-05T00:10:00Z', '2026-10-05T00:20:00Z'),
			(1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', NULL, '2026-10-05T00:30:00Z', '2026-10-05T00:40:00Z'),
			(3, 1, 'o', 'o/a', 'Reviewer', 'claude-code', 'sonnet', NULL, '2026-10-05T14:00:00Z', '2026-10-05T14:10:00Z');
		INSERT INTO step_times (kind, session, task, issue, workstream, organization, repository, role, harness, model, effort,
			started_at, ended_at, attempt, result) VALUES
			('queue', 1, 1, 1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'high',
				'2026-10-04T23:30:00.5Z', '2026-10-04T23:30:10.5Z', NULL, NULL),
			('check', 1, 1, 1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'high',
				'2026-10-05T00:10:00Z', '2026-10-05T00:12:00Z', 1, 'fail'),
			('check', 1, 1, 1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'high',
				'2026-10-05T00:20:00Z', '2026-10-05T00:21:00Z', 2, NULL),
			('queue', 2, 2, 2, 1, 'o', 'o/b', 'Reviewer', 'devin', 'swe', NULL,
				'2026-10-05T12:00:00Z', '2026-10-05T12:00:30Z', NULL, NULL),
			('queue', 3, 1, 1, 1, 'o', 'o/a', 'Reviewer', 'claude-code', 'sonnet', 'low',
				'2026-10-05T23:59:59.9Z', '2026-10-06T00:00:04.9Z', NULL, NULL),
			('ci', NULL, 1, 1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'high',
				'2026-10-05T10:00:00.000Z', '2026-10-05T10:05:00Z', NULL, 'stopped'),
			('queue', 1, 1, 1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'high',
				'2026-10-06T00:00:00Z', '2026-10-06T00:00:10Z', NULL, NULL),
			('queue', 1, 1, 1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'opus', 'high',
				'2026-10-03T23:59:59.9Z', '2026-10-04T00:00:10Z', NULL, NULL);
		INSERT INTO task_states (task, state, issue, workstream, organization, repository, session, role, harness, model, effort,
			started_at, ended_at) VALUES
			(1, 'working', 1, 1, 'o', 'o/a', 1, 'Implementer', 'claude-code', 'opus', 'high',
				'2026-10-05T00:00:00.000Z', '2026-10-05T00:30:00.000Z'),
			(2, 'approval', 2, 1, 'o', 'o/b', NULL, NULL, NULL, NULL, NULL,
				'2026-10-05T01:00:00.000Z', '2026-10-05T03:00:00.000Z');
	`)
	return func(query string) (int, usageTimesBody) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usage/times?"+query, nil))
		var body struct {
			Data usageTimesBody `json:"data"`
		}
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
		}
		return rec.Code, body.Data
	}
}

func getUsageTimes(t *testing.T, query string) usageTimesBody {
	t.Helper()
	get := seedUsageTimes(t)
	code, body := get(query)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	return body
}

func statCount(stats []timeStat) (total int64) {
	for _, stat := range stats {
		total += stat.Count
	}
	return total
}

func TestGetUsageTimesTotalsCountTheRowsInThePeriod(t *testing.T) {
	body := getUsageTimes(t, usagePeriod)

	type stat struct {
		kind, name string
		count      int64
		total      float64
		mean       float64
	}
	var got []stat
	for _, item := range body.Totals {
		got = append(got, stat{item.Kind, item.Name, item.Count, item.TotalSeconds, item.MeanSeconds})
	}
	want := []stat{
		{"state", "approval", 1, 7200, 7200},
		{"state", "working", 1, 1800, 1800},
		{"step", "check", 2, 180, 90},
		{"step", "ci", 1, 300, 300},
		{"step", "queue", 3, 45, 15},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("totals = %v, want %v", got, want)
	}
}

func TestGetUsageTimesResultsCountNullAsSuccess(t *testing.T) {
	body := getUsageTimes(t, usagePeriod)

	type result struct {
		result string
		count  int64
	}
	got := map[string][]result{}
	for _, item := range body.Totals {
		results := []result{}
		for _, r := range item.Results {
			results = append(results, result{r.Result, r.Count})
		}
		got[item.Name] = results
	}
	want := map[string][]result{
		"approval": {},
		"working":  {},
		"check":    {{"fail", 1}, {"success", 1}},
		"ci":       {{"stopped", 1}},
		"queue":    {{"success", 3}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("results = %v, want %v", got, want)
	}
}

func TestGetUsageTimesFilters(t *testing.T) {
	tests := []struct {
		name  string
		query string
		rows  int64
	}{
		{"no filter", "", 8},
		{"harness", "harness=devin", 1},
		{"harness excludes a state with no session", "harness=claude-code", 6},
		{"model", "model=opus", 5},
		{"two models", "model=swe&model=sonnet", 2},
		{"model does not match the reported model", "model=claude-opus-5", 0},
		{"model version of a session", "modelVersion=claude-opus-5", 4},
		{"model version does not match the model", "modelVersion=opus", 0},
		{"effort", "effort=high", 5},
		{"role", "role=Reviewer", 2},
		{"repository", "repository=o/b", 2},
		{"filters together", "role=Reviewer&repository=o/a", 1},
		{"no match", "harness=antigravity", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := getUsageTimes(t, usagePeriod+"&"+tt.query)

			if got := statCount(body.Totals); got != tt.rows {
				t.Errorf("rows = %d, want %d", got, tt.rows)
			}
		})
	}
}

func TestGetUsageTimesGroups(t *testing.T) {
	tests := []struct {
		name  string
		group string
		want  []string
	}{
		{"harness", "harness", []string{"", "claude-code", "devin"}},
		{"model", "model", []string{"", "opus", "sonnet", "swe"}},
		{"model version", "modelVersion", []string{"", "claude-opus-5"}},
		{"effort", "effort", []string{"", "high", "low"}},
		{"role", "role", []string{"", "Implementer", "Reviewer"}},
		{"repository", "repository", []string{"o/a", "o/b"}},
		{"no group", "", []string{""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := getUsageTimes(t, usagePeriod+"&group="+tt.group)

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

func TestGetUsageTimesModelVersionOfAStepWithNoTurn(t *testing.T) {
	body := getUsageTimes(t, usagePeriod+"&group=modelVersion")

	counts := map[string]int64{}
	for _, group := range body.Groups {
		counts[group.Group] = statCount(group.Stats)
	}
	want := map[string]int64{"": 4, "claude-opus-5": 4}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("rows of the groups = %v, want %v", counts, want)
	}
}

func TestGetUsageTimesDaysInUTC(t *testing.T) {
	body := getUsageTimes(t, usagePeriod)

	counts := map[string]int64{}
	for _, item := range body.Days {
		counts[item.Day] = statCount(item.Stats)
	}
	want := map[string]int64{"2026-10-04": 1, "2026-10-05": 7}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("rows of the days = %v, want %v", counts, want)
	}
	if first := body.Days[0]; first.Stats[0].Name != "queue" || first.Stats[0].TotalSeconds != 10 {
		t.Errorf("first day = %+v, want one queue step of 10 seconds", first)
	}
}

func TestGetUsageTimesDaysInATimeZone(t *testing.T) {
	body := getUsageTimes(t, usagePeriod+"&tz=Europe/Berlin")

	counts := map[string]int64{}
	for _, item := range body.Days {
		counts[item.Day] = statCount(item.Stats)
	}
	want := map[string]int64{"2026-10-05": 7, "2026-10-06": 1}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("rows of the days = %v, want %v", counts, want)
	}
}

func TestGetUsageTimesOptionsIgnoreTheFilters(t *testing.T) {
	body := getUsageTimes(t, usagePeriod+"&harness=devin")

	got := map[string][]string{
		"harnesses":     body.Options.Harnesses,
		"models":        body.Options.Models,
		"modelVersions": body.Options.ModelVersions,
		"efforts":       body.Options.Efforts,
		"roles":         body.Options.Roles,
		"repositories":  body.Options.Repositories,
	}
	want := map[string][]string{
		"harnesses":     {"claude-code", "devin"},
		"models":        {"opus", "sonnet", "swe"},
		"modelVersions": {"claude-opus-5"},
		"efforts":       {"high", "low"},
		"roles":         {"Implementer", "Reviewer"},
		"repositories":  {"o/a", "o/b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("options = %v, want %v", got, want)
	}
}

func TestGetUsageTimesRejectsBadRequests(t *testing.T) {
	get := seedUsageTimes(t)
	tests := []struct {
		name  string
		query string
	}{
		{"unknown time zone", usagePeriod + "&tz=Mars/Base"},
		{"unknown group", usagePeriod + "&group=session"},
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
