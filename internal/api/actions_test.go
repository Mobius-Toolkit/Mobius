package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

func TestAnActionGivesTheStoredOldBrief(t *testing.T) {
	found, err := actionOf(store.PendingAction{
		ID: 1, Role: "lead", Tool: "edit_brief", Arguments: `{"brief":"New.","reason":"Why."}`, State: "done",
		OldBrief: "Old.", Time: "2026-10-04T10:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if found.OldBrief != "Old." {
		t.Errorf("OldBrief = %q", found.OldBrief)
	}
}

func TestTheBriefOfAnUnknownActionGives404(t *testing.T) {
	mux, _ := testMux(t)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/actions/9/brief", nil))

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status %d: %s", recorder.Code, recorder.Body)
	}
}
