package engine_test

import (
	"fmt"
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

// quickResearcher is a Researcher that answers at once.
const quickResearcher = `
[[prompts]]
when = "You are a Researcher"
reply = ["Plans store the price in cents.\n"]
shell = "pwd && git rev-parse HEAD && git rev-parse --abbrev-ref HEAD"
`

// connectResearch starts a server with the Workstream #12, a Lead that plays lead, and a Researcher on Antigravity.
func connectResearch(t *testing.T, fake *testkit.FakeGitHub, lead string) (*testserver.Server, string) {
	t.Helper()
	return connectResearcher(t, fake, lead, quickResearcher)
}

// connectResearcher is connectResearch with a Researcher that plays researcher.
func connectResearcher(t *testing.T, fake *testkit.FakeGitHub, lead, researcher string) (*testserver.Server, string) {
	t.Helper()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+lead)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+researcher)
	cfg := testserver.Config(t, dataDir)
	cfg.LeadIdleTimeout = 30 * time.Second
	server := startServerWith(t, fake, cfg, "")
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
	waitForLeadPrompt(t, server, fmt.Sprintf("# Researcher message\n\nReport of the Researcher %d on \"%s\":\n\n%s", session.ID, question, report))
	if want := fmt.Sprintf("Started the Researcher %d. The report arrives later.", session.ID); !slices.ContainsFunc(leadCalls(t, server), func(call map[string]any) bool { return call["result"] == want }) {
		t.Errorf("calls = %+v", leadCalls(t, server))
	}
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

// hangingResearcher is a Researcher that hangs on its question, and answers new details.
const hangingResearcher = `
[[prompts]]
when = "# Question"
reply = ["Looking."]
hang = true

[[prompts]]
when = "The Owner gave new details"
reply = ["Discounts are in cents.\n"]
`

// researchLeadOfDetails is a Lead that starts a Researcher, sends it details, or stops it. The Lead is the session 1,
// and the first Researcher is the session 2.
const researchLeadOfDetails = `
[[prompts]]
when = "# Researcher message"
reply = ["I have the report."]

[[prompts]]
when = "Begin alpha"
call = { tool = "start_researcher", arguments = { question = "Alpha question" } }

[[prompts]]
when = "Begin beta"
call = { tool = "start_researcher", arguments = { question = "Beta question" } }

[[prompts]]
when = "Details for alpha"
call = { tool = "send_researcher_details", arguments = { id = 2, text = "Ask about the discount too." } }

[[prompts]]
when = "Details for beta"
call = { tool = "send_researcher_details", arguments = { id = 3, text = "Ask about the discount too." } }

[[prompts]]
when = "Stop alpha"
call = { tool = "stop_researcher", arguments = { id = 2 } }
`

// waitForResearchers waits until the Researchers have the replies, and gives them in start order.
func waitForResearchers(t *testing.T, server *testserver.Server, replies ...string) []store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := roleSessions(t, server, engine.ResearcherRole)
		if len(sessions) != len(replies) {
			return nil, false
		}
		for i, session := range sessions {
			if !strings.Contains(reply(t, server, session.ID), replies[i]) {
				return nil, false
			}
		}
		return sessions, true
	})
}

func TestSendResearcherDetailsStopsTheTurnAndGivesTheReportOfTheNewTurn(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearcher(t, fake, researchLeadOfDetails, hangingResearcher)
	sendChat(t, server, leadChat, "Begin alpha.")
	session := waitForResearchers(t, server, "Looking.")[0]
	if session.ID != 2 {
		t.Fatalf("session = %+v", session)
	}

	sendChat(t, server, leadChat, "Details for alpha.")

	report := waitForLeadPrompt(t, server, "# Researcher message\n\nReport of the Researcher 2")
	if want := "# Researcher message\n\nReport of the Researcher 2 on \"Alpha question\":\n\nDiscounts are in cents.\n"; report != want {
		t.Errorf("report = %q", report)
	}
	if sessions := roleSessions(t, server, engine.ResearcherRole); len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Errorf("sessions = %+v", sessions)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 2 {
		t.Fatalf("prompts = %q", prompts)
	}
	if want := "The Owner gave new details for the question. They replace the old text where they differ.\n\nAsk about the discount too."; prompts[1] != want {
		t.Errorf("prompt = %q", prompts[1])
	}
	ended := waitForChatSession(t, server, leadChat, engine.ResearcherRole, func(session store.Session) bool { return session.EndedAt.Valid })
	if ended.EndReason.String != "done" {
		t.Errorf("end reason = %s", ended.EndReason.String)
	}
}

