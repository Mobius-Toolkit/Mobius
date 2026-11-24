package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func displayOnlyMessages(t *testing.T, server *testserver.Server, key engine.ChatKey) []ownerChatMessage {
	t.Helper()
	return slices.DeleteFunc(ownerChatMessages(t, server, key), func(m ownerChatMessage) bool { return !m.DisplayOnly })
}

func TestTheOwnerApprovesAnActionOfTheLeadWithAMessageAndTheChatShowsOneOwnerMessageThatTheLeadDoesNotGet(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "# Approval message"
reply = ["Created the Workstream #13."]

[[prompts]]
when = "Move the API work to a new Workstream."
call = { tool = "create_workstream", arguments = { title = "Shop API", brief = "The public API of the shop. The context is in #12." } }
`)
	sendChat(t, server, leadChat, "Move the API work to a new Workstream.")
	action := waitForActions(t, server, 1)[0]
	item := waitForItem(t, server, "approval")

	decided(t, server, action.ID, "approve", "Use the same name.")

	waitForChat(t, server, leadChat, "Lead", "Created the Workstream #13.")
	waitForNoItem(t, server, "approval")
	wantCards := []apiInboxItem{{Title: item.Title, Reason: item.Reason}}
	got := displayOnlyMessages(t, server, leadChat)
	if len(got) != 1 || got[0].Author != "Owner" || got[0].Text != "Approved\n\nUse the same name." ||
		got[0].BrowserID != "" || got[0].DeliveredAt != nil || !slices.Equal(got[0].InboxItems, wantCards) {
		t.Errorf("messages = %+v, want one Owner message with card %+v", got, wantCards)
	}
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, "# Owner message") && strings.Contains(prompt, "Approved") {
			t.Errorf("the Lead got the decision through the chat: %s", prompt)
		}
	}
}

func TestTheOwnerRejectsAnActionOfTheCoordinatorWithNoMessageAndTheChatShowsOneOwnerMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
	run(t, server, coordinatorSpec(t), "Send.")
	action := waitForActions(t, server, 1)[0]
	item := waitForItem(t, server, "approval")

	decided(t, server, action.ID, "reject", "")

	waitForNoItem(t, server, "approval")
	wantCards := []apiInboxItem{{Title: item.Title, Reason: item.Reason}}
	got := displayOnlyMessages(t, server, coordinatorChat)
	if len(got) != 1 || got[0].Author != "Owner" || got[0].Text != "Rejected" ||
		got[0].BrowserID != "" || got[0].DeliveredAt != nil || !slices.Equal(got[0].InboxItems, wantCards) {
		t.Errorf("messages = %+v, want one Owner message with card %+v", got, wantCards)
	}
}

func TestAnApprovalThatFailsAddsNoOwnerMessageToTheChat(t *testing.T) {
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
	if got := displayOnlyMessages(t, server, coordinatorChat); len(got) != 0 {
		t.Errorf("messages = %+v", got)
	}
}

func TestTheOwnerDecidesAnActionOfTheTriagerAndNoChatGetsAnOwnerMessage(t *testing.T) {
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
	if got := count(t, server, "SELECT COUNT(*) FROM chat_messages WHERE display_only = 1"); got != 0 {
		t.Errorf("display-only messages = %d, want 0", got)
	}
}
