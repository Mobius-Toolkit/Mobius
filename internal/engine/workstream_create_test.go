package engine_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const createLater = "Mobius creates the Workstream on GitHub later. The number comes after the send."

func chatLinkOf(number int64) string {
	return strings.Replace(chatLink, "/12)", fmt.Sprintf("/%d)", number), 1)
}

// startCreate starts a server whose Coordinator asks to create the Workstream Upkeep, and gives the id of the action.
func startCreate(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, int64) {
	t.Helper()
	server, _ := connectWith(t, fake, approvalSeen+callCreateWorkstream("Create.", "Upkeep", "Keep the code healthy.", "mobius:maintenance")+seenScript, withUIURL)
	run(t, server, coordinatorSpec(t), "Create.")
	return server, waitForActions(t, server, 1)[0].ID
}

func upkeepRows(t *testing.T, server *testserver.Server, condition string) int {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM workstreams WHERE title = 'Upkeep' AND "+condition)
}

func creationEvents(t *testing.T, server *testserver.Server) int {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM lead_events WHERE repository = ? AND workstream = 13 AND kind = 'creation'", shop)
}

func TestAnApprovedCreateWorkstreamGivesTheNumberAndTheIssueHasTheTitleTheBriefWithTheLinkAndTheLabels(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)

	done := decided(t, server, action, "approve", "")

	if done.State != "done" || done.Result != "Created the Workstream #13." {
		t.Errorf("action = %+v", done)
	}
	testkit.WaitFor(t, func() bool {
		labels := fake.Labels(shop, 13)
		_, body := fake.Issue(shop, 13)
		return slices.Contains(labels, "mobius:workstream") && slices.Contains(labels, "mobius:evergreen") && slices.Contains(labels, "mobius:maintenance") &&
			strings.HasSuffix(body, chatLinkOf(13))
	})
	title, body := fake.Issue(shop, 13)
	if want := "Keep the code healthy.\n\n" + chatLinkOf(13); title != "Upkeep" || body != want {
		t.Errorf("#13 = %q, %q, want body %q", title, body, want)
	}
	if got := upkeepRows(t, server, "number = 13 AND state = 'open' AND body = 'Keep the code healthy.' AND evergreen = 1 AND maintenance = 1 AND autopilot = 0"); got != 1 {
		t.Errorf("rows = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field = 'create' AND status = 'sent' AND number IS NULL AND workstream IS NOT NULL"); got != 1 {
		t.Errorf("sent create changes = %d", got)
	}
	if got := creationEvents(t, server); got != 1 {
		t.Errorf("creation events = %d", got)
	}
}

func TestAFailedFirstCreateGivesTheResultThatMobiusCreatesTheWorkstreamLaterAndALaterPollCreatesTheIssueAndAddsOneCreationEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)
	fake.FailIssueCreates(1)

	done := decided(t, server, action, "approve", "")

	if done.State != "done" || done.Result != createLater {
		t.Errorf("action = %+v", done)
	}
	if got := upkeepRows(t, server, "number IS NULL AND state = 'open'"); got != 1 {
		t.Errorf("rows with no number = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field = 'create' AND status = 'pending' AND number IS NULL"); got != 1 {
		t.Errorf("pending create changes = %d", got)
	}
	if got := creationEvents(t, server); got != 0 {
		t.Errorf("creation events before the send = %d", got)
	}

	testkit.WaitFor(t, func() bool { return upkeepRows(t, server, "number = 13") == 1 })
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 13), "mobius:workstream") })
	waitForPolls(t, fake)
	if title, _ := fake.Issue(shop, 13); title != "Upkeep" || fake.HasIssue(shop, 14) {
		t.Errorf("title = %q, second issue = %v", title, fake.HasIssue(shop, 14))
	}
	if got := creationEvents(t, server); got != 1 {
		t.Errorf("creation events = %d", got)
	}
}

func approvalMessages(t *testing.T, server *testserver.Server, text string) int {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM chat_messages WHERE author = 'Approval' AND instr(text, ?) > 0", text)
}

