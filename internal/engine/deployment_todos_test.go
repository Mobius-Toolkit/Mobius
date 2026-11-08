package engine_test

import (
	"database/sql"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	// mandatoryNeed is a Reviewer that reports one mandatory env var.
	mandatoryNeed = "[[prompts]]\nwhen = \"You are the Reviewer\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"env_var\", name = \"PLAN_API_KEY\", path = \"plan.txt\", line = 1, reason = \"The code reads it at start.\", mandatory = true, after_merge = false }] } }\n\n"
	// optionalNeed is a Reviewer that reports one optional config value.
	optionalNeed = "[[prompts]]\nwhen = \"You are the Reviewer\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"config\", name = \"plan.currency\", path = \"plan.txt\", line = 2, reason = \"The code has a default.\", mandatory = false, after_merge = false }] } }\n\n"
	// mandatoryNeedInRoundTwo is a Reviewer that reports plan.currency as mandatory for the issue body of the second round.
	mandatoryNeedInRoundTwo = "[[prompts]]\nwhen = \"Plans have a price. Round two.\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"config\", name = \"plan.currency\", path = \"plan.txt\", line = 2, reason = \"The code needs it at start.\", mandatory = true, after_merge = false }] } }\n\n"
	// afterMergeNeed is a Reviewer that reports one mandatory config key that a person can add only after the merge.
	afterMergeNeed = "[[prompts]]\nwhen = \"You are the Reviewer\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"config\", name = \"roles.lead.context\", path = \"plan.txt\", line = 3, reason = \"An older release refuses the key.\", mandatory = true, after_merge = true }] } }\n\n"
	// afterMergeInRoundTwo is a Reviewer that reports PLAN_API_KEY as an after-merge need for the issue body of the second round.
	afterMergeInRoundTwo = "[[prompts]]\nwhen = \"Plans have a price. Round two.\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"env_var\", name = \"PLAN_API_KEY\", path = \"plan.txt\", line = 1, reason = \"Add it after the upgrade.\", mandatory = true, after_merge = true }] } }\n\n"
	// noNeedInRoundTwo is a Reviewer that reports no need for the issue body of the second round.
	noNeedInRoundTwo = "[[prompts]]\nwhen = \"Plans have a price. Round two.\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [] } }\n\n"
	blockedText      = "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY. Mobius merges it after the Owner closes the open todos."
	workstreamTitle  = "Integrate loyalty plans"
	openTodosReason  = "Mobius merges after you close the open todos."
	// optionalAfterMergeNeed is a Reviewer that reports one optional config value that a person can add only after the merge.
	optionalAfterMergeNeed = "[[prompts]]\nwhen = \"You are the Reviewer\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"config\", name = \"plan.currency\", path = \"plan.txt\", line = 2, reason = \"An older release refuses the key.\", mandatory = false, after_merge = true }] } }\n\n"
)

