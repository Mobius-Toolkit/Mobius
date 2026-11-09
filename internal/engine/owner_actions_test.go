package engine_test

import (
	"database/sql"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	loginAction = "Run `wrangler login` on the Mobius server"
	// findingAndAction is a Reviewer with one finding and one action of the Owner.
	findingAndAction = "[[prompts]]\nwhen = \"You are the Reviewer\"\n" +
		"call = { tool = \"submit_review\", arguments = { body = \"One finding.\", comments = [{ path = \"plan.txt\", line = 1, body = \"Store the unit.\" }] } }\n" +
		"then = { tool = \"report_owner_action\", arguments = { action = \"" + loginAction + "\" } }\n\n"
	// secondAction is a Reviewer that reports a new action when its prompt has the thread of the first review.
	secondAction = "[[prompts]]\nwhen = \"Store the unit.\"\ncall = { tool = \"report_owner_action\", arguments = { action = \"Run the login again.\" } }\n\n"
)

type ownerActionRow struct {
	Role   string
	Action string
	Next   string
	Done   bool
}

func ownerActionRows(t *testing.T, server *testserver.Server) []ownerActionRow {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, row *ownerActionRow) error {
		return rows.Scan(&row.Role, &row.Action, &row.Next, &row.Done)
	}, "SELECT role, action, next, done_at IS NOT NULL FROM owner_actions ORDER BY id")
}

// pausedTask runs a task whose Reviewer reports an action of the Owner, and gives the server, the fake GitHub, and the
// open owner action Inbox item. reviewer is the script of the Reviewer.
func pausedTask(t *testing.T, reviewer string) (*testserver.Server, *testkit.FakeGitHub, inboxItem) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, reviewer+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	item := waitForItem(t, server, "owner action")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "owner_action" })
	return server, fake, item
}

func TestAReviewerThatReportsAnActionOfTheOwnerPausesTheTaskWithNoFixRound(t *testing.T) {
	t.Parallel()
	server, fake, item := pausedTask(t, findingAndAction)

	waitForPolls(t, fake)

	if item.Title != loginAction || item.Reason != workstreamTitle || item.Issue != 41 || item.PullRequest != 42 || item.Link == "" ||
		item.Text != loginAction+"\nhttps://github.com/owner/shop/pull/42" {
		t.Errorf("item = %+v", item)
	}
	if items := itemsOf(t, server, "owner action"); len(items) != 1 {
		t.Errorf("items = %+v", items)
	}
	if rows := ownerActionRows(t, server); len(rows) != 1 || rows[0] != (ownerActionRow{Role: "reviewer", Action: loginAction, Next: "review"}) {
		t.Errorf("rows = %+v", rows)
	}
	var events []string
	for _, event := range leadEvents(t, server) {
		if event.Kind == "owner_action" {
			events = append(events, event.Payload)
		}
	}
	if len(events) != 1 || !strings.Contains(events[0], ` #41 "Add plan model" waits for an action of the Owner: `+loginAction+". Pull request #42 https://github.com/owner/shop/pull/42.") {
		t.Errorf("events = %q", events)
	}
	task := liveTask(t, server, 41)
	if task.State != "owner_action" || task.FixRounds != 0 || task.ReviewRounds != 1 {
		t.Errorf("task = %+v", task)
	}
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 {
		t.Errorf("Implementers = %+v", sessions)
	}
	if count, err := store.New(server.DB).CountActiveTasks(t.Context()); err != nil || count != 0 {
		t.Errorf("active tasks = %d, %v", count, err)
	}
	testkit.WaitFor(t, func() bool {
		runs := mobiusCheckRuns(fake)
		return len(runs) == 1 && runs[0].Output.Title == "Wait for the Owner"
	})
	if bodies := roundComments(fake); !slices.ContainsFunc(bodies, func(body string) bool {
		return strings.Contains(body, "Result: The task waits for an action of the Owner: "+loginAction+". Mobius reviews the head again after the Owner marks the action as done.")
	}) {
		t.Errorf("comments = %q", bodies)
	}
}

