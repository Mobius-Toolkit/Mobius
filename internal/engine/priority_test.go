package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// leadStartsThree is a Lead that starts an Implementer for the dispatch of #41, #43 and #45. The prompt of a Lead
// holds the earlier events, so the rule of the newest dispatch comes first.
const leadStartsThree = `
[[prompts]]
when = "dispatch of #45"
call = { tool = "start_implementer", arguments = { n = 45, instructions = "Add a price page." } }

` + leadStartsTwo

// connectThree starts a server with the tasks #41, #43 and #45 of the Workstream #12, one agent slot, a short
// review_quiet_period, and the Implementer.
func connectThree(t *testing.T, fake *testkit.FakeGitHub, implementer string) (*testserver.Server, string) {
	t.Helper()
	server, dataDir := connectTask(t, fake, leadStartsThree, implementer, func(cfg *config.Config) {
		cfg.MaxAgents = 1
		cfg.ReviewQuietPeriod = 200 * time.Millisecond
	})
	for _, number := range []int64{43, 45} {
		fake.AddSubIssueOf(shop, 12, number, fmt.Sprintf("Task %d", number))
	}
	return server, dataDir
}

func waitForState(t *testing.T, server *testserver.Server, number int64, state string) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, number) == state })
}

// waitForImplementerEnd waits until the Implementer session number count of the issue number ended. A poll that stays at
// a held request starts no check of the CI, so a test that holds the poll cannot wait for a task to wait for the Lead.
func waitForImplementerEnd(t *testing.T, server *testserver.Server, number int64, count int) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		sessions := issueImplementers(t, server, number)
		return len(sessions) >= count && sessions[count-1].EndedAt.Valid
	})
}

// liveTaskState gives the state of the live task of the issue number, or "".
func liveTaskState(t *testing.T, server *testserver.Server, number int64) string {
	t.Helper()
	var state string
	if err := server.DB.QueryRow("SELECT coalesce(max(state), '') FROM tasks WHERE issue = ? AND state <> 'ended'", number).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// issueImplementers gives the Implementer sessions of the issue number, the oldest first.
func issueImplementers(t *testing.T, server *testserver.Server, number int64) []store.Session {
	t.Helper()
	return slices.DeleteFunc(roleSessions(t, server, engine.ImplementerRole), func(session store.Session) bool { return session.Issue.Int64 != number })
}

// readyPullRequest dispatches #41, waits until its task waits for the Lead, and gives the head of its branch and the
// number of its pull request.
func readyPullRequest(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) (string, int64) {
	t.Helper()
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForState(t, server, 41, "approval")
	return head(t, fake, "mobius/41"), fake.PullRequests(shop)[0].Number
}

// fixesAndMerges is an Implementer that fixes the check in a fix round and merges the base branch in a conflict round.
const fixesAndMerges = fixes + "\n" + conflictWhen + "shell = \"git merge -q origin/main\"\n"

// waitForHeld waits until the poll reaches a held request.
func waitForHeld(t *testing.T, reached <-chan struct{}) {
	t.Helper()
	select {
	case <-reached:
	case <-time.After(time.Minute):
		t.Fatal("the poll did not reach the held request after one minute")
	}
}

// testARoundTakesTheFreeSlotBeforeANewTicket makes trigger give #41 a round while #43 holds the only slot and #45 waits
// in the queue with an earlier queue time. The poll that starts the round waits at the issue #43, so no poll gives the
// pull request its work before the slot frees.
func testARoundTakesTheFreeSlotBeforeANewTicket(t *testing.T, trigger func(fake *testkit.FakeGitHub, sha string, pullRequest int64)) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectThree(t, fake, fixesAndMerges)
	sha, pullRequest := readyPullRequest(t, server, fake)
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForState(t, server, 43, "working")
	fake.AddLabel(shop, 45, "mobius:ready", "owner")
	waitForState(t, server, 45, "queued")
	testkit.WaitFor(t, func() bool {
		sessions := issueImplementers(t, server, 45)
		return len(sessions) > 0 && sessions[0].QueueReason.Valid
	})

	reached, release := fake.HoldIssue(shop, 43)
	waitForHeld(t, reached)
	trigger(fake, sha, pullRequest)
	roundReached, releaseRound := fake.HoldIssue(shop, 43)
	t.Cleanup(sync.OnceFunc(releaseRound))
	release()
	waitForHeld(t, roundReached)
	waitForState(t, server, 41, "queued")

	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	waitForImplementerEnd(t, server, 45, 1)
	waitForImplementerEnd(t, server, 41, 2)
	startsAfter(t, issueImplementers(t, server, 45)[0], issueImplementers(t, server, 41)[1])
}

