package engine_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// friday is a Friday at 09:00 local time.
var friday = time.Date(2026, 11, 13, 9, 0, 0, 0, time.Local)

const (
	hourly     = "0 * * * *"
	planCheck  = "Check the plans."
	holdThenOk = "[[prompts]]\n" + holdEvent + "\n[[prompts]]\nreply = [\"Noted.\"]\n[[prompts]]\nreply = [\"Noted.\"]\n[[prompts]]\nreply = [\"Noted.\"]\n"
)

func setNow(server *testserver.Server, t time.Time) {
	server.Engine.SetClock(func() time.Time { return t })
}

type recurringEvent struct {
	ID        int64
	Payload   string
	Delivered bool
}

func recurringEvents(t *testing.T, server *testserver.Server) []recurringEvent {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, event *recurringEvent) error {
		return rows.Scan(&event.ID, &event.Payload, &event.Delivered)
	}, "SELECT id, payload, delivered_at IS NOT NULL FROM lead_events WHERE kind = 'recurring' ORDER BY id")
}

func recurringTasks(t *testing.T, server *testserver.Server) []engine.RecurringTask {
	t.Helper()
	tasks, err := server.Engine.RecurringTasks(t.Context(), shop, 12)
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func addRecurringTask(t *testing.T, server *testserver.Server, expression, text string) engine.RecurringTask {
	t.Helper()
	task, err := server.Engine.AddRecurringTask(t.Context(), shop, 12, expression, text)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func waitForRuns(t *testing.T, server *testserver.Server, runs int) engine.RecurringTask {
	t.Helper()
	return testkit.WaitForValue(t, func() (engine.RecurringTask, bool) {
		tasks := recurringTasks(t, server)
		return tasks[0], len(tasks[0].Runs) == runs
	})
}

func TestTheNextTimeOfACronExpressionIsAfterTheEngineTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)

	times, err := server.Engine.NextTimes("0 8 * * 1-5", 3)

	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{
		time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local),
		time.Date(2026, 11, 17, 8, 0, 0, 0, time.Local),
		time.Date(2026, 11, 18, 8, 0, 0, 0, time.Local),
	}
	if len(times) != len(want) {
		t.Fatalf("times = %v, want %v", times, want)
	}
	for i := range want {
		if !times[i].Equal(want[i]) {
			t.Errorf("time %d = %v, want %v", i, times[i], want[i])
		}
	}
	task := addRecurringTask(t, server, "0 8 * * 1-5", planCheck)
	if !task.Next.Equal(want[0]) {
		t.Errorf("next time of the task = %v, want %v", task.Next, want[0])
	}
}

func TestAWrongCronExpressionGivesAnErrorThatNamesTheExpression(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	for _, test := range []struct{ cron, want string }{
		{"0 8 * *", `the cron expression "0 8 * *" is wrong: expected exactly 5 fields, found 4`},
		{"0 0 8 * * *", `the cron expression "0 0 8 * * *" is wrong: expected exactly 5 fields, found 6`},
		{"@daily", `the cron expression "@daily" is wrong: parser does not accept descriptors: @daily`},
		{"0 8 * * FOO", `the cron expression "0 8 * * FOO" is wrong: `},
		{"CRON_TZ=UTC 0 8 * * *", `the cron expression "CRON_TZ=UTC 0 8 * * *" is wrong: `},
	} {
		_, nextErr := server.Engine.NextTimes(test.cron, 1)
		_, addErr := server.Engine.AddRecurringTask(t.Context(), shop, 12, test.cron, planCheck)
		for _, err := range []error{nextErr, addErr} {
			var wrong *engine.CronError
			if !errors.As(err, &wrong) || !strings.HasPrefix(err.Error(), test.want) {
				t.Errorf("error of %q = %v, want %q", test.cron, err, test.want)
			}
		}
	}
	if tasks := recurringTasks(t, server); len(tasks) != 0 {
		t.Errorf("tasks = %+v", tasks)
	}
}

func TestARecurringTaskGivesOneLeadEventAtItsTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	task := addRecurringTask(t, server, "0 8 * * 1-5", "Check the plans.\nTell me what changed.")

	monday := time.Date(2026, 11, 16, 8, 0, 30, 0, time.Local)
	setNow(server, monday)

	events := testkit.WaitForValue(t, func() ([]recurringEvent, bool) {
		events := recurringEvents(t, server)
		return events, len(events) == 1
	})
	want := monday.UTC().Format("2006-01-02 15:04 UTC") + " recurring task 1 `0 8 * * 1-5`:\n\n> Check the plans.\n> Tell me what changed."
	if events[0].Payload != want {
		t.Errorf("payload = %q, want %q", events[0].Payload, want)
	}
	if task.ID != 1 {
		t.Errorf("task id = %d", task.ID)
	}
	got := waitForRuns(t, server, 1)
	if got.Runs[0].Result != "event" || got.Runs[0].Reason != "" || !got.Runs[0].Time.Equal(monday) {
		t.Errorf("run = %+v", got.Runs[0])
	}
	setNow(server, time.Date(2026, 11, 17, 7, 59, 0, 0, time.Local))
	waitForPolls(t, fake)
	if got := recurringEvents(t, server); len(got) != 1 {
		t.Errorf("events before the following time = %d, want 1", len(got))
	}
	if got := recurringTasks(t, server)[0]; len(got.Runs) != 1 {
		t.Errorf("runs before the following time = %+v", got.Runs)
	}
}

