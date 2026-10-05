package engine_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const question = "Where do plans store the price?"

// researchLead is a Lead that starts a Researcher for a message of the Owner, and answers its report. The first
// prompt of a new chat session also has the Role prompt, so the report prompt comes first in the script.
const researchLead = `
[[prompts]]
when = "# Researcher message"
reply = ["I have the report."]

[[prompts]]
when = "# Owner message"
call = { tool = "start_researcher", arguments = { question = "Where do plans store the price?" } }
`

// connectResearch starts a server with the Workstream #12, a Lead that plays lead, and a Researcher on Antigravity.
func connectResearch(t *testing.T, fake *testkit.FakeGitHub, lead string) (*testserver.Server, string) {
	t.Helper()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+lead)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+`
[[prompts]]
when = "You are a Researcher"
reply = ["Plans store the price in cents.\n"]
shell = "pwd && git rev-parse HEAD && git rev-parse --abbrev-ref HEAD"
`)
	server := startServerWith(t, fake, testserver.Config(t, dataDir), "")
	server.WaitForFirstPoll(t, shop)
	return server, dataDir
}

func TestAReportOfTheResearcherGoesToTheLeadChat(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectResearch(t, fake, researchLead)

	sendChat(t, server, leadChat, "Plan the price model.")

	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := roleSessions(t, server, engine.ResearcherRole)
		if len(sessions) == 0 {
			return store.Session{}, false
		}
		return sessions[0], sessions[0].EndedAt.Valid
	})
	if session.EndReason.String != "done" || session.Parent.Int64 != chatSessions(t, server, leadChat, engine.LeadRole)[0].ID {
		t.Errorf("session = %+v", session)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	for _, part := range []string{"You are a Researcher", "# Brief\n\nShip loyalty plans to all shops.\n", "# Question\n\n" + question} {
		if !strings.Contains(prompts[0], part) {
			t.Errorf("%q is not in %s", part, prompts[0])
		}
	}
	report := reply(t, server, session.ID)
	dir := fmt.Sprintf("/worktrees/owner/shop/research-%d", session.ID)
	if want := dir + "\n" + head(t, fake, "main") + "\nHEAD\nexit 0"; !strings.Contains(report, want) || !strings.HasPrefix(report, "Plans store the price in cents.\n") {
		t.Errorf("report = %s", report)
	}
	if exists(t, filepath.Join(dataDir, dir)) {
		t.Errorf("%s stays", dir)
	}
	waitForLeadPrompt(t, server, fmt.Sprintf("# Researcher message\n\nReport of the Researcher on \"%s\":\n\n%s", question, report))
	waitForChat(t, server, leadChat, "Lead", "I have the report.")
	messages := chatView(t, server, leadChat).Messages
	if slices.ContainsFunc(messages, func(message store.ChatMessage) bool { return message.Author == "Researcher" }) {
		t.Errorf("messages = %+v", messages)
	}
	unread, err := server.Engine.UnreadChats(t.Context())
	if err != nil || len(unread) != 1 || unread[0].Count != int64(len(slices.DeleteFunc(messages, func(message store.ChatMessage) bool { return message.Author == "Owner" }))) {
		t.Errorf("unread = %+v, %v", unread, err)
	}
}

func TestTheDrainRefusesANewResearcher(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearch(t, fake, researchLead)
	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}
	defer cancelDrain(t, server)

	sendChat(t, server, leadChat, "Research the plan flow.")

	call := testkit.WaitForValue(t, func() (map[string]any, bool) {
		calls := leadCalls(t, server)
		if len(calls) == 0 {
			return nil, false
		}
		return calls[0], true
	})
	if call["tool"] != "start_researcher" || call["error"] != "Mobius prepares an upgrade, so no Researcher starts now." {
		t.Errorf("call = %+v", call)
	}
	if sessions := roleSessions(t, server, engine.ResearcherRole); len(sessions) != 0 {
		t.Errorf("Researchers = %+v", sessions)
	}
}
