package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnAnswerOfAnUnknownItemGives404AnEmptyTextGives400AndAnItemWithNoIssueGives400(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO inbox_items (id, kind, organization, repository, workstream, issue, text, link, time) VALUES
		(1, 'Lead failed', 'owner', '', 0, 0, 'The Lead did not take the message.', '', '2026-10-04T10:00:00Z'),
		(2, 'question', 'owner', 'owner/shop', 12, 41, 'Cents or dollars?', '', '2026-10-04T10:00:00Z')`)

	for _, test := range []struct {
		path, body string
		status     int
	}{
		{"/api/inbox/9/answer", `{"text": "Cents."}`, http.StatusNotFound},
		{"/api/inbox/2/answer", `{"text": ""}`, http.StatusBadRequest},
		{"/api/inbox/2/answer", `{"text": " \n"}`, http.StatusBadRequest},
		{"/api/inbox/1/answer", `{"text": "Cents."}`, http.StatusBadRequest},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != test.status {
			t.Errorf("%s %s: status %d: %s", test.path, test.body, recorder.Code, recorder.Body)
		}
	}
}
