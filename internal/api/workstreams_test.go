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
		INSERT INTO tasks (repository, issue, workstream, state, dispatched_at) VALUES
			('o/a', 11, 1, 'working', '2026-10-01T10:00:00Z'),
			('o/a', 12, 1, 'ended', '2026-10-01T11:00:00Z');
		INSERT INTO chat_messages (repository, workstream, author, time, text, organization) VALUES
			('o/b', 5, 'owner', '2026-10-02T10:00:00.25Z', 'Hello', 'o');
	`)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workstreams", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	type workstream struct {
		Repository   string `json:"repository"`
		Number       int64  `json:"number"`
		Tasks        int64  `json:"tasks"`
		OpenTasks    int64  `json:"openTasks"`
		LastActivity string `json:"lastActivity"`
	}
	var body struct {
		Workstreams []workstream `json:"workstreams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	want := []workstream{
		{Repository: "o/b", Number: 5, Tasks: 0, OpenTasks: 0, LastActivity: "2026-10-02T10:00:00.25Z"},
		{Repository: "o/a", Number: 1, Tasks: 2, OpenTasks: 1, LastActivity: "2026-10-01T11:00:00Z"},
	}
	if !slices.Equal(body.Workstreams, want) {
		t.Errorf("workstreams = %+v, want %+v", body.Workstreams, want)
	}
}

func TestListWorkstreamsEmpty(t *testing.T) {
	mux, _ := testMux(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workstreams", nil))

	if got, want := rec.Body.String(), `{"workstreams":[]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}
