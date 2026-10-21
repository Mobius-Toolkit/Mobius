package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func TestTheFirstPromptTellsTheLeadIfAutopilotIsOn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	waitForPoll(t, server, fake)
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot })

	sendChat(t, server, leadChat, "Plan the next step")

	prompt := promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]
	if !strings.Contains(prompt, "Autopilot is on: Mobius starts each open sub-issue with no blocker and no earlier task at the next poll.\n\n# MEMORY.md") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestTheFirstPromptTellsTheLeadIfAutopilotIsOff(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")

	sendChat(t, server, leadChat, "Plan the next step")

	prompt := promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]
	if !strings.Contains(prompt, "Autopilot is off: a task starts only after `mark_ready` or a start of the Owner.\n\n# MEMORY.md") {
		t.Errorf("prompt = %s", prompt)
	}
}

func autopilotEvents(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var payloads []string
	for _, event := range leadEvents(t, server) {
		if event.Kind == "autopilot" {
			payloads = append(payloads, event.Payload)
		}
	}
	return payloads
}

func setWorkstreamAutopilot(t *testing.T, server *testserver.Server, on string) {
	t.Helper()
	if status, body := send(t, server, http.MethodPut, "/api/workstreams/owner/shop/12/autopilot", `{"on":`+on+`}`); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
}

func TestAChangeOfAutopilotByTheOwnerAddsOneEventForTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)
	authorize(t, server)

	setWorkstreamAutopilot(t, server, "true")

	testkit.WaitFor(t, func() bool { return len(autopilotEvents(t, server)) == 1 })
	waitForPolls(t, fake)
	got := autopilotEvents(t, server)
	if len(got) != 1 || !strings.HasSuffix(got[0], ` Autopilot of Workstream #12 "Integrate loyalty plans" is on, by @owner.`) {
		t.Fatalf("events = %q", got)
	}

	setWorkstreamAutopilot(t, server, "false")

	testkit.WaitFor(t, func() bool { return len(autopilotEvents(t, server)) == 2 })
	waitForPolls(t, fake)
	got = autopilotEvents(t, server)
	if len(got) != 2 || !strings.HasSuffix(got[1], ` Autopilot of Workstream #12 "Integrate loyalty plans" is off, by @owner.`) {
		t.Errorf("events = %q", got)
	}
}

func TestACloseOfTheWorkstreamAddsNoEventForAutopilot(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	server := startCopiedWithLead(t, fake)

	fake.CloseIssue(shop, 12)

	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 12), "mobius:autopilot") })
	waitForPolls(t, fake)
	if got := autopilotEvents(t, server); len(got) != 0 {
		t.Errorf("events = %q", got)
	}
}