func TestTheChatOfTheCoordinatorGetsTheApprovalMessageWithTheNumberOneTimeAfterALaterPollFillsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)
	fake.FailIssueCreates(1)

	decided(t, server, action, "approve", "")

	testkit.WaitFor(t, func() bool { return approvalMessages(t, server, "The result: Created the Workstream #13.") == 1 })
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if got := approvalMessages(t, server, "The result: Created the Workstream #13."); got != 1 {
		t.Errorf("messages with the number = %d", got)
	}
	if got := approvalMessages(t, server, "The result: "+createLater); got != 1 {
		t.Errorf("messages that the number comes later = %d", got)
	}
}

func TestTheChatOfTheCoordinatorGetsOneApprovalMessageWithTheRefusalWhenALaterPollGetsA4xxOfTheCreate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)
	fake.FailIssueCreates(1)
	fake.RefuseIssueCreates(http.StatusUnprocessableEntity)

	done := decided(t, server, action, "approve", "")

	if done.State != "done" || done.Result != createLater {
		t.Errorf("action = %+v", done)
	}
	testkit.WaitFor(t, func() bool { return approvalMessages(t, server, "The result: GitHub refused the write.") == 1 })
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if got := approvalMessages(t, server, "The result: GitHub refused the write."); got != 1 {
		t.Errorf("messages with the refusal = %d", got)
	}
	if got := approvalMessages(t, server, "Created the Workstream #"); got != 0 {
		t.Errorf("messages with a number = %d", got)
	}
	if got := upkeepRows(t, server, "1 = 1"); got != 0 {
		t.Errorf("rows = %d", got)
	}
}

// Serial: the test changes the hook afterActionRun.
func TestARefusalOfTheCreateBeforeTheActionIsDoneComesAfterTheMessageThatTheNumberComesLater(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)
	fake.FailIssueCreates(1)
	*engine.AfterActionRun = func() {
		fake.RefuseIssueCreates(http.StatusUnprocessableEntity)
		waitForPolls(t, fake)
		waitForPolls(t, fake)
	}
	t.Cleanup(func() { *engine.AfterActionRun = nil })

	done := decided(t, server, action, "approve", "")

	if done.State != "done" || done.Result != createLater {
		t.Errorf("action = %+v", done)
	}
	testkit.WaitFor(t, func() bool { return approvalMessages(t, server, "The result: GitHub refused the write.") == 1 })
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if got := approvalMessages(t, server, "The result: GitHub refused the write."); got != 1 {
		t.Errorf("messages with the refusal = %d", got)
	}
	if got := countRows(t, server, `SELECT count(*) FROM chat_messages refusal WHERE refusal.author = 'Approval' AND instr(refusal.text, 'The result: GitHub refused the write.') > 0
		AND EXISTS (SELECT 1 FROM chat_messages later WHERE later.author = 'Approval' AND instr(later.text, 'The result: '||?) > 0 AND later.id < refusal.id)`, createLater); got != 1 {
		t.Errorf("refusals after the message that the number comes later = %d", got)
	}
}

func TestTheTriagerGetsOneCommentWithTheRefusalWhenALaterPollGetsA4xxOfTheCreate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 66, "Add refunds", "Refund each order.", "owner")
	proposal := triagerComment(66, "Proposal: the Workstream Refunds.")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 66), proposal) })
	fake.AddComment(shop, 66, "owner", "Create it as proposed.")
	action := waitForActions(t, server, 1)[0]
	fake.FailIssueCreates(1)
	fake.RefuseIssueCreates(http.StatusUnprocessableEntity)

	decided(t, server, action.ID, "approve", "")

	refusals := func() int {
		n := 0
		for _, comment := range fake.Comments(shop, 66) {
			if strings.Contains(comment.Body, "The result: GitHub refused the write.") {
				n++
			}
		}
		return n
	}
	testkit.WaitFor(t, func() bool { return refusals() == 1 })
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if got := refusals(); got != 1 {
		t.Errorf("comments with the refusal = %d", got)
	}
	if state, _ := fake.State(shop, 66); state != "open" {
		t.Errorf("state = %s", state)
	}
}

func TestACreateThatTheFirstSendFillsGivesNoSecondApprovalMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)

	decided(t, server, action, "approve", "")

	testkit.WaitFor(t, func() bool { return approvalMessages(t, server, "The result: Created the Workstream #13.") == 1 })
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if got := approvalMessages(t, server, "Created the Workstream #13."); got != 1 {
		t.Errorf("messages with the number = %d", got)
	}
}

