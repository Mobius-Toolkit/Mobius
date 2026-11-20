package engine_test

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const workstreamLabelName = "mobius:workstream"

func hasWorkstreamRow(t *testing.T, server *testserver.Server, number int64) bool {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM workstreams WHERE repository = ? AND number = ?", shop, number) == 1
}

func workstreamRow(t *testing.T, server *testserver.Server, number int64) (state, title, body string) {
	t.Helper()
	err := server.DB.QueryRow("SELECT state, title, body FROM workstreams WHERE repository = ? AND number = ?", shop, number).Scan(&state, &title, &body)
	if err != nil {
		t.Fatal(err)
	}
	return state, title, body
}

func eventsOfKind(t *testing.T, server *testserver.Server, kind string, workstream int64) []leadEvent {
	t.Helper()
	var found []leadEvent
	for _, event := range leadEvents(t, server) {
		if event.Kind == kind && event.Workstream == workstream {
			found = append(found, event)
		}
	}
	return found
}

// startWithChatLink starts a server with ui_url, so the body of a Workstream issue ends with the link to the Lead chat,
// and with a Lead that answers each prompt.
func startWithChatLink(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nreply = [\"Seen\"]\n")
	cfg := testserver.Config(t, dataDir)
	withUIURL(cfg)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	return server
}

func TestACloseOfAWorkstreamIssueByATrustedUserClosesTheRowAndRunsTheClose(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	fake.CloseIssueBy(shop, 12, "owner")

	testkit.WaitFor(t, func() bool {
		state, reason := fake.State(shop, 41)
		return state == "closed" && reason == "not_planned"
	})
	if state, _, _ := workstreamRow(t, server, 12); state != "closed" {
		t.Errorf("state = %s", state)
	}
}

func TestACloseAndThenARemovalOfTheWorkstreamLabelByATrustedUserInOnePollStillCloseTheTasks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)
	// The next poll reads both events together.
	fake.AfterNextIssueListRead(shop, func() {
		fake.CloseIssueBy(shop, 12, "owner")
		fake.RemoveLabel(shop, 12, workstreamLabelName, "owner")
	})

	testkit.WaitFor(t, func() bool {
		state, reason := fake.State(shop, 41)
		return state == "closed" && reason == "not_planned"
	})
	testkit.WaitFor(t, func() bool { return !hasWorkstreamRow(t, server, 12) })
}

// closeInOnePoll starts a server for fake, makes change and the close of the Workstream #12 while one poll waits for
// its issue list, and waits for more polls.
func closeInOnePoll(t *testing.T, fake *testkit.FakeGitHub, change func()) *testserver.Server {
	t.Helper()
	server := startCopied(t, fake)
	issueListReads := func() int {
		reads := 0
		for _, request := range fake.Requests() {
			if request.Method == "GET" && request.Path == "/repos/"+shop+"/issues" {
				reads++
			}
		}
		return reads
	}
	beforeReads := issueListReads()
	release := fake.HoldNext("GET /repos/{owner}/{repo}/issues")
	testkit.WaitFor(t, func() bool { return issueListReads() > beforeReads })

	change()
	fake.CloseIssueBy(shop, 12, "owner")
	release()

	waitForPolls(t, fake)
	return server
}

// closeWithNewSubIssue makes one poll see the close of #12 and the add of #42, which author wrote, as a sub-issue of
// #12 by adder.
func closeWithNewSubIssue(t *testing.T, author, adder string) *testkit.FakeGitHub {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 42, "Add plan view")
	fake.SetAuthor(shop, 42, author)
	closeInOnePoll(t, fake, func() { fake.AddSubIssueBy(shop, 12, 42, adder) })
	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 41); return state == "closed" })
	return fake
}

func requireClosedNotPlanned(t *testing.T, fake *testkit.FakeGitHub, numbers ...int64) {
	t.Helper()
	for _, number := range numbers {
		testkit.WaitFor(t, func() bool {
			state, reason := fake.State(shop, number)
			return state == "closed" && reason == "not_planned"
		})
		if got := len(fake.Comments(shop, number)); got != 1 {
			t.Errorf("comments of #%d = %d", number, got)
		}
	}
}

