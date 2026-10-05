package api

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// rustTimes are times in the format that the Rust version writes into each time column: RFC 3339 of the time crate in
// UTC, with the fraction of a second only when it is not zero, and with no trailing zero.
var rustTimes = []string{"2026-10-04T10:00:00Z", "2026-10-04T10:00:01.5Z", "2026-10-04T10:00:02.123456789Z"}

func TestTheAPIReadsTheTimesThatTheRustVersionWrote(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, "UPDATE device_logins SET created_at = '"+rustTimes[1]+"'")
	exec(t, db, `INSERT INTO sessions (id, role, harness, model, organization, repository, workstream, started_at, ended_at, end_reason)
		VALUES (1, 'lead_chat', 'claude-code', 'opus', 'owner', 'owner/shop', 12, '`+rustTimes[0]+`', '`+rustTimes[2]+`', 'idle')`)
	for _, at := range rustTimes {
		exec(t, db, `INSERT INTO transcript (session, time, kind, json) VALUES (1, '`+at+`', 'prompt', '{"text":"Plan"}')`)
		exec(t, db, `INSERT INTO chat_messages (organization, repository, workstream, author, time, text) VALUES ('owner', 'owner/shop', 12, 'Owner', '`+at+`', 'Plan')`)
		exec(t, db, `INSERT INTO inbox_items (kind, organization, repository, workstream, issue, text, link, time) VALUES ('question', 'owner', 'owner/shop', 12, 41, 'Why?', 'https://example.com/41', '`+at+`')`)
		exec(t, db, `INSERT INTO events (time, repository, workstream, issue, actor, text, link) VALUES ('`+at+`', 'owner/shop', 12, 41, 'owner', 'Dispatched', 'https://example.com/41')`)
	}

	for path, times := range map[string][]string{
		"/api/devices":                          {rustTimes[1]},
		"/api/workstreams/owner/shop/12/agents": {rustTimes[0], rustTimes[2]},
		"/api/agents/1/transcript":              rustTimes,
		"/api/chat?organization=owner&repository=owner/shop&workstream=12": rustTimes,
		"/api/inbox": rustTimes,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d: %s", path, rec.Code, rec.Body)
			continue
		}
		for _, at := range times {
			if !strings.Contains(rec.Body.String(), `"`+at+`"`) {
				t.Errorf("%s: %s is not in %s", path, at, rec.Body)
			}
		}
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(server.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	events := bufio.NewReader(response.Body)
	for _, at := range rustTimes {
		if event := readEvent(t, events); !strings.Contains(event, `"time":"`+at+`"`) {
			t.Errorf("event = %q", event)
		}
	}
}
