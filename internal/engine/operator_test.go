package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// operatorLead is a Lead that starts an Operator for a message of the Owner, and answers its report. The Lead and the
// Operator run on the same fake Harness, so the entries of the Operator are in the same script. The first prompt of a
// new chat session also has the Role prompt, so the newest prompt comes first in the script.
const operatorLead = `
[[prompts]]
when = "# Operator message"
reply = ["I have the Operator report."]

[[prompts]]
when = "Ping"
reply = ["Pong"]

[[prompts]]
when = "Start the operator"
call = { tool = "start_operator", arguments = { text = "Update the listings." } }
`

// operatorWork is an Operator that reports at once, and shows its directory.
const operatorWork = `
[[prompts]]
when = "You are an Operator"
reply = ["Updated 3 listings.\n"]
shell = "pwd"
`

// connectOperator starts a server with the Workstream #12 and a fake Harness that plays script for the Lead and for the
// Operator. It gives the data directory.
func connectOperator(t *testing.T, fake *testkit.FakeGitHub, script string) (*testserver.Server, string) {
	t.Helper()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+script)
	cfg := testserver.Config(t, dataDir)
	cfg.LeadIdleTimeout = 30 * time.Second
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	return server, dataDir
}

// operatorFolder gives the memory folder of the Operators of the Workstream #12.
func operatorFolder(dataDir string) string {
	return filepath.Join(dataDir, "operators", shop, "12")
}

// endedOperators waits until count Operator sessions ended, and gives them in start order.
func endedOperators(t *testing.T, server *testserver.Server, count int) []store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := roleSessions(t, server, engine.OperatorRole)
		return sessions, len(sessions) == count && !slices.ContainsFunc(sessions, func(session store.Session) bool { return !session.EndedAt.Valid })
	})
}

func TestAnOperatorRunsInItsFolderWithTheBriefTheMemoryAndTheTextAndItsReportGoesToTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectOperator(t, fake, operatorLead+operatorWork)
	folder := operatorFolder(dataDir)
	if err := os.MkdirAll(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "MEMORY.md"), []byte("The directory has 40 listings.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sendChat(t, server, leadChat, "Start the operator.")

	session := endedOperators(t, server, 1)[0]
	if session.EndReason.String != "done" || session.Parent.Int64 != chatSessions(t, server, leadChat, engine.LeadRole)[0].ID {
		t.Errorf("session = %+v", session)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	for _, part := range []string{
		"You are an Operator",
		"# Brief\n\nShip loyalty plans to all shops.\n",
		"# MEMORY.md\n\nThe directory has 40 listings.\n",
		"# Text\n\nUpdate the listings.",
	} {
		if !strings.Contains(prompts[0], part) {
			t.Errorf("%q is not in %s", part, prompts[0])
		}
	}
	report := reply(t, server, session.ID)
	if !strings.HasPrefix(report, "Updated 3 listings.\n") || !strings.Contains(report, "/operators/owner/shop/12\nexit 0") {
		t.Errorf("report = %q", report)
	}
	waitForLeadPrompt(t, server, fmt.Sprintf("# Operator message\n\nReport of the Operator %d:\n\n%s", session.ID, report))
	if want := fmt.Sprintf("Started the Operator %d. The report arrives later as an Operator message.", session.ID); !slices.ContainsFunc(leadCalls(t, server), func(call map[string]any) bool { return call["result"] == want }) {
		t.Errorf("calls = %+v", leadCalls(t, server))
	}
}

func TestAnOperatorMessageStaysOutOfTheChatAndTheReplyTextOfItsTurnGoesOnlyToTheTranscript(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectOperator(t, fake, operatorLead+operatorWork)
	sendChat(t, server, leadChat, "Start the operator.")
	session := endedOperators(t, server, 1)[0]
	waitForLeadPrompt(t, server, fmt.Sprintf("# Operator message\n\nReport of the Operator %d:", session.ID))

	sendChat(t, server, leadChat, "Ping")

	waitForChat(t, server, leadChat, "Lead", "Pong")
	lead := chatSessions(t, server, leadChat, engine.LeadRole)[0]
	if got := reply(t, server, lead.ID); !strings.Contains(got, "I have the Operator report.") {
		t.Errorf("Transcript reply = %q", got)
	}
	messages := chatView(t, server, leadChat).Messages
	if slices.ContainsFunc(messages, func(message store.ChatMessage) bool {
		return message.Author == "Operator" || strings.Contains(message.Text, "I have the Operator report.")
	}) {
		t.Errorf("messages = %+v", messages)
	}
	unread, err := server.Engine.UnreadChats(t.Context())
	if err != nil || len(unread) != 1 || unread[0].Count != int64(len(slices.DeleteFunc(messages, func(message store.ChatMessage) bool { return message.Author == "Owner" }))) {
		t.Errorf("unread = %+v, %v", unread, err)
	}
}

func TestAChatIsNotStoppableDuringTheTurnForAnOperatorMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectOperator(t, fake, "[[prompts]]\nwhen = \"# Operator message\"\nhang = true\n"+operatorLead+operatorWork)
	sendChat(t, server, leadChat, "Start the operator.")
	session := endedOperators(t, server, 1)[0]
	waitForLeadPrompt(t, server, fmt.Sprintf("# Operator message\n\nReport of the Operator %d:", session.ID))

	if view := chatView(t, server, leadChat); !view.Writing || view.Stoppable {
		t.Errorf("writing = %v, stoppable = %v", view.Writing, view.Stoppable)
	}
}