func TestAClosedWorkstreamAlsoClosesTheSubIssueOfANewSubIssueThatTheSamePollSees(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 42, "Add plan view")
	fake.AddIssue(shop, 43, "Add plan list")
	closeInOnePoll(t, fake, func() {
		fake.AddSubIssueBy(shop, 12, 42, "owner")
		fake.AddSubIssueBy(shop, 42, 43, "owner")
	})

	requireClosedNotPlanned(t, fake, 42, 43)
}

func TestAClosedWorkstreamAlsoClosesANewSubIssueOfAnIssueOfTheCopiedTreeThatTheSamePollSees(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 43, "Add plan list")
	closeInOnePoll(t, fake, func() { fake.AddSubIssueBy(shop, 41, 43, "owner") })

	requireClosedNotPlanned(t, fake, 43)
}

func TestAClosedWorkstreamAlsoClosesASubIssueThatTheSamePollSees(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, author string }{{"trusted author", "owner"}, {"author who is not trusted", "mallory"}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := closeWithNewSubIssue(t, test.author, "owner")

			requireClosedNotPlanned(t, fake, 42)
		})
	}
}

func TestAClosedWorkstreamKeepsANewSubIssueOfAClosedIssueOfTheCopiedTree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 40, "Add plan table")
	fake.AddSubIssue(shop, 12, 40)
	fake.CloseIssue(shop, 40)
	fake.AddIssue(shop, 43, "Add plan list")
	closeInOnePoll(t, fake, func() { fake.AddSubIssueBy(shop, 40, 43, "owner") })

	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 41); return state == "closed" })
	if state, _ := fake.State(shop, 43); state != "open" {
		t.Errorf("state of #43 = %s", state)
	}
	if got := len(fake.Comments(shop, 43)); got != 0 {
		t.Errorf("comments of #43 = %d", got)
	}
}

func TestARetryOfAClosedWorkstreamClosesTheSubIssueThatTheFirstTryDidNotClose(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 42, "Add plan view")
	fake.FailClose(shop, 42)
	server := closeInOnePoll(t, fake, func() { fake.AddSubIssueBy(shop, 12, 42, "owner") })
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM workstreams WHERE number = 12 AND close_pending = 1") == 1
	})
	if state, _ := fake.State(shop, 42); state != "open" {
		t.Fatalf("state of #42 = %s", state)
	}

	fake.AllowClose(shop, 42)

	requireClosedNotPlanned(t, fake, 42)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM workstreams WHERE number = 12 AND close_pending = 0") == 1
	})
}

func TestAClosedWorkstreamKeepsASubIssueThatAPersonWhoIsNotTrustedAddsInTheSamePoll(t *testing.T) {
	t.Parallel()
	fake := closeWithNewSubIssue(t, "owner", "mallory")

	if state, _ := fake.State(shop, 42); state != "open" {
		t.Errorf("state of #42 = %s", state)
	}
	if got := len(fake.Comments(shop, 42)); got != 0 {
		t.Errorf("comments of #42 = %d", got)
	}
}

func TestARetryOfAClosedWorkstreamKeepsASubIssueThatAPersonAddedAfterTheCloseEnded(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 42, "Add plan view")
	server := startCopied(t, fake)
	fake.FailClose(shop, 41)
	fake.CloseIssueBy(shop, 12, "owner")
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM workstreams WHERE number = 12 AND close_pending = 1") == 1
	})
	waitForPolls(t, fake)

	fake.AddSubIssueBy(shop, 12, 42, "owner")
	fake.AllowClose(shop, 41)

	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM workstreams WHERE number = 12 AND close_pending = 0 AND state = 'closed'") == 1
	})
	waitForPolls(t, fake)
	if state, _ := fake.State(shop, 42); state != "open" {
		t.Errorf("state of #42 = %s", state)
	}
}

