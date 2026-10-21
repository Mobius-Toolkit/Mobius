package engine_test

import (
	"fmt"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestTheDispatchEventOfATaskInAnotherRepositoryNamesTheRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectGarden(t, fake, "", "")

	fake.AddLabel(garden, 7, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " dispatch of owner/garden#7 \"Water the plants\" by @owner")
}

func TestTheReadyForApprovalEventOfATaskInAnotherRepositoryNamesTheIssueAndThePullRequestWithTheRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"dispatch of owner/garden#7\"\ncall = { tool = \"start_implementer\", arguments = { repository = \"owner/garden\", n = 7, instructions = \"Water them twice a day.\" } }\n"
	server := connectGarden(t, fake, lead, commits)

	fake.AddLabel(garden, 7, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(garden)) == 1 })
	number := fake.PullRequests(garden)[0].Number
	waitForLeadPrompt(t, server, fmt.Sprintf(" ready for Lead approval of owner/garden#7 \"Water the plants\": pull request owner/garden#%d https://github.com/owner/garden/pull/%d.", number, number))
}

func TestTheDispatchEventOfATaskInTheWorkstreamRepositoryNamesTheIssueWithNoRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadSeesFortyOne, "", noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " dispatch of #41 \"Add plan model\" by @owner")
}
