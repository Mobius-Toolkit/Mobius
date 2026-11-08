package engine_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const weekdays = "0 8 * * 1-5"

func callRecurring(when, tool, arguments string) string {
	return fmt.Sprintf("[[prompts]]\nwhen = %q\ncall = { tool = %q, arguments = %s }\n\n", when, tool, arguments)
}

func callCreateRecurring(when, expression, text string) string {
	return callRecurring(when, "create_recurring_task", fmt.Sprintf("{ cron = %q, text = %q }", expression, text))
}

func callUpdateRecurring(when string, id int, expression, text string) string {
	return callRecurring(when, "update_recurring_task", fmt.Sprintf("{ id = %d, cron = %q, text = %q }", id, expression, text))
}

func callDeleteRecurring(when string, id int) string {
	return callRecurring(when, "delete_recurring_task", fmt.Sprintf("{ id = %d }", id))
}

// weekdayTimes gives the next 3 times of weekdays at 08:00 after friday, as the title of an approval shows them.
func weekdayTimes() string {
	var texts []string
	for _, day := range []int{16, 17, 18} {
		texts = append(texts, time.Date(2026, 11, day, 8, 0, 0, 0, time.Local).Format("Mon 2 Jan 15:04 MST"))
	}
	return strings.Join(texts, ", ")
}

func utcText(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func TestTheLeadCallsCreateRecurringTaskAndTheTaskExistsOnlyAfterTheOwnerApproves(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callCreateRecurring("Add it.", weekdays, "Check the plans.")+seenScript)
	setNow(server, friday)

	session := run(t, server, leadSpec(t), "Add it.")

	if got := reply(t, server, session); !strings.HasPrefix(got, actionWaits) {
		t.Errorf("reply = %q", got)
	}
	action := waitForActions(t, server, 1)[0]
	waitForPolls(t, fake)
	items := itemsOf(t, server, "approval")
	if len(items) != 1 || items[0].Title != "Add a recurring task: next "+weekdayTimes() || items[0].Workstream != 12 ||
		items[0].Text != "text: Check the plans.\ncron: "+weekdays {
		t.Errorf("items = %+v", items)
	}
	if tasks := recurringTasks(t, server); action.State != "pending" || len(tasks) != 0 {
		t.Errorf("action = %+v, tasks = %+v", action, tasks)
	}

	done := decided(t, server, action.ID, "approve", "")

	wantResult := "Added the recurring task 1. Next time: " + utcText(time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local)) + "."
	if done.State != "done" || done.Result != wantResult {
		t.Errorf("action = %+v, want result %q", done, wantResult)
	}
	if tasks := recurringTasks(t, server); len(tasks) != 1 || tasks[0].Cron != weekdays || tasks[0].Text != "Check the plans." {
		t.Errorf("tasks = %+v", tasks)
	}
}

func TestTheLeadChangesAndDeletesARecurringTaskAfterTheOwnerApproves(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callUpdateRecurring("Change it.", 1, weekdays, "Check the invoices.")+callDeleteRecurring("Delete it.", 1)+seenScript)
	setNow(server, friday)
	addRecurringTask(t, server, hourly, planCheck)

	run(t, server, leadSpec(t), "Change it.", "Delete it.")

	rows := waitForActions(t, server, 2)
	waitForPolls(t, fake)
	items := itemsOf(t, server, "approval")
	if len(items) != 2 || items[0].Title != "Change the recurring task 1: next "+weekdayTimes() || items[1].Title != "Delete the recurring task 1" {
		t.Errorf("items = %+v", items)
	}
	if tasks := recurringTasks(t, server); len(tasks) != 1 || tasks[0].Cron != hourly || tasks[0].Text != planCheck {
		t.Errorf("tasks before the approval = %+v", tasks)
	}

	changed := decided(t, server, rows[0].ID, "approve", "")

	wantResult := "Changed the recurring task 1. Next time: " + utcText(time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local)) + "."
	if changed.State != "done" || changed.Result != wantResult {
		t.Errorf("changed = %+v, want result %q", changed, wantResult)
	}
	if tasks := recurringTasks(t, server); len(tasks) != 1 || tasks[0].Cron != weekdays || tasks[0].Text != "Check the invoices." {
		t.Errorf("tasks after the change = %+v", tasks)
	}

	deleted := decided(t, server, rows[1].ID, "approve", "")

	if deleted.State != "done" || deleted.Result != "Deleted the recurring task 1." {
		t.Errorf("deleted = %+v", deleted)
	}
	if tasks := recurringTasks(t, server); len(tasks) != 0 {
		t.Errorf("tasks after the delete = %+v", tasks)
	}
}