func draft(fake *testkit.FakeGitHub) bool {
	return fake.PullRequests(shop)[0].Draft
}

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

	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY, plan.currency. The Owner got it for review. The pull request stays a draft until the Owner closes the open todos.")
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
	if approvals(fake) != 0 || autoMerge(t, server) != 0 || fake.MergeCalls() != 0 || len(itemsOf(t, server, "todo")) != 1 || !draft(fake) {
		t.Errorf("approvals = %d, merge calls = %d, draft = %v", approvals(fake), fake.MergeCalls(), draft(fake))
	}

	if status, body := dismissItem(t, server, second.ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if draft(fake) {
		t.Error("the pull request stays a draft after the last todo closed")
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
	if ready.Merging || ready.MergeRefusal != openTodosReason || approvals(fake) != 0 || autoMerge(t, server) != 0 || !draft(fake) {
		t.Errorf("ready = %+v, approvals = %d, draft = %v", ready, approvals(fake), draft(fake))
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
	if len(itemsOf(t, server, "todo")) != 0 || approvals(fake) != 1 || autoMerge(t, server) != 1 || draft(fake) {
		t.Errorf("approvals = %d, draft = %v", approvals(fake), draft(fake))
	}
	fake.SetCheckRunStatus(running, "completed", "success")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d", calls)
	}
}

func TestTheDoneWriteOfATodoAddsOneRecordToTheLeadChatThatIsNotUnread(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, mandatoryNeed)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY. The Owner got it for review. The pull request stays a draft until the Owner closes the open todos.")
	todo := waitForItem(t, server, "todo")
	unread, err := server.Engine.UnreadChats(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if status, body := doneTodo(t, server, todo.ID, `{"note":"Set in prod."}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForRecords(t, server, leadChat, "You set the Autopilot level to Off.", `You marked the todo "PLAN_API_KEY" of PR #42 as done.`)
	if got, err := server.Engine.UnreadChats(t.Context()); err != nil || !reflect.DeepEqual(got, unread) {
		t.Errorf("unread = %+v, %v, want %+v", got, err, unread)
	}
}

func TestAFailedReadOfThePullRequestAtTheDoneWriteKeepsTheTodoOpenSoTheOwnerCanMarkItAsDoneAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, mandatoryNeed)
	fake.AddCheckRun(shop, checkRun("CI", head(t, fake, "mobius/41"), "in_progress", ""))
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", blockedText)
	todo := waitForItem(t, server, "todo")
	fake.FailPullRequestReads(true)

	if status, body := doneTodo(t, server, todo.ID, `{"note":"Set in prod."}`); status == http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if len(itemsOf(t, server, "todo")) != 1 || approvals(fake) != 0 || autoMerge(t, server) != 0 {
		t.Errorf("todos = %d, approvals = %d", len(itemsOf(t, server, "todo")), approvals(fake))
	}
	fake.FailPullRequestReads(false)

	if status, body := doneTodo(t, server, todo.ID, `{"note":"Set in prod."}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].Merging && items[0].MergeRefusal == ""
	})
	if len(itemsOf(t, server, "todo")) != 0 || approvals(fake) != 1 || autoMerge(t, server) != 1 {
		t.Errorf("approvals = %d", approvals(fake))
	}
}

func TestWithAMergeLevelDismissOfTheLastOptionalTodoMergesThePullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, optionalNeed)

	sendChat(t, server, leadChat, "Approve #41")

	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: plan.currency. Mobius merges it after the Owner closes the open todos.")
	todo := waitForItem(t, server, "todo")
	waitForPolls(t, fake)
	if todo.Reason != "optional · "+workstreamTitle || fake.MergeCalls() != 0 || approvals(fake) != 0 || !draft(fake) {
		t.Errorf("todo = %+v, merge calls = %d, approvals = %d, draft = %v", todo, fake.MergeCalls(), approvals(fake), draft(fake))
	}

	if status, body := dismissItem(t, server, todo.ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if fake.MergeCalls() != 1 || approvals(fake) != 1 {
		t.Errorf("merge calls = %d, approvals = %d", fake.MergeCalls(), approvals(fake))
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

// TestASecondApprovalOfTheSameTaskAddsNoSecondTodoForTheSameNeed checks that the approval again after the update of the
// branch adds no second todo for the same need, and that the task still merges.
func TestASecondApprovalOfTheSameTaskAddsNoSecondTodoForTheSameNeed(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, afterMergeNeed)
	old := head(t, fake, "mobius/41")
	fake.CommitFile(shop, "price.txt", "cents\n", "Add price")
	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if head(t, fake, "mobius/41") == old {
		t.Fatal("the head did not change")
	}
	if got := approvalAgainComments(fake); got != 1 {
		t.Errorf("comments of the approval again = %d, want 1", got)
	}
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
		return len(items) == 1 && items[0].MergeRefusal == openTodosReason
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

// optionalTodoOfAMandatoryNeed gives a server with a task whose todo row is optional and whose current need of the same
// kind and name is mandatory, and gives the todo item of the first approval. The Autopilot level is off. With
// dismissFirst, the Owner dismisses that todo before the second review.
func optionalTodoOfAMandatoryNeed(t *testing.T, fake *testkit.FakeGitHub, dismissFirst bool) (*testserver.Server, inboxItem) {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fixRound := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo fixed > fix.txt && git add fix.txt && git commit -q -m Fix\"\n\n"
	server, _ := connectTask(t, fake, mandatoryNeedInRoundTwo+optionalNeed+leadApproves+leadFindings+leadStarts, fixRound+commits, keepSessionOpen)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "off")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "off" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	todo := waitForItem(t, server, "todo")
	if dismissFirst {
		if status, body := dismissItem(t, server, todo.ID); status != http.StatusNoContent {
			t.Fatalf("status = %d: %s", status, body)
		}
	}
	fake.SetBody(shop, 41, "Plans have a price. Round two.")
	sendChat(t, server, leadChat, "Send the findings to #41")
	waitForReadyEvents(t, fake, server, 2)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool {
		rows := todoRows(t, server)
		return taskState(t, server) == "ready_for_review" && len(rows) == 1 && rows[0].Mandatory == 1 && !rows[0].DismissedAt.Valid
	})
	if needs := deploymentNeeds(t, server); len(needs) != 1 || needs[0].Mandatory != 1 || len(itemsOf(t, server, "todo")) != 1 {
		t.Fatalf("needs = %+v", needs)
	}
	return server, todo
}

func TestAnOptionalTodoOfANeedThatTheReviewerLaterReportsAsMandatoryContinuesTheMergeWhenItIsDone(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, todo := optionalTodoOfAMandatoryNeed(t, fake, false)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].MergeRefusal == openTodosReason
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

func TestAMandatoryNeedOfAFixRoundEndsTheApprovalThatMobiusAlreadyHas(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fixRound := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo fixed > fix.txt && git add fix.txt && git commit -q -m Fix\"\n\n"
	server, _ := connectTask(t, fake, mandatoryNeedInRoundTwo+leadApproves+leadFindings+leadStarts, fixRound+commits, keepSessionOpen)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "merge")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "merge" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	fake.AddCheckRun(shop, checkRun("CI", head(t, fake, "mobius/41"), "in_progress", ""))
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool { return autoMerge(t, server) == 1 })
	fake.SetBody(shop, 41, "Plans have a price. Round two.")
	sendChat(t, server, leadChat, "Send the findings to #41")
	waitForReadyEvents(t, fake, server, 2)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].MergeRefusal == openTodosReason
	})
	waitForPolls(t, fake)
	if taskState(t, server) != "ready_for_review" || autoMerge(t, server) != 0 || fake.MergeCalls() != 0 || len(itemsOf(t, server, "todo")) != 1 {
		t.Errorf("state = %s, merge calls = %d", taskState(t, server), fake.MergeCalls())
	}
}

func todoTimes(t *testing.T, server *testserver.Server, name string) (dismissedAt, doneAt sql.NullString) {
	t.Helper()
	if err := server.DB.QueryRow("SELECT dismissed_at, done_at FROM deployment_todos WHERE name = ?", name).Scan(&dismissedAt, &doneAt); err != nil {
		t.Fatal(err)
	}
	return dismissedAt, doneAt
}

func dismissItem(t *testing.T, server *testserver.Server, id int64) (int, string) {
	t.Helper()
	return send(t, server, http.MethodPost, "/api/inbox/"+itoa(id)+"/dismiss", "")
}

func TestTheOwnerDismissesAnOptionalTodoAndTheTodoRowKeepsTheTimeOfTheDismissal(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, optionalNeed)
	sendChat(t, server, leadChat, "Approve #41")
	todo := waitForItem(t, server, "todo")
	if todo.Todo == nil || !todo.Todo.Dismissable {
		t.Fatalf("todo = %+v", todo.Todo)
	}

	if status, body := dismissItem(t, server, todo.ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	dismissedAt, doneAt := todoTimes(t, server, "plan.currency")
	if !dismissedAt.Valid || doneAt.Valid {
		t.Errorf("dismissed_at = %v, done_at = %v", dismissedAt, doneAt)
	}
	if got := itemsOf(t, server, "todo"); len(got) != 0 {
		t.Errorf("todo items = %+v", got)
	}
	waitForRecords(t, server, leadChat, "You set the Autopilot level to Off.", `You dismissed the todo "plan.currency" of PR #42.`)
}

func TestMobiusRefusesToDismissAMandatoryTodoAndTheTodoAndItsItemStayOpen(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, mandatoryNeed)
	sendChat(t, server, leadChat, "Approve #41")
	todo := waitForItem(t, server, "todo")
	if todo.Todo == nil || todo.Todo.Dismissable {
		t.Fatalf("todo = %+v", todo.Todo)
	}

	status, body := dismissItem(t, server, todo.ID)

	if status != http.StatusConflict || !strings.Contains(body, "A mandatory todo cannot be dismissed.") {
		t.Errorf("status = %d: %s", status, body)
	}
	dismissedAt, doneAt := todoTimes(t, server, "PLAN_API_KEY")
	if dismissedAt.Valid || doneAt.Valid {
		t.Errorf("dismissed_at = %v, done_at = %v", dismissedAt, doneAt)
	}
	if got := itemsOf(t, server, "todo"); len(got) != 1 || got[0].DismissedAt != nil {
		t.Errorf("todo items = %+v", got)
	}
}

