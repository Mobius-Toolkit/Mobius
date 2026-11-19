package engine_test

import (
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestATrustedUserChangesTheTitleBodyStateAndProjectKindOfATreeIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.SetBody(shop, 41, "Plans have a price.\n\n"+chatLink)
	server := startWithChatLink(t, fake)

	fake.SetTitle(shop, 41, "Add pricing table")
	testkit.WaitFor(t, func() bool {
		return issueRowOf(t, server, shop, 41).Title == "Add pricing table" && doneChangesOfPersons(t, server) == 1
	})

	fake.SetBodyBy(shop, 41, "Prices are stored in cents.\n\n"+chatLink, "owner")
	testkit.WaitFor(t, func() bool {
		return issueRowOf(t, server, shop, 41).Body == "Prices are stored in cents." && doneChangesOfPersons(t, server) == 2
	})

	fake.CloseIssue(shop, 41)
	testkit.WaitFor(t, func() bool {
		return issueRowOf(t, server, shop, 41).State == "closed" && doneChangesOfPersons(t, server) == 3
	})

	fake.ReopenIssue(shop, 41)
	testkit.WaitFor(t, func() bool {
		return issueRowOf(t, server, shop, 41).State == "open" && doneChangesOfPersons(t, server) == 4
	})

	fake.AddLabel(shop, 41, "mobius:project", "owner")
	testkit.WaitFor(t, func() bool {
		return issueRowOf(t, server, shop, 41).Kind == "project" && doneChangesOfPersons(t, server) == 5
	})

	fake.RemoveLabel(shop, 41, "mobius:project", "owner")
	testkit.WaitFor(t, func() bool {
		return issueRowOf(t, server, shop, 41).Kind == "task" &&
			doneChangesOfPersons(t, server) == 6 &&
			incomingChanges(t, server, "pending") == 0 &&
			ownWrites(t, server) == 0
	})
}

func TestAnUntrustedPersonCannotEditBodyOrCloseATreeIssueAndGitHubIsWrittenBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.SetBody(shop, 41, "Plans have a price.\n\n"+chatLink)
	server := startWithChatLink(t, fake)

	fake.SetBodyBy(shop, 41, "Untrusted body.", "mallory")
	fake.CloseIssueBy(shop, 41, "mallory")

	wantBody := "Plans have a price.\n\n" + chatLink
	testkit.WaitFor(t, func() bool {
		_, gotBody := fake.Issue(shop, 41)
		gotState, _ := fake.State(shop, 41)
		return gotBody == wantBody && gotState == "open"
	})
	testkit.WaitFor(t, func() bool {
		return ownWrites(t, server) == 0 && doneChangesOfPersons(t, server) == 2 && incomingChanges(t, server, "pending") == 0
	})
	waitForPolls(t, fake)
	row := issueRowOf(t, server, shop, 41)
	if row.Body != "Plans have a price." {
		t.Errorf("row body = %q, want %q", row.Body, "Plans have a price.")
	}
	if row.State != "open" {
		t.Errorf("row state = %q, want open", row.State)
	}
}

func TestAnUntrustedPersonCannotRenameReopenOrChangeProjectOfATreeIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startWithChatLink(t, fake)

	fake.CloseIssue(shop, 41)
	testkit.WaitFor(t, func() bool {
		return issueRowOf(t, server, shop, 41).State == "closed" && doneChangesOfPersons(t, server) == 1
	})

	fake.SetTitleBy(shop, 41, "Untrusted title", "mallory")
	fake.ReopenIssueBy(shop, 41, "mallory")
	fake.AddLabel(shop, 41, "mobius:project", "mallory")

	testkit.WaitFor(t, func() bool {
		title, _ := fake.Issue(shop, 41)
		state, _ := fake.State(shop, 41)
		return title == "Add plan model" && state == "closed" && !slices.Contains(fake.Labels(shop, 41), "mobius:project")
	})
	testkit.WaitFor(t, func() bool {
		return ownWrites(t, server) == 0 && doneChangesOfPersons(t, server) == 4 && incomingChanges(t, server, "pending") == 0
	})
	waitForPolls(t, fake)
	row := issueRowOf(t, server, shop, 41)
	if row.Title != "Add plan model" {
		t.Errorf("row title = %q, want %q", row.Title, "Add plan model")
	}
	if row.State != "closed" {
		t.Errorf("row state = %q, want closed", row.State)
	}
	if row.Kind != "task" {
		t.Errorf("row kind = %q, want task", row.Kind)
	}
}