func TestAFixRoundTakesTheFreeSlotBeforeANewTicketWithNoPollBetween(t *testing.T) {
	t.Parallel()
	testARoundTakesTheFreeSlotBeforeANewTicket(t, func(fake *testkit.FakeGitHub, sha string, _ int64) {
		fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	})
}

func TestAConflictRoundTakesTheFreeSlotBeforeANewTicketWithNoPollBetween(t *testing.T) {
	t.Parallel()
	testARoundTakesTheFreeSlotBeforeANewTicket(t, func(fake *testkit.FakeGitHub, _ string, pullRequest int64) {
		fake.SetBehind(shop, pullRequest)
		fake.CommitFile(shop, "price.txt", "dollars\n", "Add price")
	})
}

func TestAFreeSlotGoesToTheFixRoundOfAnOldPullRequestBeforeANewTicket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectThree(t, fake, fixes)
	sha, pullRequest := readyPullRequest(t, server, fake)
	fake.SetCreatedAt(shop, pullRequest, 1000)
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForState(t, server, 43, "working")
	fake.AddLabel(shop, 45, "mobius:ready", "owner")
	waitForState(t, server, 45, "queued")

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	waitForState(t, server, 41, "queued")
	ticket := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := issueImplementers(t, server, 45)
		if len(sessions) == 0 {
			return store.Session{}, false
		}
		return sessions[0], sessions[0].QueueReason.Valid
	})
	if ticket.QueueReason.String != "no free agent slot (1/1)" {
		t.Errorf("queue reason = %s", ticket.QueueReason.String)
	}
	// The poll after the start of the round gives the pull request its work.
	waitForPolls(t, fake)

	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	waitForState(t, server, 41, "working")
	if state := liveTaskState(t, server, 45); state != "queued" {
		t.Errorf("state of #45 = %s", state)
	}
	waitForImplementerEnd(t, server, 45, 1)
	waitForImplementerEnd(t, server, 41, 2)
	startsAfter(t, issueImplementers(t, server, 45)[0], issueImplementers(t, server, 41)[1])
	if head(t, fake, "mobius/41") == sha {
		t.Error("the fix round pushed nothing")
	}
}

func TestAFixRoundOfReviewFindingsDoesNotGoBeforeAnEarlierNewTicket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectThree(t, fake, fixes)
	_, pullRequest := readyPullRequest(t, server, fake)
	fake.SetCreatedAt(shop, pullRequest, 1000)
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForState(t, server, 43, "working")
	fake.AddLabel(shop, 45, "mobius:ready", "owner")
	waitForState(t, server, 45, "queued")
	fake.AddReviewComment(shop, pullRequest, 0, "mobius-test[bot]", "Store the unit.")
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'needs_human' WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForState(t, server, 41, "queued")
	waitForPolls(t, fake)
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	waitForState(t, server, 45, "approval")
	testkit.WaitFor(t, func() bool { return len(issueImplementers(t, server, 41)) >= 2 })
	startsAfter(t, issueImplementers(t, server, 41)[1], issueImplementers(t, server, 45)[0])
}

// A pull request with work for an agent does not hold a new ticket while a slot is free (Mobius-rust#385).
func TestWithTwoFreeSlotsAFixAndANewTicketBothStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fixSpec := implementerSpec(t, server, fake, 41)
	server.Engine.ReplaceWork(map[int64]engine.Work{fixSpec.Task: {Repository: shop, PullRequest: 42, CreatedAt: time.Unix(1000, 0)}})

	ticket := start(t, server, implementerSpec(t, server, fake, 45))
	defer end(t, ticket, "done")
	fix := start(t, server, fixSpec)
	defer end(t, fix, "done")

	for _, agent := range []*engine.Agent{ticket, fix} {
		if got := session(t, server, agent.ID()); got.QueueReason.Valid {
			t.Errorf("session = %+v", got)
		}
	}
}

