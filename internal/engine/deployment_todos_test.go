package engine_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	// mandatoryNeed is a Reviewer that reports one mandatory env var.
	mandatoryNeed = "[[prompts]]\nwhen = \"You are the Reviewer\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"env_var\", name = \"PLAN_API_KEY\", path = \"plan.txt\", line = 1, reason = \"The code reads it at start.\", mandatory = true }] } }\n\n"
	// optionalNeed is a Reviewer that reports one optional config value.
	optionalNeed = "[[prompts]]\nwhen = \"You are the Reviewer\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"config\", name = \"plan.currency\", path = \"plan.txt\", line = 2, reason = \"The code has a default.\", mandatory = false }] } }\n\n"
	// mandatoryNeedInRoundTwo is a Reviewer that reports plan.currency as mandatory for the issue body of the second round.
	mandatoryNeedInRoundTwo = "[[prompts]]\nwhen = \"Plans have a price. Round two.\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"config\", name = \"plan.currency\", path = \"plan.txt\", line = 2, reason = \"The code needs it at start.\", mandatory = true }] } }\n\n"
	blockedText     = "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY. Mobius merges it after the Owner marks the mandatory todos as done."
	workstreamTitle = "Integrate loyalty plans"
)

func todoRows(t *testing.T, server *testserver.Server) []store.DeploymentTodo {
	t.Helper()
	todos, err := store.New(server.DB).ListDeploymentTodos(t.Context(), liveTaskOf(t, server, 41).ID)
	if err != nil {
		t.Fatal(err)
	}
	return todos
}

func doneTodo(t *testing.T, server *testserver.Server, id int64, body string) (int, string) {
	t.Helper()
	return send(t, server, http.MethodPost, "/api/inbox/"+itoa(id)+"/done", body)
}

func TestWithoutAMergeLevelEachDeploymentNeedGivesATodoAndTheOwnerGetsNoApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, twoNeeds)

	sendChat(t, server, leadChat, "Approve #41")

	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY, plan.currency. The Owner got it for review.")
	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "todo")) == 2 })
	todos := itemsOf(t, server, "todo")
	first, second := todos[0], todos[1]
	if first.Title != "PLAN_API_KEY" || first.Reason != "mandatory · "+workstreamTitle || first.PullRequest != 42 || first.Issue != 41 ||
		first.Text != "env_var: PLAN_API_KEY\nThe code reads it at start.\nplan.txt:1" || first.Link == "" {
		t.Errorf("first = %+v", first)
	}
	if second.Title != "plan.currency" || second.Reason != "optional · "+workstreamTitle || second.Text != "config: plan.currency\nThe code has a default.\nplan.txt:2" {
		t.Errorf("second = %+v", second)
	}
	if first.Todo == nil || !first.Todo.Mandatory || first.Todo.Path != "plan.txt" || first.Todo.Line != 1 || second.Todo == nil || second.Todo.Mandatory {
		t.Errorf("todos = %+v, %+v", first.Todo, second.Todo)
	}

	if status, body := doneTodo(t, server, first.ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForPolls(t, fake)
	if approvals(fake) != 0 || autoMerge(t, server) != 0 || fake.MergeCalls() != 0 || len(itemsOf(t, server, "todo")) != 1 {
		t.Errorf("approvals = %d, merge calls = %d", approvals(fake), fake.MergeCalls())
	}
}

func TestWithAMergeLevelAMandatoryTodoBlocksTheMergeUntilTheOwnerMarksItAsDone(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, mandatoryNeed)
	running := fake.AddCheckRun(shop, checkRun("CI", head(t, fake, "mobius/41"), "in_progress", ""))

	sendChat(t, server, leadChat, "Approve #41")

	waitForChat(t, server, leadChat, "Lead", blockedText)
	todo := waitForItem(t, server, "todo")
	ready := waitForItem(t, server, "ready for review")
	waitForPolls(t, fake)
	if ready.Merging || ready.MergeRefusal != "Mobius merges after the mandatory todos are done." || approvals(fake) != 0 || autoMerge(t, server) != 0 {
		t.Errorf("ready = %+v, approvals = %d", ready, approvals(fake))
	}
	if task := boardTaskOf(t, server, "ready_for_review"); task.Merging || task.MergeRefusal != ready.MergeRefusal {
		t.Errorf("task = %+v", task)
	}

	status, body := doneTodo(t, server, todo.ID, `{"note":"Set in prod."}`)

	if status != http.StatusOK || !strings.Contains(body, `"dismissedAt":"`) || !strings.Contains(body, `"note":"Set in prod."`) {
		t.Fatalf("status = %d: %s", status, body)
	}
	rows := todoRows(t, server)
	if len(rows) != 1 || rows[0].Note != "Set in prod." || !rows[0].DoneAt.Valid || rows[0].InboxItem != todo.ID {
		t.Errorf("rows = %+v", rows)
	}
	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].Merging && items[0].MergeRefusal == ""
	})
	if len(itemsOf(t, server, "todo")) != 0 || approvals(fake) != 1 || autoMerge(t, server) != 1 {
		t.Errorf("approvals = %d", approvals(fake))
	}
	fake.SetCheckRunStatus(running, "completed", "success")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d", calls)
	}
}