func TestTheSendOfEditIssueBodyMakesNoIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.SetBody(shop, 41, "Plans have a price.\n\n"+chatLink)
	server, _ := connectWith(t, fake, editBodyCall("Edit #41.", `{ repository = "owner/shop", n = 41, body = "Prices are stored in cents." }`)+seen, withUIURL)

	run(t, server, leadSpec(t), "Edit #41.")

	wantBody := "Prices are stored in cents.\n\n" + chatLink
	testkit.WaitFor(t, func() bool {
		_, gotBody := fake.Issue(shop, 41)
		return gotBody == wantBody
	})
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if got := doneChangesOfPersons(t, server); got != 0 {
		t.Errorf("person incoming changes = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'body' AND actor = ? AND status = 'done'", appActor); got != 1 {
		t.Errorf("app body incoming changes = %d, want 1", got)
	}
	if got := issueRowOf(t, server, shop, 41).Body; got != "Prices are stored in cents." {
		t.Errorf("row body = %q, want %q", got, "Prices are stored in cents.")
	}
}

func TestTheSendOfCloseTaskRecordsAnOwnWriteAndMakesNoPersonIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	script := `[[prompts]]
when = "Close #41."
call = { tool = "close_task", arguments = { repository = "owner/shop", n = 41, reason = "Already done." } }

` + seen
	server, _ := connect(t, fake, script)

	run(t, server, leadSpec(t), "Close #41.")

	testkit.WaitFor(t, func() bool {
		state, _ := fake.State(shop, 41)
		return state == "closed"
	})
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if got := doneChangesOfPersons(t, server); got != 0 {
		t.Errorf("person incoming changes = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'state' AND actor = ? AND status = 'done'", appActor); got != 1 {
		t.Errorf("app state incoming changes = %d, want 1", got)
	}
	if got := issueRowOf(t, server, shop, 41).State; got != "closed" {
		t.Errorf("row state = %q, want closed", got)
	}
}

func TestAnAppMergeThatAutoClosesTheTaskIssueSetsStateClosedWithNoWriteBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)
	fake.AutoCloseOnMerge(shop, 42, 41)

	sendChat(t, server, leadChat, "Approve #41")

	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. Mobius merges it after its checks pass.")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if state, _ := fake.State(shop, 41); state != "closed" {
		t.Errorf("github state = %q, want closed", state)
	}
	if row := issueRowOf(t, server, shop, 41); row.State != "closed" {
		t.Errorf("row state = %q, want closed", row.State)
	}
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE number = 41 AND field = 'state' AND actor = ? AND status = 'done'", appActor); got != 1 {
		t.Errorf("app state incoming changes = %d, want 1", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE repository = ? AND number = 41 AND field = 'state'", shop); got != 0 {
		t.Errorf("state outgoing changes = %d, want 0", got)
	}
}

func TestARemovedEditedAndReattachedSubIssueGetsItsNewValuesFromGitHub(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.SetBody(shop, 41, "Plans have a price.\n\n"+chatLink)
	fake.AddIssue(shop, 42, "Show plans")
	fake.AddSubIssue(shop, 12, 42)
	fake.AddBlockedBy(shop, 42, 41)
	server := startWithChatLink(t, fake)

	fake.RemoveSubIssue(shop, 12, 41)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 41 AND workstream IS NULL", shop) == 1
	})

	fake.SetTitle(shop, 41, "Add billing model")
	fake.SetBodyBy(shop, 41, "Billing uses cents.\n\n"+chatLink, "owner")
	fake.CloseIssue(shop, 41)
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	waitForPolls(t, fake)

	fake.AddSubIssue(shop, 12, 41)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 41 AND workstream IS NOT NULL", shop) == 1
	})
	row := issueRowOf(t, server, shop, 41)
	if row.Title != "Add billing model" {
		t.Errorf("row title = %q, want %q", row.Title, "Add billing model")
	}
	if row.Body != "Billing uses cents." {
		t.Errorf("row body = %q, want %q", row.Body, "Billing uses cents.")
	}
	if row.State != "closed" {
		t.Errorf("row state = %q, want closed", row.State)
	}
	if row.Kind != "project" {
		t.Errorf("row kind = %q, want project", row.Kind)
	}
}
