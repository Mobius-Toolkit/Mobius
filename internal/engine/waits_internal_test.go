package engine

import "testing"

func TestTheWaitOfATaskOrASessionHasEachValueOfTheTable(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		state   string
		held    bool
		session Wait
		want    Wait
	}{
		{"no state and no session wait", "", false, "", ""},
		{"a task that works", "working", false, "", ""},
		{"a task in review", "reviewed", false, "", ""},
		{"a task that needs a person", "needs_human", false, "", ""},
		{"a task that waits for the merge", "ready_for_review", false, "", ""},
		{"a task that ended", "ended", false, "", ""},
		{"a task in queued with no session wait", "queued", false, "", waitSlot},
		{"a task in checks", "checks", false, "", waitCI},
		{"a task in approval with no held event", "approval", false, "", waitLead},
		{"a task in approval with a held event", "approval", true, "", waitOwner},
		{"a task in dispatched", "dispatched", false, "", waitStartImplementer},
		{"a task in owner_action", "owner_action", false, "", waitOwnerAction},
		{"a session that waits for a pause", "", false, waitPaused, waitPaused},
		{"a session that waits for the Start switch", "", false, waitStartOff, waitStartOff},
		{"a session that waits for the drain", "", false, waitDrain, waitDrain},
		{"a session that waits for the access check", "", false, waitNoAccess, waitNoAccess},
		{"a session that waits for a slot", "", false, waitSlot, waitSlot},
		{"a session that waits for a check slot", "", false, waitCheckSlot, waitCheckSlot},
		{"a session that waits for a low load", "", false, waitLowLoad, waitLowLoad},
		{"a session that runs the check", "", false, waitLocalCheck, waitLocalCheck},
		{"a session that runs the init script", "", false, waitInit, waitInit},
		{"a session wait wins over a queued task", "queued", false, waitPaused, waitPaused},
		{"a session wait wins over a task that works", "working", false, waitLocalCheck, waitLocalCheck},
		{"a session wait wins over a task in approval", "approval", true, waitNoAccess, waitNoAccess},
	} {
		if got := waitOf(c.state, c.held, c.session); got != c.want {
			t.Errorf("%s: wait = %q, want %q", c.name, got, c.want)
		}
	}
}
