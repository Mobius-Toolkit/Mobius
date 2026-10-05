package engine_test

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type workstream struct {
	Repository     string `json:"repository"`
	Number         int64  `json:"number"`
	Title          string `json:"title"`
	Brief          string `json:"brief"`
	Autopilot      bool   `json:"autopilot"`
	AllTasksClosed bool   `json:"allTasksClosed"`
}

type activity struct {
	ID         int64  `json:"id"`
	Repository string `json:"repository"`
	Workstream int64  `json:"workstream"`
	Issue      int64  `json:"issue"`
	Actor      string `json:"actor"`
	Text       string `json:"text"`
	Link       string `json:"link"`
}

// send sends a request with the JSON body to the API path, and gives the status and the body of the response.
func send(t *testing.T, server *testserver.Server, method, path, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	text, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(text)
}

func workstreams(t *testing.T, server *testserver.Server) []workstream {
	t.Helper()
	status, text := send(t, server, http.MethodGet, "/api/workstreams", "")
	var body struct {
		Data []workstream `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil {
		t.Fatalf("workstreams: status %d, %v: %s", status, err, text)
	}
	return body.Data
}

func complete(t *testing.T, server *testserver.Server, number int64) (int, string) {
	t.Helper()
	return send(t, server, http.MethodPost, "/api/workstreams/owner/shop/"+itoa(number)+"/complete", "")
}

func activities(t *testing.T, server *testserver.Server) []activity {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, a *activity) error {
		return rows.Scan(&a.ID, &a.Repository, &a.Workstream, &a.Issue, &a.Actor, &a.Text, &a.Link)
	}, "SELECT id, repository, workstream, issue, actor, text, link FROM events ORDER BY id")
}

// liveEvents gives the data of each event name of the live event stream.
func liveEvents(t *testing.T, server *testserver.Server, name string) <-chan string {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	found := make(chan string, 100)
	go func() {
		events := bufio.NewReader(response.Body)
		for {
			event, data, err := nextEvent(events)
			if err != nil {
				return
			}
			if event == name {
				found <- data
			}
		}
	}()
	return found
}

// nextActivity waits for the next activity of the live event stream. The test fails after one minute.
func nextActivity(t *testing.T, events <-chan string) activity {
	t.Helper()
	select {
	case data := <-events:
		var found activity
		if err := json.Unmarshal([]byte(data), &found); err != nil {
			t.Fatal(err)
		}
		return found
	case <-time.After(time.Minute):
		t.Fatal("no activity after one minute")
	}
	return activity{}
}

// addWorkstream adds the Workstream #12 with the task #41.
func addWorkstream(fake *testkit.FakeGitHub) {
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
}

func TestAWorkstreamLabelShowsTheIssueInTheWorkstreamList(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddIssue(shop, 13, "Fix the footer")
	server := startCopied(t, fake)

	fake.AddLabel(shop, 12, "mobius:workstream", "owner")

	list := testkit.WaitForValue(t, func() ([]workstream, bool) {
		list := workstreams(t, server)
		return list, len(list) > 0
	})
	if want := []workstream{{Repository: shop, Number: 12, Title: "Integrate loyalty plans"}}; !reflect.DeepEqual(list, want) {
		t.Errorf("workstreams = %+v", list)
	}
}

func TestANewWorkstreamAddsOneActivityThatGoesLive(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddIssue(shop, 13, "Price rounding")
	server := startCopied(t, fake)
	events := liveEvents(t, server, "activity")

	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	first := nextActivity(t, events)
	fake.AddLabel(shop, 13, "mobius:workstream", "Owner")
	second := nextActivity(t, events)

	want := activity{ID: first.ID, Repository: shop, Workstream: 12, Issue: 12, Actor: "owner", Text: `New Workstream "Integrate loyalty plans"`, Link: "https://github.com/owner/shop/issues/12"}
	if first != want {
		t.Errorf("first = %+v", first)
	}
	if second.Issue != 13 {
		t.Errorf("second = %+v", second)
	}
	if got := activities(t, server); !reflect.DeepEqual(got, []activity{first, second}) {
		t.Errorf("activities = %+v", got)
	}
}

func TestAWorkstreamLabelFromAnUntrustedAuthorAddsNoActivity(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Mine the servers")
	fake.AddIssue(shop, 13, "Integrate loyalty plans")
	server := startCopied(t, fake)
	events := liveEvents(t, server, "activity")

	fake.AddLabel(shop, 12, "mobius:workstream", "mallory")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")

	if got := nextActivity(t, events); got.Issue != 13 {
		t.Errorf("activity = %+v", got)
	}
}

// The first poll cannot see which event is new, so the Lead gets no creation event.
func TestAWorkstreamLabelFromBeforeTheFirstPollAddsAnActivity(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	server := startCopied(t, fake)

	if got := nextActivity(t, liveEvents(t, server, "activity")); got.Issue != 12 {
		t.Errorf("activity = %+v", got)
	}
	if got := leadEvents(t, server); len(got) != 0 {
		t.Errorf("Lead events = %+v", got)
	}
}

func TestANewWorkstreamAddsACreationEventForTheLead(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 13, "Plan prices")
	fake.SetBody(shop, 13, "Price each plan.\nRound to cents.")
	server := startCopied(t, fake)

	fake.AddLabel(shop, 13, "mobius:workstream", "owner")

	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := leadEvents(t, server)
		return events, len(events) > 0
	})
	if len(events) != 1 || events[0].Workstream != 13 || events[0].Kind != "creation" ||
		!strings.HasSuffix(events[0].Payload, " creation of Workstream #13 \"Plan prices\" by @owner:\n\n> Price each plan.\n> Round to cents.") {
		t.Errorf("Lead events = %+v", events)
	}
}

func TestABodyEditSendsAWorkstreamsChangeAndTheListHasTheNewBrief(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	server := startCopied(t, fake)
	changes := listen(t, server)

	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")

	waitForWorkstreams(t, changes)
	if got := workstreams(t, server)[0].Brief; got != "Ship loyalty plans to all shops." {
		t.Errorf("Brief = %q", got)
	}
}

func TestTheLiveEventsGiveAWorkstreamsEventForAChangeOfTheList(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	server := startCopied(t, fake)
	events := liveEvents(t, server, "workstreams")

	fake.SetTitle(shop, 12, "Integrate loyalty tiers")

	select {
	case data := <-events:
		if data != "{}" {
			t.Errorf("data = %s", data)
		}
	case <-time.After(time.Minute):
		t.Fatal("no workstreams event after one minute")
	}
}

func TestAWorkstreamWithNoSubIssueHasNotAllTasksClosed(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")

	server := startCopied(t, fake)

	if workstreams(t, server)[0].AllTasksClosed {
		t.Error("all tasks closed")
	}
}

func TestAWorkstreamWithAnOpenSubIssueHasNotAllTasksClosed(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 42, "Add plan price")
	fake.AddSubIssue(shop, 12, 42)
	server := startCopied(t, fake)

	fake.CloseIssue(shop, 41)

	waitForPoll(t, server, fake)
	if workstreams(t, server)[0].AllTasksClosed {
		t.Error("all tasks closed")
	}
}

func TestAWorkstreamWithOnlyClosedSubIssuesHasAllTasksClosed(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	fake.CloseIssue(shop, 41)

	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })
}

// The list reads only the copy, so a failed read of GitHub does not change it.
func TestAWorkstreamWhoseSubIssuesCannotBeReadStaysInTheList(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)
	fake.FailSubIssues(shop, 12, true)

	fake.CloseIssue(shop, 41)

	waitForPoll(t, server, fake)
	if list := workstreams(t, server); len(list) != 1 || list[0].Number != 12 {
		t.Errorf("workstreams = %+v", list)
	}
}

func TestAnOpenNestedSubIssueDoesNotCount(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 43, "Price table")
	fake.AddSubIssue(shop, 41, 43)
	server := startCopied(t, fake)

	fake.CloseIssue(shop, 41)

	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })
}

func TestTheCloseOfASubIssueGoesLiveAsAWorkstreamsChange(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)
	changes := listen(t, server)

	fake.CloseIssue(shop, 41)

	waitForWorkstreams(t, changes)
	if !workstreams(t, server)[0].AllTasksClosed {
		t.Error("not all tasks closed")
	}
}

func TestTheNeedsHumanLabelOnANestedTaskGoesLiveAsAWorkstreamsChange(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 43, "Price table")
	fake.AddSubIssue(shop, 41, 43)
	server := startCopied(t, fake)
	changes := listen(t, server)

	fake.AddLabel(shop, 43, "mobius:needs-human", "mobius-test[bot]")

	waitForWorkstreams(t, changes)
}

func TestACompletionClosesTheWorkstreamIssueAndLeavesTheList(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.CloseIssue(shop, 41)
	server := startCopied(t, fake)
	changes := listen(t, server)

	status, body := complete(t, server, 12)

	if status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	if state, reason := fake.State(shop, 12); state != "closed" || reason != "completed" {
		t.Errorf("state = %s, %s", state, reason)
	}
	if list := workstreams(t, server); len(list) != 0 {
		t.Errorf("workstreams = %+v", list)
	}
	waitForWorkstreams(t, changes)
}

func TestAFailedCompletionGivesTheErrorAndKeepsTheWorkstreamOpen(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.CloseIssue(shop, 41)
	server := startCopied(t, fake)
	fake.FailClose(shop, 12)

	status, body := complete(t, server, 12)

	if status != http.StatusInternalServerError {
		t.Errorf("status = %d: %s", status, body)
	}
	if state, reason := fake.State(shop, 12); state != "open" || reason != "" {
		t.Errorf("state = %s, %s", state, reason)
	}
	if list := workstreams(t, server); len(list) != 1 {
		t.Errorf("workstreams = %+v", list)
	}
}

func TestACompletionWithAnOpenSubIssueClosesNothing(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	status, body := complete(t, server, 12)

	if status != http.StatusConflict || !strings.Contains(body, "The Workstream has no task, or a task is open.") {
		t.Errorf("status = %d: %s", status, body)
	}
	for _, number := range []int64{12, 41} {
		if state, _ := fake.State(shop, number); state != "open" {
			t.Errorf("state of #%d = %s", number, state)
		}
	}
}

func TestACompletionOfAnIssueWithoutTheWorkstreamLabelClosesNothing(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 13, "Fix the footer")
	fake.AddIssue(shop, 42, "Round the price")
	fake.AddSubIssue(shop, 13, 42)
	fake.CloseIssue(shop, 42)
	server := startCopied(t, fake)

	status, body := complete(t, server, 13)

	if status != http.StatusConflict || !strings.Contains(body, "The issue is not an open Workstream.") {
		t.Errorf("status = %d: %s", status, body)
	}
	if state, _ := fake.State(shop, 13); state != "open" {
		t.Errorf("state = %s", state)
	}
}

func TestCreateWorkstreamAddsTheWorkstreamToTheListAtOnce(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_workstream", `{ title = "Billing", brief = "Bill the plans." }`))
	changes := listen(t, server)

	run(t, server, leadSpec(t), "Create it.")

	want := workstream{Repository: shop, Number: 13, Title: "Billing", Brief: "Bill the plans."}
	if list := workstreams(t, server); len(list) != 2 || list[0] != want {
		t.Errorf("workstreams = %+v", list)
	}
	waitForWorkstreams(t, changes)
}

func TestMoveTaskMovesTheTaskInTheCopyAtOnce(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	server, _ := connect(t, fake, call("move_task", "{ n = 41, workstream = 20 }"))
	fake.AddSubIssue(shop, 12, 41)
	fake.SetTitle(shop, 41, "Add the plan model")
	testkit.WaitFor(t, func() bool { return len(copiedIssues(t, server)) == 1 })
	changes := listen(t, server)

	run(t, server, leadSpec(t), "Move it.")

	if got := copiedIssues(t, server); !reflect.DeepEqual(got, []copiedIssue{{20, 41, 20, "open", "owner"}}) {
		t.Errorf("issues = %+v", got)
	}
	waitForWorkstreams(t, changes)
}
