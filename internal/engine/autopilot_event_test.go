package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const mergeSentence = "Mobius merges each pull request after your approval, when the branch contains the current default branch and the checks passed. A mandatory deployment need that the Reviewer reports blocks the merge until the Owner marks its todo as done."

func TestTheFirstPromptTellsTheLeadTheAutopilotLevel(t *testing.T) {
	t.Parallel()
	levels := []struct {
		level  string
		labels []string
		text   string
	}{
		{"start", []string{"mobius:autopilot"}, "Autopilot is \"Start tasks\": Mobius starts each open sub-issue with no blocker and no earlier task at the next poll."},
		{"merge", []string{"mobius:auto-merge"}, "Autopilot is \"Merge only\": a task starts only after `mark_ready` or a start of the Owner. " + mergeSentence},
		{"start_and_merge", []string{"mobius:autopilot", "mobius:auto-merge"}, "Autopilot is \"Start and merge\": Mobius starts each open sub-issue with no blocker and no earlier task at the next poll. " + mergeSentence},
	}
	for _, want := range levels {
		t.Run(want.level, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := connect(t, fake, "")
			for _, label := range want.labels {
				fake.AddLabel(shop, 12, label, "owner")
			}
			waitForPoll(t, server, fake)
			testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == want.level })

			sendChat(t, server, leadChat, "Plan the next step")

			prompt := promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]
			if !strings.Contains(prompt, want.text+"\n\n# MEMORY.md") {
				t.Errorf("prompt = %s", prompt)
			}
		})
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

func setWorkstreamAutopilot(t *testing.T, server *testserver.Server, level string) {
	t.Helper()
	if status, body := send(t, server, http.MethodPut, "/api/workstreams/owner/shop/12/autopilot", `{"level":"`+level+`"}`); status != http.StatusNoContent {
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

	setWorkstreamAutopilot(t, server, "start_and_merge")

	testkit.WaitFor(t, func() bool { return len(autopilotEvents(t, server)) == 1 })
	waitForPolls(t, fake)
	got := autopilotEvents(t, server)
	if len(got) != 1 || !strings.HasSuffix(got[0], ` Autopilot of Workstream #12 "Integrate loyalty plans" is "Start and merge", by @owner.`) {
		t.Fatalf("events = %q", got)
	}

	setWorkstreamAutopilot(t, server, "off")

	testkit.WaitFor(t, func() bool { return len(autopilotEvents(t, server)) == 2 })
	waitForPolls(t, fake)
	got = autopilotEvents(t, server)
	if len(got) != 2 || !strings.HasSuffix(got[1], ` Autopilot of Workstream #12 "Integrate loyalty plans" is "Off", by @owner.`) {
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
