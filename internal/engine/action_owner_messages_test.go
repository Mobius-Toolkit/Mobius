package engine_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func displayOnlyOwnerMessages(t *testing.T, server *testserver.Server, key engine.ChatKey) []ownerChatMessage {
	t.Helper()
	return slices.DeleteFunc(ownerChatMessages(t, server, key), func(m ownerChatMessage) bool { return !m.DisplayOnly || m.Author != "Owner" })
}

const createWorkstreamScript = `
[[prompts]]
when = "# Approval message"
reply = ["Created the Workstream #13."]

[[prompts]]
when = "Move the API work to a new Workstream."
call = { tool = "create_workstream", arguments = { title = "Shop API", brief = "The public API of the shop. The context is in #12." } }
`

func TestTheOwnerApprovesAnActionOfTheLeadWithAMessageAndTheChatShowsOneRecordLineThatTheLeadDoesNotGet(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, createWorkstreamScript)
	sendChat(t, server, leadChat, "Move the API work to a new Workstream.")
	action := waitForActions(t, server, 1)[0]
	item := waitForItem(t, server, "approval")

	decided(t, server, action.ID, "approve", "Use the same name.")

	waitForChat(t, server, leadChat, "Lead", "Created the Workstream #13.")
	waitForNoItem(t, server, "approval")
	waitForRecords(t, server, leadChat, `You approved "`+item.Title+"\".\n\nUse the same name.")
	if got := displayOnlyOwnerMessages(t, server, leadChat); len(got) != 0 {
		t.Errorf("Owner messages = %+v, want none", got)
	}
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, "You approved") {
			t.Errorf("the Lead got the Record: %s", prompt)
		}
	}
}

func TestTheCuratorPromptHasNoDecisionMessageOfTheOwnerOnAnActionOfTheLead(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), createWorkstreamScript+curatorScript)
	addChat(t, server, "Lead", "Lead reply alpha", time.Now().Add(-time.Hour))
	addChat(t, server, "Owner", "Owner correction alpha", time.Now().Add(-time.Hour+time.Second))
	sendChat(t, server, leadChat, "Move the API work to a new Workstream.")
	action := waitForActions(t, server, 1)[0]
	decided(t, server, action.ID, "approve", "Use the same name.")
	waitForNoItem(t, server, "approval")
	waitForChat(t, server, leadChat, "Lead", "Created the Workstream #13.")
	if got := records(t, server, leadChat); len(got) != 1 {
		t.Fatalf("records = %q, want one decision Record", got)
	}

	prompt := nextCuratorPrompt(t, server, 1)

	if !strings.Contains(prompt, "Owner correction alpha") {
		t.Errorf("the prompt has no correction of the Owner: %s", prompt)
	}
	if strings.Contains(prompt, "Use the same name.") {
		t.Errorf("the prompt has the decision message of the Owner: %s", prompt)
	}
}

func TestTheOwnerRejectsAnActionOfTheCoordinatorWithNoMessageAndTheChatShowsOneRecordLine(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	run(t, server, coordinatorSpec(t), "Send.")
	action := waitForActions(t, server, 1)[0]
	item := waitForItem(t, server, "approval")

	decided(t, server, action.ID, "reject", "")

	waitForNoItem(t, server, "approval")
	waitForRecords(t, server, coordinatorChat, `You rejected "`+item.Title+`".`)
	if got := displayOnlyOwnerMessages(t, server, coordinatorChat); len(got) != 0 {
		t.Errorf("Owner messages = %+v, want none", got)
	}
	if got := records(t, server, leadChat); len(got) != 0 {
		t.Errorf("records of the Lead chat = %q, want none", got)
	}
}

func TestAnApprovalThatFailsAddsNoRecordToTheChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 20, "Create the task issues.")+seenScript)
	run(t, server, coordinatorSpec(t), "Send.")
	action := waitForActions(t, server, 1)[0]

	failed := decided(t, server, action.ID, "approve", "Go.")

	if failed.State != "pending" {
		t.Fatalf("action = %+v", failed)
	}
	testkit.WaitFor(t, func() bool { return len(approvalPrompts(t, server)) == 1 })
	if got := records(t, server, coordinatorChat); len(got) != 0 {
		t.Errorf("records = %q", got)
	}
}

func TestTheOwnerDecidesAnActionOfTheTriagerAndNoChatGetsARecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 72, "Add refunds", "Refund each order.", "owner")
	proposal := triagerComment(72, "Proposal: a message to the Lead of #12.")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 72), proposal) })
	fake.AddComment(shop, 72, "owner", "Send it to the Lead.")
	action := waitForActions(t, server, 1)[0]

	done := decided(t, server, action.ID, "approve", "Go.")

	if done.State != "done" {
		t.Fatalf("action = %+v", done)
	}
	waitForNoItem(t, server, "approval")
	waitForPolls(t, fake)
	if got := count(t, server, "SELECT COUNT(*) FROM chat_messages WHERE display_only = 1 OR author = 'Record'"); got != 0 {
		t.Errorf("display-only messages and Records = %d, want 0", got)
	}
}
