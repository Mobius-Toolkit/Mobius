package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// heldRead is a hold of the fake GitHub on a read of an issue.
type heldRead struct {
	reached <-chan struct{}
	release func()
}

// issueStart gives start_requested_by of the Start of #41, and false when the issue has no Start.
func issueStart(t *testing.T, server *testserver.Server) (string, bool) {
	t.Helper()
	start, err := store.New(server.DB).GetIssueStart(t.Context(), store.GetIssueStartParams{Repository: shop, Number: 41})
	if err != nil {
		t.Fatal(err)
	}
	return start.StartRequestedBy, start.StartRequestedAt.Valid
}

// dispatchEvents gives the number of dispatch events of the issue number.
func dispatchEvents(t *testing.T, server *testserver.Server, number int64) int {
	t.Helper()
	var count int
	err := server.DB.QueryRow("SELECT count(*) FROM lead_events WHERE repository = ? AND kind = 'dispatch' AND issue = ?", shop, number).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// issueBlocked tells if #41 has an open blocker in the rows of Mobius.
func issueBlocked(t *testing.T, server *testserver.Server) bool {
	t.Helper()
	blocked, err := store.New(server.DB).IsIssueBlocked(t.Context(), store.IsIssueBlockedParams{Repository: shop, Number: 41})
	if err != nil {
		t.Fatal(err)
	}
	return blocked
}

func TestADispatchedTaskThatGetsABlockerEndsAndFreesItsSlotAndDispatchesAgainAfterTheBlockerCloses(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	addTaskIssue(fake, 42, "Add plan price")
	fake.AddIssue(shop, 88, "Invoice totals")
	server := startAutopilot(t, fake, func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 }, "")
	liveTaskOf(t, server, 41)
	testkit.WaitFor(t, func() bool { return fake.LabelAddedBy(shop, 41, "mobius:working", testkit.AppSlug+"[bot]") })
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 42) {
		t.Fatal("the second task started with no free slot")
	}

	fake.AddBlockedBy(shop, 41, 88)

	liveTaskOf(t, server, 42)
	testkit.WaitFor(t, func() bool { return !hasLabel(fake, "mobius:working") })
	if hasLiveTask(t, server, 41) {
		t.Error("the blocked task is still live")
	}
	if by, ok := issueStart(t, server); !ok || by != app {
		t.Errorf("Start = %q, %t", by, ok)
	}
	if got := dispatchEvents(t, server, 41); got != 1 {
		t.Errorf("dispatch events of #41 = %d", got)
	}

	fake.CloseIssue(shop, 88)

	testkit.WaitFor(t, func() bool { return dispatchEvents(t, server, 41) == 2 })
	if task := liveTaskOf(t, server, 41); task.State != "dispatched" {
		t.Errorf("task = %+v", task)
	}
	if _, ok := issueStart(t, server); ok {
		t.Error("the issue still has its Start")
	}
}

func TestADispatchedTaskWithAPullRequestStaysDispatchedWhenItsIssueGetsABlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	implementer := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\n" + cannotDoCall + "\n" + commits
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, implementer, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	sendChat(t, server, leadChat, "Send the findings to #41")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })
	if task := liveTaskOf(t, server, 41); !task.PullRequest.Valid || !task.Branch.Valid {
		t.Fatalf("task = %+v", task)
	}

	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddBlockedBy(shop, 41, 88)
	testkit.WaitFor(t, func() bool { return issueBlocked(t, server) })
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}
	if _, ok := issueStart(t, server); ok {
		t.Error("the issue has a Start")
	}
}

func TestADispatchedTaskWithABranchAndNoPullRequestStaysDispatchedWhenItsIssueGetsABlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, "[[prompts]]\nwhen = \"cannot_do on #41\"\nreply = [\"ok\"]\n\n"+leadStarts, "[[prompts]]\n"+cannotDoCall, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForLeadPrompt(t, server, " cannot_do on #41 ")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })
	if task := liveTaskOf(t, server, 41); task.PullRequest.Valid || !task.Branch.Valid {
		t.Fatalf("task = %+v", task)
	}

	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddBlockedBy(shop, 41, 88)
	testkit.WaitFor(t, func() bool { return issueBlocked(t, server) })
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}
	if _, ok := issueStart(t, server); ok {
		t.Error("the issue has a Start")
	}
}

func TestADispatchedTaskWithAnOpenQuestionStaysDispatchedWhenItsIssueGetsABlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	ask := "[[prompts]]\nwhen = \"dispatch of #41\"\ncall = { tool = \"ask\", arguments = { repository = \"owner/shop\", n = 41, text = \"Cents or dollars?\" } }\n\n"
	server, _ := connectWith(t, fake, ask+leadWaits, keepSessionOpen)
	// The hold keeps the task in dispatched: ask moves it to asked after the write of its comment.
	reached, release := fake.HoldComment(shop, 41)
	dispatchTask(fake, 41, "Add plan model")
	waitForItem(t, server, "question")
	testkit.WaitFor(t, func() bool {
		select {
		case <-reached:
			return true
		default:
			return false
		}
	})

	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddBlockedBy(shop, 41, 88)
	testkit.WaitFor(t, func() bool { return issueBlocked(t, server) })
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}
	if _, ok := issueStart(t, server); ok {
		t.Error("the issue has a Start")
	}
	release()
}

func TestATaskThatLeavesDispatchedBetweenTheReadAndTheEndStepKeepsItsImplementerAndGetsNoStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"\\n\\nStart #41 now\"\n" + startImplementer + "\n" + leadWaits
	server, _ := connectTask(t, fake, lead, commits, keepSessionOpen)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForLeadPrompt(t, server, "dispatch of #41")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	fake.AddIssue(shop, 88, "Invoice totals")
	// The next poll reads the blocker in its copy step, and then waits at its read of #41 in checkTask.
	holds := make(chan heldRead, 1)
	fake.AfterNextIssueListRead(shop, func() {
		reached, release := fake.HoldIssue(shop, 41)
		fake.AddBlockedBy(shop, 41, 88)
		holds <- heldRead{reached, release}
	})
	hold := testkit.WaitForValue(t, func() (heldRead, bool) {
		select {
		case hold := <-holds:
			return hold, true
		default:
			return heldRead{}, false
		}
	})
	testkit.WaitFor(t, func() bool {
		select {
		case <-hold.reached:
			return true
		default:
			return false
		}
	})
	if !issueBlocked(t, server) || taskState(t, server) != "dispatched" {
		t.Fatalf("the poll read the task before the blocker: blocked = %t, state = %s", issueBlocked(t, server), taskState(t, server))
	}

	sendChat(t, server, leadChat, "Start #41 now")
	testkit.WaitFor(t, func() bool { return taskState(t, server) != "dispatched" })
	hold.release()

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	waitForPolls(t, fake)
	if _, ok := issueStart(t, server); ok {
		t.Error("the issue has a Start")
	}
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 {
		t.Errorf("Implementer sessions = %d", len(sessions))
	}
}
