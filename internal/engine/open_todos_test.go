package engine_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	// threeNeeds is a Reviewer that reports a mandatory env var, an optional config value and an optional after-merge config value.
	threeNeeds = "[[prompts]]\nwhen = \"You are the Reviewer\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"env_var\", name = \"PLAN_API_KEY\", path = \"plan.txt\", line = 1, reason = \"The code reads it at start.\", mandatory = true, after_merge = false }, " +
		"{ kind = \"config\", name = \"plan.currency\", path = \"plan.txt\", line = 2, reason = \"The code has a default.\", mandatory = false, after_merge = false }, " +
		"{ kind = \"config\", name = \"plan.region\", path = \"plan.txt\", line = 3, reason = \"An older release refuses the key.\", mandatory = false, after_merge = true }] } }\n\n"
	todosClosedText = "The Owner closed the last open todo of the Workstream. All tasks of the Workstream are closed."
)

// openTodosOf gives the section "# Open todos" of the prompt, without its heading.
func openTodosOf(t *testing.T, prompt string) string {
	t.Helper()
	_, section, found := strings.Cut(prompt, "# Open todos\n\n")
	if !found {
		t.Fatalf("no section Open todos in %s", prompt)
	}
	section, _, _ = strings.Cut(section, "\n\n")
	return section
}

func todosClosedEvents(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	rows, err := server.DB.Query("SELECT payload FROM lead_events WHERE repository = ? AND workstream = 12 AND kind = 'todos_closed' AND issue IS NULL ORDER BY id", shop)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var payloads []string
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return payloads
}

func todoItemsByTitle(t *testing.T, server *testserver.Server, count int) map[string]inboxItem {
	t.Helper()
	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "todo")) == count })
	items := make(map[string]inboxItem)
	for _, item := range itemsOf(t, server, "todo") {
		items[item.Title] = item
	}
	return items
}

func TestTheFirstPromptOfALeadSessionListsTheOpenTodosOfTheWorkstreamAndNotTheDoneOrDismissedTodos(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableAdjusting(t, fake, "off", true, threeNeeds, noChange)
	sendChat(t, server, leadChat, "Approve #41")
	todos := todoItemsByTitle(t, server, 3)
	if status, body := doneTodo(t, server, todos["PLAN_API_KEY"].ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if status, body := dismissItem(t, server, todos["plan.currency"].ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool {
		sessions := chatSessions(t, server, leadChat, engine.LeadRole)
		return len(sessions) > 0 && sessions[len(sessions)-1].EndedAt.Valid
	})
	first := len(chatSessions(t, server, leadChat, engine.LeadRole))

	sendChat(t, server, leadChat, "Which todos are open?")

	prompt := promptTexts(t, server, endedChatSession(t, server, first).ID)[0]
	if !strings.Contains(prompt, "# Brief") {
		t.Fatalf("prompt = %s", prompt)
	}
	if got := openTodosOf(t, prompt); got != "- plan.region (optional, after merge), PR #42" {
		t.Errorf("open todos = %q", got)
	}
	inOrder(t, prompt, "# Task list\n\n", "# Open todos\n\n", "# Recurring tasks\n\n")
}

func TestAFirstPromptOfAWorkstreamWithNoOpenTodoSaysSo(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, "")

	prompt := waitForLeadPrompt(t, server, "# Brief")

	if got := openTodosOf(t, prompt); got != "No todo is open." {
		t.Errorf("open todos = %q", got)
	}
}

func TestALeadPromptForALaterMessageOfTheSameSessionListsATodoThatMobiusAddedAfterTheFirstPrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, mandatoryNeed)
	first := waitForLeadPrompt(t, server, "# Brief")
	sendChat(t, server, leadChat, "Approve #41")
	waitForItem(t, server, "todo")

	sendChat(t, server, leadChat, "Which todos are open?")

	later := waitForLeadPrompt(t, server, "Which todos are open?")
	if strings.Contains(later, "# Brief") {
		t.Fatalf("later prompt is a first prompt: %s", later)
	}
	if got := openTodosOf(t, first); got != "No todo is open." {
		t.Errorf("first open todos = %q", got)
	}
	if got := openTodosOf(t, later); got != "- PLAN_API_KEY (mandatory), PR #42" {
		t.Errorf("later open todos = %q", got)
	}
}

func TestTheCloseOfTheLastTodoOfAWorkstreamWithAllTasksClosedAddsOneTodosClosedEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, twoNeeds)
	sendChat(t, server, leadChat, "Approve #41")
	todos := todoItemsByTitle(t, server, 2)
	fake.CloseIssue(shop, 41)
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })

	if status, body := doneTodo(t, server, todos["PLAN_API_KEY"].ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if events := todosClosedEvents(t, server); len(events) != 0 {
		t.Fatalf("events after the first todo = %q", events)
	}
	if status, body := dismissItem(t, server, todos["plan.currency"].ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	if events := todosClosedEvents(t, server); len(events) != 1 || events[0] != todosClosedText {
		t.Errorf("events = %q", events)
	}
}

func TestTheCloseOfTheLastTodoOfAWorkstreamWithAnOpenSubIssueAddsNoTodosClosedEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, twoNeeds)
	sendChat(t, server, leadChat, "Approve #41")
	todos := todoItemsByTitle(t, server, 2)
	if status, body := doneTodo(t, server, todos["PLAN_API_KEY"].ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if status, body := dismissItem(t, server, todos["plan.currency"].ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForRecords(t, server, leadChat, "You set the Autopilot level to Off.", `You marked the todo "PLAN_API_KEY" of PR #42 as done.`, `You dismissed the todo "plan.currency" of PR #42.`)
	if workstreams(t, server)[0].AllTasksClosed || len(itemsOf(t, server, "todo")) != 0 {
		t.Fatalf("workstreams = %+v", workstreams(t, server))
	}
	if events := todosClosedEvents(t, server); len(events) != 0 {
		t.Errorf("events = %q", events)
	}
}