func TestACloseOfAWorkstreamIssueByAPersonWhoIsNotTrustedIsWrittenBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	fake.CloseIssueBy(shop, 12, "mallory")

	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 12); return state == "open" })
	testkit.WaitFor(t, func() bool { return doneChangesOfPersons(t, server) == 1 && ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if state, _, _ := workstreamRow(t, server, 12); state != "open" {
		t.Errorf("state = %s", state)
	}
	if state, _ := fake.State(shop, 41); state != "open" {
		t.Errorf("state of #41 = %s", state)
	}
}

func TestARemovalOfTheWorkstreamLabelByAPersonWhoIsNotTrustedKeepsTheRowAndTheTasksAndAddsTheLabelAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addLiveTask(fake)
	server := startWithLiveTask(t, fake)

	fake.RemoveLabel(shop, 12, workstreamLabelName, "mallory")

	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 12), workstreamLabelName) })
	testkit.WaitFor(t, func() bool { return doneChangesOfPersons(t, server) == 1 && ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if !hasWorkstreamRow(t, server, 12) || !live(t, server) {
		t.Errorf("row = %t, live = %t", hasWorkstreamRow(t, server, 12), live(t, server))
	}
	if !slices.Contains(fake.Labels(shop, 41), "mobius:working") {
		t.Errorf("labels of #41 = %v", fake.Labels(shop, 41))
	}
}

func TestAnAddOfTheWorkstreamLabelByATrustedUserAddsTheRowAndACreationEventWithTheLogin(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 13, "Plan prices")
	server := startCopied(t, fake)

	fake.AddLabel(shop, 13, workstreamLabelName, "owner")

	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := eventsOfKind(t, server, "creation", 13)
		return events, len(events) > 0
	})
	if len(events) != 1 || !strings.Contains(events[0].Payload, ` creation of Workstream #13 "Plan prices" by @owner:`) {
		t.Errorf("events = %+v", events)
	}
	if !hasWorkstreamRow(t, server, 13) {
		t.Error("the Workstream has no row")
	}
}

func TestAnAddOfTheWorkstreamLabelByAPersonWhoIsNotTrustedAddsNoRowAndTheLabelGoesAway(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 13, "Plan prices")
	server := startCopied(t, fake)

	fake.AddLabel(shop, 13, workstreamLabelName, "mallory")

	testkit.WaitFor(t, func() bool { return fake.LabelRemovedBy(shop, 13, workstreamLabelName, testkit.AppSlug+"[bot]") })
	testkit.WaitFor(t, func() bool { return doneChangesOfPersons(t, server) == 1 && ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if hasWorkstreamRow(t, server, 13) {
		t.Error("the Workstream has a row")
	}
	if events := leadEvents(t, server); len(events) != 0 {
		t.Errorf("events = %+v", events)
	}
}

func TestARenameOfAWorkstreamIssueByATrustedUserChangesTheRowAndTheTitlesOfTheBlockers(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithBlocker(t, fake)

	fake.SetTitleBy(shop, 13, "Invoices", "owner")

	testkit.WaitFor(t, func() bool {
		_, title, _ := workstreamRow(t, server, 13)
		return title == "Invoices" && reflect.DeepEqual(copiedBlockers(t, server), []copiedBlocker{blocker(42, 60, 13, "Invoices")})
	})
}

func TestARenameOfAWorkstreamIssueByAPersonWhoIsNotTrustedIsWrittenBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithBlocker(t, fake)

	fake.SetTitleBy(shop, 13, "Invoices", "mallory")

	testkit.WaitFor(t, func() bool { title, _ := fake.Issue(shop, 13); return title == "Billing" })
	testkit.WaitFor(t, func() bool { return doneChangesOfPersons(t, server) == 1 && ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if _, title, _ := workstreamRow(t, server, 13); title != "Billing" {
		t.Errorf("title = %q", title)
	}
	if got := copiedBlockers(t, server); !reflect.DeepEqual(got, []copiedBlocker{blocker(42, 60, 13, "Billing")}) {
		t.Errorf("blockers = %+v", got)
	}
}

func TestABodyEditByATrustedUserChangesTheBriefWithNoLinkLineAndGivesTheLeadAnEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.SetBody(shop, 12, "Ship loyalty plans.\n\n"+chatLink)
	server := startWithChatLink(t, fake)

	fake.SetBodyBy(shop, 12, "Ship loyalty plans and gift cards.\n\n"+chatLink, "owner")

	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := eventsOfKind(t, server, "brief_change", 12)
		return events, len(events) > 0
	})
	if len(events) != 1 || !strings.Contains(events[0].Payload, ` change of the Brief of Workstream #12 "Integrate loyalty plans" by @owner:`) ||
		!strings.HasSuffix(events[0].Payload, "> Ship loyalty plans and gift cards.") {
		t.Errorf("events = %+v", events)
	}
	if _, _, body := workstreamRow(t, server, 12); body != "Ship loyalty plans and gift cards." {
		t.Errorf("Brief = %q", body)
	}
	waitForPolls(t, fake)
	if got := eventsOfKind(t, server, "brief_change", 12); len(got) != 1 {
		t.Errorf("events = %+v", got)
	}
}

