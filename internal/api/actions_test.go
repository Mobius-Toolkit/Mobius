package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestAnActionGivesTheStoredOldBrief(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO inbox_items (id, kind, organization, repository, workstream_repository, workstream, issue, text, link, time) VALUES
		(1, 'approval', 'owner', 'owner/shop', 'owner/shop', 12, 0, '', '', '2026-10-04T10:00:00Z')`)
	exec(t, db, `INSERT INTO pending_actions (id, organization, repository, workstream, issue, role, tool, arguments, state, time, inbox_item, old_brief) VALUES
		(1, 'owner', 'owner/shop', 12, 0, 'lead', 'edit_brief', '{"brief":"New.","reason":"Why."}', 'done', '2026-10-04T10:00:00Z', 1, 'Old.')`)

	found := listedActions(t, mux)[1]

	if found.OldBrief != "Old." {
		t.Errorf("OldBrief = %q", found.OldBrief)
	}
}

func TestAnActionGivesItsArgumentsInTheOrderOfItsTool(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO inbox_items (id, kind, organization, repository, workstream_repository, workstream, issue, text, link, time) VALUES
		(1, 'approval', 'owner', 'owner/shop', 'owner/shop', 12, 0, '', '', '2026-10-04T10:00:00Z')`)
	exec(t, db, `INSERT INTO pending_actions (id, organization, repository, workstream, issue, role, tool, arguments, state, time, inbox_item) VALUES
		(1, 'owner', 'owner/shop', 12, 0, 'lead', 'edit_brief', '{"brief":"New.","reason":"Why."}', 'pending', '2026-10-04T10:00:00Z', 1)`)

	var names []string
	for _, argument := range listedActions(t, mux)[1].Arguments {
		names = append(names, argument.Name)
	}

	if want := []string{"reason", "brief"}; !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestAnActionGivesTheLabelsOfItsLabelsArgument(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO inbox_items (id, kind, organization, repository, workstream_repository, workstream, issue, text, link, time) VALUES
		(1, 'approval', 'owner', '', '', 0, 0, '', '', '2026-10-04T10:00:00Z')`)
	exec(t, db, `INSERT INTO pending_actions (id, organization, repository, workstream, issue, role, tool, arguments, state, time, inbox_item) VALUES
		(1, 'owner', '', 0, 0, 'coordinator', 'create_workstream', '{"title":"Plans","labels":["mobius:maintenance","mobius:evergreen","other"],"brief":"Brief."}', 'pending', '2026-10-04T10:00:00Z', 1)`)

	found := listedActions(t, mux)[1]

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

func TestAnActionOfALeadGivesItsWorkstreamAndAnActionOfTheCoordinatorGivesNone(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO copied_workstreams (repository, number, title, body, autopilot, evergreen) VALUES
		('owner/shop', 12, 'Checkout', '', 0, 0)`)
	exec(t, db, `INSERT INTO inbox_items (id, kind, organization, repository, workstream_repository, workstream, issue, text, link, time) VALUES
		(1, 'approval', 'owner', 'owner/shop', 'owner/shop', 12, 0, '', '', '2026-10-04T10:00:00Z'),
		(2, 'approval', 'owner', 'owner/shop', 'owner/shop', 12, 0, '', '', '2026-10-04T10:00:01Z'),
		(3, 'approval', 'owner', 'owner/shop', 'owner/shop', 40, 0, '', '', '2026-10-04T10:00:02Z')`)
	exec(t, db, `INSERT INTO pending_actions (id, organization, repository, workstream, issue, role, tool, arguments, state, time, inbox_item) VALUES
		(1, 'owner', 'owner/shop', 12, 0, 'lead', 'message_lead', '{"workstream":13,"text":"Hi."}', 'pending', '2026-10-04T10:00:00Z', 1),
		(2, 'owner', '', 0, 0, 'coordinator', 'message_lead', '{"workstream":13,"text":"Hi."}', 'pending', '2026-10-04T10:00:01Z', 2),
		(3, 'owner', 'owner/shop', 40, 0, 'lead', 'message_lead', '{"workstream":13,"text":"Hi."}', 'pending', '2026-10-04T10:00:02Z', 3)`)

	actions := listedActions(t, mux)

	if got, want := actions[1].Sender, (&ActionSender{Repository: "owner/shop", Number: 12, Title: "Checkout"}); got == nil || *got != *want {
		t.Errorf("sender of the Lead = %+v", got)
	}
	if actions[2].Sender != nil {
		t.Errorf("sender of the Coordinator = %+v", actions[2].Sender)
	}
	if got, want := actions[3].Sender, (&ActionSender{Repository: "owner/shop", Number: 40}); got == nil || *got != *want {
		t.Errorf("sender of a Lead with no copied Workstream = %+v", got)
	}
}

// listedActions gives the action of each approval item of the Inbox by the id of the action.
func listedActions(t *testing.T, mux http.Handler) map[int64]PendingAction {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
	var body Envelope[[]InboxItem]
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	actions := map[int64]PendingAction{}
	for _, item := range body.Data {
		if item.Action != nil {
			actions[item.Action.ID] = *item.Action
		}
	}
	return actions
}

func TestTheBriefOfAnUnknownActionGives404(t *testing.T) {
	mux, _ := testMux(t)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/actions/9/brief", nil))

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status %d: %s", recorder.Code, recorder.Body)
	}
}
