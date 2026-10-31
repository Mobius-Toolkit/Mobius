package engine_test

import (
	"fmt"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	leadUpdates = "[[prompts]]\nwhen = \"Update #41\"\ncall = { tool = \"update_branch\", arguments = { repository = \"owner/shop\", n = 41 } }\n\n"

	leadUpdatesAndDispatches = "[[prompts]]\nwhen = \"Update #41\"\ncall = { tool = \"update_branch\", arguments = { repository = \"owner/shop\", n = 41 } }\n\n" +
		"[[prompts]]\nwhen = \"dispatch of #41\"\nreply = [\"Seen\"]\n\n"
)

func TestUpdateBranchUpdatesABranchThatIsBehindMain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadUpdates+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	oldHead := head(t, fake, "mobius/41")

	fake.CommitFile(shop, "other.txt", "content\n", "Commit on main")

	sendChat(t, server, leadChat, "Update #41")

	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != oldHead })
	newHead := head(t, fake, "mobius/41")
	want := fmt.Sprintf("Mobius updated the branch of pull request #42: new head %.8s, old head %.8s. The new head gets a new CI run, and a new review round only when an Implementer pushes.", newHead, oldHead)
	waitForChat(t, server, leadChat, "Lead", want)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" })
}

func TestUpdateBranchReportsABranchThatIsAlreadyUpToDate(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadUpdates+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	oldHead := head(t, fake, "mobius/41")

	callsBefore := fake.UpdateBranchCalls()

	sendChat(t, server, leadChat, "Update #41")

	want := fmt.Sprintf("The branch of pull request #42 is already up to date with main, head %.8s.", oldHead)
	waitForChat(t, server, leadChat, "Lead", want)

	if got := fake.UpdateBranchCalls(); got != callsBefore {
		t.Errorf("update branch calls = %d, want %d", got, callsBefore)
	}
}

func TestUpdateBranchRefusesATaskWithNoPullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadUpdatesAndDispatches, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })

	sendChat(t, server, leadChat, "Update #41")

	waitForChat(t, server, leadChat, "Lead", "error: The task of #41 has no open pull request.")
}

func TestUpdateBranchReportsARefusalOfGitHub(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadUpdates+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)

	fake.CommitFile(shop, "plan.txt", "euros\n", "Add price in euros")

	sendChat(t, server, leadChat, "Update #41")

	waitForChat(t, server, leadChat, "Lead", "error: merge conflict between base and head")
}