func TestTheCurrentNeedListDecidesIfTheOwnerCanDismissATodo(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, todo := optionalTodoOfAMandatoryNeed(t, fake, false)
	if rows := todoRows(t, server); len(rows) != 1 || rows[0].Mandatory != 1 {
		t.Fatalf("rows = %+v", rows)
	}

	status, body := dismissItem(t, server, todo.ID)

	if status != http.StatusConflict {
		t.Errorf("status = %d: %s", status, body)
	}
	if dismissedAt, _ := todoTimes(t, server, "plan.currency"); dismissedAt.Valid {
		t.Errorf("dismissed_at = %v", dismissedAt)
	}
	got := itemsOf(t, server, "todo")
	if len(got) != 1 || got[0].DismissedAt != nil || got[0].Todo == nil || got[0].Todo.Dismissable || !got[0].Todo.Mandatory || got[0].ID != todo.ID ||
		!strings.HasPrefix(got[0].Reason, "mandatory") {
		t.Errorf("todo items = %+v", got)
	}
}

func TestTheOwnerDismissesAnOptionalTodoOfAnEndedTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, optionalAfterMergeNeed)
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	todo := waitForItem(t, server, "todo")

	if status, body := dismissItem(t, server, todo.ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if dismissedAt, doneAt := todoTimes(t, server, "plan.currency"); !dismissedAt.Valid || doneAt.Valid {
		t.Errorf("dismissed_at = %v, done_at = %v", dismissedAt, doneAt)
	}
	if got := itemsOf(t, server, "todo"); len(got) != 0 {
		t.Errorf("todo items = %+v", got)
	}
}

