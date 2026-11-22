package engine_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func callCreateRecurringIn(when string, project int) string {
	return callRecurring(when, "create_recurring_task", fmt.Sprintf("{ cron = %q, text = %q, project = %d }", weekdays, planCheck, project))
}

func callUpdateRecurringIn(when string, id, project int) string {
	return callRecurring(when, "update_recurring_task", fmt.Sprintf("{ id = %d, cron = %q, text = %q, project = %d }", id, weekdays, planCheck, project))
}

// addProject adds the Project number below the Workstream owner/shop#12 on the fake GitHub, and waits until the local
// copy has it.
func addProject(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, number int64, title string) {
	t.Helper()
	fake.AddIssue(shop, number, title)
	fake.AddLabel(shop, number, "mobius:project", "owner")
	fake.AddSubIssue(shop, 12, number)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = ? AND kind = 'project' AND workstream IS NOT NULL", shop, number) == 1
	})
}

func TestTheLeadCreatesARecurringTaskInAProjectAndTheApprovalNamesTheProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callCreateRecurringIn("Add it.", 41)+seenScript)
	setNow(server, friday)
	addProject(t, server, fake, 41, "Stats display")

	run(t, server, leadSpec(t), "Add it.")

	action := waitForActions(t, server, 1)[0]
	waitForPolls(t, fake)
	items := itemsOf(t, server, "approval")
	if want := "Add a recurring task in the Project #41 Stats display: next " + weekdayTimes(); len(items) != 1 || items[0].Title != want {
		t.Errorf("items = %+v, want title %q", items, want)
	}
	if tasks := recurringTasks(t, server); len(tasks) != 0 {
		t.Errorf("tasks before the approval = %+v", tasks)
	}

	decided(t, server, action.ID, "approve", "")

	tasks := recurringTasks(t, server)
	if len(tasks) != 1 || tasks[0].Project != 41 || tasks[0].ProjectTitle != "Stats display" {
		t.Errorf("tasks = %+v", tasks)
	}
}

func TestAnUpdateOfARecurringTaskSetsItsProjectAndAnUpdateWithNoProjectClearsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+callUpdateRecurringIn("Change 1.", 1, 41)+callUpdateRecurring("Change 2.", 2, weekdays, planCheck)+seenScript)
	setNow(server, friday)
	addProject(t, server, fake, 41, "Stats display")
	addRecurringTask(t, server, hourly, planCheck)
	addRecurringTaskIn(t, server, hourly, planCheck, 41)

	run(t, server, leadSpec(t), "Change 1.", "Change 2.")

	rows := waitForActions(t, server, 2)
	waitForPolls(t, fake)
	items := itemsOf(t, server, "approval")
	if len(items) != 2 || items[0].Title != "Change the recurring task 1 in the Project #41 Stats display: next "+weekdayTimes() || items[1].Title != "Change the recurring task 2: next "+weekdayTimes() {
		t.Errorf("items = %+v", items)
	}
	decided(t, server, rows[0].ID, "approve", "")
	decided(t, server, rows[1].ID, "approve", "")

	tasks := recurringTasks(t, server)
	if len(tasks) != 2 || tasks[0].Project != 41 || tasks[0].ProjectTitle != "Stats display" || tasks[1].Project != 0 || tasks[1].ProjectTitle != "" {
		t.Errorf("tasks = %+v", tasks)
	}
}

func TestARecurringTaskToolCallWithANumberThatIsNotAnOpenProjectOfTheWorkstreamAddsNoPendingAction(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, callCreateRecurringIn("Add 1.", 43)+callCreateRecurringIn("Add 2.", 99)+
		callUpdateRecurringIn("Change 1.", 1, 42)+callCreateRecurringIn("Add 3.", 12)+
		callCreateRecurringIn("Add 4.", 51)+callCreateRecurringIn("Add 5.", -1)+seenScript)
	setNow(server, friday)
	addRecurringTask(t, server, hourly, planCheck)
	addProject(t, server, fake, 42, "Old display")
	fake.CloseIssue(shop, 42)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 42 AND state = 'closed'", shop) == 1
	})
	fake.AddIssue(shop, 43, "Show the counters")
	fake.AddSubIssue(shop, 12, 43)
	fake.AddIssue(shop, 13, "Other Workstream")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 51, "Other project")
	fake.AddLabel(shop, 51, "mobius:project", "owner")
	fake.AddSubIssue(shop, 13, 51)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number IN (43, 51) AND workstream IS NOT NULL", shop) == 2
	})

	session := run(t, server, leadSpec(t), "Add 1.", "Add 2.", "Change 1.", "Add 3.", "Add 4.", "Add 5.")

	calls := mcpCalls(t, server, session)
	want := []string{
		"owner/shop#43 is not an open Project of this Workstream.",
		"owner/shop#99 is not an open Project of this Workstream.",
		"owner/shop#42 is not an open Project of this Workstream.",
		"owner/shop#12 is not an open Project of this Workstream.",
		"owner/shop#51 is not an open Project of this Workstream.",
		"project must be 1 or more.",
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v", calls)
	}
	for i, message := range want {
		if calls[i]["error"] != message {
			t.Errorf("call %d error = %v, want %q", i, calls[i]["error"], message)
		}
	}
	if rows := actionRows(t, server); len(rows) != 0 {
		t.Errorf("actions = %+v", rows)
	}
	if items := itemsOf(t, server, "approval"); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}

