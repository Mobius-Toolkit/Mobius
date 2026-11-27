package engine_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
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

// triageState gives the row, the labels, the unsent outgoing changes and the incoming changes of the issue number, for
// the failure message of a wait.
func triageState(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, number int64) string {
	t.Helper()
	scan := func(rows *sql.Rows, line *string) error { return rows.Scan(line) }
	row, _ := triageRowOf(t, server, number)
	outgoing := query(t, server, scan, `SELECT printf('%d #%s %s %s', id, coalesce(number, ''), field, status) FROM outgoing_changes WHERE status != 'sent'`)
	incoming := query(t, server, scan, `SELECT printf('%d %s %s by %s %s', id, field, change, actor, status) FROM incoming_changes WHERE number = ?`, number)
	return fmt.Sprintf("row = %+v\nlabels = %q\nunsent outgoing changes = %q\nincoming changes = %q", row, fake.Labels(shop, number), outgoing, incoming)
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
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(triageState(t, server, fake, 30))
		}
	})
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

func TestAnAcceptedIssueStaysAcceptedWhenTheCopyOfTheTreeAddsItsRowBeforeThePollReadsTheLabelEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 30, "Add plan price")
	fake.AddIssue(shop, 31, "Rename plans")
	server, _ := connect(t, fake, call("accept_issue", `{ repository = "owner/shop", n = 30 }`))
	var release func()
	// The page of the next poll has no label on #30, and its copy of the tree reads #30 with the label.
	fake.AfterNextIssueListRead(shop, func() {
		release = fake.HoldNext("GET /repos/{owner}/{repo}/issues")
		fake.AddLabel(shop, 30, "mobius:triage", "owner")
		fake.AddSubIssue(shop, 12, 30)
		fake.AddSubIssue(shop, 12, 31)
	})
	testkit.WaitFor(t, func() bool {
		first, found := triageRowOf(t, server, 30)
		_, second := triageRowOf(t, server, 31)
		return found && first.Triage && second
	})

	session := run(t, server, leadSpec(t), "1. ")
	if got := reply(t, server, session); got != "Accepted owner/shop#30." {
		t.Errorf("reply = %q", got)
	}
	release()
	waitForPolls(t, fake)

	row, _ := triageRowOf(t, server, 30)
	if row.Triage || slices.Contains(fake.Labels(shop, 30), "mobius:triage") {
		t.Error(triageState(t, server, fake, 30))
	}
}

func TestAnAcceptedIssueStaysAcceptedWhenTheAcceptComesAfterThePollReadTheLabelEventAndBeforeThePollReadsTheRemoval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 30, "Add plan price")
	fake.AddIssue(shop, 31, "Rename plans")
	server, _ := connect(t, fake, call("accept_issue", `{ repository = "owner/shop", n = 30 }`))
	read := make(chan struct{})
	accepted := make(chan struct{})
	var release func()
	// The copy of the tree gives #30 the triage state from the label. The next poll then reads the label event, and the
	// answer of that read waits for the accept.
	fake.AfterNextIssueListRead(shop, func() {
		release = fake.HoldNext("GET /repos/{owner}/{repo}/issues")
		fake.AddLabel(shop, 30, "mobius:triage", "owner")
		fake.AddSubIssue(shop, 12, 30)
		fake.AddSubIssue(shop, 12, 31)
	})
	testkit.WaitFor(t, func() bool {
		first, found := triageRowOf(t, server, 30)
		_, second := triageRowOf(t, server, 31)
		return found && first.Triage && second
	})
	fake.AfterNextIssueListRead(shop, func() {
		fake.AfterNextIssueEventsRead(shop, 30, func() {
			close(read)
			<-accepted
		})
	})
	release()
	<-read

	session := run(t, server, leadSpec(t), "1. ")
	if got := reply(t, server, session); got != "Accepted owner/shop#30." {
		t.Errorf("reply = %q", got)
	}
	close(accepted)
	waitForPolls(t, fake)

	row, _ := triageRowOf(t, server, 30)
	if row.Triage || slices.Contains(fake.Labels(shop, 30), "mobius:triage") {
		t.Error(triageState(t, server, fake, 30))
	}
}

func TestARemovalOfMobiusTriageByATrustedUserAndThenAnAddByAnUntrustedUserInOnePollEndWithNoTriageState(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)
	fake.AddLabel(shop, 41, "mobius:triage", "owner")
	testkit.WaitFor(t, func() bool { return issueRowOf(t, server, shop, 41).Triage && doneChangesOfPersons(t, server) == 1 })
	// The next poll reads both events together.
	fake.AfterNextIssueListRead(shop, func() {
		fake.RemoveLabel(shop, 41, "mobius:triage", "owner")
		fake.AddLabel(shop, 41, "mobius:triage", "mallory")
	})

	testkit.WaitFor(t, func() bool { return doneChangesOfPersons(t, server) == 3 })
	waitForPolls(t, fake)

	if issueRowOf(t, server, shop, 41).Triage || slices.Contains(fake.Labels(shop, 41), "mobius:triage") {
		t.Error(triageState(t, server, fake, 41))
	}
}