func TestTheOwnerDismissesAnItemOfAnotherKindWithTheRecordOfTheTitle(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, optionalNeed)
	sendChat(t, server, leadChat, "Approve #41")
	ready := waitForItem(t, server, "ready for review")

	if status, body := dismissItem(t, server, ready.ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if got := itemsOf(t, server, "ready for review"); len(got) != 0 {
		t.Errorf("ready items = %+v", got)
	}
	if dismissedAt, doneAt := todoTimes(t, server, "plan.currency"); dismissedAt.Valid || doneAt.Valid {
		t.Errorf("dismissed_at = %v, done_at = %v", dismissedAt, doneAt)
	}
	waitForRecords(t, server, leadChat, "You set the Autopilot level to Off.", `You dismissed "`+ready.Title+`".`)
}

func TestTheReviewerMarksANeedAsAfterMergeAndTheNeedRowAndTheTodoRowKeepTheMark(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, afterMergeNeed)

	sendChat(t, server, leadChat, "Approve #41")

	todo := waitForItem(t, server, "todo")
	needs := deploymentNeeds(t, server)
	if len(needs) != 1 || needs[0].Mandatory != 1 || needs[0].AfterMerge != 1 {
		t.Errorf("needs = %+v", needs)
	}
	if rows := todoRows(t, server); len(rows) != 1 || rows[0].Mandatory != 1 || rows[0].AfterMerge != 1 {
		t.Errorf("rows = %+v", rows)
	}
	if todo.Todo == nil || !todo.Todo.Mandatory || !todo.Todo.AfterMerge || todo.Todo.Dismissable {
		t.Errorf("todo = %+v", todo.Todo)
	}
}

