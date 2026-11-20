package engine_test

import (
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// triageRowOf gives the row of the issue in issues, or false when the issue has no row.
func triageRowOf(t *testing.T, server *testserver.Server, number int64) (issueRow, bool) {
	t.Helper()
	for _, row := range issueRows(t, server) {
		if row.Number == number {
			return row, true
		}
	}
	return issueRow{}, false
}

func TestATrustedUserAddsAndRemovesMobiusTriageAndTheTriageStateFollows(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	fake.AddLabel(shop, 41, "mobius:triage", "owner")
	testkit.WaitFor(t, func() bool {
		return issueRowOf(t, server, shop, 41).Triage && doneChangesOfPersons(t, server) == 1
	})

	fake.RemoveLabel(shop, 41, "mobius:triage", "owner")
	testkit.WaitFor(t, func() bool {
		return !issueRowOf(t, server, shop, 41).Triage &&
			doneChangesOfPersons(t, server) == 2 &&
			incomingChanges(t, server, "pending") == 0 &&
			outgoingChanges(t, server, "pending") == 0 &&
			ownWrites(t, server) == 0
	})
	if labels := fake.Labels(shop, 41); slices.Contains(labels, "mobius:triage") {
		t.Errorf("labels = %v", labels)
	}
}

func TestAnUntrustedPersonCannotAddOrRemoveMobiusTriageAndGitHubIsWrittenBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	fake.AddLabel(shop, 41, "mobius:triage", "mallory")
	testkit.WaitFor(t, func() bool {
		return !slices.Contains(fake.Labels(shop, 41), "mobius:triage") &&
			doneChangesOfPersons(t, server) == 1 &&
			incomingChanges(t, server, "pending") == 0 &&
			ownWrites(t, server) == 0
	})
	waitForPolls(t, fake)
	if issueRowOf(t, server, shop, 41).Triage {
		t.Error("the add of mallory set the triage state")
	}

	fake.AddLabel(shop, 41, "mobius:triage", "owner")
	testkit.WaitFor(t, func() bool { return issueRowOf(t, server, shop, 41).Triage && doneChangesOfPersons(t, server) == 2 })

	fake.RemoveLabel(shop, 41, "mobius:triage", "mallory")
	testkit.WaitFor(t, func() bool {
		return slices.Contains(fake.Labels(shop, 41), "mobius:triage") &&
			doneChangesOfPersons(t, server) == 3 &&
			incomingChanges(t, server, "pending") == 0 &&
			ownWrites(t, server) == 0
	})
	waitForPolls(t, fake)
	if !issueRowOf(t, server, shop, 41).Triage {
		t.Error("the removal of mallory cleared the triage state")
	}
}

func TestATrustedAddOfMobiusTriageOnAnIssueInNoWorkstreamAddsARowWithNoWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 50, "Rename plans")
	fake.SetAuthor(shop, 50, "mallory")
	server := startCopiedWithLead(t, fake)

	fake.AddLabel(shop, 50, "mobius:triage", "owner")

	testkit.WaitFor(t, func() bool {
		row, found := triageRowOf(t, server, 50)
		return found && row.Triage
	})
	row, _ := triageRowOf(t, server, 50)
	if row.Workstream.Valid || row.Title != "Rename plans" || row.Author != "mallory" || row.State != "open" {
		t.Errorf("row = %+v", row)
	}
}

func TestATrustedCreatorOfAnIssueInNoWorkstreamGetsTheTriageStateAndTheLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)

	fake.CreateIssue(shop, 51, "Rename plans", "Use the new name.", "owner")

	testkit.WaitFor(t, func() bool {
		row, found := triageRowOf(t, server, 51)
		return found && row.Triage && slices.Contains(fake.Labels(shop, 51), "mobius:triage")
	})
	row, _ := triageRowOf(t, server, 51)
	if row.Workstream.Valid || row.Body != "Use the new name." {
		t.Errorf("row = %+v", row)
	}
}

func TestAcceptIssueClearsTheTriageStateAndRemovesTheLabelAndRefusesAnIssueWithNoTriageState(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake,
		call("accept_issue", `{ repository = "owner/shop", n = 30 }`)+
			call("accept_issue", `{ repository = "owner/shop", n = 31 }`)+
			call("accept_issue", `{ repository = "owner/shop", n = 32 }`))
	fake.AddIssue(shop, 30, "Add plan price")
	fake.AddLabel(shop, 30, "mobius:triage", "owner")
	fake.AddIssue(shop, 31, "Rename plans")
	fake.AddIssue(shop, 32, "Outside")
	fake.AddSubIssue(shop, 12, 30)
	fake.AddSubIssue(shop, 12, 31)
	testkit.WaitFor(t, func() bool {
		first, found := triageRowOf(t, server, 30)
		_, second := triageRowOf(t, server, 31)
		return found && first.Triage && second
	})

	session := run(t, server, leadSpec(t), "1. ", "2. ", "3. ")

	want := "Accepted owner/shop#30." +
		"error: owner/shop#31 has no triage state." +
		"error: owner/shop#32 has no triage state."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	testkit.WaitFor(t, func() bool {
		row, _ := triageRowOf(t, server, 30)
		return !row.Triage && !slices.Contains(fake.Labels(shop, 30), "mobius:triage") && outgoingChanges(t, server, "pending") == 0
	})
	if _, found := triageRowOf(t, server, 32); found {
		t.Error("the refused call added a row for #32")
	}
}