func TestTheLeadEventOfARunOfARecurringTaskInAProjectNamesTheProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	addProject(t, server, fake, 41, "Stats display")
	addRecurringTaskIn(t, server, "0 8 * * 1-5", planCheck, 41)

	monday := time.Date(2026, 11, 16, 8, 0, 30, 0, time.Local)
	setNow(server, monday)

	events := testkit.WaitForValue(t, func() ([]recurringEvent, bool) {
		events := recurringEvents(t, server)
		return events, len(events) == 1
	})
	want := monday.UTC().Format("2006-01-02 15:04 UTC") + " recurring task 1 `0 8 * * 1-5` in the Project #41 Stats display:\n\n> Check the plans."
	if events[0].Payload != want {
		t.Errorf("payload = %q, want %q", events[0].Payload, want)
	}
}

func TestTheLeadEventOfARunNowOfARecurringTaskInAProjectNamesTheProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Noted.\"]\n")
	setNow(server, friday)
	addProject(t, server, fake, 41, "Stats display")
	task := addRecurringTaskIn(t, server, weekdays, planCheck, 41)

	status, text := postRun(t, server, task.ID)

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, text)
	}
	want := friday.UTC().Format("2006-01-02 15:04 UTC") + " the Owner started the recurring task 1 `0 8 * * 1-5` in the Project #41 Stats display by hand:\n\n> Check the plans."
	if events := recurringEvents(t, server); len(events) != 1 || events[0].Payload != want {
		t.Errorf("recurring events = %+v, want one with %q", events, want)
	}
}

func TestCloseProjectRefusesAProjectWithARecurringTaskAlsoAPausedOne(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, closeProject(41, "Done."))
	setNow(server, friday)
	addProject(t, server, fake, 41, "Stats display")
	addRecurringTaskIn(t, server, hourly, planCheck, 41)
	paused := addRecurringTaskIn(t, server, hourly, "Count the shops.", 41)
	addRecurringTask(t, server, hourly, "Other work.")
	if _, err := server.Engine.SetRecurringTaskPaused(t.Context(), shop, 12, paused.ID, true); err != nil {
		t.Fatal(err)
	}

	session := run(t, server, leadSpec(t), "Close 41")

	calls := mcpCalls(t, server, session)
	want := "owner/shop#41 has recurring tasks: 1 \"Check the plans.\", 2 \"Count the shops.\"."
	if len(calls) != 1 || calls[0]["error"] != want {
		t.Fatalf("calls = %+v, want error %q", calls, want)
	}
	if state, _ := fake.State(shop, 41); state != "open" || len(fake.Comments(shop, 41)) != 0 {
		t.Errorf("state = %s, comments = %+v", state, fake.Comments(shop, 41))
	}
}

func TestTheFirstPromptOfTheLeadNamesTheProjectOfARecurringTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, seenScript)
	setNow(server, friday)
	addProject(t, server, fake, 41, "Stats display")
	addRecurringTaskIn(t, server, hourly, "Count the shops.", 41)

	sendChat(t, server, leadChat, "What recurs?")

	prompt := promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]
	want := fmt.Sprintf("# Recurring tasks\n\n- 1: `%s`, next %s, no time yet, Project #41 Stats display, %q\n\n", hourly, utcText(time.Date(2026, 11, 13, 10, 0, 0, 0, time.Local)), "Count the shops.")
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt = %s, want %q", prompt, want)
	}
}

func TestTheListGivesTheProjectNumberAndTitleOfARecurringTaskAndNullForATaskWithNoProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	addProject(t, server, fake, 41, "Stats display")
	addRecurringTaskIn(t, server, hourly, planCheck, 41)
	addRecurringTask(t, server, hourly, "Count the shops.")

	got := apiData[struct {
		Tasks []struct {
			Project      *int64  `json:"project"`
			ProjectTitle *string `json:"projectTitle"`
		} `json:"tasks"`
	}](t, server, tasksPath).Tasks

	if len(got) != 2 || got[0].Project == nil || *got[0].Project != 41 || got[0].ProjectTitle == nil || *got[0].ProjectTitle != "Stats display" || got[1].Project != nil || got[1].ProjectTitle != nil {
		t.Errorf("tasks = %+v", got)
	}
	_, text := send(t, server, http.MethodGet, tasksPath, "")
	if !strings.Contains(text, `"project":null`) || !strings.Contains(text, `"projectTitle":null`) {
		t.Errorf("body = %s", text)
	}
}
