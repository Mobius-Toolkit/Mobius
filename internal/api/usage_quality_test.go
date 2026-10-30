package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type qualityValues struct {
	Tasks            int64    `json:"tasks"`
	Merged           int64    `json:"merged"`
	ReviewRounds     float64  `json:"reviewRounds"`
	FixRounds        float64  `json:"fixRounds"`
	NeedsHuman       float64  `json:"needsHuman"`
	Questions        float64  `json:"questions"`
	WorkSeconds      *float64 `json:"workSeconds"`
	InputTokens      *float64 `json:"inputTokens"`
	OutputTokens     *float64 `json:"outputTokens"`
	CacheReadTokens  *float64 `json:"cacheReadTokens"`
	CacheWriteTokens *float64 `json:"cacheWriteTokens"`
	CostUSD          *float64 `json:"costUsd"`
}

type qualityDay struct {
	Day    string        `json:"day"`
	Group  []string      `json:"group"`
	Values qualityValues `json:"values"`
}

type qualityGroup struct {
	Group  []string      `json:"group"`
	Values qualityValues `json:"values"`
}

type qualityBody struct {
	Totals  qualityValues  `json:"totals"`
	Days    []qualityDay   `json:"days"`
	Groups  []qualityGroup `json:"groups"`
	Options struct {
		Harnesses    []string `json:"harnesses"`
		Models       []string `json:"models"`
		Efforts      []string `json:"efforts"`
		Repositories []string `json:"repositories"`
	} `json:"options"`
}