func TestWithAMergeLevelAnOptionalNeedGivesATodoAndMobiusMerges(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, optionalNeed)

	sendChat(t, server, leadChat, "Approve #41")

	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: plan.currency. Mobius merges it after its checks pass.")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	todos := itemsOf(t, server, "todo")
	if len(todos) != 1 || todos[0].Reason != "optional · "+workstreamTitle || fake.MergeCalls() != 1 || approvals(fake) != 1 {
		t.Errorf("todos = %+v, merge calls = %d", todos, fake.MergeCalls())
	}
}

func TestWithAMergeLevelAndNoNeedThereIsNoTodoAndMobiusMerges(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "merge", true)

	sendChat(t, server, leadChat, "Approve #41")

	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. Mobius merges it after its checks pass.")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if todos := itemsOf(t, server, "todo"); len(todos) != 0 || fake.MergeCalls() != 1 {
		t.Errorf("todos = %+v, merge calls = %d", todos, fake.MergeCalls())
	}
}

func TestASecondApprovalOfTheSameTaskAddsNoSecondTodoForTheSameNeed(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, optionalNeed)
	old := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool { return readyEvents(t, server) == 2 && taskState(t, server) == "approval" })
	if head(t, fake, "mobius/41") == old {
		t.Fatal("the head did not change")
	}

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM deployment_todos").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if todos := itemsOf(t, server, "todo"); len(todos) != 1 || count != 1 {
		t.Errorf("todos = %+v, rows = %d", todos, count)
	}
}

func TestAChangeOfTheLevelToOffRemovesTheReasonOfTheMandatoryTodos(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, mandatoryNeed)
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].MergeRefusal == "Mobius merges after the mandatory todos are done."
	})

	setWorkstreamAutopilot(t, server, "off")

	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].MergeRefusal == ""
	})
	if item := itemsOf(t, server, "ready for review")[0]; item.Merging || approvals(fake) != 0 {
		t.Errorf("item = %+v, approvals = %d", item, approvals(fake))
	}
}

func TestDoneInboxItemRefusesAnUnknownItemAndAnItemThatIsNotATodo(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "off", true)
	sendChat(t, server, leadChat, "Approve #41")
	ready := waitForItem(t, server, "ready for review")

	if status, body := doneTodo(t, server, 9999, `{"note":""}`); status != http.StatusNotFound {
		t.Errorf("unknown item: status = %d: %s", status, body)
	}
	if status, body := doneTodo(t, server, ready.ID, `{"note":""}`); status != http.StatusConflict {
		t.Errorf("ready for review item: status = %d: %s", status, body)
	}
}

func TestAnOptionalTodoOfANeedThatTheReviewerLaterReportsAsMandatoryContinuesTheMergeWhenItIsDone(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fixRound := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo fixed > fix.txt && git add fix.txt && git commit -q -m Fix\"\n\n"
	server, _ := connectTask(t, fake, mandatoryNeedInRoundTwo+optionalNeed+leadApproves+leadFindings+leadStarts, fixRound+commits, keepSessionOpen)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "off")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "off" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	todo := waitForItem(t, server, "todo")
	fake.SetBody(shop, 41, "Plans have a price. Round two.")
	sendChat(t, server, leadChat, "Send the findings to #41")
	waitForReadyEvents(t, server, 2)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	if needs := deploymentNeeds(t, server); len(needs) != 1 || needs[0].Mandatory != 1 || len(itemsOf(t, server, "todo")) != 1 {
		t.Fatalf("needs = %+v", needs)
	}
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].MergeRefusal == "Mobius merges after the mandatory todos are done."
	})

	if status, body := doneTodo(t, server, todo.ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].Merging && items[0].MergeRefusal == ""
	})
	if approvals(fake) != 1 || autoMerge(t, server) != 1 {
		t.Errorf("approvals = %d", approvals(fake))
	}
}
