package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
)

const fact = "Screenshots come from a CI job."

// firstLeadPrompt commits each file of files, a path and a content, sends a message to the Lead of the Workstream
// #12, and gives the first prompt of the session.
func firstLeadPrompt(t *testing.T, fake *testkit.FakeGitHub, files ...[2]string) string {
	t.Helper()
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Hello\"]\n")
	for _, file := range files {
		fake.CommitFile(shop, file[0], file[1], "Add "+file[0])
	}
	sendChat(t, server, leadChat, "Plan the loyalty API")
	return promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]
}

func TestTheLeadGetsTheFactsFromTheDefaultBranchAndNoRoleSectionWithNoFile(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)

	prompt := firstLeadPrompt(t, fake, [2]string{"AGENTS.md", fact})

	if !strings.Contains(prompt, "# Repository facts\n\n"+fact+"\n\n") {
		t.Errorf("prompt = %s", prompt)
	}
	if strings.Contains(prompt, "# Role instructions") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestTheLeadGetsOnlyTheInstructionsOfTheLead(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)

	prompt := firstLeadPrompt(t, fake, [2]string{".mobius/roles/reviewer.md", "Check the units of each price."}, [2]string{".mobius/roles/lead.md", "Plan small tasks."})

	if !strings.Contains(prompt, "# Role instructions\n\nPlan small tasks.\n\n") || strings.Contains(prompt, "Check the units") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestTheImplementerAndTheResearcherGetTheFactsAndOnlyTheirOwnInstructions(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"" + question + "\"\ncall = { tool = \"start_researcher\", arguments = { question = \"" + question + "\" } }\n\n" + leadStarts
	server, dataDir := connectTask(t, fake, lead, commits, noChange)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+"[[prompts]]\nreply = [\"Plans store the price in cents.\"]\n")
	fake.CommitFile(shop, "AGENTS.md", fact, "Add the facts")
	fake.CommitFile(shop, ".mobius/roles/implementer.md", "Commit small steps.", "Add the role file")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	sendChat(t, server, leadChat, question)

	for _, role := range []string{engine.ImplementerRole, engine.ResearcherRole} {
		prompt := testkit.WaitForValue(t, func() (string, bool) {
			sessions := roleSessions(t, server, role)
			if len(sessions) == 0 {
				return "", false
			}
			prompts := promptTexts(t, server, sessions[0].ID)
			return strings.Join(prompts, ""), len(prompts) > 0
		})
		if !strings.Contains(prompt, "# Repository facts\n\n"+fact+"\n\n") {
			t.Errorf("%s: %s", role, prompt)
		}
		if section := strings.Contains(prompt, "# Role instructions\n\nCommit small steps.\n\n"); section != (role == engine.ImplementerRole) || strings.Count(prompt, "# Role instructions") > 1 {
			t.Errorf("%s: %s", role, prompt)
		}
	}
}