func seedUsageQuality(t *testing.T) func(query string) (int, qualityBody) {
	t.Helper()
	mux, db := testMux(t)
	exec(t, db, `
		INSERT INTO tasks (id, repository, workstream_repository, issue, workstream, state, dispatched_at) VALUES
			(1, 'o/a', 'o/a', 1, 1, 'ended', '2026-10-03T23:00:00Z'),
			(2, 'o/a', 'o/a', 2, 1, 'ended', '2026-10-03T20:00:00Z'),
			(3, 'o/a', 'o/a', 3, 1, 'ended', '2026-10-05T23:00:00Z'),
			(4, 'o/b', 'o/b', 1, 1, 'ended', '2026-10-05T22:00:00Z'),
			(5, 'o/a', 'o/a', 4, 1, 'ended', '2026-10-03T22:00:00Z'),
			(6, 'o/a', 'o/a', 5, 1, 'checks', '2026-10-04T10:00:00Z');
		INSERT INTO sessions (id, role, harness, model, repository, workstream, issue, started_at, organization, end_reason) VALUES
			(1, 'implementer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-02T10:00:00Z', 'o', 'done'),
			(2, 'reviewer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-04T06:00:00Z', 'o', 'done'),
			(3, 'reviewer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-04T07:00:00Z', 'o', 'done'),
			(4, 'reviewer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-04T08:00:00Z', 'o', 'restart'),
			(5, 'reviewer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-04T09:00:00Z', 'o', 'failed'),
			(6, 'implementer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-04T10:00:00Z', 'o', 'done'),
			(7, 'implementer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-04T11:00:00Z', 'o', 'done'),
			(8, 'implementer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-04T12:00:00Z', 'o', 'done'),
			(9, 'implementer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-04T13:00:00Z', 'o', 'restart'),
			(10, 'reviewer', 'devin', 'swe', 'o/b', 1, 1, '2026-10-04T14:00:00Z', 'o', 'done'),
			(11, 'reviewer', 'claude-code', 'sonnet', 'o/a', 1, 1, '2026-10-05T02:00:00Z', 'o', 'done'),
			(12, 'implementer', 'claude-code', 'opus', 'o/a', 1, 2, '2026-10-04T21:30:00Z', 'o', 'done');
		INSERT INTO task_states (task, state, issue, workstream, organization, repository, session, role, harness, model, effort,
			started_at, ended_at) VALUES
			(1, 'dispatched', 1, 1, 'o', 'o/a', NULL, NULL, NULL, NULL, NULL,
				'2026-10-03T23:00:00Z', '2026-10-04T00:30:00Z'),
			(1, 'working', 1, 1, 'o', 'o/a', 6, 'implementer', 'claude-code', 'opus', 'high',
				'2026-10-04T00:30:00Z', '2026-10-04T01:00:00Z'),
			(1, 'needs_human', 1, 1, 'o', 'o/a', NULL, NULL, NULL, NULL, NULL,
				'2026-10-04T01:00:00Z', '2026-10-04T02:00:00Z'),
			(1, 'asked', 1, 1, 'o', 'o/a', NULL, NULL, NULL, NULL, NULL,
				'2026-10-04T02:00:00Z', '2026-10-04T03:00:00Z'),
			(1, 'needs_human', 1, 1, 'o', 'o/a', NULL, NULL, NULL, NULL, NULL,
				'2026-10-04T03:00:00Z', '2026-10-04T04:00:00Z'),
			(1, 'approval', 1, 1, 'o', 'o/a', NULL, NULL, NULL, NULL, NULL,
				'2026-10-04T04:00:00Z', '2026-10-05T01:00:00Z'),
			(2, 'dispatched', 2, 1, 'o', 'o/a', NULL, NULL, NULL, NULL, NULL,
				'2026-10-03T20:00:00Z', '2026-10-04T20:00:00Z'),
			(2, 'working', 2, 1, 'o', 'o/a', 12, 'implementer', 'claude-code', 'sonnet', 'high',
				'2026-10-04T20:00:00Z', '2026-10-04T21:00:00Z'),
			(2, 'working', 2, 1, 'o', 'o/a', 12, 'implementer', 'dsh', 'deepseek-account/deepseek-flash', 'high',
				'2026-10-04T21:00:00Z', '2026-10-04T22:00:00Z'),
			(3, 'dispatched', 3, 1, 'o', 'o/a', NULL, NULL, NULL, NULL, NULL,
				'2026-10-05T23:00:00Z', '2026-10-05T23:30:00Z'),
			(4, 'dispatched', 1, 1, 'o', 'o/b', NULL, NULL, NULL, NULL, NULL,
				'2026-10-05T22:00:00Z', '2026-10-06T00:00:00Z'),
			(5, 'dispatched', 4, 1, 'o', 'o/a', NULL, NULL, NULL, NULL, NULL,
				'2026-10-03T22:00:00Z', '2026-10-03T23:00:00Z'),
			(6, 'dispatched', 5, 1, 'o', 'o/a', NULL, NULL, NULL, NULL, NULL,
				'2026-10-04T10:00:00Z', '2026-10-04T11:00:00Z');
		INSERT INTO turn_usage (session, task, issue, workstream, organization, repository, role, harness, model, effort,
			started_at, ended_at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd) VALUES
			(2, 1, 1, 1, 'o', 'o/a', 'Reviewer', 'claude-code', 'sonnet', 'high',
				'2026-10-04T06:00:00Z', '2026-10-04T06:10:00Z', 100, 10, 1000, 5, 1.5),
			(6, 1, 1, 1, 'o', 'o/a', 'Implementer', 'claude-code', 'sonnet', 'high',
				'2026-10-04T10:00:00Z', '2026-10-04T10:10:00Z', 50, 5, NULL, NULL, 0.5),
			(2, 3, 3, 1, 'o', 'o/a', 'Reviewer', 'claude-code', 'sonnet', 'high',
				'2026-10-05T23:10:00Z', '2026-10-05T23:20:00Z', NULL, NULL, NULL, NULL, NULL);
		INSERT INTO lead_events (repository, workstream, kind, payload, time, issue_repository, issue) VALUES
			('o/a', 1, 'end', 'The pull request #0 merged.', '2026-10-02T00:00:00Z', 'o/a', 1),
			('o/a', 1, 'end', 'The pull request #1 merged.', '2026-10-05T01:00:30Z', 'o/a', 1),
			('o/a', 1, 'end', 'The pull request #2 closed with no merge.', '2026-10-04T22:00:30Z', 'o/a', 2),
			('o/a', 1, 'end', 'The pull request #3 merged.', '2026-10-01T00:00:00Z', 'o/a', 3);
	`)
	return func(query string) (int, qualityBody) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usage/quality?"+query, nil))
		var body struct {
			Data qualityBody `json:"data"`
		}
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
		}
		return rec.Code, body.Data
	}
}

func getUsageQuality(t *testing.T, query string) qualityBody {
	t.Helper()
	get := seedUsageQuality(t)
	code, body := get(query)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	return body
}

