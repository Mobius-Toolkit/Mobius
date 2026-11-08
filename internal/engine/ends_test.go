package engine_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// hangs is an Implementer whose turn ends only at a cancel.
const hangs = "[[prompts]]\nhang = true\n"

// waitForApproval dispatches #41 and waits until its task waits for the Lead.
func waitForApproval(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) {
	t.Helper()
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
}

// noTaskLabels waits until #41 has no mobius:working, no mobius:review and no mobius:needs-human. Mobius removes the
// labels after it changes the state of the task.
func noTaskLabels(t *testing.T, fake *testkit.FakeGitHub) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		return !hasLabel(fake, "mobius:working") && !hasLabel(fake, "mobius:review") && !hasLabel(fake, "mobius:needs-human") && !hasLabel(fake, "mobius:question")
	})
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// waitForLeadEvent waits for a delivered event of kind of the Workstream #12 with text.
func waitForLeadEvent(t *testing.T, server *testserver.Server, kind, text string) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		for _, event := range leadEvents(t, server) {
			if event.Kind == kind && strings.Contains(event.Payload, text) && event.Delivered {
				return true
			}
		}
		return false
	})
}

func TestAMergeEndsTheTaskAndKeepsTheBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, commits, noChange)
	waitForApproval(t, server, fake)
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	if !exists(t, worktree) {
		t.Fatal("the worktree is gone")
	}

	fake.MergePullRequest(shop, 42)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	noTaskLabels(t, fake)
	if exists(t, worktree) {
		t.Error("the worktree stays")
	}
	head(t, fake, "mobius/41")
	waitForLeadPrompt(t, server, " end of #41 \"Add plan model\": pull request #42 merged.")
	waitForLeadEvent(t, server, "end", " end of #41 \"Add plan model\": pull request #42 merged.")
}

func TestAMergeKeepsTheEndEventWhenALabelRemovalFails(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	waitForApproval(t, server, fake)

	fake.FailRemoveLabels(shop, 41, true)
	fake.MergePullRequest(shop, 42)

	testkit.WaitFor(t, func() bool {
		var count int
		if err := server.DB.QueryRow("SELECT count(*) FROM lead_events WHERE repository = ? AND issue = 41 AND kind = 'end'", shop).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count > 0
	})
	var (
		kind    string
		payload string
	)
	if err := server.DB.QueryRow("SELECT kind, payload FROM lead_events WHERE repository = ? AND issue = 41 AND kind = 'end'", shop).Scan(&kind, &payload); err != nil {
		t.Fatal(err)
	}
	if kind != "end" || !strings.Contains(payload, "pull request #42 merged.") {
		t.Errorf("kind = %s, payload = %s", kind, payload)
	}
	waitForPolls(t, fake)
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM lead_events WHERE repository = ? AND issue = 41 AND kind = 'end'", shop).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("end events = %d, want 1", count)
	}
}

func TestAMergeSetsTheMergedQualityValue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	waitForApproval(t, server, fake)

	fake.MergePullRequest(shop, 42)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	waitForLeadEvent(t, server, "end", " end of #41 \"Add plan model\": pull request #42 merged.")

	rows, err := store.New(server.DB).ListEndedTaskQuality(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no ended task quality rows")
	}
	if !rows[0].Merged {
		t.Errorf("task quality row merged = %v, want true", rows[0].Merged)
	}
}

func TestAMergeClosesTheOpenIssueAsCompleted(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	waitForApproval(t, server, fake)

	fake.MergePullRequest(shop, 42)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if state, reason := fake.State(shop, 41); state != "closed" || reason != "completed" {
		t.Errorf("state = %s, %s", state, reason)
	}
}

func TestAMergeShowsTheEndedTaskAsClosedInTheTasksTabWhenTheIssueListHidesTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	waitForApproval(t, server, fake)

	fake.HideFromIssueList(shop, 41)
	fake.MergePullRequest(shop, 42)

	testkit.WaitFor(t, func() bool {
		lines, err := server.Engine.Tasks(context.Background(), shop, 12)
		if err != nil || len(lines) == 0 {
			return false
		}
		for _, line := range lines {
			if line.Number == 41 {
				return line.State == "closed"
			}
		}
		return false
	})
}

func TestACloseOfTheIssueBeforeAPullRequestStopsTheImplementerAndEndsTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, hangs, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) > 0 && sessions[0].AcpSessionID.Valid
	})

	fake.AddLabel(shop, 41, "mobius:question", testkit.AppSlug+"[bot]")
	fake.CloseIssue(shop, 41)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if session := endedImplementers(t, server, 1)[0]; session.EndReason.String != "stopped" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	noTaskLabels(t, fake)
	if exists(t, filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")) {
		t.Error("the worktree stays")
	}
	if len(fake.PullRequests(shop)) != 0 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
}

