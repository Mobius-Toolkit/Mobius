package engine

import (
	"testing"
)

func TestTheSameSettingEventsAddNoSecondIncomingChange(t *testing.T) {
	t.Parallel()
	e := storingEngine(t)
	added := labelEvent("labeled", "mobius:autopilot", "owner")
	added.ID = new(int64(7))
	removed := labelEvent("unlabeled", "mobius:evergreen", "owner")
	removed.ID = new(int64(8))
	events := []settingEvent{{12, added}, {12, removed}}

	for range 2 {
		if err := addSettingChanges(t.Context(), e.queries, appRepository, events); err != nil {
			t.Fatal(err)
		}
	}

	changes, err := e.queries.ListPendingIncomingChanges(t.Context(), "owner/shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0].ExternalID != "7" || changes[1].ExternalID != "8" {
		t.Errorf("changes = %+v", changes)
	}
}