func sameFloat(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

func TestGetUsageQualityMeansTheReviewRoundsOfTheDoneReviewers(t *testing.T) {
	body := getUsageQuality(t, usagePeriod+"&model=opus")

	if body.Totals.Tasks != 1 || !sameFloat(body.Totals.ReviewRounds, 2) {
		t.Errorf("tasks = %d, review rounds = %v, want 1 task and 2 rounds", body.Totals.Tasks, body.Totals.ReviewRounds)
	}
}

func TestGetUsageQualityMeansTheFixRoundsAfterTheFirstImplementerSession(t *testing.T) {
	body := getUsageQuality(t, usagePeriod+"&model=opus")
	if !sameFloat(body.Totals.FixRounds, 2) {
		t.Errorf("fix rounds = %v, want 2", body.Totals.FixRounds)
	}

	body = getUsageQuality(t, usagePeriod+"&model=sonnet")
	if body.Totals.Tasks != 1 || !sameFloat(body.Totals.FixRounds, 0) {
		t.Errorf("tasks = %d, fix rounds = %v, want 1 task and 0 rounds", body.Totals.Tasks, body.Totals.FixRounds)
	}
}

func TestGetUsageQualityCountsTheNeedsHumanAndAskedStates(t *testing.T) {
	body := getUsageQuality(t, usagePeriod+"&model=opus")

	if !sameFloat(body.Totals.NeedsHuman, 2) {
		t.Errorf("needs human = %v, want 2", body.Totals.NeedsHuman)
	}
	if !sameFloat(body.Totals.Questions, 1) {
		t.Errorf("questions = %v, want 1", body.Totals.Questions)
	}
}

func TestGetUsageQualityCountsTheWorkSecondsFromTheFirstWorkingState(t *testing.T) {
	body := getUsageQuality(t, usagePeriod+"&model=opus")

	if body.Totals.WorkSeconds == nil || !sameFloat(*body.Totals.WorkSeconds, 88200) {
		t.Errorf("work seconds = %v, want 88200", body.Totals.WorkSeconds)
	}
}

func TestGetUsageQualitySumsTheTokensAndTheCostOfTwoRoles(t *testing.T) {
	body := getUsageQuality(t, usagePeriod)

	want := qualityValues{
		InputTokens: float64Ptr(150), OutputTokens: float64Ptr(15),
		CacheReadTokens: float64Ptr(1000), CacheWriteTokens: float64Ptr(5), CostUSD: float64Ptr(2),
	}
	got := qualityValues{
		InputTokens: body.Totals.InputTokens, OutputTokens: body.Totals.OutputTokens,
		CacheReadTokens: body.Totals.CacheReadTokens, CacheWriteTokens: body.Totals.CacheWriteTokens,
		CostUSD: body.Totals.CostUSD,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tokens and cost = %+v, want %+v", got, want)
	}
}

func TestGetUsageQualityMeansTheCostOnlyOverTheTasksWithAValue(t *testing.T) {
	body := getUsageQuality(t, usagePeriod)

	if body.Totals.WorkSeconds == nil || !sameFloat(*body.Totals.WorkSeconds, 47700) {
		t.Errorf("work seconds = %v, want the mean 47700 over two tasks", body.Totals.WorkSeconds)
	}
	if body.Totals.CostUSD == nil || !sameFloat(*body.Totals.CostUSD, 2) {
		t.Errorf("cost = %v, want the mean 2 over one task", body.Totals.CostUSD)
	}
}

func TestGetUsageQualityCountsAMergedPullRequest(t *testing.T) {
	body := getUsageQuality(t, usagePeriod)

	if body.Totals.Tasks != 3 || body.Totals.Merged != 1 {
		t.Errorf("tasks = %d, merged = %d, want 3 tasks and 1 merged", body.Totals.Tasks, body.Totals.Merged)
	}
}

func TestGetUsageQualityUsesTheDayOfTheEnd(t *testing.T) {
	body := getUsageQuality(t, usagePeriod)

	counts := map[string]int64{}
	for _, day := range body.Days {
		counts[day.Day] = day.Values.Tasks
	}
	want := map[string]int64{"2026-10-04": 1, "2026-10-05": 2}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("tasks of the days = %v, want %v", counts, want)
	}
	if first := body.Days[0]; first.Day != "2026-10-04" || first.Values.WorkSeconds == nil ||
		!sameFloat(*first.Values.WorkSeconds, 7200) {
		t.Errorf("first day = %+v, want one task with 7200 work seconds", first)
	}
}

