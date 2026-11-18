package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// replyToApproval is a Lead script that replies to the approval event. The prompt of the session of that event holds the
// text "dispatch of #41" of the earlier event, so a script with that rule after it calls start_implementer, which reads
// the pull request at a time that no poll sets.
const replyToApproval = "[[prompts]]\nwhen = \"ready for Lead approval of #41\"\nreply = [\"Noted.\"]\n\n"

const leadRepliesOnApproval = replyToApproval + leadStarts

// approvalAfterSettling moves the task of #41 to approval and waits until the polls have no change.
func approvalAfterSettling(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) {
	t.Helper()
	sha := checksHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "success"))
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	waitForPolls(t, fake)
	waitForPolls(t, fake)
}

// pullReads gives the number of reads of the pull request #42 in the requests of the fake GitHub after the index from.
func pullReads(fake *testkit.FakeGitHub, from int) int {
	reads := 0
	for _, request := range fake.Requests()[from:] {
		if request.Method == "GET" && request.Path == "/repos/owner/shop/pulls/42" {
			reads++
		}
	}
	return reads
}

func TestAPollWithNoChangeDoesNotReadThePullRequestOfATask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadRepliesOnApproval, commits, longGrace)
	approvalAfterSettling(t, server, fake)
	from := len(fake.Requests())

	waitForPolls(t, fake)

	if reads := pullReads(fake, from); reads != 0 {
		t.Errorf("reads = %d", reads)
	}
}

func TestAPollReadsThePullRequestOfATaskAfterAPushToItsBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadRepliesOnApproval, commits, longGrace)
	approvalAfterSettling(t, server, fake)
	from := len(fake.Requests())

	work := t.TempDir()
	testkit.Git(t, work, "clone", "--branch=mobius/41", fake.Remote(shop), ".")
	testkit.Git(t, work, "commit", "--allow-empty", "-m", "Change the plan model")
	testkit.Git(t, work, "push", "origin", "HEAD:refs/heads/mobius/41")

	testkit.WaitFor(t, func() bool { return pullReads(fake, from) > 0 })
}

func TestAPollReadsThePullRequestOfATaskAfterAPushToTheDefaultBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadRepliesOnApproval, commits, longGrace)
	approvalAfterSettling(t, server, fake)
	from := len(fake.Requests())

	fake.CommitFile(shop, "README.md", "Shop", "Add the README")

	testkit.WaitFor(t, func() bool { return pullReads(fake, from) > 0 })
}

func TestAPollReadsAKeptPullRequestWithAnUnknownMergeabilityAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadRepliesOnApproval, commits, longGrace)
	approvalAfterSettling(t, server, fake)
	fake.SetMergeableUnknown(shop, 42, true)
	waitForPolls(t, fake)
	from := len(fake.Requests())

	fake.SetMergeableUnknown(shop, 42, false)

	testkit.WaitFor(t, func() bool { return pullReads(fake, from) > 0 })
	waitForPolls(t, fake)
	settled := len(fake.Requests())
	waitForPolls(t, fake)
	if reads := pullReads(fake, settled); reads != 0 {
		t.Errorf("reads = %d", reads)
	}
}
