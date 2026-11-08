package api

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const recurringPath = "/api/workstreams/o/a/1/recurring-tasks"

type recurringRun struct {
	Time   time.Time `json:"time"`
	Result string    `json:"result"`
	Reason string    `json:"reason"`
}

type recurringTask struct {
	ID     int64          `json:"id"`
	Cron   string         `json:"cron"`
	Text   string         `json:"text"`
	Paused bool           `json:"paused"`
	Next   *time.Time     `json:"next"`
	Runs   []recurringRun `json:"runs"`
}

func addRecurringRows(t *testing.T, db *sql.DB) {
	t.Helper()
	exec(t, db, `
		INSERT INTO copied_workstreams (repository, number, title, body, autopilot, evergreen) VALUES
			('o/a', 1, 'Loyalty plans', '', 0, 0);
		INSERT INTO recurring_tasks (id, repository, workstream, cron, text, paused, checked_at) VALUES
			(1, 'o/a', 1, '0 8 * * 1-5', 'Check the plans.', 0, '2026-11-09T07:00:00Z'),
			(2, 'o/a', 1, '0 * * * *', 'Read the news.', 1, '2026-11-09T07:00:00Z'),
			(3, 'o/a', 2, '0 * * * *', 'Other Workstream.', 0, '2026-11-09T07:00:00Z');
		INSERT INTO recurring_task_runs (recurring_task, time, result, reason) VALUES
			(1, '2026-11-09T08:00:00Z', 'event', ''),
			(1, '2026-11-10T08:00:00Z', 'skipped', 'the earlier event is not delivered yet');
	`)
}

func recurringRequest(t *testing.T, mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, request)
	return rec
}

func decodeRecurring[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body.Data
}

func TestListRecurringTasksGivesTheTasksOfTheWorkstreamWithNextAndRuns(t *testing.T) {
	mux, db := testMux(t)
	addRecurringRows(t, db)

	rec := recurringRequest(t, mux, http.MethodGet, recurringPath, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	tasks := decodeRecurring[struct {
		Tasks []recurringTask `json:"tasks"`
	}](t, rec).Tasks
	if len(tasks) != 2 || tasks[0].ID != 1 || tasks[1].ID != 2 {
		t.Fatalf("tasks = %+v", tasks)
	}
	first := tasks[0]
	if first.Cron != "0 8 * * 1-5" || first.Text != "Check the plans." || first.Paused || first.Next == nil || !first.Next.After(time.Now()) {
		t.Errorf("first task = %+v", first)
	}
	wantRuns := []recurringRun{
		{Time: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC), Result: "skipped", Reason: "the earlier event is not delivered yet"},
		{Time: time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC), Result: "event"},
	}
	if len(first.Runs) != 2 || first.Runs[0] != wantRuns[0] || first.Runs[1] != wantRuns[1] {
		t.Errorf("runs = %+v, want %+v", first.Runs, wantRuns)
	}
	if second := tasks[1]; !second.Paused || second.Next != nil || second.Runs == nil || len(second.Runs) != 0 {
		t.Errorf("second task = %+v", second)
	}
}

func TestSetRecurringTaskPausedPausesAndResumesATask(t *testing.T) {
	mux, db := testMux(t)
	addRecurringRows(t, db)

	rec := recurringRequest(t, mux, http.MethodPut, recurringPath+"/1/paused", `{"paused": true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("pause status = %d: %s", rec.Code, rec.Body)
	}
	if paused := decodeRecurring[recurringTask](t, rec); !paused.Paused || paused.Next != nil || paused.ID != 1 || len(paused.Runs) != 2 {
		t.Errorf("paused task = %+v", paused)
	}
	rec = recurringRequest(t, mux, http.MethodPut, recurringPath+"/1/paused", `{"paused": false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume status = %d: %s", rec.Code, rec.Body)
	}
	if resumed := decodeRecurring[recurringTask](t, rec); resumed.Paused || resumed.Next == nil {
		t.Errorf("resumed task = %+v", resumed)
	}
}

func TestDeleteRecurringTaskDeletesTheTaskAndItsRuns(t *testing.T) {
	mux, db := testMux(t)
	addRecurringRows(t, db)

	rec := recurringRequest(t, mux, http.MethodDelete, recurringPath+"/1", "")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var tasks, runs int
	if err := db.QueryRow("SELECT (SELECT count(*) FROM recurring_tasks), (SELECT count(*) FROM recurring_task_runs)").Scan(&tasks, &runs); err != nil {
		t.Fatal(err)
	}
	if tasks != 2 || runs != 0 {
		t.Errorf("tasks = %d, runs = %d, want 2 and 0", tasks, runs)
	}
}

func TestAnUnknownRecurringTaskGivesNotFoundForPutAndDelete(t *testing.T) {
	mux, db := testMux(t)
	addRecurringRows(t, db)
	if rec := recurringRequest(t, mux, http.MethodDelete, recurringPath+"/2", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE known id: status = %d: %s", rec.Code, rec.Body)
	}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"PUT unknown id":          recurringRequest(t, mux, http.MethodPut, recurringPath+"/9/paused", `{"paused": true}`),
		"PUT task of another one": recurringRequest(t, mux, http.MethodPut, recurringPath+"/3/paused", `{"paused": true}`),
		"DELETE unknown id":       recurringRequest(t, mux, http.MethodDelete, recurringPath+"/9", ""),
		"DELETE task of another":  recurringRequest(t, mux, http.MethodDelete, recurringPath+"/3", ""),
	} {
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want %d: %s", name, rec.Code, http.StatusNotFound, rec.Body)
		}
	}
	var events int
	if err := db.QueryRow("SELECT count(*) FROM lead_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Errorf("lead events = %d, want 1 for the delete of the known task", events)
	}
}

func TestAPauseSendsTheLiveEventRecurringTasks(t *testing.T) {
	mux, db := testMux(t)
	addRecurringRows(t, db)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(server.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	r := bufio.NewReader(resp.Body)
	if got, want := readEvent(t, r), "event: ping\ndata: {}\n"; got != want {
		t.Fatalf("first event = %q, want %q", got, want)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, server.URL+recurringPath+"/1/paused", strings.NewReader(`{"paused": true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	put, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = put.Body.Close()

	if got, want := readEvent(t, r), "event: recurring_tasks\ndata: {\"number\":1,\"repository\":\"o/a\"}\n"; got != want {
		t.Errorf("event = %q, want %q", got, want)
	}
}
