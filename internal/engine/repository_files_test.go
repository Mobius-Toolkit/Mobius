package engine_test

import (
	"strings"
	"testing"

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
