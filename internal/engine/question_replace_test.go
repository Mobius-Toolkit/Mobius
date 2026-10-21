package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	askFirst  = "call = { tool = \"ask\", arguments = { repository = \"owner/shop\", n = 41, text = \"Cents or dollars?\" } }\n\n"
	askSecond = "call = { tool = \"ask\", arguments = { repository = \"owner/shop\", n = 41, text = \"Euros or dollars?\" } }\n\n"

	leadAsksTwice = leadSeesAnswer +
		"[[prompts]]\nwhen = \"Ask two\"\n" + askSecond +
		"[[prompts]]\nwhen = \"Ask one\"\n" + askFirst +
		"[[prompts]]\nwhen = \" stop of #41\"\nreply = [\"Seen\"]\n\n"
)

func TestASecondQuestionOnAnIssueReplacesTheFirstQuestionItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Ask two"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 12, text = "Euros or dollars?" } }

[[prompts]]
when = "Ask one"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 12, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)

	sendChat(t, server, leadChat, "Ask one")
	waitForItem(t, server, "question")
	sendChat(t, server, leadChat, "Ask two")

	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "question")
		return len(items) == 1 && items[0].Issue == 12 && items[0].Text == "Euros or dollars?"
	})
	waitForPolls(t, fake)
	if items := itemsOf(t, server, "question"); len(items) != 1 {
		t.Errorf("question items = %+v", items)
	}
}

func TestTheAnswerToTheSecondQuestionAboutAStoppedTaskOpensTheStoppedItemAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadAsksTwice+leadStarts, fixesNothing, noChange)
	sha := approvalHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	waitForItem(t, server, "stopped")
	waitForLeadPrompt(t, server, " stop of #41")

	sendChat(t, server, leadChat, "Ask one")
	waitForNoItem(t, server, "stopped")
	sendChat(t, server, leadChat, "Ask two")
	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "question")
		return len(items) == 1 && items[0].Text == "Euros or dollars?"
	})
	waitForPolls(t, fake)
	wantOpenKinds(t, server, "question")

	fake.RemoveLabel(shop, 41, "mobius:question", "owner")

	waitForNoItem(t, server, "question")
	waitForItem(t, server, "stopped")
	waitForPolls(t, fake)
	wantOpenKinds(t, server, "stopped")
}
