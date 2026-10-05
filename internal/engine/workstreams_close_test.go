package engine_test

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/testserver"
)

const closed = "Workstream closed"

var closedComment = testkit.Comment{Author: "mobius-test[bot]", Body: closed}

type leadEvent struct {
	Workstream int64
	Kind       string
	Payload    string
	Delivered  bool
}

func leadEvents(t *testing.T, server *testserver.Server) []leadEvent {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, e *leadEvent) error {
		return rows.Scan(&e.Workstream, &e.Kind, &e.Payload, &e.Delivered)
	}, "SELECT workstream, kind, payload, delivered_at IS NOT NULL FROM lead_events WHERE repository = ? ORDER BY id", shop)
}

// live tells if the task of #41 is live.
func live(t *testing.T, server *testserver.Server) bool {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM tasks WHERE repository = ? AND issue = 41 AND state <> 'ended'", shop).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}

func noMobiusLabels(t *testing.T, fake *testkit.FakeGitHub, number int64) {
	t.Helper()
	labels := fake.Labels(shop, number)
	if slices.ContainsFunc(labels, func(label string) bool { return strings.HasPrefix(label, "mobius:") }) {
		t.Errorf("labels of #%d = %v", number, labels)
	}
}

// addLiveTask adds the Workstream #12 with the task #41, the label mobius:working of #41, and the open pull request #45.
func addLiveTask(fake *testkit.FakeGitHub) {
	addWorkstream(fake)
	fake.AddLabel(shop, 41, "mobius:working", "mobius-test[bot]")
	fake.AddPullRequest(shop, 45, "Add plan model")
}

// startWithLiveTask starts a server with the live task of #41 and its pull request #45.
func startWithLiveTask(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	server := startServer(t, fake, t.TempDir(), `INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, pull_request)
		VALUES ('owner/shop', 41, 12, 'working', '2026-10-04T10:00:00Z', 45)`)
	server.WaitForFirstPoll(t, shop)
	return server
}

func TestACloseEndsTheTasksAndClosesThePullRequestsAndIssuesBelow(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addLiveTask(fake)
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	fake.AddIssue(shop, 43, "Add plan price")
	fake.AddSubIssue(shop, 12, 43)
	fake.AddIssue(shop, 44, "Price table")
	fake.AddBlockedBy(shop, 43, 44)
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	server := startWithLiveTask(t, fake)
	if _, err := server.DB.Exec(`INSERT INTO lead_events (repository, workstream, kind, payload, time) VALUES ('owner/shop', 12, 'stop', 'An old event', '2026-10-04T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	fake.CloseIssue(shop, 12)

	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 43); return state == "closed" })
	for _, number := range []int64{41, 43} {
		if state, reason := fake.State(shop, number); state != "closed" || reason != "not_planned" {
			t.Errorf("state of #%d = %s, %s", number, state, reason)
		}
		if !slices.Contains(fake.Comments(shop, number), closedComment) {
			t.Errorf("comments of #%d = %v", number, fake.Comments(shop, number))
		}
		noMobiusLabels(t, fake, number)
	}
	if state, _ := fake.State(shop, 45); state != "closed" || !slices.Contains(fake.Comments(shop, 45), closedComment) {
		t.Errorf("pull request = %s, %v", state, fake.Comments(shop, 45))
	}
	if state, _ := fake.State(shop, 44); state != "open" {
		t.Errorf("state of #44 = %s", state)
	}
	if labels := fake.Labels(shop, 12); !slices.Equal(labels, []string{"mobius:workstream"}) {
		t.Errorf("labels of #12 = %v", labels)
	}
	if live(t, server) {
		t.Error("the task of #41 is live")
	}
	if events := leadEvents(t, server); len(events) != 1 || !events[0].Delivered {
		t.Errorf("Lead events = %+v", events)
	}
}

func TestACompletionClosesTheWorkstreamAndEndsTheLiveTask(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addLiveTask(fake)
	server := startWithLiveTask(t, fake)
	if workstreams(t, server)[0].AllTasksClosed {
		t.Fatal("all tasks closed")
	}

	fake.CloseIssue(shop, 41)

	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })
	if !live(t, server) {
		t.Fatal("the task of #41 is not live")
	}
	if status, body := complete(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	if state, reason := fake.State(shop, 12); state != "closed" || reason != "completed" {
		t.Errorf("state = %s, %s", state, reason)
	}
	if live(t, server) {
		t.Error("the task of #41 is live")
	}
	if state, _ := fake.State(shop, 45); state != "closed" {
		t.Errorf("state of the pull request = %s", state)
	}
}

func TestARemovalOfTheWorkstreamLabelEndsTheTasksAndClosesNothing(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addLiveTask(fake)
	server := startWithLiveTask(t, fake)

	fake.RemoveLabel(shop, 12, "mobius:workstream", "mallory")

	testkit.WaitFor(t, func() bool { return !live(t, server) })
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:working") })
	for _, number := range []int64{12, 41, 45} {
		if state, reason := fake.State(shop, number); state != "open" || reason != "" {
			t.Errorf("state of #%d = %s, %s", number, state, reason)
		}
	}
}

func TestAReopenAddsAnEventForTheLeadAndReopensNoIssue(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)
	fake.CloseIssue(shop, 12)
	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 41); return state == "closed" })

	fake.ReopenIssue(shop, 12)

	event := testkit.WaitForValue(t, func() (leadEvent, bool) {
		for _, event := range leadEvents(t, server) {
			if event.Kind == "reopen" {
				return event, true
			}
		}
		return leadEvent{}, false
	})
	if event.Workstream != 12 || !strings.Contains(event.Payload, ` reopen of Workstream #12 "Integrate loyalty plans" by @owner:`) {
		t.Errorf("event = %+v", event)
	}
	var message string
	if err := server.DB.QueryRow("SELECT text FROM chat_messages WHERE repository = ? AND workstream = 12 AND author = 'Event'", shop).Scan(&message); err != nil || message != event.Payload {
		t.Errorf("chat message = %q: %v", message, err)
	}
	if state, reason := fake.State(shop, 41); state != "closed" || reason != "not_planned" {
		t.Errorf("state of #41 = %s, %s", state, reason)
	}
}

// startWithLead starts a server with the Workstream #12 and its task #41, and a Lead that never ends its turn for a
// message of the Owner. It sends a message to the Lead and waits for the start of the Lead.
func startWithLead(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string) {
	t.Helper()
	server, dataDir := connect(t, fake, "[[prompts]]\nwhen = \"# Owner message\"\nhang = true\n")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	sendChat(t, server, leadChat, "Plan the next step.")
	waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool { return session.AcpSessionID.Valid })
	return server, dataDir
}

