package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestListWorkstreams(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `
		INSERT INTO workstreams (repository, number, title, body, autopilot, evergreen, state, finished_at) VALUES
			('o/a', 1, 'Loyalty plans', 'Ship the plans.', 1, 0, 'open', '2026-10-04T11:00:00Z'),
			('o/a', 2, 'Seasonal prices', '', 0, 0, 'open', NULL),
			('o/a', 3, 'Early renewals', '', 0, 1, 'open', NULL),
			('o/a', 4, 'Closed plans', '', 0, 0, 'closed', NULL),
			('o/a', 5, 'Reported design', 'Finish the design.', 0, 0, 'open', '2026-10-04T12:00:00Z');
		INSERT INTO issues (repository, number, title, body, state, author, workstream) VALUES
			('o/a', 11, 'Plan model', '', 'closed', 'owner', 1);
		INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at) VALUES
			('o/a', 'o/a', 21, 2, 'ready_for_review', '2026-10-04T10:00:00Z'),
			('o/a', 'o/a', 31, 3, 'approval', '2026-10-04T10:00:00Z'),
			('o/a', 'o/a', 32, 3, 'ended', '2026-10-04T10:00:00Z');
	`)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workstreams", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	type workstream struct {
		Repository       string `json:"repository"`
		Number           int64  `json:"number"`
		Title            string `json:"title"`
		Brief            string `json:"brief"`
		Autopilot        string `json:"autopilot"`
		Evergreen        bool   `json:"evergreen"`
		AllTasksClosed   bool   `json:"allTasksClosed"`
		ReportedFinished bool   `json:"reportedFinished"`
		ReadyToMerge     bool   `json:"readyToMerge"`
	}
	var body struct {
		Data []workstream `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	want := []workstream{
		{Repository: "o/a", Number: 5, Title: "Reported design", Brief: "Finish the design.", Autopilot: "off", ReportedFinished: true},
		{Repository: "o/a", Number: 3, Title: "Early renewals", Autopilot: "off", Evergreen: true},
		{Repository: "o/a", Number: 2, Title: "Seasonal prices", Autopilot: "off", ReadyToMerge: true},
		{Repository: "o/a", Number: 1, Title: "Loyalty plans", Brief: "Ship the plans.", Autopilot: "start", AllTasksClosed: true},
	}
	if !slices.Equal(body.Data, want) {
		t.Errorf("workstreams = %+v, want %+v", body.Data, want)
	}
}

func TestListWorkstreamsEmpty(t *testing.T) {
	mux, _ := testMux(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workstreams", nil))

	if got, want := rec.Body.String(), `{"data":[]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestListWaitingWorkstreams(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `
		INSERT INTO workstreams (id, repository, number, title, body, autopilot, evergreen, state) VALUES
			(1, 'o/a', NULL, 'Hourly backup', '', 0, 0, 'open'),
			(2, 'o/a', 7, 'Loyalty plans', '', 0, 0, 'open'),
			(3, 'o/b', NULL, 'Upkeep', '', 0, 1, 'open'),
			(4, 'o/a', NULL, 'Dropped plans', '', 0, 0, 'closed');
	`)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workstreams/waiting", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	type waiting struct {
		ID         int64  `json:"id"`
		Repository string `json:"repository"`
		Title      string `json:"title"`
		Evergreen  bool   `json:"evergreen"`
	}
	var body struct {
		Data []waiting `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	want := []waiting{
		{ID: 3, Repository: "o/b", Title: "Upkeep", Evergreen: true},
		{ID: 1, Repository: "o/a", Title: "Hourly backup"},
	}
	if !slices.Equal(body.Data, want) {
		t.Errorf("waiting = %+v, want %+v", body.Data, want)
	}
}