func TestARefusedWriteOfMobiusTriageLeavesTheLabelsOfTheRowAndMarksTheChangeRefused(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)
	fake.AddLabel(shop, 41, "mobius:triage", "owner")
	testkit.WaitFor(t, func() bool { return issueRowOf(t, server, shop, 41).Triage && doneChangesOfPersons(t, server) == 1 })
	fake.RefuseAddLabels(shop, 41, http.StatusUnprocessableEntity)

	fake.RemoveLabel(shop, 41, "mobius:triage", "mallory")

	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "refused") == 1 })
	waitForPolls(t, fake)
	row := issueRowOf(t, server, shop, 41)
	if !row.Triage || strings.Contains(row.Labels, "mobius:triage") {
		t.Errorf("triage = %t, labels = %s", row.Triage, row.Labels)
	}
}

func TestTheRemovalOfMobiusTriageByAnUntrustedPersonDoesNotStopTheTriager(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 51, "Rename plans")
	fake.AddLabel(shop, 51, "mobius:triage", "owner")
	waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return !session.EndedAt.Valid })

	fake.RemoveLabel(shop, 51, "mobius:triage", "mallory")

	testkit.WaitFor(t, func() bool {
		return doneChangesOfPersons(t, server) == 2 && incomingChanges(t, server, "pending") == 0 && ownWrites(t, server) == 0 &&
			slices.Contains(fake.Labels(shop, 51), "mobius:triage")
	})
	waitForPolls(t, fake)
	if sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(sessions) != 1 || sessions[0].EndedAt.Valid {
		t.Errorf("sessions = %+v", sessions)
	}
}

func TestTheFirstPollAddsARowWithNoWorkstreamForAnOpenIssueWithMobiusTriageAndAcceptIssueAcceptsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.CreateIssue(shop, 30, "Add plan price", "Show the price.", "mallory")
	fake.AddLabel(shop, 30, "mobius:triage", "owner")
	fake.CreateIssue(shop, 31, "Rename plans", "Use the new name.", "owner")
	server, _ := connect(t, fake, call("accept_issue", `{ repository = "owner/shop", n = 30 }`))

	row, found := triageRowOf(t, server, 30)
	if !found || !row.Triage || row.Workstream.Valid || row.Title != "Add plan price" || row.Body != "Show the price." || row.Author != "mallory" || row.State != "open" {
		t.Errorf("row = %+v, found = %t", row, found)
	}
	if _, found := triageRowOf(t, server, 31); found {
		t.Error("the first poll added a row for #31, which has no label")
	}
	session := run(t, server, leadSpec(t), "1. ")

	if got := reply(t, server, session); got != "Accepted owner/shop#30." {
		t.Errorf("reply = %q", got)
	}
	testkit.WaitFor(t, func() bool {
		row, _ := triageRowOf(t, server, 30)
		return !row.Triage && !slices.Contains(fake.Labels(shop, 30), "mobius:triage") && outgoingChanges(t, server, "pending") == 0
	})
	waitForPolls(t, fake)
	if row, _ := triageRowOf(t, server, 30); row.Triage {
		t.Error("a later poll set the triage state again")
	}
}

func TestACommentOnAnIssueWithTheTriageStateAndNoLabelStartsTheTriagerAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 53, "Add points")
	fake.AddLabel(shop, 53, "mobius:triage", "owner")
	waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	testkit.WaitFor(t, func() bool {
		return len(fake.Comments(shop, 53)) == 1 && outgoingChanges(t, server, "pending") == 0 && doneChangesOfPersons(t, server) == 1
	})
	fake.RefuseAddLabels(shop, 53, http.StatusUnprocessableEntity)
	fake.RemoveLabel(shop, 53, "mobius:triage", "mallory")
	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "refused") == 1 })
	if !issueRowOf(t, server, shop, 53).Triage || slices.Contains(fake.Labels(shop, 53), "mobius:triage") {
		t.Fatalf("triage = %t, labels = %v", issueRowOf(t, server, shop, 53).Triage, fake.Labels(shop, 53))
	}

	fake.AddComment(shop, 53, "owner", "Please use points.")

	second := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions[len(sessions)-1], len(sessions) == 2 && sessions[1].EndedAt.Valid
	})
	if prompts := promptTexts(t, server, second.ID); len(prompts) != 1 || !strings.Contains(prompts[0], ":\nPlease use points.\n") {
		t.Errorf("prompts = %q", prompts)
	}
}
