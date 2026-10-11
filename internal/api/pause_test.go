package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func pausedUntils(t *testing.T, mux http.Handler, path string) []*string {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", path, rec.Code, rec.Body)
	}
	var body struct {
		Data []struct {
			PausedUntil *string `json:"pausedUntil"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var found []*string
	for _, item := range body.Data {
		found = append(found, item.PausedUntil)
	}
	return found
}

func TestTheAPISendsTheEndOfThePauseOfAnAgent(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO sessions (id, role, harness, model, organization, repository, workstream, started_at, queue_reason) VALUES
		(1, 'implementer', 'claude-code', 'sonnet', 'owner', 'owner/shop', 12, '2026-10-04T10:00:00Z', 'paused until 2026-10-08 17:10 UTC'),
		(2, 'implementer', 'claude-code', 'sonnet', 'owner', 'owner/shop', 12, '2026-10-04T10:00:00Z', 'waits for a check slot'),
		(3, 'implementer', 'devin', 'sonnet', 'owner', 'owner/shop', 12, '2026-10-04T10:00:00Z', 'paused until 2026-10-08 17:10 UTC')`)
	exec(t, db, "INSERT INTO harness_pauses (harness, paused_until) VALUES ('claude-code', '2026-10-08T17:10:00Z')")

	want := "2026-10-08T17:10:00Z"
	agents := pausedUntils(t, mux, "/api/workstreams/owner/shop/12/agents")
	if len(agents) != 3 || agents[0] == nil || *agents[0] != want || agents[1] != nil || agents[2] != nil {
		t.Errorf("agents = %v", agents)
	}
	if pauses := pausedUntils(t, mux, "/api/pauses"); len(pauses) != 1 || pauses[0] == nil || *pauses[0] != want {
		t.Errorf("pauses = %v", pauses)
	}

	exec(t, db, "DELETE FROM harness_pauses")
	if agents := pausedUntils(t, mux, "/api/workstreams/owner/shop/12/agents"); agents[0] != nil {
		t.Errorf("agents = %v", agents)
	}
	if pauses := pausedUntils(t, mux, "/api/pauses"); len(pauses) != 0 {
		t.Errorf("pauses = %v", pauses)
	}
}