func TestStopResearcherStopsOneResearcherAndTheOtherGivesItsReport(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearcher(t, fake, researchLeadOfDetails, hangingResearcher)
	sendChat(t, server, leadChat, "Begin alpha.")
	waitForResearchers(t, server, "Looking.")
	sendChat(t, server, leadChat, "Begin beta.")
	sessions := waitForResearchers(t, server, "Looking.", "Looking.")
	if sessions[0].ID != 2 || sessions[1].ID != 3 {
		t.Fatalf("sessions = %+v", sessions)
	}

	sendChat(t, server, leadChat, "Stop alpha.")

	waitForLeadPrompt(t, server, "# Researcher message\n\nThe Researcher 2 stopped. No report arrives.")
	stopped := waitForChatSession(t, server, leadChat, engine.ResearcherRole, func(session store.Session) bool { return session.EndedAt.Valid })
	if stopped.ID != 2 || stopped.EndReason.String != "stopped" {
		t.Errorf("stopped = %+v", stopped)
	}
	if beta := roleSessions(t, server, engine.ResearcherRole)[1]; beta.EndedAt.Valid {
		t.Errorf("beta = %+v", beta)
	}

	sendChat(t, server, leadChat, "Details for beta.")

	report := waitForLeadPrompt(t, server, "Report of the Researcher 3")
	if want := "# Researcher message\n\nReport of the Researcher 3 on \"Beta question\":\n\nDiscounts are in cents.\n"; report != want {
		t.Errorf("report = %q", report)
	}
	if prompt := strings.Join(leadPrompts(t, server), "\n"); strings.Contains(prompt, "Report of the Researcher 2") {
		t.Errorf("prompts = %s", prompt)
	}
}

func TestTheResearcherToolsRefuseAResearcherOfAnotherWorkstream(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Add coupons")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	server, _ := connectResearcher(t, fake, researchLeadOfDetails, hangingResearcher)
	other := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 20}
	sendChat(t, server, other, "Begin alpha.")
	foreign := waitForChatSession(t, server, other, engine.ResearcherRole, func(session store.Session) bool { return reply(t, server, session.ID) == "Looking." })
	if foreign.ID != 2 {
		t.Fatalf("foreign = %+v", foreign)
	}

	sendChat(t, server, leadChat, "Details for alpha.")
	sendChat(t, server, leadChat, "Stop alpha.")

	calls := testkit.WaitForValue(t, func() ([]map[string]any, bool) {
		calls := leadCalls(t, server)
		return calls, len(calls) == 2
	})
	for _, call := range calls {
		if call["error"] != "No Researcher 2 of this Workstream runs now." {
			t.Errorf("call = %+v", call)
		}
	}
	if session := chatSessions(t, server, other, engine.ResearcherRole)[0]; session.EndedAt.Valid {
		t.Errorf("session = %+v", session)
	}
}

// waitingResearcherLead is researchLeadOfDetails with a third Researcher, the session 4, that waits for a slot while
// the sessions 2 and 3 hold the two Researcher slots.
const waitingResearcherLead = researchLeadOfDetails + `
[[prompts]]
when = "Begin gamma"
call = { tool = "start_researcher", arguments = { question = "Gamma question" } }

[[prompts]]
when = "Details for gamma"
call = { tool = "send_researcher_details", arguments = { id = 4, text = "Ask about the discount too." } }
`

// answeringGammaResearcher is hangingResearcher with a Gamma question that gets an answer.
const answeringGammaResearcher = `
[[prompts]]
when = "Gamma question"
reply = ["Plans have a price.\n"]
` + hangingResearcher

func TestDetailsForAResearcherThatWaitsForASlotGoToItsSecondPrompt(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearcher(t, fake, waitingResearcherLead, answeringGammaResearcher)
	sendChat(t, server, leadChat, "Begin alpha.")
	waitForResearchers(t, server, "Looking.")
	sendChat(t, server, leadChat, "Begin beta.")
	waitForResearchers(t, server, "Looking.", "Looking.")
	sendChat(t, server, leadChat, "Begin gamma.")
	waiting := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := roleSessions(t, server, engine.ResearcherRole)
		return sessions[len(sessions)-1], len(sessions) == 3 && sessions[2].QueueReason.Valid
	})
	if waiting.ID != 4 {
		t.Fatalf("waiting = %+v", waiting)
	}

	sendChat(t, server, leadChat, "Details for gamma.")
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadCalls(t, server), func(call map[string]any) bool { return call["result"] == "Sent the details to the Researcher 4." })
	})
	sendChat(t, server, leadChat, "Stop alpha.")

	report := waitForLeadPrompt(t, server, "Report of the Researcher 4")
	if want := "# Researcher message\n\nReport of the Researcher 4 on \"Gamma question\":\n\nDiscounts are in cents.\n"; report != want {
		t.Errorf("report = %q", report)
	}
	prompts := promptTexts(t, server, waiting.ID)
	if len(prompts) != 2 || !strings.Contains(prompts[0], "# Question\n\nGamma question") ||
		prompts[1] != "The Owner gave new details for the question. They replace the old text where they differ.\n\nAsk about the discount too." {
		t.Errorf("prompts = %q", prompts)
	}
	if want := "Plans have a price.\nDiscounts are in cents.\n"; reply(t, server, waiting.ID) != want {
		t.Errorf("reply = %q", reply(t, server, waiting.ID))
	}
	reports := slices.DeleteFunc(leadPrompts(t, server), func(prompt string) bool { return !strings.Contains(prompt, "Report of the Researcher 4") })
	if len(reports) != 1 {
		t.Errorf("reports = %q", reports)
	}
}