func TestAMandatoryNeedThatIsAfterMergeDoesNotStopTheAutomaticMergeAndItsTodoStaysOpenAfterTheMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, afterMergeNeed)

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if fake.MergeCalls() != 1 || approvals(fake) != 1 || autoMerge(t, server) != 1 {
		t.Errorf("merge calls = %d, approvals = %d", fake.MergeCalls(), approvals(fake))
	}
	todo := waitForItem(t, server, "todo")
	if todo.DismissedAt != nil || todo.Todo == nil || !todo.Todo.AfterMerge {
		t.Fatalf("todo = %+v", todo)
	}

	if status, body := doneTodo(t, server, todo.ID, `{"note":"Added."}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if got := itemsOf(t, server, "todo"); len(got) != 0 {
		t.Errorf("todo items = %+v", got)
	}
	if dismissedAt, doneAt := todoTimes(t, server, "roles.lead.context"); dismissedAt.Valid || !doneAt.Valid {
		t.Errorf("dismissed_at = %v, done_at = %v", dismissedAt, doneAt)
	}
}

// todoBeforeAFixRound gives a server with a task whose first review gave a todo item, and that the Lead sent to a fix
// round. It waits for the second review and the new approval state of the task. The check of the first head is open.
func todoBeforeAFixRound(t *testing.T, fake *testkit.FakeGitHub, level, reviewer string) (*testserver.Server, inboxItem) {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fixRound := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo fixed > fix.txt && git add fix.txt && git commit -q -m Fix\"\n\n"
	server, _ := connectTask(t, fake, reviewer+leadApproves+leadFindings+leadStarts, fixRound+commits, keepSessionOpen)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, level)
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == level })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	fake.AddCheckRun(shop, checkRun("CI", head(t, fake, "mobius/41"), "in_progress", ""))
	sendChat(t, server, leadChat, "Approve #41")
	first := waitForItem(t, server, "todo")
	waitForPolls(t, fake)
	if autoMerge(t, server) != 0 {
		t.Fatalf("auto merge = %d", autoMerge(t, server))
	}
	fake.SetBody(shop, 41, "Plans have a price. Round two.")
	sendChat(t, server, leadChat, "Send the findings to #41")
	waitForReadyEvents(t, fake, server, 2)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	return server, first
}

func TestALaterReviewThatMarksTheNeedOfAnOpenMandatoryTodoAsAfterMergeUnblocksTheAutomaticMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, first := todoBeforeAFixRound(t, fake, "merge", afterMergeInRoundTwo+mandatoryNeed)
	if first.Todo == nil || !first.Todo.Mandatory || first.Todo.AfterMerge {
		t.Fatalf("first = %+v", first.Todo)
	}

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return autoMerge(t, server) == 1 })
	testkit.WaitFor(t, func() bool {
		items := itemsOf(t, server, "ready for review")
		return len(items) == 1 && items[0].Merging && items[0].MergeRefusal == ""
	})
	todos := itemsOf(t, server, "todo")
	if len(todos) != 1 || todos[0].DismissedAt != nil || todos[0].Todo == nil || !todos[0].Todo.Mandatory || !todos[0].Todo.AfterMerge {
		t.Errorf("todos = %+v", todos)
	}
	if rows := todoRows(t, server); len(rows) != 1 || rows[0].AfterMerge != 0 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestATodoWhoseNeedTheReviewerLaterRemovedKeepsTheAfterMergeMarkOfItsRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, first := todoBeforeAFixRound(t, fake, "off", noNeedInRoundTwo+afterMergeNeed)
	if first.Todo == nil || !first.Todo.AfterMerge {
		t.Fatalf("first = %+v", first.Todo)
	}

	if needs := deploymentNeeds(t, server); len(needs) != 0 {
		t.Fatalf("needs = %+v", needs)
	}

	todos := itemsOf(t, server, "todo")
	if len(todos) != 1 || todos[0].DismissedAt != nil || todos[0].Todo == nil || !todos[0].Todo.AfterMerge {
		t.Errorf("todos = %+v", todos)
	}
	if rows := todoRows(t, server); len(rows) != 1 || rows[0].AfterMerge != 1 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestAnApprovalWithAnOpenTodoKeepsThePullRequestADraftAndTheTaskIsReadyForReview(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, reviewer string }{{"mandatory", mandatoryNeed}, {"optional", optionalNeed}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := approvableWith(t, fake, "off", true, test.reviewer)
			fake.SetDraft(shop, 42, false)

			sendChat(t, server, leadChat, "Approve #41")

			waitForItem(t, server, "ready for review")
			testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" && draft(fake) })
			if len(itemsOf(t, server, "todo")) != 1 {
				t.Errorf("todo items = %+v", itemsOf(t, server, "todo"))
			}
		})
	}
}

func TestAnApprovalWithOnlyAnAfterMergeNeedMakesThePullRequestReady(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, afterMergeNeed)

	sendChat(t, server, leadChat, "Approve #41")

	waitForItem(t, server, "ready for review")
	waitForItem(t, server, "todo")
	if draft(fake) {
		t.Error("the pull request is a draft")
	}
}

func TestANewNeedOfALaterReviewMakesAReadyPullRequestADraftAtTheApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := todoBeforeAFixRound(t, fake, "off", mandatoryNeedInRoundTwo+afterMergeNeed)
	fake.SetDraft(shop, 42, false)

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" && draft(fake) })
}

func TestAReviewThatRemovesTheLastBlockingNeedMakesThePullRequestReadyAtTheNextApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := todoBeforeAFixRound(t, fake, "off", noNeedInRoundTwo+mandatoryNeed)
	if !draft(fake) {
		t.Fatal("the pull request is ready for review with an open mandatory todo")
	}

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" && !draft(fake) })
}

func TestWithNoMergeLevelCompleteOfTheLastMandatoryTodoMakesThePullRequestReadyAndMergeWorks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, ready := readyToMerge(t, fake, mandatoryNeed)
	todo := waitForItem(t, server, "todo")

	status, body := merge(t, server, ready.ID)

	if status != http.StatusConflict || !strings.Contains(body, "Pull request #42 has open todos. Close them first.") {
		t.Errorf("status = %d: %s", status, body)
	}
	if approvals(fake) != 0 || !draft(fake) {
		t.Errorf("approvals = %d, draft = %v", approvals(fake), draft(fake))
	}

	if status, body := doneTodo(t, server, todo.ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if draft(fake) {
		t.Error("the pull request is a draft after the last todo closed")
	}
	if status, body := merge(t, server, ready.ID); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if fake.MergeCalls() != 1 {
		t.Errorf("merge calls = %d", fake.MergeCalls())
	}
}

func TestWithNoMergeLevelDismissOfTheLastOptionalTodoMakesThePullRequestReady(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := readyToMerge(t, fake, optionalNeed)
	todo := waitForItem(t, server, "todo")
	if !draft(fake) {
		t.Fatal("the pull request is ready for review with an open optional todo")
	}

	if status, body := dismissItem(t, server, todo.ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if draft(fake) {
		t.Error("the pull request is a draft after the last todo closed")
	}
}

func TestAFailureOfMarkReadyForReviewKeepsTheTodoItemOpenAndASecondCompleteMakesThePullRequestReady(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := readyToMerge(t, fake, mandatoryNeed)
	todo := waitForItem(t, server, "todo")
	fake.FailMarkReadyForReview(true)

	if status, body := doneTodo(t, server, todo.ID, `{"note":""}`); status == http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if len(itemsOf(t, server, "todo")) != 1 || !draft(fake) {
		t.Errorf("todo items = %d, draft = %v", len(itemsOf(t, server, "todo")), draft(fake))
	}
	fake.FailMarkReadyForReview(false)

	if status, body := doneTodo(t, server, todo.ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if len(itemsOf(t, server, "todo")) != 0 || draft(fake) {
		t.Errorf("todo items = %d, draft = %v", len(itemsOf(t, server, "todo")), draft(fake))
	}
}

func TestAPullRequestWithABlockingTodoAndAnApprovalOfTheOwnerDoesNotMergeEvenWhenAPersonMarkedItReady(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := readyToMerge(t, fake, mandatoryNeed)
	todo := waitForItem(t, server, "todo")
	fake.AddReview(shop, 42, "owner", "APPROVED", "")
	fake.SetDraft(shop, 42, false)

	waitForPolls(t, fake)

	if fake.MergeCalls() != 0 || taskState(t, server) != "ready_for_review" || draft(fake) {
		t.Errorf("merge calls = %d, state = %s, draft = %v", fake.MergeCalls(), taskState(t, server), draft(fake))
	}

	if status, body := doneTodo(t, server, todo.ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	if fake.MergeCalls() != 1 {
		t.Errorf("merge calls = %d", fake.MergeCalls())
	}
}

func TestADismissedOptionalTodoWhoseNeedTurnsMandatoryGetsANewInboxItemThatBlocksTheMergeAndHasNoDismiss(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, old := optionalTodoOfAMandatoryNeed(t, fake, true)

	todos := itemsOf(t, server, "todo")

	if len(todos) != 1 || todos[0].ID == old.ID || todos[0].DismissedAt != nil || todos[0].Todo == nil || !todos[0].Todo.Mandatory || todos[0].Todo.Dismissable ||
		!strings.HasPrefix(todos[0].Reason, "mandatory") {
		t.Fatalf("todos = %+v", todos)
	}
	rows := todoRows(t, server)
	if len(rows) != 1 || rows[0].Mandatory != 1 || rows[0].DismissedAt.Valid || rows[0].InboxItem != todos[0].ID {
		t.Errorf("rows = %+v", rows)
	}
	if status, body := dismissItem(t, server, todos[0].ID); status != http.StatusConflict {
		t.Errorf("dismiss: status = %d: %s", status, body)
	}
	ready := waitForItem(t, server, "ready for review")
	if status, body := merge(t, server, ready.ID); status != http.StatusConflict || !strings.Contains(body, "has open todos") {
		t.Errorf("merge: status = %d: %s", status, body)
	}
	if approvals(fake) != 0 || !draft(fake) {
		t.Errorf("approvals = %d, draft = %v", approvals(fake), draft(fake))
	}
}
