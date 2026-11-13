package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTheAPISendsTheIssueTitleOfAnAgentOnlyWhenTheStoreHasACopyOfTheIssue(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `
		INSERT INTO copied_issues (repository, workstream, position, number, parent, title, body, state, author, html_url, repository_url) VALUES
		('owner/shop', 12, 0, 41, 12, 'Add plan model', '', 'open', 'owner', 'https://github.com/owner/shop/issues/41', 'https://api.github.com/repos/owner/shop');
		INSERT INTO sessions (id, role, harness, model, organization, repository, workstream_repository, workstream, issue, started_at) VALUES
		(1, 'implementer', 'claude-code', 'sonnet', 'owner', 'owner/shop', 'owner/shop', 12, 41, '2026-10-04T10:00:00Z'),
		(2, 'reviewer', 'claude-code', 'sonnet', 'owner', 'owner/shop', 'owner/shop', 12, 99, '2026-10-04T10:00:00Z'),
		(3, 'implementer', 'claude-code', 'sonnet', 'owner', 'owner/other', 'owner/shop', 12, 41, '2026-10-04T10:00:00Z'),
		(4, 'lead_chat', 'claude-code', 'sonnet', 'owner', 'owner/shop', 'owner/shop', 12, NULL, '2026-10-04T10:00:00Z')`)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workstreams/owner/shop/12/agents", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Data []struct {
			ID         int64   `json:"id"`
			IssueTitle *string `json:"issueTitle"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	titles := map[int64]*string{}
	for _, agent := range body.Data {
		titles[agent.ID] = agent.IssueTitle
	}
	if len(titles) != 4 {
		t.Fatalf("agents = %s", rec.Body)
	}
	if titles[1] == nil || *titles[1] != "Add plan model" {
		t.Errorf("issue with a copy: issueTitle = %v, want Add plan model", titles[1])
	}
	for id, what := range map[int64]string{2: "issue with no copy", 3: "issue of another repository", 4: "session with no issue"} {
		if titles[id] != nil {
			t.Errorf("%s: issueTitle = %q, want null", what, *titles[id])
		}
	}
}