func TestAMobiusThatMissedThreeTimesGivesOneEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	addRecurringTask(t, server, hourly, planCheck)

	setNow(server, friday.Add(3*time.Hour+30*time.Minute))

	waitForRuns(t, server, 1)
	waitForPolls(t, fake)
	if got := recurringEvents(t, server); len(got) != 1 {
		t.Errorf("events = %d, want 1", len(got))
	}
	if got := recurringTasks(t, server)[0]; len(got.Runs) != 1 {
		t.Errorf("runs = %+v", got.Runs)
	}
}

func TestATimeWithAnUndeliveredEarlierEventIsSkipped(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, holdThenOk)
	setNow(server, friday)
	addRecurringTask(t, server, hourly, planCheck)
	setNow(server, friday.Add(time.Hour))
	testkit.WaitFor(t, func() bool {
		events := recurringEvents(t, server)
		return len(events) == 1 && held(t, server, 1)
	})

	setNow(server, friday.Add(2*time.Hour))

	task := waitForRuns(t, server, 2)
	if run := task.Runs[0]; run.Result != "skipped" || run.Reason != "the earlier event is not delivered yet" || !run.Time.Equal(friday.Add(2*time.Hour)) {
		t.Errorf("newest run = %+v", run)
	}
	if got := recurringEvents(t, server); len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}

	sendChat(t, server, leadChat, "Go on.")
	testkit.WaitFor(t, func() bool {
		events := recurringEvents(t, server)
		return len(events) == 1 && events[0].Delivered
	})
	setNow(server, friday.Add(3*time.Hour))

	task = waitForRuns(t, server, 3)
	if run := task.Runs[0]; run.Result != "event" {
		t.Errorf("newest run = %+v", run)
	}
	if got := recurringEvents(t, server); len(got) != 2 {
		t.Errorf("events = %d, want 2", len(got))
	}
}

func TestAPausedRecurringTaskGivesNoEventAndAResumeSkipsThePausedTimes(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	task := addRecurringTask(t, server, hourly, planCheck)
	setNow(server, friday.Add(30*time.Minute))
	paused, err := server.Engine.SetRecurringTaskPaused(t.Context(), shop, 12, task.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !paused.Paused || !paused.Next.IsZero() {
		t.Errorf("paused task = %+v", paused)
	}

	setNow(server, friday.Add(90*time.Minute))
	waitForPolls(t, fake)
	if got := recurringEvents(t, server); len(got) != 0 {
		t.Fatalf("events of the paused task = %d", len(got))
	}
	resumed, err := server.Engine.SetRecurringTaskPaused(t.Context(), shop, 12, task.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Paused || !resumed.Next.Equal(friday.Add(2*time.Hour)) {
		t.Errorf("resumed task = %+v", resumed)
	}

	setNow(server, friday.Add(100*time.Minute))
	waitForPolls(t, fake)
	if got := recurringEvents(t, server); len(got) != 0 {
		t.Fatalf("events after the resume = %d", len(got))
	}
	setNow(server, friday.Add(2*time.Hour))

	got := waitForRuns(t, server, 1)
	if len(recurringEvents(t, server)) != 1 || got.Runs[0].Result != "event" {
		t.Errorf("task = %+v", got)
	}
}

func TestARecurringTaskKeepsTheNewestTwentyTimes(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	addRecurringTask(t, server, hourly, planCheck)

	for hour := 1; hour <= 21; hour++ {
		at := friday.Add(time.Duration(hour) * time.Hour)
		setNow(server, at)
		testkit.WaitFor(t, func() bool {
			runs := recurringTasks(t, server)[0].Runs
			return len(runs) > 0 && runs[0].Time.Equal(at)
		})
	}

	runs := recurringTasks(t, server)[0].Runs
	if len(runs) != 20 {
		t.Fatalf("runs = %d, want 20", len(runs))
	}
	if oldest := runs[19].Time; !oldest.Equal(friday.Add(2 * time.Hour)) {
		t.Errorf("oldest run = %v, want %v", oldest, friday.Add(2*time.Hour))
	}
	var stored int
	if err := server.DB.QueryRow("SELECT count(*) FROM recurring_task_runs").Scan(&stored); err != nil || stored != 20 {
		t.Errorf("stored runs = %d, %v", stored, err)
	}
}

func TestTheCloseOfAWorkstreamDeletesItsRecurringTasks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	addRecurringTask(t, server, hourly, planCheck)
	setNow(server, friday.Add(time.Hour))
	waitForRuns(t, server, 1)

	fake.CloseIssue(shop, 12)

	testkit.WaitFor(t, func() bool { return len(recurringTasks(t, server)) == 0 })
	var stored int
	if err := server.DB.QueryRow("SELECT count(*) FROM recurring_task_runs").Scan(&stored); err != nil || stored != 0 {
		t.Errorf("stored runs = %d, %v", stored, err)
	}
	before := len(recurringEvents(t, server))
	setNow(server, friday.Add(3*time.Hour))
	waitForPolls(t, fake)
	if got := len(recurringEvents(t, server)); got != before {
		t.Errorf("events after the close = %d, want %d", got, before)
	}
}

func TestTheRemovalOfTheWorkstreamLabelDeletesItsRecurringTasks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	addRecurringTask(t, server, hourly, planCheck)
	setNow(server, friday.Add(time.Hour))
	waitForRuns(t, server, 1)

	fake.RemoveLabel(shop, 12, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool { return len(recurringTasks(t, server)) == 0 })
	var stored int
	if err := server.DB.QueryRow("SELECT count(*) FROM recurring_task_runs").Scan(&stored); err != nil || stored != 0 {
		t.Errorf("stored runs = %d, %v", stored, err)
	}
	before := len(recurringEvents(t, server))
	setNow(server, friday.Add(3*time.Hour))
	waitForPolls(t, fake)
	if got := len(recurringEvents(t, server)); got != before {
		t.Errorf("events after the removal = %d, want %d", got, before)
	}
}

