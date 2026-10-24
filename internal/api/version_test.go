package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

func TestGetVersionGivesTheRelease(t *testing.T) {
	for _, release := range []string{"v0.2.0", ""} {
		t.Run(release, func(t *testing.T) {
			previous := engine.Release
			engine.Release = release
			t.Cleanup(func() { engine.Release = previous })
			mux, _ := testMux(t)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			var body struct {
				Data struct {
					Release string `json:"release"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
			if body.Data.Release != release {
				t.Errorf("release = %q, want %q", body.Data.Release, release)
			}
		})
	}
}

func TestGetVersionNeedsADeviceLogin(t *testing.T) {
	mux, _, _ := newMux(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
