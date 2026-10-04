package testserver

import (
	"net/http"
	"testing"
	"time"
)

func TestStartServesTheAPI(t *testing.T) {
	server := Start(t, t.TempDir())

	response, err := http.Get(server.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d", response.StatusCode)
	}
}

func TestWaitForFirstPollWaitsForTheSinceCursorOfTheIssues(t *testing.T) {
	server := Start(t, t.TempDir())
	if _, err := server.DB.Exec(`INSERT INTO sync_cursors (repository, endpoint, since) VALUES ('owner/shop', 'pulls', '2026-10-04T10:00:00Z'), ('owner/shop', 'issues', NULL)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = server.DB.Exec(`UPDATE sync_cursors SET since = '2026-10-04T10:00:00Z' WHERE repository = 'owner/shop' AND endpoint = 'issues'`)
		close(done)
	}()

	server.WaitForFirstPoll(t, "owner/shop")

	select {
	case <-done:
	default:
		t.Error("the wait ended before the poll stored the cursor")
	}
}