func TestARecurringTaskOfAWorkstreamThatLeftTheLocalCopyGivesNoEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	addRecurringTask(t, server, hourly, planCheck)
	copied := func() int {
		var count int
		if err := server.DB.QueryRow("SELECT count(*) FROM copied_workstreams WHERE repository = ?", shop).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	testkit.WaitFor(t, func() bool { return copied() == 1 })

	fake.RemoveRepository(shop)

	testkit.WaitFor(t, func() bool { return copied() == 0 })
	setNow(server, friday.Add(time.Hour))
	server.Engine.RunRecurringTasks(t.Context())
	if got := recurringEvents(t, server); len(got) != 0 {
		t.Errorf("events = %d, want 0", len(got))
	}
	var runs int
	if err := server.DB.QueryRow("SELECT count(*) FROM recurring_task_runs").Scan(&runs); err != nil || runs != 0 {
		t.Errorf("stored runs = %d, %v", runs, err)
	}
}

func TestAddRecurringTaskRefusesAnEmptyTextAndAWorkstreamThatIsNotOpen(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")

	if _, err := server.Engine.AddRecurringTask(t.Context(), shop, 12, hourly, " \n"); !errors.Is(err, engine.ErrEmptyRecurringText) {
		t.Errorf("error of an empty text = %v", err)
	}
	if _, err := server.Engine.AddRecurringTask(t.Context(), shop, 99, hourly, planCheck); !errors.Is(err, engine.ErrNoRecurringWorkstream) {
		t.Errorf("error of an unknown Workstream = %v", err)
	}
	fake.CloseIssue(shop, 12)
	testkit.WaitFor(t, func() bool {
		_, err := server.Engine.AddRecurringTask(t.Context(), shop, 12, hourly, planCheck)
		return errors.Is(err, engine.ErrNoRecurringWorkstream)
	})
	if tasks := recurringTasks(t, server); len(tasks) != 0 {
		t.Errorf("tasks = %+v", tasks)
	}
}