func TestACreateRecurringTaskCallWithAWrongCronExpressionOrNoTextAddsNoPendingAction(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, callCreateRecurring("Add 1.", "every day", planCheck)+callCreateRecurring("Add 2.", hourly, " ")+callUpdateRecurring("Change it.", 1, "61 * * * *", planCheck)+seenScript)

	session := run(t, server, leadSpec(t), "Add 1.", "Add 2.", "Change it.")

	calls := mcpCalls(t, server, session)
	if len(calls) != 3 {
		t.Fatalf("calls = %v", calls)
	}
	for i, expression := range map[int]string{0: "every day", 2: "61 * * * *"} {
		if got, _ := calls[i]["error"].(string); !strings.HasPrefix(got, fmt.Sprintf("the cron expression %q is wrong: ", expression)) {
			t.Errorf("error of call %d = %v", i, calls[i]["error"])
		}
	}
	if calls[1]["error"] != engine.ErrEmptyRecurringText.Error() {
		t.Errorf("error of the empty text = %v", calls[1]["error"])
	}
	if rows := actionRows(t, server); len(rows) != 0 {
		t.Errorf("actions = %+v", rows)
	}
	if items := itemsOf(t, server, "approval"); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}

func TestAnApprovedUpdateOfARecurringTaskOfAnotherWorkstreamOrOfADeletedTaskChangesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBilling(fake)
	server, _ := connect(t, fake, approvalSeen+callUpdateRecurring("Change 1.", 1, weekdays, "Changed.")+callUpdateRecurring("Change 2.", 2, weekdays, "Changed.")+seenScript)
	setNow(server, friday)
	var billing engine.RecurringTask
	testkit.WaitFor(t, func() bool {
		task, err := server.Engine.AddRecurringTask(t.Context(), shop, 20, hourly, "Send the invoices.")
		billing = task
		return err == nil
	})
	mine := addRecurringTask(t, server, hourly, planCheck)
	if billing.ID != 1 || mine.ID != 2 {
		t.Fatalf("billing = %+v, mine = %+v", billing, mine)
	}
	run(t, server, leadSpec(t), "Change 1.", "Change 2.")
	rows := waitForActions(t, server, 2)
	if err := server.Engine.DeleteRecurringTask(t.Context(), shop, 12, mine.ID); err != nil {
		t.Fatal(err)
	}

	other := decided(t, server, rows[0].ID, "approve", "")
	deleted := decided(t, server, rows[1].ID, "approve", "")

	for _, action := range []actionJSON{other, deleted} {
		if action.State != "pending" || action.Error != engine.ErrNoRecurringTask.Error() {
			t.Errorf("action = %+v", action)
		}
	}
	tasks, err := server.Engine.RecurringTasks(t.Context(), shop, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Cron != hourly || tasks[0].Text != "Send the invoices." {
		t.Errorf("tasks of the other Workstream = %+v", tasks)
	}
	if tasks := recurringTasks(t, server); len(tasks) != 0 {
		t.Errorf("tasks = %+v", tasks)
	}
	want := "and it failed: " + engine.ErrNoRecurringTask.Error() + ". The action still waits for the approval of the Owner."
	testkit.WaitFor(t, func() bool {
		return strings.Count(strings.Join(leadPrompts(t, server), ""), want) == 2
	})
}

