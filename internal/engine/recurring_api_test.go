package engine_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	weekdaysText  = "Check the plans.\nTell me what changed."
	weekdaysQuote = "> Check the plans.\n> Tell me what changed."
	tasksPath     = "/api/workstreams/owner/shop/12/recurring-tasks"
)

type apiRecurringTask struct {
	ID     int64      `json:"id"`
	Cron   string     `json:"cron"`
	Text   string     `json:"text"`
	Paused bool       `json:"paused"`
	Next   *time.Time `json:"next"`
	Runs   []struct {
		Time   time.Time `json:"time"`
		Result string    `json:"result"`
		Reason string    `json:"reason"`
	} `json:"runs"`
}

func putPaused(t *testing.T, server *testserver.Server, id int64, paused bool) (int, string) {
	t.Helper()
	body := `{"paused": false}`
	if paused {
		body = `{"paused": true}`
	}
	return send(t, server, http.MethodPut, tasksPath+"/"+itoa(id)+"/paused", body)
}

func recurringChanges(t *testing.T, server *testserver.Server) []leadEvent {
	t.Helper()
	var changes []leadEvent
	for _, event := range leadEvents(t, server) {
		if event.Kind == "recurring_change" {
			changes = append(changes, event)
		}
	}
	return changes
}

func TestAPauseThroughTheAPIGivesOneQuietLeadEventAndTheLeadReadsItInItsNextPrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n", keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the loyalty API")
	waitForChat(t, server, leadChat, "Lead", "Seen")
	waitForLeadPrompts(t, server, 1)
	session := chatSessions(t, server, leadChat, engine.LeadRole)[0]
	setNow(server, friday)
	task := addRecurringTask(t, server, weekdays, weekdaysText)

	status, text := putPaused(t, server, task.ID, true)

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, text)
	}
	want := friday.UTC().Format("2006-01-02 15:04 UTC") + " the Owner paused the recurring task 1 `0 8 * * 1-5`:\n\n" + weekdaysQuote
	events := recurringChanges(t, server)
	if len(events) != 1 || events[0].Payload != want || events[0].Delivered {
		t.Fatalf("recurring_change events = %+v, want one undelivered with %q", events, want)
	}
	waitForPolls(t, fake)
	if got := promptTexts(t, server, session.ID); len(got) != 1 {
		t.Fatalf("the quiet event started a turn: prompts = %q", got)
	}

	if status, text := putPaused(t, server, task.ID, true); status != http.StatusOK {
		t.Fatalf("second status = %d: %s", status, text)
	}

	waitForPolls(t, fake)
	if got := recurringChanges(t, server); len(got) != 1 {
		t.Errorf("recurring_change events after the same state = %+v", got)
	}
	sendChat(t, server, leadChat, "Go on")
	waitForLeadPrompts(t, server, 2)
	if prompt := leadPrompts(t, server)[1]; !strings.Contains(prompt, quietSection(want)) {
		t.Errorf("the prompt has no quiet section with the event: %s", prompt)
	}
}

func TestAResumeAndADeleteThroughTheAPIGiveALeadEventEach(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	task := addRecurringTask(t, server, weekdays, weekdaysText)
	if status, text := putPaused(t, server, task.ID, true); status != http.StatusOK {
		t.Fatalf("pause status = %d: %s", status, text)
	}

	status, text := putPaused(t, server, task.ID, false)
	if status != http.StatusOK {
		t.Fatalf("resume status = %d: %s", status, text)
	}
	status, text = send(t, server, http.MethodDelete, tasksPath+"/"+itoa(task.ID), "")
	if status != http.StatusNoContent || text != "" {
		t.Fatalf("delete status = %d: %s", status, text)
	}

	stamp := friday.UTC().Format("2006-01-02 15:04 UTC")
	var got []string
	for _, event := range recurringChanges(t, server) {
		got = append(got, event.Payload)
	}
	want := []string{
		stamp + " the Owner paused the recurring task 1 `0 8 * * 1-5`:\n\n" + weekdaysQuote,
		stamp + " the Owner resumed the recurring task 1 `0 8 * * 1-5`:\n\n" + weekdaysQuote,
		stamp + " the Owner deleted the recurring task 1 `0 8 * * 1-5`:\n\n" + weekdaysQuote,
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("recurring_change events = %q, want %q", got, want)
	}
	if tasks := recurringTasks(t, server); len(tasks) != 0 {
		t.Errorf("tasks after the delete = %+v", tasks)
	}
}

func TestAPauseOrADeleteOfAnUnknownTaskThroughTheAPIGivesNotFoundAndNoLeadEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	task := addRecurringTask(t, server, weekdays, weekdaysText)

	pauseStatus, _ := putPaused(t, server, task.ID+1, true)
	deleteStatus, _ := send(t, server, http.MethodDelete, tasksPath+"/"+itoa(task.ID+1), "")
	otherStatus, _ := send(t, server, http.MethodPut, "/api/workstreams/owner/shop/13/recurring-tasks/"+itoa(task.ID)+"/paused", `{"paused": true}`)

	for name, status := range map[string]int{"pause": pauseStatus, "delete": deleteStatus, "pause in another Workstream": otherStatus} {
		if status != http.StatusNotFound {
			t.Errorf("%s status = %d, want %d", name, status, http.StatusNotFound)
		}
	}
	if got := recurringChanges(t, server); len(got) != 0 {
		t.Errorf("recurring_change events = %+v", got)
	}
	if tasks := recurringTasks(t, server); len(tasks) != 1 || tasks[0].Paused {
		t.Errorf("tasks = %+v", tasks)
	}
}

