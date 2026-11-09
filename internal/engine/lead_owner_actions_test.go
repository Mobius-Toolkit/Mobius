package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	// leadSees is a Lead that only replies to a dispatch of #41, so the task stays dispatched.
	leadSees     = "[[prompts]]\nwhen = \"dispatch of #41\"\nreply = [\"Seen\"]\n\n"
	loginRefusal = "error: The task of #41 has no open action that the Lead marked."
)

// leadMarks is a Lead that marks the action loginAction on #41 for each next step, clears it, and asks a question.
var leadMarks = "[[prompts]]\nwhen = \"Mark review on #41\"\n" + markCall("review") +
	"[[prompts]]\nwhen = \"Mark fix round on #41\"\n" + markCall("fix_round") +
	"[[prompts]]\nwhen = \"Mark implementer on #41\"\n" + markCall("implementer") +
	"[[prompts]]\nwhen = \"Clear #41\"\ncall = { tool = \"clear_owner_action\", arguments = { repository = \"owner/shop\", n = 41 } }\n\n" +
	"[[prompts]]\nwhen = \"Ask on #41\"\ncall = { tool = \"ask\", arguments = { repository = \"owner/shop\", n = 41, text = \"Which unit?\" } }\n\n"

func markCall(next string) string {
	return "call = { tool = \"mark_owner_action\", arguments = { repository = \"owner/shop\", n = 41, action = \"" + loginAction + "\", next = \"" + next + "\" } }\n\n"
}

func TestTheLeadMarksAnActionOfTheOwnerOnATaskInChecksAndTheDoneStartsAFixRoundWithTheFailedCheckRuns(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadMarks+leadStarts, fixes, longGrace)
	sha := checksHead(t, server, fake)

	sendChat(t, server, leadChat, "Mark fix round on #41")

	item := waitForItem(t, server, "owner action")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "owner_action" })
	if item.Title != loginAction || item.Issue != 41 || item.PullRequest != 42 || item.Text != loginAction+"\nhttps://github.com/owner/shop/pull/42" {
		t.Errorf("item = %+v", item)
	}
	if rows := ownerActionRows(t, server); len(rows) != 1 || rows[0] != (ownerActionRow{Role: engine.LeadRole, Action: loginAction, Next: "fix_round"}) {
		t.Errorf("rows = %+v", rows)
	}
	for _, event := range leadEvents(t, server) {
		if event.Kind == "owner_action" {
			t.Errorf("event = %+v", event)
		}
	}

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	waitForPolls(t, fake)

	if task := liveTask(t, server, 41); task.State != "owner_action" || task.FixRounds != 0 {
		t.Errorf("task = %+v", task)
	}
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 {
		t.Errorf("Implementers = %+v", sessions)
	}

	status, body := doneTodo(t, server, item.ID, `{"note":"Logged in."}`)

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if round := roundPrompt(t, server); !strings.Contains(round, "Check run \"build\"") {
		t.Errorf("round = %s", round)
	}
	if rows := ownerActionRows(t, server); len(rows) != 1 || !rows[0].Done {
		t.Errorf("rows = %+v", rows)
	}
	if items := itemsOf(t, server, "owner action"); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
	testkit.WaitFor(t, func() bool {
		return slices.Contains(records(t, server, leadChat), "You marked the action \""+loginAction+"\" of PR #42 as done. Note: Logged in.")
	})
}

func TestTheLeadMarksAnActionOnADispatchedTaskAndTheDoneGivesTheLeadADispatchEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadMarks+leadSees, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	if labels := fake.Labels(shop, 41); !slices.Contains(labels, "mobius:working") {
		t.Fatalf("labels = %q", labels)
	}

	sendChat(t, server, leadChat, "Mark implementer on #41")

	item := waitForItem(t, server, "owner action")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "owner_action" })
	if item.Text != loginAction || item.PullRequest != 0 {
		t.Errorf("item = %+v", item)
	}
	if rows := ownerActionRows(t, server); len(rows) != 1 || rows[0] != (ownerActionRow{Role: engine.LeadRole, Action: loginAction, Next: "implementer"}) {
		t.Errorf("rows = %+v", rows)
	}
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:working") })
	if count, err := store.New(server.DB).CountActiveTasks(t.Context()); err != nil || count != 0 {
		t.Errorf("active tasks = %d, %v", count, err)
	}

	status, body := doneTodo(t, server, item.ID, `{"note":"Logged in."}`)

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 41), "mobius:working") })
	var dispatches []string
	for _, event := range leadEvents(t, server) {
		if event.Kind == "dispatch" && strings.Contains(event.Payload, "action of the Owner done of") {
			dispatches = append(dispatches, event.Payload)
		}
	}
	if len(dispatches) != 1 || !strings.Contains(dispatches[0], " action of the Owner done of #41 \"Add plan model\": "+loginAction+"\nNote of the Owner: Logged in.") {
		t.Errorf("events = %q", dispatches)
	}
	testkit.WaitFor(t, func() bool {
		return slices.Contains(records(t, server, leadChat), "You marked the action \""+loginAction+"\" of #41 as done. Note: Logged in.")
	})
}

