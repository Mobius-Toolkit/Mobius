package engine_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func callWithdraw(when string, id int) string {
	return "[[prompts]]\nwhen = " + `"` + when + `"` + "\ncall = { tool = \"withdraw\", arguments = { id = " + itoa(int64(id)) + " } }\n\n"
}

func TestTheCoordinatorWithdrawsItsQuestionAndTheOwnerCanNoLongerAnswerIt(t *testing.T) {
	t.Parallel()
	script := callWithdraw("Withdraw it.", 1) +
		"[[prompts]]\nwhen = \"Ask the Owner\"\ncall = { tool = \"ask\", arguments = { text = \"Which unit?\" } }\n\n" + seenScript
	server, _ := connect(t, testkit.NewFakeGitHub(t), script)

	asked := run(t, server, coordinatorSpec(t), "Ask the Owner")
	items := coordinatorItems(t, server)
	if len(items) != 1 || items[0].ID != 1 || !strings.Contains(reply(t, server, asked), "Its ID is 1.") {
		t.Fatalf("items = %+v", items)
	}

	withdrawn := run(t, server, coordinatorSpec(t), "Withdraw it.")

	if got := reply(t, server, withdrawn); got != "The request is withdrawn." {
		t.Errorf("reply = %q", got)
	}
	if items := coordinatorItems(t, server); len(items) != 0 {
		t.Errorf("inbox = %+v", items)
	}
	body := `{"text":"Cents."}`
	if status, _ := send(t, server, http.MethodPost, "/api/inbox/1/answer", body); status != http.StatusConflict {
		t.Errorf("status = %d", status)
	}
	for _, line := range chatLines(t, server, coordinatorChat) {
		if line.Text == "Cents." {
			t.Errorf("chat has the answer: %+v", line)
		}
	}
}

func TestTheLeadWithdrawsMessageLeadAndTheActionDoesNotRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	script := approvalSeen + callWithdraw("Withdraw it.", 1) + callMessageLead("Tell it.", 20, "Create the task issues.") + seenScript
	server, _ := connect(t, fake, script)

	called := run(t, server, leadSpec(t), "Tell it.")
	action := waitForActions(t, server, 1)[0]
	items := itemsOf(t, server, "approval")
	if len(items) != 1 || items[0].ID != 1 || !strings.Contains(reply(t, server, called), "Its ID is 1.") {
		t.Fatalf("items = %+v", items)
	}

	withdrawn := run(t, server, leadSpec(t), "Withdraw it.")

	if got := reply(t, server, withdrawn); got != "The request is withdrawn." {
		t.Errorf("reply = %q", got)
	}
	if rows := actionRows(t, server); len(rows) != 1 || rows[0].State != "withdrawn" {
		t.Errorf("actions = %+v", rows)
	}
	waitForNoItem(t, server, "approval")
	waitForPolls(t, fake)
	if events := leadEvents(t, server); len(events) != 0 {
		t.Errorf("events = %v", events)
	}
	if status, _ := decide(t, server, action.ID, "approve", ""); status != http.StatusConflict {
		t.Errorf("status = %d", status)
	}
}

func TestAnAgentCannotWithdrawTheRequestOfAnotherAgent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	otherLead := engine.Spec{Role: engine.LeadRole, Organization: "owner", Repository: shop, Workstream: 20, Dir: t.TempDir()}
	script := approvalSeen + callWithdraw("Withdraw the second.", 2) + callWithdraw("Withdraw the first.", 1) +
		callMessageLead("Tell it.", 12, "Create the task issues.") + seenScript
	server, _ := connect(t, fake, script)
	run(t, server, coordinatorSpec(t), "Tell it.")
	waitForActions(t, server, 1)
	run(t, server, otherLead, "Tell it.")
	waitForActions(t, server, 2)

	attempt := run(t, server, leadSpec(t), "Withdraw the first.", "Withdraw the second.")

	got := reply(t, server, attempt)
	refusal := "error: You have no open request with the ID "
	if strings.Count(got, refusal) != 2 {
		t.Errorf("reply = %q", got)
	}
	for _, action := range actionRows(t, server) {
		if action.State != "pending" {
			t.Errorf("action = %+v", action)
		}
	}
}
