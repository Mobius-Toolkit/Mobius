package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const briefOnGitHub = "Ship loyalty plans to all shops.\n\n" + chatLink

func TestTheImplementerReadsTheBriefFromTheDatabaseWithNoLinkToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, withUIURL)
	fake.SetBody(shop, 12, briefOnGitHub)
	waitForPoll(t, server, fake)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)
	if _, body := fake.Issue(shop, 12); body != briefOnGitHub {
		t.Fatalf("body of #12 = %q", body)
	}
	prompt := promptTexts(t, server, endedImplementers(t, server, 1)[0].ID)[0]
	inOrder(t, prompt, "You are the Implementer of one task.", "# Brief\n\nShip loyalty plans to all shops.\n\n# Issue")
	if strings.Contains(prompt, "Open the Lead chat") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestTheLeadReadsTheBriefFromTheDatabaseWithNoLinkToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", withUIURL)
	fake.SetBody(shop, 12, briefOnGitHub)
	waitForPoll(t, server, fake)

	sendChat(t, server, leadChat, "Plan the next step")

	prompt := promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]
	inOrder(t, prompt, "You are the Lead of one Workstream.", "# Brief\n\nShip loyalty plans to all shops.\n\n")
	if strings.Contains(prompt, "Open the Lead chat") {
		t.Errorf("prompt = %s", prompt)
	}
}