func TestABodyEditByAPersonWhoIsNotTrustedIsWrittenBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.SetBody(shop, 12, "Ship loyalty plans.\n\n"+chatLink)
	server := startWithChatLink(t, fake)

	fake.SetBodyBy(shop, 12, "Ship nothing.", "mallory")

	testkit.WaitFor(t, func() bool { _, body := fake.Issue(shop, 12); return body == "Ship loyalty plans.\n\n"+chatLink })
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'body' AND status = 'done'") == 1
	})
	waitForPolls(t, fake)
	if _, _, body := workstreamRow(t, server, 12); body != "Ship loyalty plans." {
		t.Errorf("Brief = %q", body)
	}
	if got := eventsOfKind(t, server, "brief_change", 12); len(got) != 0 {
		t.Errorf("events = %+v", got)
	}
}

func TestABodyEditByTheMobiusAppAddsNoIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.SetBody(shop, 12, "Ship loyalty plans.\n\n"+chatLink)
	server := startWithChatLink(t, fake)

	fake.SetBodyBy(shop, 12, "Ship loyalty plans and gift cards.\n\n"+chatLink, testkit.AppSlug+"[bot]")

	waitForPolls(t, fake)
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'body'"); got != 0 {
		t.Errorf("incoming body changes = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field = 'body'"); got != 0 {
		t.Errorf("outgoing body changes = %d", got)
	}
}

func TestTheSendOfTheUICloseAddsNoIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.CloseIssue(shop, 41)
	server := startCopied(t, fake)

	if status, text := complete(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, text)
	}

	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 12); return state == "closed" })
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if got := doneChangesOfPersons(t, server); got != 0 {
		t.Errorf("incoming changes of a person = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE number = 12 AND field = 'state'"); got != 1 {
		t.Errorf("state outgoing changes = %d", got)
	}
	if state, _, _ := workstreamRow(t, server, 12); state != "closed" {
		t.Errorf("state = %s", state)
	}
}

func TestTheSendOfAnEditBriefAddsNoIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, approvalSeen+callEditBrief("Change it.", "Ship loyalty plans to all shops.", "Ships everywhere.")+seenScript, withUIURL)

	run(t, server, leadSpec(t), "Change it.")
	approveNext(t, server, 1)

	testkit.WaitFor(t, func() bool {
		_, body := fake.Issue(shop, 12)
		return strings.HasPrefix(body, "Ship loyalty plans to all shops.")
	})
	waitForPolls(t, fake)
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field <> 'workstream'"); got != 0 {
		t.Errorf("incoming changes = %d", got)
	}
	if got := eventsOfKind(t, server, "brief_change", 12); len(got) != 0 {
		t.Errorf("events = %+v", got)
	}
}

func TestARemovalOfTheWorkstreamLabelThatTheGHOfTheLeadMadeIsWrittenBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectLeadWrites(t, fake, "gh issue edit 12 -R owner/shop --remove-label mobius:workstream")
	leadWrites(t, server, 1)

	fake.RemoveLabel(shop, 12, workstreamLabelName, app)

	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 12), workstreamLabelName) })
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if !hasWorkstreamRow(t, server, 12) {
		t.Error("the Workstream has no row")
	}
	if !fake.LabelAddedBy(shop, 12, workstreamLabelName, app) {
		t.Error("the App did not add the label again")
	}
}

func TestAnApprovedCreateWorkstreamKeepsTheWorkstreamLabelAndGivesOneCreationEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+call("create_workstream", `{ title = "Billing", brief = "Bill the plans." }`))

	run(t, server, leadSpec(t), "Create it.")
	approveNext(t, server, 1)

	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if !slices.Contains(fake.Labels(shop, 13), workstreamLabelName) {
		t.Errorf("labels = %v", fake.Labels(shop, 13))
	}
	if !hasWorkstreamRow(t, server, 13) {
		t.Error("the Workstream has no row")
	}
	if got := eventsOfKind(t, server, "creation", 13); len(got) != 1 {
		t.Errorf("creation events = %+v", got)
	}
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
}