func TestAFailedOperatorGivesTheFailureAsItsReport(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectOperator(t, fake, operatorLead+`
[[prompts]]
when = "You are an Operator"
error = { code = -32000, message = "The external system is down." }
`)

	sendChat(t, server, leadChat, "Start the operator.")

	session := endedOperators(t, server, 1)[0]
	if session.EndReason.String != "failed" {
		t.Errorf("session = %+v", session)
	}
	report := waitForLeadPrompt(t, server, fmt.Sprintf("# Operator message\n\nReport of the Operator %d:\n\nThe Operator %d failed: ", session.ID, session.ID))
	if !strings.Contains(report, "The external system is down.") {
		t.Errorf("report = %q", report)
	}
}

func TestTheMemoryFolderStaysAfterAnOperatorAndTheNextOperatorGetsItsMemory(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectOperator(t, fake, `
[[prompts]]
when = "# Operator message"
reply = ["I have the Operator report."]

[[prompts]]
when = "Start the second"
call = { tool = "start_operator", arguments = { text = "Do the second job." } }

[[prompts]]
when = "Start the first"
call = { tool = "start_operator", arguments = { text = "Do the first job." } }

[[prompts]]
when = "Do the second job."
reply = ["Second done.\n"]

[[prompts]]
when = "Do the first job."
reply = ["First done.\n"]
shell = "printf 'The sitemap is /sitemap.xml.\\n' > MEMORY.md"
`)

	sendChat(t, server, leadChat, "Start the first.")

	first := endedOperators(t, server, 1)[0]
	memory, err := os.ReadFile(filepath.Join(operatorFolder(dataDir), "MEMORY.md"))
	if err != nil || string(memory) != "The sitemap is /sitemap.xml.\n" {
		t.Errorf("MEMORY.md = %q, %v", memory, err)
	}
	if prompt := promptTexts(t, server, first.ID)[0]; strings.Contains(prompt, "# MEMORY.md") {
		t.Errorf("the first prompt has a memory section: %s", prompt)
	}
	waitForLeadPrompt(t, server, fmt.Sprintf("# Operator message\n\nReport of the Operator %d:", first.ID))

	sendChat(t, server, leadChat, "Start the second.")

	second := endedOperators(t, server, 2)[1]
	if want := "# MEMORY.md\n\nThe sitemap is /sitemap.xml.\n\n# Text\n\nDo the second job."; !strings.Contains(promptTexts(t, server, second.ID)[0], want) {
		t.Errorf("prompt = %s", promptTexts(t, server, second.ID)[0])
	}
}

func TestTheDrainRefusesANewOperator(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectOperator(t, fake, operatorLead+operatorWork)
	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}
	defer cancelDrain(t, server)

	sendChat(t, server, leadChat, "Start the operator.")

	call := testkit.WaitForValue(t, func() (map[string]any, bool) {
		calls := leadCalls(t, server)
		if len(calls) == 0 {
			return nil, false
		}
		return calls[0], true
	})
	if call["tool"] != "start_operator" || call["error"] != "Mobius prepares an upgrade, so no Operator starts now." {
		t.Errorf("call = %+v", call)
	}
	if sessions := roleSessions(t, server, engine.OperatorRole); len(sessions) != 0 {
		t.Errorf("Operators = %+v", sessions)
	}
}