func stoppedLead(t *testing.T, server *testserver.Server) {
	t.Helper()
	waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool { return session.EndReason.String == "stopped" })
}

func TestACloseStopsTheLeadAndKeepsItsDirectory(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := startWithLead(t, fake)

	fake.CloseIssue(shop, 12)

	stoppedLead(t, server)
	if _, err := os.Stat(filepath.Join(dataDir, "leads", "owner", "shop", "12")); err != nil {
		t.Error(err)
	}
}

func TestACompletionStopsTheLead(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := startWithLead(t, fake)
	fake.CloseIssue(shop, 41)
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })

	if status, body := complete(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	stoppedLead(t, server)
}

func TestAFailedCompletionKeepsTheLeadRunning(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := startWithLead(t, fake)
	fake.CloseIssue(shop, 41)
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })
	fake.FailClose(shop, 12)

	if status, body := complete(t, server, 12); status == http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
		if session.EndedAt.Valid {
			t.Errorf("session = %+v", session)
		}
	}
}

func TestAReopenStartsALeadWithTheEvent(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	waitForPoll(t, server, fake)
	fake.CloseIssue(shop, 12)
	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 41); return state == "closed" })

	fake.ReopenIssue(shop, 12)

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool {
			return strings.Contains(prompt, "# Event\n\n") && strings.Contains(prompt, ` reopen of Workstream #12 "Integrate loyalty plans" by @owner:`)
		})
	})
	if state, reason := fake.State(shop, 41); state != "closed" || reason != "not_planned" {
		t.Errorf("state of #41 = %s, %s", state, reason)
	}
}

func TestACloseKeepsTheBranchOfTheClosedPullRequest(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	readyWithItem(t, server, fake)

	fake.CloseIssue(shop, 12)

	// Mobius closes the pull request after its comment.
	testkit.WaitFor(t, func() bool {
		state, _ := fake.State(shop, 42)
		return slices.Contains(fake.Comments(shop, 42), closedComment) && state == "closed"
	})
	if live(t, server) {
		t.Error("the task of #41 is live")
	}
	head(t, fake, "mobius/41")
}

func TestARemovalOfTheWorkstreamLabelStopsTheRunningImplementer(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, hangs, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) > 0 && sessions[0].AcpSessionID.Valid
	})

	fake.RemoveLabel(shop, 12, "mobius:workstream", "mallory")

	if session := endedImplementers(t, server, 1)[0]; session.EndReason.String != "stopped" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	testkit.WaitFor(t, func() bool { return !live(t, server) && !hasLabel(fake, "mobius:working") })
	for _, number := range []int64{12, 41} {
		if state, _ := fake.State(shop, number); state != "open" {
			t.Errorf("state of #%d = %s", number, state)
		}
	}
}

func TestACompletionStopsTheRunningReviewer(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, "[[prompts]]\nwhen = \"You are the Reviewer\"\nhang = true\n\n"+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		reviewers := roleSessions(t, server, engine.ReviewerRole)
		return len(reviewers) == 1 && reviewers[0].AcpSessionID.Valid
	})

	// The pull request is open and the Reviewer works, so only the completion ends them.
	fake.CloseIssue(shop, 41)

	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })
	if !live(t, server) {
		t.Fatal("the task of #41 is not live")
	}
	if status, body := complete(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	if live(t, server) {
		t.Error("the task of #41 is live")
	}
	testkit.WaitFor(t, func() bool {
		reviewers := roleSessions(t, server, engine.ReviewerRole)
		return reviewers[0].EndReason.String == "stopped"
	})
}
