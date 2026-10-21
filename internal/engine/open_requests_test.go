package engine_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const askOnTheWorkstream = "[[prompts]]\nwhen = \"Ask it.\"\ncall = { tool = \"ask\", arguments = { repository = \"owner/shop\", n = 12, text = \"Cents or dollars?\" } }\n\n"

func requestLine(id int, text, title string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^- ` + itoa(int64(id)) + ` ` + regexp.QuoteMeta(text) + ` \(\d{4}-\d\d-\d\d \d\d:\d\d UTC\): ` + regexp.QuoteMeta(title) + `$`)
}

func requestsOf(t *testing.T, prompt string) string {
	t.Helper()
	_, after, found := strings.Cut(prompt, "# Open requests\n\n")
	if !found {
		t.Fatalf("no section in %s", prompt)
	}
	section, _, _ := strings.Cut(after, "# Chat history")
	return section
}

func TestANewLeadSessionGetsItsOpenRequestsAndNotAWithdrawnAction(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	script := callWithdraw("Withdraw it.", 1) + askOnTheWorkstream + callMessageLead("Tell it.", 20, "Create the task issues.") + seenScript
	server, _ := connect(t, fake, script)
	run(t, server, leadSpec(t), "Tell it.", "Ask it.")

	sendChat(t, server, leadChat, "Plan the next step")

	section := requestsOf(t, promptTexts(t, server, endedChatSession(t, server, 1).ID)[0])
	for _, line := range []*regexp.Regexp{requestLine(1, "action message_lead", "Message to the Lead of #20 Billing"), requestLine(2, "question on #12", "Integrate loyalty plans")} {
		if !line.MatchString(section) {
			t.Errorf("%s not in %q", line, section)
		}
	}
	if strings.Index(section, "- 1 ") > strings.Index(section, "- 2 ") {
		t.Errorf("section = %q", section)
	}

	run(t, server, leadSpec(t), "Withdraw it.")
	sendChat(t, server, leadChat, "Plan again")

	section = requestsOf(t, promptTexts(t, server, endedChatSession(t, server, 3).ID)[0])
	if strings.Contains(section, "action message_lead") || !requestLine(2, "question on #12", "Integrate loyalty plans").MatchString(section) {
		t.Errorf("section = %q", section)
	}
}

func TestANewCoordinatorSessionGetsItsOwnOpenRequests(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	script := "[[prompts]]\nwhen = \"Coordinator asks.\"\ncall = { tool = \"ask\", arguments = { text = \"Which unit?\" } }\n\n" +
		callMessageLead("Lead tells.", 20, "Create the task issues.") +
		callMessageLead("Coordinator tells.", 12, "Plan the work.") + seenScript
	server, _ := connect(t, fake, script)
	run(t, server, leadSpec(t), "Lead tells.")
	run(t, server, coordinatorSpec(t), "Coordinator asks.", "Coordinator tells.")

	sendChat(t, server, coordinatorChat, "Open the list")

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		prompts := slices.DeleteFunc(coordinatorPrompts(t, server), func(prompt string) bool { return !strings.Contains(prompt, "# Open requests") })
		return strings.Join(prompts, ""), len(prompts) == 1
	})
	section := requestsOf(t, prompt)
	for _, line := range []*regexp.Regexp{requestLine(2, "question", "Which unit?"), requestLine(3, "action message_lead", "Message to the Lead of #12 Integrate loyalty plans")} {
		if !line.MatchString(section) {
			t.Errorf("%s not in %q", line, section)
		}
	}
	if strings.Contains(section, "- 1 ") {
		t.Errorf("section = %q", section)
	}
}

func TestALeadSessionWithNoOpenRequestGetsThatLine(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), seenScript)

	sendChat(t, server, leadChat, "Plan the next step")

	if section := requestsOf(t, promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]); section != "You have no open request.\n\n" {
		t.Errorf("section = %q", section)
	}
}