func TestAPullRequestThatWaitsForTheOwnerDoesNotStopANewTicket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectThree(t, fake, fixes)
	readyPullRequest(t, server, fake)

	fake.AddLabel(shop, 43, "mobius:ready", "owner")

	waitForState(t, server, 43, "approval")
	if state := liveTaskState(t, server, 41); state != "approval" {
		t.Errorf("state of #41 = %s", state)
	}
}

func TestAPullRequestInNeedsHumanDoesNotStopANewTicket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectThree(t, fake, fixes)
	_, pullRequest := readyPullRequest(t, server, fake)
	fake.SetCreatedAt(shop, pullRequest, 0)
	fake.SetBehind(shop, pullRequest)
	fake.CommitFile(shop, "price.txt", "dollars\n", "Add price")
	waitForState(t, server, 41, "needs_human")

	fake.AddLabel(shop, 43, "mobius:ready", "owner")

	waitForState(t, server, 43, "approval")
	if state := liveTaskState(t, server, 41); state != "needs_human" {
		t.Errorf("state of #41 = %s", state)
	}
}

func TestAFailedCheckOnAHeadThatGotItsFixRoundDoesNotStopANewTicket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectThree(t, fake, fixesNothing)
	sha, _ := readyPullRequest(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return len(issueImplementers(t, server, 41)) == 2 })
	waitForState(t, server, 41, "needs_human")

	fake.AddLabel(shop, 43, "mobius:ready", "owner")

	waitForState(t, server, 43, "approval")
	if count := len(issueImplementers(t, server, 41)); count != 2 {
		t.Errorf("Implementers of #41 = %d", count)
	}
}

// The Judge of a task in needs_human holds its slot, so a new ticket waits (Mobius-rust#385).
func TestAJudgeThatRunsFromNeedsHumanHoldsANewTicket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectThree(t, fake, fixes)
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+"[[prompts]]\nwhen = \"You are the Judge\"\nhang = true\n\n"+leadStartsThree)
	_, pullRequest := readyPullRequest(t, server, fake)
	fake.SetCreatedAt(shop, pullRequest, 0)
	fake.SetBehind(shop, pullRequest)
	fake.CommitFile(shop, "price.txt", "dollars\n", "Add price")
	waitForState(t, server, 41, "needs_human")
	fake.AddComment(shop, pullRequest, "owner", "Continue.")
	waitForState(t, server, 41, "working")

	fake.AddLabel(shop, 43, "mobius:ready", "owner")

	ticket := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := issueImplementers(t, server, 43)
		if len(sessions) == 0 {
			return store.Session{}, false
		}
		return sessions[0], sessions[0].QueueReason.Valid
	})
	if ticket.QueueReason.String != "no free agent slot (1/1)" {
		t.Errorf("queue reason = %s", ticket.QueueReason.String)
	}
}

func TestAReviewedPullRequestWithAnOpenThreadDoesNotStopANewTicket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectThree(t, fake, fixes)
	_, pullRequest := readyPullRequest(t, server, fake)
	fake.SetCreatedAt(shop, pullRequest, 0)
	task := liveTask(t, server, 41)
	if _, err := server.DB.Exec("UPDATE tasks SET judged_at = ? WHERE id = ?", time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano), task.ID); err != nil {
		t.Fatal(err)
	}
	fake.AddReviewComment(shop, pullRequest, 0, "owner", "Use cents.")
	if result, err := server.DB.Exec("UPDATE tasks SET state = 'reviewed' WHERE id = ? AND state = 'approval'", task.ID); err != nil {
		t.Fatal(err)
	} else if rows, _ := result.RowsAffected(); rows != 1 {
		t.Fatalf("the task of #41 is %s", taskState(t, server))
	}
	fake.SetBehind(shop, pullRequest)
	fake.CommitFile(shop, "price.txt", "dollars\n", "Add price")
	waitForPolls(t, fake)

	fake.AddLabel(shop, 43, "mobius:ready", "owner")

	waitForState(t, server, 43, "approval")
	if state := liveTaskState(t, server, 41); state != "reviewed" {
		t.Errorf("state of #41 = %s", state)
	}
}
