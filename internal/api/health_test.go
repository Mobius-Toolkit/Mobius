package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetHealth(t *testing.T) {
	mux, _ := testMux(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body struct {
		Data struct {
			Status string `json:"status"`
			Time   string `json:"time"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Data.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Data.Status, "ok")
	}
	parsed, err := time.Parse(time.RFC3339, body.Data.Time)
	if err != nil {
		t.Errorf("time %q is not RFC3339: %v", body.Data.Time, err)
	}
	if _, offset := parsed.Zone(); offset != 0 {
		t.Errorf("time %q is not UTC", body.Data.Time)
	}
}
