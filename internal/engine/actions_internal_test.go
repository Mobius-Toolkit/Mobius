package engine

import (
	"testing"
)

func TestAnActionOfTheTriagerKeepsItsIssueAndAFailedApprovalLeavesItPending(t *testing.T) {
	t.Parallel()
	e := researchEngine(t)
	triager := caller{role: TriagerRole, organization: "owner", repository: "owner/shop", issue: 7}

	if _, err := e.holdAction(t.Context(), triager, "message_lead", "Message to the Lead of #12", []byte(`{"workstream":12,"text":"Hello."}`)); err != nil {
		t.Fatal(err)
	}
	action, err := e.queries.GetPendingAction(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if action.Issue != 7 || actionCaller(action).issue != 7 {
		t.Errorf("issue = %d, caller issue = %d", action.Issue, actionCaller(action).issue)
	}

	for range 2 {
		failed, err := e.ApproveAction(t.Context(), action.ID, "")

		if err != nil {
			t.Fatal(err)
		}
		if failed.State != "pending" || failed.Error == "" {
			t.Errorf("action = %+v", failed)
		}
	}
}
