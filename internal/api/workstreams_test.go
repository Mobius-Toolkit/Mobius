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
		INSERT INTO copied_workstreams (repository, number, title, body, autopilot) VALUES
			('o/a', 1, 'Loyalty plans', 'Ship the plans.', 1),
			('o/a', 2, 'Seasonal prices', '', 0),
			('o/a', 3, 'Early renewals', '', 0);
		INSERT INTO copied_issues (repository, workstream, position, number, parent, title, body, state, author, html_url, repository_url) VALUES
			('o/a', 1, 0, 11, 1, 'Plan model', '', 'closed', 'owner', '', '');
		INSERT INTO tasks (repository, issue, workstream, state, dispatched_at) VALUES
			('o/a', 21, 2, 'ready_for_review', '2026-10-04T10:00:00Z'),
			('o/a', 31, 3, 'approval', '2026-10-04T10:00:00Z'),
			('o/a', 32, 3, 'ended', '2026-10-04T10:00:00Z');
	`)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workstreams", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	type workstream struct {
		Repository     string `json:"repository"`
		Number         int64  `json:"number"`
		Title          string `json:"title"`
		Brief          string `json:"brief"`
		Autopilot      bool   `json:"autopilot"`
		AllTasksClosed bool   `json:"allTasksClosed"`
		ReadyToMerge   bool   `json:"readyToMerge"`
	}
	var body struct {
		Data []workstream `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	want := []workstream{
		{Repository: "o/a", Number: 3, Title: "Early renewals"},
		{Repository: "o/a", Number: 2, Title: "Seasonal prices", ReadyToMerge: true},
		{Repository: "o/a", Number: 1, Title: "Loyalty plans", Brief: "Ship the plans.", Autopilot: true, AllTasksClosed: true},
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