func TestAFailedCIOfTheHeadOfATaskInOwnerActionStartsNoCIFixRound(t *testing.T) {
	t.Parallel()
	server, fake, _ := pausedTask(t, findingAndAction)
	fake.AddCheckRun(shop, checkRun("sentinel", head(t, fake, "mobius/41"), "completed", "failure"))

	waitForPolls(t, fake)

	if task := liveTask(t, server, 41); task.State != "owner_action" || task.FixRounds != 0 {
		t.Errorf("task = %+v", task)
	}
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 {
		t.Errorf("Implementers = %+v", sessions)
	}
}

func TestDoneOfAnOwnerActionResetsTheCountersAndStartsAReviewerOfTheCurrentHead(t *testing.T) {
	t.Parallel()
	server, fake, item := pausedTask(t, secondAction+findingAndAction)
	if _, err := server.DB.Exec("UPDATE tasks SET fix_rounds = 3 WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	status, body := doneTodo(t, server, item.ID, `{"note":"Logged in."}`)

	if status != http.StatusOK || !strings.Contains(body, `"dismissedAt":"`) {
		t.Fatalf("status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		return len(itemsOf(t, server, "owner action")) == 1 && itemsOf(t, server, "owner action")[0].ID != item.ID
	})
	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.ReviewerRole)) == 2 })
	reviewers := roleSessions(t, server, engine.ReviewerRole)
	prompts := promptTexts(t, server, reviewers[1].ID)
	if len(prompts) == 0 || !strings.Contains(prompts[0], "Head commit: "+head(t, fake, "mobius/41")) {
		t.Errorf("prompts = %q", prompts)
	}
	task := liveTask(t, server, 41)
	if task.State != "owner_action" || task.FixRounds != 0 || task.ReviewRounds != 1 {
		t.Errorf("task = %+v", task)
	}
	rows := ownerActionRows(t, server)
	if len(rows) != 2 || !rows[0].Done || rows[1].Done || rows[1].Action != "Run the login again." {
		t.Errorf("rows = %+v", rows)
	}
	testkit.WaitFor(t, func() bool {
		return slices.Contains(records(t, server, leadChat), "You marked the action \""+loginAction+"\" of PR #42 as done. Note: Logged in.")
	})
}

func TestDoneInboxItemRefusesADismissedOwnerActionItem(t *testing.T) {
	t.Parallel()
	server, _, item := pausedTask(t, secondAction+findingAndAction)
	if status, body := doneTodo(t, server, item.ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	status, body := doneTodo(t, server, item.ID, `{"note":""}`)

	if status != http.StatusConflict {
		t.Errorf("status = %d: %s", status, body)
	}
}

func TestAStopOfATaskInOwnerActionDismissesItsOwnerActionItem(t *testing.T) {
	t.Parallel()
	server, _, _ := pausedTask(t, findingAndAction)
	authorize(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/stop", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool {
		rows := ownerActionRows(t, server)
		return taskState(t, server) == "stopped" && len(itemsOf(t, server, "owner action")) == 0 && len(rows) == 1 && rows[0].Done
	})
}

func TestAnEmptyActionOfTheReviewerGivesARefusalAndNoPause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	empty := "[[prompts]]\nwhen = \"You are the Reviewer\"\ncall = { tool = \"report_owner_action\", arguments = { action = \"  \" } }\n\n"
	server, _ := connectTask(t, fake, empty+leadStarts, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\"")
	reviewers := endedReviewers(t, server, 1)
	if text := reply(t, server, reviewers[0].ID); !strings.Contains(text, "error: action must not be empty.") {
		t.Errorf("reply = %s", text)
	}
	if items := itemsOf(t, server, "owner action"); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
	if rows := ownerActionRows(t, server); len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
}