func TestTheLeadClearsItsActionOnATaskInApprovalAndAReviewerReviewsTheHeadAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadMarks+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	sendChat(t, server, leadChat, "Mark review on #41")
	item := waitForItem(t, server, "owner action")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "owner_action" })
	reviewers := len(roleSessions(t, server, engine.ReviewerRole))

	sendChat(t, server, leadChat, "Clear #41")

	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.ReviewerRole)) == reviewers+1 })
	prompt := testkit.WaitForValue(t, func() (string, bool) {
		sessions := roleSessions(t, server, engine.ReviewerRole)
		prompts := promptTexts(t, server, sessions[len(sessions)-1].ID)
		return strings.Join(prompts, "\n"), len(prompts) > 0
	})
	if !strings.Contains(prompt, "Head commit: "+head(t, fake, "mobius/41")) {
		t.Errorf("prompt = %s", prompt)
	}
	testkit.WaitFor(t, func() bool {
		rows := ownerActionRows(t, server)
		return len(rows) == 1 && rows[0].Done && len(itemsOf(t, server, "owner action")) == 0
	})
	testkit.WaitFor(t, func() bool {
		return slices.Contains(records(t, server, leadChat), "The Lead cleared the action \""+loginAction+"\" of PR #42.")
	})
	if status, body := doneTodo(t, server, item.ID, `{"note":""}`); status != http.StatusConflict {
		t.Errorf("status = %d: %s", status, body)
	}
}

func TestClearOwnerActionRefusesATaskWhoseOpenActionIsOfTheReviewer(t *testing.T) {
	t.Parallel()
	server, _, _ := pausedTask(t, findingAndAction+leadMarks)

	sendChat(t, server, leadChat, "Clear #41")

	waitForChat(t, server, leadChat, "Lead", loginRefusal)
	if state := taskState(t, server); state != "owner_action" {
		t.Errorf("state = %s", state)
	}
	if rows := ownerActionRows(t, server); len(rows) != 1 || rows[0].Done {
		t.Errorf("rows = %+v", rows)
	}
}

func TestMarkOwnerActionRefusesATaskInWorking(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	gate := t.TempDir() + "/go"
	holds := "[[prompts]]\nshell = '''while [ ! -e '" + gate + "' ]; do sleep 0.05; done'''\n"
	server, _ := connectTask(t, fake, leadMarks+leadStarts, holds, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForState(t, server, 41, "working")

	sendChat(t, server, leadChat, "Mark fix round on #41")

	waitForChat(t, server, leadChat, "Lead", "error: The task of #41 is working, not dispatched, checks, approval, ready_for_review or needs_human.")
	if state := taskState(t, server); state != "working" {
		t.Errorf("state = %s", state)
	}
	if rows := ownerActionRows(t, server); len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestMarkOwnerActionRefusesAReviewOfATaskWithNoPullRequestAndATaskInAsked(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadMarks+leadSees, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })

	sendChat(t, server, leadChat, "Mark review on #41")

	waitForChat(t, server, leadChat, "Lead", "error: The task of #41 has no pull request.")

	sendChat(t, server, leadChat, "Ask on #41")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "asked" })
	sendChat(t, server, leadChat, "Mark implementer on #41")

	waitForChat(t, server, leadChat, "Lead", "error: The task of #41 waits for an answer to a question.")
	if state := taskState(t, server); state != "asked" {
		t.Errorf("state = %s", state)
	}
	if rows := ownerActionRows(t, server); len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestMarkOwnerActionOnAStoppedTaskInNeedsHumanDismissesTheStoppedItemAndRemovesTheLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadMarks+leadStarts, fixesNothing, longGrace)
	sha := checksHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	waitForItem(t, server, "stopped")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 41), "mobius:needs-human") })

	sendChat(t, server, leadChat, "Mark fix round on #41")

	waitForItem(t, server, "owner action")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "owner_action" })
	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "stopped")) == 0 })
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:needs-human") })
	if labels := fake.Labels(shop, 42); slices.Contains(labels, "mobius:needs-human") {
		t.Errorf("labels of the pull request = %q", labels)
	}
}
