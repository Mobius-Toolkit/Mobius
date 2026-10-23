package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestAnActionGivesTheLabelsOfItsLabelsArgument(t *testing.T) {
	found, err := actionOf(store.PendingAction{
		ID: 1, Role: "coordinator", Tool: "create_workstream", State: "pending", Time: "2026-10-04T10:00:00Z",
		Arguments: `{"title":"Plans","labels":["mobius:maintenance","mobius:evergreen","other"],"brief":"Brief."}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []ActionLabel{{Name: "mobius:maintenance", Color: "BFDADC"}, {Name: "mobius:evergreen", Color: "0052CC"}, {Name: "other"}}
	for _, argument := range found.Arguments {
		if argument.Name != "labels" {
			if argument.Labels == nil || len(argument.Labels) != 0 {
				t.Errorf("%s: Labels = %v", argument.Name, argument.Labels)
			}
			continue
		}
		if !slices.Equal(argument.Labels, want) {
			t.Errorf("Labels = %v, want %v", argument.Labels, want)
		}
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