func TestGetUsageQualityUsesTheTimeZoneForTheDay(t *testing.T) {
	body := getUsageQuality(t, usagePeriod+"&tz=Europe/Berlin")

	counts := map[string]int64{}
	for _, day := range body.Days {
		counts[day.Day] = day.Values.Tasks
	}
	want := map[string]int64{"2026-10-05": 2, "2026-10-06": 1}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("tasks of the days = %v, want %v", counts, want)
	}
}

func TestGetUsageQualityGroupsByTheFirstWorkingState(t *testing.T) {
	body := getUsageQuality(t, usagePeriod+"&group=model")

	var got [][]string
	for _, group := range body.Groups {
		got = append(got, group.Group)
	}
	want := [][]string{{""}, {"opus"}, {"sonnet"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %q, want %q", got, want)
	}
}

func TestGetUsageQualityCountsATaskWithNoImplementerSession(t *testing.T) {
	body := getUsageQuality(t, usagePeriod+"&group=model")

	if len(body.Groups) != 3 {
		t.Fatalf("groups = %v, want 3", body.Groups)
	}
	noWorking := body.Groups[0]
	if !reflect.DeepEqual(noWorking.Group, []string{""}) {
		t.Fatalf("first group = %q, want the task with no working state", noWorking.Group)
	}
	if noWorking.Values.Tasks != 1 || noWorking.Values.WorkSeconds != nil || noWorking.Values.CostUSD != nil {
		t.Errorf("values = %+v, want one task with no work seconds and no cost", noWorking.Values)
	}
}

func TestGetUsageQualitySkipsATaskThatIsNotEnded(t *testing.T) {
	body := getUsageQuality(t, usagePeriod)

	if body.Totals.Tasks != 3 {
		t.Errorf("tasks = %d, want 3 ended tasks", body.Totals.Tasks)
	}
}

func TestGetUsageQualityGroupsByTwoFieldsInTheOrderOfTheRoute(t *testing.T) {
	body := getUsageQuality(t, usagePeriod+"&group=effort&group=model")

	var got [][]string
	for _, group := range body.Groups {
		got = append(got, group.Group)
	}
	want := [][]string{{"", ""}, {"opus", "high"}, {"sonnet", "high"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %q, want %q", got, want)
	}
}

func TestGetUsageQualityFiltersTheTasks(t *testing.T) {
	tests := []struct {
		name  string
		query string
		tasks int64
	}{
		{"no filter", "", 3},
		{"harness", "harness=claude-code", 2},
		{"model", "model=sonnet", 1},
		{"effort", "effort=high", 2},
		{"repository", "repository=o/a", 3},
		{"repository with no task in the period", "repository=o/b", 0},
		{"no match", "harness=devin", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := usagePeriod
			if tt.query != "" {
				query += "&" + tt.query
			}
			body := getUsageQuality(t, query)

			if body.Totals.Tasks != tt.tasks {
				t.Errorf("tasks = %d, want %d", body.Totals.Tasks, tt.tasks)
			}
		})
	}
}

func TestGetUsageQualityOptionsIgnoreTheFilters(t *testing.T) {
	body := getUsageQuality(t, usagePeriod+"&model=sonnet")

	got := map[string][]string{
		"harnesses":    body.Options.Harnesses,
		"models":       body.Options.Models,
		"efforts":      body.Options.Efforts,
		"repositories": body.Options.Repositories,
	}
	want := map[string][]string{
		"harnesses":    {"claude-code"},
		"models":       {"opus", "sonnet"},
		"efforts":      {"high"},
		"repositories": {"o/a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("options = %v, want %v", got, want)
	}
}

func TestGetUsageQualityRejectsBadRequests(t *testing.T) {
	get := seedUsageQuality(t)
	tests := []struct {
		name  string
		query string
	}{
		{"unknown time zone", usagePeriod + "&tz=Mars/Base"},
		{"unknown group", usagePeriod + "&group=role"},
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