func TestACreateThatGitHubMadeButAnsweredWith500IsFoundByItsMarkerAndGitHubHasOneIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)
	fake.LoseIssueCreateAnswers(1)

	done := decided(t, server, action, "approve", "")

	if done.State != "done" || done.Result != createLater {
		t.Errorf("action = %+v", done)
	}
	if !fake.HasIssue(shop, 13) {
		t.Fatal("GitHub made no issue")
	}
	testkit.WaitFor(t, func() bool { return upkeepRows(t, server, "number = 13") == 1 })
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 13), "mobius:workstream") })
	waitForPolls(t, fake)
	if fake.HasIssue(shop, 14) {
		t.Error("GitHub has two issues")
	}
	if got := creationEvents(t, server); got != 1 {
		t.Errorf("creation events = %d", got)
	}
}

func TestARefusedCreateDeletesTheRowAndASecondApprovalAddsANewRowAndCreatesTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)
	fake.RefuseIssueCreates(http.StatusUnprocessableEntity)

	refused := decided(t, server, action, "approve", "")

	if refused.State != "pending" || refused.Error != "GitHub refused the write." {
		t.Errorf("action = %+v", refused)
	}
	if got := upkeepRows(t, server, "1 = 1"); got != 0 {
		t.Errorf("rows = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field = 'create' AND status = 'refused'"); got != 1 {
		t.Errorf("refused create changes = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM pending_actions WHERE id = ? AND created_workstream IS NULL", action); got != 1 {
		t.Errorf("actions with no created Workstream = %d", got)
	}

	fake.RefuseIssueCreates(0)
	done := decided(t, server, action, "approve", "")

	if done.State != "done" || done.Result != "Created the Workstream #13." {
		t.Errorf("action = %+v", done)
	}
	if got := upkeepRows(t, server, "number = 13"); got != 1 {
		t.Errorf("rows = %d", got)
	}
}

func TestTheLabelWritesOfTheCreateAddNoIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)

	decided(t, server, action, "approve", "")

	testkit.WaitFor(t, func() bool {
		labels := fake.Labels(shop, 13)
		return slices.Contains(labels, "mobius:workstream") && slices.Contains(labels, "mobius:evergreen") && slices.Contains(labels, "mobius:maintenance")
	})
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE number = 13 AND actor <> ?", testkit.AppSlug+"[bot]"); got != 0 {
		t.Errorf("incoming changes of a person for #13 = %d", got)
	}
	if got := upkeepRows(t, server, "number = 13 AND state = 'open'"); got != 1 {
		t.Errorf("rows = %d", got)
	}
	// The changes of the create: the label, the two settings and the Brief. A handler of an incoming change would add a change that writes a label back.
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE number = 13 AND status = 'sent' AND field IN ('workstream', 'evergreen', 'maintenance', 'body')") == 4
	})
	if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE number = 13"); got != 4 {
		t.Errorf("changes of #13 = %d", got)
	}
}

func TestACreateOfTheTriagerClosesTheTriagedIssueWithTheLinkToTheNewWorkstreamAfterTheSend(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 66, "Add refunds", "Refund each order.", "owner")
	proposal := triagerComment(66, "Proposal: the Workstream Refunds.")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 66), proposal) })
	fake.AddComment(shop, 66, "owner", "Create it as proposed.")
	action := waitForActions(t, server, 1)[0]
	fake.FailIssueCreates(1)

	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Result != createLater {
		t.Errorf("action = %+v", done)
	}
	if state, _ := fake.State(shop, 66); state != "open" {
		t.Errorf("state before the send = %s", state)
	}
	link := triagerComment(66, "The triage ends. The Workstream is https://github.com/owner/shop/issues/67")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 66), link) })
	testkit.WaitFor(t, func() bool { state, reason := fake.State(shop, 66); return state == "closed" && reason == "completed" })
	if title, _ := fake.Issue(shop, 67); title != "Refunds" {
		t.Errorf("#67 = %q", title)
	}
}

func TestTheListOfWorkstreamsHasNoRowWithNoNumber(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, action := startCreate(t, fake)
	fake.FailIssueCreates(1000)

	decided(t, server, action, "approve", "")

	if got := upkeepRows(t, server, "number IS NULL"); got != 1 {
		t.Fatalf("rows with no number = %d", got)
	}
	waitForPolls(t, fake)
	found := workstreams(t, server)
	if len(found) != 1 || found[0].Number != 12 {
		t.Errorf("workstreams = %+v", found)
	}
}