func TestASecondPutWithTheSameStateDoesNotMoveTheCheckTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	task := addRecurringTask(t, server, hourly, planCheck)
	if status, text := putPaused(t, server, task.ID, true); status != http.StatusOK {
		t.Fatalf("pause status = %d: %s", status, text)
	}
	setNow(server, friday.Add(5*time.Hour))

	if status, text := putPaused(t, server, task.ID, true); status != http.StatusOK {
		t.Fatalf("second pause status = %d: %s", status, text)
	}
	if status, text := putPaused(t, server, task.ID, false); status != http.StatusOK {
		t.Fatalf("resume status = %d: %s", status, text)
	}

	var checked string
	if err := server.DB.QueryRow("SELECT checked_at FROM recurring_tasks WHERE id = ?", task.ID).Scan(&checked); err != nil {
		t.Fatal(err)
	}
	if want := friday.Add(5 * time.Hour).UTC().Format(time.RFC3339Nano); checked != want {
		t.Errorf("checked_at = %q, want %q", checked, want)
	}
	if got := recurringChanges(t, server); len(got) != 2 {
		t.Errorf("recurring_change events = %+v, want the pause and the resume", got)
	}
}

func TestTheListGivesTheTasksWithNextAndRuns(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	first := addRecurringTask(t, server, hourly, planCheck)
	second := addRecurringTask(t, server, weekdays, weekdaysText)
	setNow(server, friday.Add(time.Hour))
	waitForRuns(t, server, 1)
	if _, err := server.Engine.SetRecurringTaskPaused(t.Context(), shop, 12, second.ID, true); err != nil {
		t.Fatal(err)
	}

	got := apiData[struct {
		Tasks []apiRecurringTask `json:"tasks"`
	}](t, server, tasksPath).Tasks

	if len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("tasks = %+v", got)
	}
	if got[0].Next == nil || !got[0].Next.Equal(friday.Add(2*time.Hour)) || got[0].Paused {
		t.Errorf("first task = %+v", got[0])
	}
	if len(got[0].Runs) != 1 || got[0].Runs[0].Result != "event" || !got[0].Runs[0].Time.Equal(friday.Add(time.Hour)) {
		t.Errorf("runs of the first task = %+v", got[0].Runs)
	}
	if got[1].Next != nil || !got[1].Paused || got[1].Runs == nil || len(got[1].Runs) != 0 {
		t.Errorf("second task = %+v", got[1])
	}
	_, text := send(t, server, http.MethodGet, tasksPath, "")
	if !strings.Contains(text, `"next":null`) || !strings.Contains(text, `"runs":[]`) {
		t.Errorf("body = %s", text)
	}
}

func TestAPauseThroughTheAPIPublishesTheRecurringTasksChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	task := addRecurringTask(t, server, weekdays, weekdaysText)
	changes, stop := server.Engine.Listen()
	defer stop()

	putPaused(t, server, task.ID, true)

	change := waitForChange(t, changes, func(change engine.Change) bool { return change.RecurringTasks != nil }).RecurringTasks
	if change.Repository != shop || change.Number != 12 {
		t.Errorf("change = %+v", change)
	}
}

func TestANewHistoryRowOfTheEventStepPublishesTheRecurringTasksChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	setNow(server, friday)
	addRecurringTask(t, server, hourly, planCheck)
	changes, stop := server.Engine.Listen()
	defer stop()

	setNow(server, friday.Add(time.Hour))

	change := waitForChange(t, changes, func(change engine.Change) bool { return change.RecurringTasks != nil }).RecurringTasks
	if change.Repository != shop || change.Number != 12 {
		t.Errorf("change = %+v", change)
	}
	if got := waitForRuns(t, server, 1); got.Runs[0].Result != "event" {
		t.Errorf("run = %+v", got.Runs[0])
	}
}

func TestTheLeadToolsOfRecurringTasksPublishTheChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	changes, stop := server.Engine.Listen()
	defer stop()
	isChange := func(change engine.Change) bool { return change.RecurringTasks != nil }

	task, err := server.Engine.AddRecurringTask(t.Context(), shop, 12, weekdays, weekdaysText)
	if err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changes, isChange)
	if _, err := server.Engine.UpdateRecurringTask(t.Context(), shop, 12, task.ID, hourly, planCheck); err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changes, isChange)
	if err := server.Engine.DeleteRecurringTask(t.Context(), shop, 12, task.ID); err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changes, isChange)
}

func TestTheListOfAWorkstreamWithNoTaskIsAnEmptyArray(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")

	status, text := send(t, server, http.MethodGet, tasksPath, "")

	var body struct {
		Data struct {
			Tasks []json.RawMessage `json:"tasks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil || body.Data.Tasks == nil || len(body.Data.Tasks) != 0 {
		t.Errorf("status %d, %v: %s", status, err, text)
	}
}