func TestUpdateRecurringTaskChangesTheTextAndMovesTheCheckOnlyForANewCronExpression(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 13, "Other Workstream")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	task := addRecurringTask(t, server, hourly, planCheck)

	setNow(server, friday.Add(90*time.Minute))
	changed, err := server.Engine.UpdateRecurringTask(t.Context(), shop, 12, task.ID, hourly, "Check the prices.")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Text != "Check the prices." || changed.Cron != hourly {
		t.Errorf("changed task = %+v", changed)
	}
	setNow(server, friday.Add(100*time.Minute))
	waitForRuns(t, server, 1)

	other, err := server.Engine.UpdateRecurringTask(t.Context(), shop, 12, task.ID, "30 * * * *", "Check the prices.")
	if err != nil {
		t.Fatal(err)
	}
	if !other.Next.Equal(friday.Add(2*time.Hour + 30*time.Minute)) {
		t.Errorf("next time = %v", other.Next)
	}
	setNow(server, friday.Add(2*time.Hour+10*time.Minute))
	waitForPolls(t, fake)
	if got := recurringTasks(t, server)[0]; len(got.Runs) != 1 {
		t.Errorf("runs before the new time = %+v", got.Runs)
	}

	if _, err := server.Engine.UpdateRecurringTask(t.Context(), shop, 13, task.ID, hourly, planCheck); !errors.Is(err, engine.ErrNoRecurringTask) {
		t.Errorf("error of the id of another Workstream = %v", err)
	}
	if _, err := server.Engine.UpdateRecurringTask(t.Context(), shop, 12, task.ID, "0 8 * *", planCheck); err == nil {
		t.Error("a wrong cron expression changed the task")
	}
	if _, err := server.Engine.UpdateRecurringTask(t.Context(), shop, 12, task.ID, hourly, ""); !errors.Is(err, engine.ErrEmptyRecurringText) {
		t.Errorf("error of an empty text = %v", err)
	}
	if got := recurringTasks(t, server)[0]; got.Cron != "30 * * * *" || got.Text != "Check the prices." {
		t.Errorf("task after the refusals = %+v", got)
	}
}

func TestDeleteRecurringTaskDeletesTheTaskAndItsHistoryOnly(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 13, "Other Workstream")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	first := addRecurringTask(t, server, hourly, planCheck)
	second := addRecurringTask(t, server, hourly, "Check the prices.")
	setNow(server, friday.Add(time.Hour))
	testkit.WaitFor(t, func() bool {
		tasks := recurringTasks(t, server)
		return len(tasks) == 2 && len(tasks[0].Runs) == 1 && len(tasks[1].Runs) == 1
	})

	if err := server.Engine.DeleteRecurringTask(t.Context(), shop, 13, first.ID); !errors.Is(err, engine.ErrNoRecurringTask) {
		t.Errorf("error of the id of another Workstream = %v", err)
	}
	if err := server.Engine.DeleteRecurringTask(t.Context(), shop, 12, first.ID); err != nil {
		t.Fatal(err)
	}

	tasks := recurringTasks(t, server)
	if len(tasks) != 1 || tasks[0].ID != second.ID || len(tasks[0].Runs) != 1 {
		t.Errorf("tasks = %+v", tasks)
	}
	var stored int
	if err := server.DB.QueryRow("SELECT count(*) FROM recurring_task_runs WHERE recurring_task = ?", first.ID).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("stored runs of the deleted task = %d, %v", stored, err)
	}
	if err := server.Engine.DeleteRecurringTask(t.Context(), shop, 12, first.ID); !errors.Is(err, engine.ErrNoRecurringTask) {
		t.Errorf("error of a deleted task = %v", err)
	}
}

func TestSetRecurringTaskPausedRefusesTheIdOfAnotherWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	task := addRecurringTask(t, server, hourly, planCheck)

	_, err := server.Engine.SetRecurringTaskPaused(t.Context(), shop, 13, task.ID, true)

	if !errors.Is(err, engine.ErrNoRecurringTask) {
		t.Errorf("error = %v", err)
	}
	if got := recurringTasks(t, server); len(got) != 1 || got[0].Paused {
		t.Errorf("tasks = %+v", got)
	}
}

func TestAChangeOfARecurringTaskDuringTheStepGivesNoEventAndNoRun(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*testing.T, *testserver.Server, int64){
		"a new cron expression": func(t *testing.T, server *testserver.Server, id int64) {
			if _, err := server.Engine.UpdateRecurringTask(t.Context(), shop, 12, id, "30 * * * *", planCheck); err != nil {
				t.Fatal(err)
			}
		},
		"a pause": func(t *testing.T, server *testserver.Server, id int64) {
			if _, err := server.Engine.SetRecurringTaskPaused(t.Context(), shop, 12, id, true); err != nil {
				t.Fatal(err)
			}
		},
		"a delete": func(t *testing.T, server *testserver.Server, id int64) {
			if err := server.Engine.DeleteRecurringTask(t.Context(), shop, 12, id); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := connect(t, fake, "")
			setNow(server, friday)
			task := addRecurringTask(t, server, hourly, planCheck)

			err := server.Engine.RunStaleRecurringTasks(t.Context(), func() {
				setNow(server, friday.Add(time.Hour))
				change(t, server, task.ID)
			})

			if err != nil {
				t.Fatal(err)
			}
			if got := recurringEvents(t, server); len(got) != 0 {
				t.Errorf("events = %d, want 0", len(got))
			}
			var runs int
			if err := server.DB.QueryRow("SELECT count(*) FROM recurring_task_runs").Scan(&runs); err != nil || runs != 0 {
				t.Errorf("stored runs = %d, %v", runs, err)
			}
		})
	}
}