func TestARemovalOfTheWorkingLabelStopsTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, commits, noChange)
	waitForApproval(t, server, fake)
	items := len(inbox(t, server))

	fake.AddLabel(shop, 41, "mobius:question", testkit.AppSlug+"[bot]")
	fake.RemoveLabel(shop, 41, "mobius:working", "mallory")

	testkit.WaitFor(t, func() bool {
		runs := fake.CheckRuns(shop)
		return len(runs) > 0 && runs[len(runs)-1].Output.Title == "Stopped"
	})
	if state := taskState(t, server); state != "stopped" {
		t.Errorf("state = %s", state)
	}
	runs := fake.CheckRuns(shop)
	if len(runs) != 1 || runs[0].HeadSHA != head(t, fake, "mobius/41") || runs[0].Conclusion != "failure" || runs[0].Output.Summary != "Stopped by a label removal." {
		t.Errorf("check runs = %+v", runs)
	}
	noTaskLabels(t, fake)
	if exists(t, filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")) {
		t.Error("the worktree stays")
	}
	if len(fake.PullRequests(shop)) != 1 || len(inbox(t, server)) != items {
		t.Errorf("pull requests = %+v, Inbox = %+v", fake.PullRequests(shop), inbox(t, server))
	}
}

func TestAReadyLabelAfterAStopWithNoPullRequestStartsANewTaskOnANewBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	implementer := "[[prompts]]\nwhen = \"Plans have a price.\"\nhang = true\n\n" + commits
	server, _ := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	stopped := testkit.WaitForValue(t, func() (store.Task, bool) {
		task, err := store.New(server.DB).GetLiveTask(t.Context(), store.GetLiveTaskParams{Repository: shop, Issue: 41})
		return task, err == nil && task.Branch.Valid
	})
	fake.RemoveLabel(shop, 41, "mobius:working", "mallory")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "stopped" })
	fake.SetBody(shop, 41, "Plans have a price in cents.")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	pullRequests := testkit.WaitForValue(t, func() ([]testkit.PullRequest, bool) {
		pullRequests := fake.PullRequests(shop)
		return pullRequests, len(pullRequests) == 1
	})
	task := liveTask(t, server, 41)
	if pullRequests[0].Head != "mobius/41-2" || task.ID == stopped.ID || task.Branch.String != "mobius/41-2" {
		t.Errorf("pull requests = %+v, task = %+v", pullRequests, task)
	}
}

func TestAReadyLabelAfterAStopContinuesThePullRequestOnTheSameBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	finishes := "[[prompts]]\nwhen = \"The human continued the task.\"\nshell = \"echo done > done.txt && git add done.txt && git commit -q -m 'Finish the issue'\"\n\n" + commits
	server, _ := connectTask(t, fake, leadStarts, finishes, noChange)
	waitForApproval(t, server, fake)
	first := head(t, fake, "mobius/41")
	fake.RemoveLabel(shop, 41, "mobius:working", "mallory")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "stopped" })
	stopped := liveTask(t, server, 41)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	round := roundPrompt(t, server)
	if !strings.Contains(round, "# Open items\n\nThe human continued the task. Finish the issue.\n") {
		t.Errorf("round = %s", round)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" && head(t, fake, "mobius/41") != first })
	isAncestor(t, fake, first, head(t, fake, "mobius/41"))
	if task := liveTask(t, server, 41); task.ID != stopped.ID || task.Branch != stopped.Branch {
		t.Errorf("task = %+v", task)
	}
	if state, _ := fake.State(shop, 42); len(fake.PullRequests(shop)) != 1 || state != "open" {
		t.Errorf("pull requests = %+v, state = %s", fake.PullRequests(shop), state)
	}
	if !slices.Contains(fake.Labels(shop, 41), "mobius:working") || slices.Contains(fake.Labels(shop, 41), "mobius:ready") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
}

// A thread of the Reviewer stays open after a stop, so the next round gets it (Mobius-rust#253).
func TestARemovalOfNeedsHumanSendsTheOpenThreadOfTheMobiusAppToAFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, fixes, noChange)
	waitForApproval(t, server, fake)
	// The round gets the new script only in a new Harness process.
	server.Engine.SetClock(func() time.Time { return time.Now().Add(61 * time.Minute) })
	thread := fake.AddReviewComment(shop, 42, 0, "mobius-test[bot]", "Store the unit.")
	testkit.InstallFakeHarness(t, dataDir, "devin", options+"[[prompts]]\nwhen = \"Action: fix\"\ncall = { tool = \"reply_thread\", arguments = { thread = "+itoa(thread)+", text = \"Stored.\" } }\n")
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'needs_human' WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") })

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")

	round := roundPrompt(t, server)
	_, items, _ := strings.Cut(round, "# Open items\n")
	inOrder(t, items, "Thread "+itoa(thread)+", src/plan.rs line 12:", "Store the unit.", "Action: fix\n")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	if len(fake.PullRequests(shop)) != 1 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
}