func TestTwoCreateRecurringTaskCallsGiveTwoWaitingActions(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callCreateRecurring("Add 1.", weekdays, planCheck)+callCreateRecurring("Add 2.", weekdays, planCheck)+seenScript)
	setNow(server, friday)

	session := run(t, server, leadSpec(t), "Add 1.", "Add 2.")

	rows := waitForActions(t, server, 2)
	if rows[0].State != "pending" || rows[1].State != "pending" {
		t.Errorf("rows = %+v", rows)
	}
	title := "Add a recurring task: next " + weekdayTimes()
	calls := mcpCalls(t, server, session)
	if len(calls) != 2 || calls[0]["result"] != actionWaitsText(title, rows[0].InboxItem.Int64) || calls[1]["result"] != actionWaitsText(title, rows[1].InboxItem.Int64) {
		t.Errorf("calls = %v", calls)
	}
	decided(t, server, rows[0].ID, "approve", "")
	decided(t, server, rows[1].ID, "approve", "")
	if tasks := recurringTasks(t, server); len(tasks) != 2 {
		t.Errorf("tasks = %+v", tasks)
	}
}

func TestANewUpdateOrDeleteRecurringTaskCallReplacesTheWaitingActionOfTheSameToolAndID(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	script := approvalSeen +
		callUpdateRecurring("Change 1.", 1, weekdays, "First.") +
		callUpdateRecurring("Change 2.", 1, weekdays, "Second.") +
		callUpdateRecurring("Change 3.", 2, weekdays, "Third.") +
		callDeleteRecurring("Delete 1.", 1) +
		callDeleteRecurring("Delete 2.", 1) +
		seenScript
	server, _ := connect(t, fake, script)
	setNow(server, friday)

	session := run(t, server, leadSpec(t), "Change 1.", "Change 2.", "Change 3.", "Delete 1.", "Delete 2.")

	rows := waitForActions(t, server, 5)
	if got := []string{rows[0].State, rows[1].State, rows[2].State, rows[3].State, rows[4].State}; !slices.Equal(got, []string{"replaced", "pending", "pending", "replaced", "pending"}) {
		t.Errorf("states = %v", got)
	}
	changeTitle := "Change the recurring task 1: next " + weekdayTimes()
	deleteTitle := "Delete the recurring task 1"
	calls := mcpCalls(t, server, session)
	if len(calls) != 5 ||
		calls[1]["result"] != actionWaitsText(changeTitle, rows[1].InboxItem.Int64)+replacesAction(changeTitle, rows[0].InboxItem.Int64) ||
		calls[4]["result"] != actionWaitsText(deleteTitle, rows[4].InboxItem.Int64)+replacesAction(deleteTitle, rows[3].InboxItem.Int64) {
		t.Errorf("calls = %v", calls)
	}
}

func TestTheFirstPromptOfTheLeadListsTheRecurringTasks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, seenScript)
	setNow(server, friday)
	addRecurringTask(t, server, weekdays, "Check the plans.\nTell me what changed.")
	paused := addRecurringTask(t, server, hourly, "Count the shops.")
	if _, err := server.Engine.SetRecurringTaskPaused(t.Context(), shop, 12, paused.ID, true); err != nil {
		t.Fatal(err)
	}
	monday := time.Date(2026, 11, 16, 8, 0, 30, 0, time.Local)
	setNow(server, monday)
	waitForRuns(t, server, 1)
	endedChatSession(t, server, 0)

	sendChat(t, server, leadChat, "What recurs?")

	prompt := promptTexts(t, server, endedChatSession(t, server, 1).ID)[0]
	inOrder(t, prompt,
		"# Task list\n\n",
		fmt.Sprintf("# Recurring tasks\n\n- 1: `%s`, next %s, last %s event, %q\n- 2: `%s`, paused, no time yet, %q\n\n",
			weekdays, utcText(time.Date(2026, 11, 17, 8, 0, 0, 0, time.Local)), utcText(monday), "Check the plans.\nTell me what changed.", hourly, "Count the shops."),
		"# Chat history\n\n",
	)
}
