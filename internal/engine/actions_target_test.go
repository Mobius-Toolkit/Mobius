package engine_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func addBilling(fake *testkit.FakeGitHub) {
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
}

func TestTwoMessageLeadCallsToTwoWorkstreamsBothStayPending(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBilling(fake)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send 1.", 12, "First text.")+callMessageLead("Send 2.", 20, "Second text.")+seenScript)

	session := run(t, server, coordinatorSpec(t), "Send 1.", "Send 2.")

	rows := waitForActions(t, server, 2)
	if rows[0].State != "pending" || rows[1].State != "pending" {
		t.Errorf("rows = %+v", rows)
	}
	if items := itemsOf(t, server, "approval"); len(items) != 2 {
		t.Errorf("items = %+v", items)
	}
	calls := mcpCalls(t, server, session)
	if len(calls) != 2 || calls[1]["result"] != actionWaitsText("Message to the Lead of #20 Billing", rows[1].InboxItem.Int64) {
		t.Errorf("calls = %v", calls)
	}
}

func TestTwoTellCuratorCallsToTwoRepositoriesBothStayPending(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository("owner/other")
	script := approvalSeen +
		"[[prompts]]\nwhen = \"Tell 1.\"\ncall = { tool = \"tell_curator\", arguments = { repository = \"owner/shop\", text = \"First request.\" } }\n\n" +
		"[[prompts]]\nwhen = \"Tell 2.\"\ncall = { tool = \"tell_curator\", arguments = { repository = \"owner/other\", text = \"Second request.\" } }\n\n" +
		"[[prompts]]\nwhen = \"Tell 3.\"\ncall = { tool = \"tell_curator\", arguments = { repository = \"owner/other\", text = \"Third request.\" } }\n\n" +
		seenScript
	server, _ := connect(t, fake, script)

	session := run(t, server, coordinatorSpec(t), "Tell 1.", "Tell 2.", "Tell 3.")

	rows := waitForActions(t, server, 3)
	if got := []string{rows[0].State, rows[1].State, rows[2].State}; !slices.Equal(got, []string{"pending", "replaced", "pending"}) {
		t.Errorf("states = %v", got)
	}
	calls := mcpCalls(t, server, session)
	title := "Request to the Curator of owner/other"
	if len(calls) != 3 || calls[1]["result"] != actionWaitsText(title, rows[1].InboxItem.Int64) ||
		calls[2]["result"] != actionWaitsText(title, rows[2].InboxItem.Int64)+replacesAction(title, rows[1].InboxItem.Int64) {
		t.Errorf("calls = %v", calls)
	}
}

func TestTheApprovalMessageNamesTheActionWithTheIDOfTheToolResultAndTheFirstLineOfTheText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBilling(fake)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send 1.", 12, "Offer a refund.\nThen tell the Owner.")+callMessageLead("Send 2.", 20, "Plan the invoices.")+seenScript)
	session := run(t, server, coordinatorSpec(t), "Send 1.", "Send 2.")
	rows := waitForActions(t, server, 2)
	var ids []string
	for _, call := range mcpCalls(t, server, session) {
		found := regexp.MustCompile(`Its ID is (\d+), only for withdraw\.`).FindStringSubmatch(call["result"].(string))
		ids = append(ids, found[1])
	}

	decided(t, server, rows[0].ID, "approve", "")
	decided(t, server, rows[1].ID, "reject", "No invoices.")

	testkit.WaitFor(t, func() bool {
		return strings.Contains(strings.Join(approvalPrompts(t, server), ""), "rejected")
	})
	prompts := strings.Join(approvalPrompts(t, server), "\n")
	for _, want := range []string{
		"The Owner approved `message_lead` " + ids[0] + " (Offer a refund.). The result: Sent the message to the Lead of #12 Integrate loyalty plans.",
		"The Owner rejected `message_lead` " + ids[1] + " (Plan the invoices.).\n\nThe message of the Owner: No invoices.",
	} {
		if !strings.Contains(prompts, want) {
			t.Errorf("%q not in %q", want, prompts)
		}
	}
}

func TestTheTitleOfMessageLeadNamesTheWorkstreamAndKeepsTheNumberOfAnUnknownOne(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callMessageLead("Send 1.", 12, "First text.")+callMessageLead("Send 2.", 9999, "Second text.")+seenScript)

	run(t, server, coordinatorSpec(t), "Send 1.", "Send 2.")

	waitForActions(t, server, 2)
	var titles []string
	for _, item := range itemsOf(t, server, "approval") {
		titles = append(titles, item.Title)
	}
	if want := []string{"Message to the Lead of #12 Integrate loyalty plans", "Message to the Lead of #9999"}; !slices.Equal(titles, want) {
		t.Errorf("titles = %q", titles)
	}
}