func TestARemovalOfNeedsHumanSendsAnOpenFollowUpThreadToTheLeadAndNotToAFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	waitForApproval(t, server, fake)
	thread := fake.AddReviewComment(shop, 42, 0, "mobius-test[bot]", "Name the unit.\n\n<!-- mobius:follow-up -->")
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'needs_human' WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") })

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")

	waitForLeadPrompt(t, server, " follow-up on pull request #42 of #41 \"Add plan model\", item "+itoa(thread)+", src/plan.rs line 12:\n\n> Name the unit.")
	round := roundPrompt(t, server)
	if !strings.Contains(round, "# Open items\n\nThe human continued the task. Finish the issue.\n") {
		t.Errorf("round = %s", round)
	}
}

func TestARetryAfterAFailedResolveOfAFollowUpThreadSendsNoSecondEventToTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	waitForApproval(t, server, fake)
	thread := fake.AddReviewComment(shop, 42, 0, "mobius-test[bot]", "Name the unit.\n\n<!-- mobius:follow-up -->")
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'needs_human' WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") })
	fake.FailResolveThreads(true)

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")

	waitForLeadPrompt(t, server, " follow-up on pull request #42 of #41 \"Add plan model\", item "+itoa(thread)+", src/plan.rs line 12:")
	fake.FailResolveThreads(false)
	testkit.WaitFor(t, func() bool { return fake.ReviewThread(shop, 42, thread).Resolved })
	var events int
	if err := server.DB.QueryRow("SELECT count(*) FROM lead_events WHERE kind = 'follow_up'").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Errorf("follow-up events = %d", events)
	}
}

type loggedLeadEvent struct {
	id          int64
	kind        string
	time        string
	deliveredAt sql.NullString
	held        int64
	payload     string
}

func TestAnEndEventStartsANewLeadSessionWhenNoSessionIsOpen(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"end of #41\"\nreply = [\"Seen\"]\n\n" + leadStarts
	server, _ := connectTask(t, fake, lead, commits, noChange)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		t.Logf("task state: %s", taskState(t, server))
		events := query(t, server, func(rows *sql.Rows, e *loggedLeadEvent) error {
			return rows.Scan(&e.id, &e.kind, &e.time, &e.deliveredAt, &e.held, &e.payload)
		}, "SELECT id, kind, time, delivered_at, held, payload FROM lead_events WHERE repository = ? ORDER BY id", shop)
		for _, e := range events {
			t.Logf("lead event: id=%d kind=%s time=%s delivered_at=%s held=%d payload=%s", e.id, e.kind, e.time, e.deliveredAt.String, e.held, e.payload)
		}
		for _, s := range chatSessions(t, server, leadChat, engine.LeadRole) {
			prompts := promptTexts(t, server, s.ID)
			t.Logf("lead session: id=%d started_at=%s ended_at=%s end_reason=%s queue_reason=%s prompts=%d", s.ID, s.StartedAt, s.EndedAt.String, s.EndReason.String, s.QueueReason.String, len(prompts))
			for i, prompt := range prompts {
				tail := prompt
				if runes := []rune(prompt); len(runes) > 500 {
					tail = string(runes[len(runes)-500:])
				}
				t.Logf("session %d prompt %d: %s", s.ID, i+1, tail)
			}
		}
	})
	waitForApproval(t, server, fake)

	waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\": pull request #42")
	testkit.WaitFor(t, func() bool {
		if !eventDelivered(t, server) {
			return false
		}
		sessions := chatSessions(t, server, leadChat, engine.LeadRole)
		if len(sessions) == 0 {
			return false
		}
		for _, s := range sessions {
			if !s.EndedAt.Valid {
				return false
			}
		}
		return true
	})
	before := chatSessions(t, server, leadChat, engine.LeadRole)
	lastBefore := before[len(before)-1]

	fake.MergePullRequest(shop, 42)

	endText := " end of #41 \"Add plan model\": pull request #42 merged."
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		texts := promptTexts(t, server, session.ID)
		return len(texts) > 0 && strings.Contains(texts[0], endText)
	})
	prompt := promptTexts(t, server, session.ID)[0]
	inOrder(t, prompt, "# Event\n\n", endText)
	if strings.Contains(prompt, "# Events with no turn\n\n") {
		t.Errorf("prompt holds end text in quiet events section: %s", prompt)
	}
	if strings.Contains(prompt, "# Owner message") {
		t.Errorf("prompt holds an Owner message: %s", prompt)
	}
	if session.ID <= lastBefore.ID {
		t.Errorf("session ID %d <= last session ID before merge %d", session.ID, lastBefore.ID)
	}
	started, err := time.Parse(time.RFC3339Nano, session.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	ended, err := time.Parse(time.RFC3339Nano, lastBefore.EndedAt.String)
	if err != nil {
		t.Fatal(err)
	}
	if started.Before(ended) {
		t.Errorf("session started at %v before last session ended at %v", started, ended)
	}
}
