package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	taskDetails     = "The Owner gave new details for the task. They replace the old text where they differ.\n\nRound prices down."
	questionDetails = "The Owner gave new details for the question. They replace the old text where they differ.\n\nAsk about the discount too."
)

// harnessFileLines gives the lines of the file name that the fake agents of dataDir write, or none when it does not exist.
func harnessFileLines(t *testing.T, dataDir, name string) []string {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "harnesses", name)))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(text))
}

// implementerGetsDetails runs an Implementer on a fake Harness with the script header, sends it details in its first
// turn, and gives the data directory when the task reaches the approval.
func implementerGetsDetails(t *testing.T, header string) string {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"Send the details\"\ncall = { tool = \"send_details\", arguments = { repository = \"owner/shop\", n = 41, text = \"Round prices down.\" } }\n\n" + leadStarts
	server, dataDir := connectTask(t, fake, lead, "", noChange)
	testkit.InstallFakeHarness(t, dataDir, "devin", header+options+
		"[[prompts]]\nreply = [\"Working.\"]\nhang = true\n\n[[prompts]]\nwhen = \"The Owner gave new details\"\n"+commitCents)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) != 1 {
			return store.Session{}, false
		}
		return sessions[0], reply(t, server, sessions[0].ID) == "Working."
	})

	sendChat(t, server, leadChat, "Send the details to #41.")

	approvalCheckRuns(t, server, fake)
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Errorf("sessions = %+v", sessions)
	}
	if plan := testkit.Git(t, fake.Remote(shop), "show", "mobius/41:plan.txt"); plan != "cents" {
		t.Errorf("plan.txt = %s", plan)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 2 || prompts[1] != taskDetails {
		t.Errorf("prompts = %q", prompts)
	}
	return dataDir
}

func TestAnImplementerWithSteeringGetsDetailsInTheRunningTurnWithNoCancel(t *testing.T) {
	t.Parallel()

	dataDir := implementerGetsDetails(t, injected)

	if cancels := harnessFileLines(t, dataDir, "cancels"); len(cancels) != 0 {
		t.Errorf("cancels = %q", cancels)
	}
}

func TestAnImplementerWithASteeringOutcomeOtherThanInjectedGetsTheCancelAndTheDetailsAsTheNextPrompt(t *testing.T) {
	t.Parallel()

	dataDir := implementerGetsDetails(t, "steering_outcome = \"promptRequired\"\n\n")

	if cancels := harnessFileLines(t, dataDir, "cancels"); len(cancels) == 0 {
		t.Error("the session got no cancel")
	}
}

func TestAResearcherWithSteeringGetsDetailsInTheRunningTurnWithNoCancel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectResearcher(t, fake, researchLeadOfDetails, "")
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", injected+options+hangingResearcher)
	sendChat(t, server, leadChat, "Begin alpha.")
	session := waitForResearchers(t, server, "Looking.")[0]

	sendChat(t, server, leadChat, "Details for alpha.")

	report := waitForLeadPrompt(t, server, "# Researcher message\n\nReport of the Researcher 2")
	if !strings.Contains(report, "Discounts are in cents.") {
		t.Errorf("report = %q", report)
	}
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 2 || prompts[1] != questionDetails {
		t.Errorf("prompts = %q", prompts)
	}
	if cancels := harnessFileLines(t, dataDir, "cancels"); len(cancels) != 0 {
		t.Errorf("cancels = %q", cancels)
	}
}

func TestDetailsThatArriveDuringASteeringRequestStayAndGoIntoTheTurnWithTheirOwnRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectScript(t, fake, injected+"steering_hold = true\n\n"+options+
		"[[prompts]]\nwhen = \"Question\"\nreply = [\"Working.\"]\nhang = true\n\n"+
		"[[prompts]]\nwhen = \"Alpha.\"\nhang = true\n\n"+
		"[[prompts]]\nwhen = \"Beta.\"\nreply = [\"Both.\"]\n", func(*config.Config) {})
	agent := start(t, server, engine.Spec{Role: engine.ResearcherRole, Organization: "owner", Repository: shop, Workstream: 12, Dir: t.TempDir()})
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Question", nil) }()
	testkit.WaitFor(t, func() bool { return reply(t, server, agent.ID()) == "Working." })
	server.Engine.AddDetails(agent, "Alpha.")
	testkit.WaitFor(t, func() bool { return len(harnessFileLines(t, dataDir, "steerings")) == 1 })

	server.Engine.AddDetails(agent, "Beta.")
	if err := os.WriteFile(filepath.Join(dataDir, "harnesses", "steering_release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := <-prompted; err != nil {
		t.Fatal(err)
	}
	prompts := promptTexts(t, server, agent.ID())
	if len(prompts) != 3 || !strings.Contains(prompts[1], "Alpha.") || strings.Contains(prompts[1], "Beta.") || !strings.Contains(prompts[2], "Beta.") || strings.Contains(prompts[2], "Alpha.") {
		t.Errorf("prompts = %q", prompts)
	}
	if left := server.Engine.TakeDetails(agent); left != "" {
		t.Errorf("details left = %q", left)
	}
	if cancels := harnessFileLines(t, dataDir, "cancels"); len(cancels) != 0 {
		t.Errorf("cancels = %q", cancels)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}
