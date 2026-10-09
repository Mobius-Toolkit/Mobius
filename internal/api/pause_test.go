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

func TestTheAPISendsTheEndOfThePauseOfAnAgentAndOfAUsageLimitItem(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO sessions (id, role, harness, model, organization, repository, workstream, started_at, queue_reason) VALUES
		(1, 'implementer', 'claude-code', 'sonnet', 'owner', 'owner/shop', 12, '2026-10-04T10:00:00Z', 'paused until 2026-10-08 17:10 UTC'),
		(2, 'implementer', 'claude-code', 'sonnet', 'owner', 'owner/shop', 12, '2026-10-04T10:00:00Z', 'waits for a check slot'),
		(3, 'implementer', 'devin', 'sonnet', 'owner', 'owner/shop', 12, '2026-10-04T10:00:00Z', 'paused until 2026-10-08 17:10 UTC')`)
	exec(t, db, `INSERT INTO inbox_items (id, kind, organization, repository, workstream, issue, text, link, time) VALUES
		(1, 'usage limit', 'owner', 'owner/shop', 0, 0, 'claude-code reached a usage limit.', '', '2026-10-04T10:00:00Z'),
		(2, 'question', 'owner', 'owner/shop', 12, 41, 'Why?', '', '2026-10-04T10:00:00Z')`)
	exec(t, db, "INSERT INTO harness_pauses (harness, paused_until, inbox_item) VALUES ('claude-code', '2026-10-08T17:10:00Z', 1)")

	want := "2026-10-08T17:10:00Z"
	agents := pausedUntils(t, mux, "/api/workstreams/owner/shop/12/agents")
	if len(agents) != 3 || agents[0] == nil || *agents[0] != want || agents[1] != nil || agents[2] != nil {
		t.Errorf("agents = %v", agents)
	}
	inbox := pausedUntils(t, mux, "/api/inbox")
	if len(inbox) != 2 || inbox[0] == nil || *inbox[0] != want || inbox[1] != nil {
		t.Errorf("inbox = %v", inbox)
	}

	exec(t, db, "DELETE FROM harness_pauses")
	if agents := pausedUntils(t, mux, "/api/workstreams/owner/shop/12/agents"); agents[0] != nil {
		t.Errorf("agents = %v", agents)
	}
	if inbox := pausedUntils(t, mux, "/api/inbox"); inbox[0] != nil {
		t.Errorf("inbox = %v", inbox)
	}
}
