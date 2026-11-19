package engine_test

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type workstream struct {
	Repository       string `json:"repository"`
	Number           int64  `json:"number"`
	Title            string `json:"title"`
	Brief            string `json:"brief"`
	Autopilot        string `json:"autopilot"`
	Evergreen        bool   `json:"evergreen"`
	AllTasksClosed   bool   `json:"allTasksClosed"`
	ReportedFinished bool   `json:"reportedFinished"`
	ReadyToMerge     bool   `json:"readyToMerge"`
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

// addWorkstream adds the Workstream #12 with the task #41.
func addWorkstream(fake *testkit.FakeGitHub) {
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
}

func TestAWorkstreamLabelShowsTheIssueInTheWorkstreamList(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddIssue(shop, 13, "Fix the footer")
	server := startCopied(t, fake)

	fake.AddLabel(shop, 12, "mobius:workstream", "owner")

	list := testkit.WaitForValue(t, func() ([]workstream, bool) {
		list := workstreams(t, server)
		return list, len(list) > 0
	})
	if want := []workstream{{Repository: shop, Number: 12, Title: "Integrate loyalty plans", Autopilot: "off"}}; !reflect.DeepEqual(list, want) {
		t.Errorf("workstreams = %+v", list)
	}
}

func TestAMaintenanceWorkstreamGetsEvergreenAndTheEvergreenLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Maintenance")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddLabel(shop, 12, "mobius:maintenance", "owner")
	fake.AddIssue(shop, 13, "Integrate loyalty plans")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")

	server := startCopied(t, fake)

	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 12), "mobius:evergreen") })
	waitForPolls(t, fake)
	if got := fake.LabelActor(shop, 12, "mobius:evergreen"); got != testkit.AppSlug+"[bot]" {
		t.Errorf("actor = %s", got)
	}
	if got := settingsOf(t, server, 12); len(got) != 1 || !got[0].Evergreen || !got[0].Maintenance {
		t.Errorf("settings of #12 = %+v", got)
	}
	if got := settingsOf(t, server, 13); len(got) != 1 || got[0].Evergreen {
		t.Errorf("settings of #13 = %+v", got)
	}
	if got := fake.Labels(shop, 12); !slices.Equal(got, []string{"mobius:workstream", "mobius:maintenance", "mobius:evergreen"}) {
		t.Errorf("labels of #12 = %v", got)
	}
	if got := fake.Labels(shop, 13); !slices.Equal(got, []string{"mobius:workstream"}) {
		t.Errorf("labels of #13 = %v", got)
	}
}

func TestARestartWithAnIssuesCursorAddsNoRowForAnOpenWorkstreamWithoutARow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Maintenance")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddLabel(shop, 12, "mobius:maintenance", "owner")
	server := startServer(t, fake, t.TempDir(), `INSERT INTO sync_cursors (repository, endpoint, since, etag) VALUES ('owner/shop', 'issues', '2999-01-01T00:00:00Z', '"old"')`)
	waitForPolls(t, fake)

	if got := settingsOf(t, server, 12); len(got) != 0 {
		t.Errorf("settings of #12 = %+v", got)
	}
	if list := workstreams(t, server); len(list) != 0 {
		t.Errorf("workstreams = %+v", list)
	}
	if slices.Contains(fake.Labels(shop, 12), "mobius:evergreen") {
		t.Errorf("labels = %v", fake.Labels(shop, 12))
	}
}

func TestAWorkstreamLabelFromAnUntrustedAuthorAddsNoCreationEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Mine the servers")
	fake.AddIssue(shop, 13, "Integrate loyalty plans")
	server := startCopied(t, fake)

	fake.AddLabel(shop, 12, "mobius:workstream", "mallory")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")

	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := leadEvents(t, server)
		return events, len(events) > 0
	})
	if len(events) != 1 || events[0].Workstream != 13 || events[0].Kind != "creation" {
		t.Errorf("Lead events = %+v", events)
	}
}

// The first poll cannot see which event is new, so the Lead gets no creation event.
func TestAWorkstreamLabelFromBeforeTheFirstPollAddsNoCreationEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	server := startCopied(t, fake)

	testkit.WaitFor(t, func() bool { return len(workstreams(t, server)) == 1 })
	waitForPolls(t, fake)
	if got := leadEvents(t, server); len(got) != 0 {
		t.Errorf("Lead events = %+v", got)
	}
}

func TestANewWorkstreamAddsACreationEventForTheLead(t *testing.T) {
	t.Parallel()
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

func TestAReaddedWorkstreamLabelAddsASecondCreationEventForTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 13, "Plan prices")
	server := startCopied(t, fake)

	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	testkit.WaitFor(t, func() bool {
		events := leadEvents(t, server)
		return len(events) == 1 && events[0].Workstream == 13 && events[0].Kind == "creation"
	})

	fake.RemoveLabel(shop, 13, "mobius:workstream", "owner")
	testkit.WaitFor(t, func() bool { return len(workstreams(t, server)) == 0 })

	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	testkit.WaitFor(t, func() bool { return len(workstreams(t, server)) == 1 })
	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := leadEvents(t, server)
		return events, len(events) == 2
	})
	for i, event := range events {
		if event.Workstream != 13 || event.Kind != "creation" {
			t.Errorf("event[%d] = %+v", i, event)
		}
	}
}

// startEmpty starts the server on a repository with no issue and waits for the poll that writes its issues cursor.
func startEmpty(t *testing.T, fake *testkit.FakeGitHub, dataDir string) *testserver.Server {
	t.Helper()
	server := startServer(t, fake, dataDir, "")
	testkit.WaitFor(t, func() bool {
		var rows int
		err := server.DB.QueryRow("SELECT count(*) FROM sync_cursors WHERE repository = ? AND endpoint = 'issues'", shop).Scan(&rows)
		if err != nil {
			t.Fatal(err)
		}
		return rows == 1
	})
	return server
}

func TestTheFirstWorkstreamOfARepositoryWithNoIssueAddsACreationEventForTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startEmpty(t, fake, t.TempDir())

	fake.CreateLabeledIssue(shop, 1, "Plan prices", "Price each plan.", "owner", "mobius:workstream")

	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := leadEvents(t, server)
		return events, len(events) > 0
	})
	waitForPolls(t, fake)
	if got := leadEvents(t, server); len(got) != 1 || got[0].Workstream != 1 || got[0].Kind != "creation" {
		t.Errorf("Lead events = %+v, first read = %+v", got, events)
	}
}

func TestABodyEditSendsAWorkstreamsChangeAndTheListHasTheNewBrief(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, approvalSeen+callEditBrief("Change it.", "Ship loyalty plans to all shops.", "Ships everywhere.")+seenScript, withUIURL)
	changes := listen(t, server)

	run(t, server, leadSpec(t), "Change it.")
	done := approveNext(t, server, 1)

	if done.State != "done" || done.Result != "Changed the Brief of #12 Integrate loyalty plans." {
		t.Fatalf("action = %+v", done)
	}
	waitForWorkstreams(t, changes)
	if got := workstreams(t, server)[0].Brief; got != "Ship loyalty plans to all shops." {
		t.Errorf("Brief = %q", got)
	}
	if _, body := fake.Issue(shop, 12); body != "Ship loyalty plans to all shops.\n\n[Open the Lead chat in Mobius](https://mobius.example.com/workstreams/owner/shop/12)" {
		t.Errorf("body on GitHub = %q", body)
	}
}

func TestTheLiveEventsGiveAWorkstreamsEventForAChangeOfTheList(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	server := startCopied(t, fake)
	events := liveEvents(t, server, "workstreams")

	fake.CreateLabeledIssue(shop, 13, "Integrate loyalty tiers", "", "owner", "mobius:workstream")

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
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")

	server := startCopied(t, fake)

	if workstreams(t, server)[0].AllTasksClosed {
		t.Error("all tasks closed")
	}
}

func TestAWorkstreamWithAnOpenSubIssueHasNotAllTasksClosed(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	fake.CloseIssue(shop, 41)

	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })
}

// The list reads only the copy, so a failed read of GitHub does not change it.
func TestAWorkstreamWhoseSubIssuesCannotBeReadStaysInTheList(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 43, "Price table")
	fake.AddSubIssue(shop, 41, 43)
	server := startCopied(t, fake)

	fake.CloseIssue(shop, 41)

	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].AllTasksClosed })
}

func TestTheCloseOfASubIssueGoesLiveAsAWorkstreamsChange(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestACompletionDismissesTheQuestionItemsAndRemovesTheQuestionLabelAtOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.CloseIssue(shop, 41)
	fake.AddLabel(shop, 12, "mobius:question", "mobius-test[bot]")
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := startOnePoll(t, fake, "")
	for _, issue := range []int64{12, 41} {
		_, err := server.DB.Exec(`INSERT INTO inbox_items (kind, repository, workstream_repository, workstream, issue, text, link, time) VALUES ('question', 'owner/shop', 'owner/shop', 12, ?, 'Cents or dollars?', '', '2026-10-04T10:00:00Z')`, issue)
		if err != nil {
			t.Fatal(err)
		}
	}

	status, body := complete(t, server, 12)

	if status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	if items := inbox(t, server); len(items) != 0 {
		t.Errorf("inbox = %+v", items)
	}
	if labels := fake.Labels(shop, 12); slices.Contains(labels, "mobius:question") {
		t.Errorf("labels = %v", labels)
	}
}

func TestAFailedCompletionClosesWorkstreamsAndClosesOnGitHubAfterRetry(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.CloseIssue(shop, 41)
	server := startCopied(t, fake)
	fake.FailClose(shop, 12)

	status, body := complete(t, server, 12)

	if status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	if list := workstreams(t, server); len(list) != 0 {
		t.Errorf("workstreams = %+v", list)
	}
	var dbState string
	if err := server.DB.QueryRow("SELECT state FROM workstreams WHERE repository = ? AND number = 12", shop).Scan(&dbState); err != nil || dbState != "closed" {
		t.Errorf("db state = %q, %v", dbState, err)
	}
	if state, _ := fake.State(shop, 12); state != "open" {
		t.Errorf("GitHub state while failing = %s", state)
	}

	fake.AllowClose(shop, 12)

	testkit.WaitFor(t, func() bool {
		state, reason := fake.State(shop, 12)
		return state == "closed" && reason == "completed"
	})
}

func TestACompletionWithAnOpenSubIssueClosesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	status, body := complete(t, server, 12)

	if status != http.StatusConflict || !strings.Contains(body, "A task of the Workstream is open.") {
		t.Errorf("status = %d: %s", status, body)
	}
	for _, number := range []int64{12, 41} {
		if state, _ := fake.State(shop, number); state != "open" {
			t.Errorf("state of #%d = %s", number, state)
		}
	}
}

func TestACompletionOfAnIssueWithoutTheWorkstreamLabelClosesNothing(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+call("create_workstream", `{ title = "Billing", brief = "Bill the plans." }`))
	changes := listen(t, server)

	run(t, server, leadSpec(t), "Create it.")
	approveNext(t, server, 1)

	want := workstream{Repository: shop, Number: 13, Title: "Billing", Brief: "Bill the plans.", Autopilot: "off"}
	if list := workstreams(t, server); len(list) != 2 || list[0] != want {
		t.Errorf("workstreams = %+v", list)
	}
	waitForWorkstreams(t, changes)
}

func TestMoveTaskMovesTheTaskInTheCopyAtOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	server, _ := connect(t, fake, approvalSeen+call("move_task", `{ repository = "owner/shop", n = 41, workstream = 20 }`))
	fake.AddSubIssue(shop, 12, 41)
	fake.SetTitle(shop, 41, "Add the plan model")
	testkit.WaitFor(t, func() bool { return len(copiedIssues(t, server)) == 1 })
	changes := listen(t, server)

	run(t, server, leadSpec(t), "Move it.")
	approveNext(t, server, 1)

	if got := copiedIssues(t, server); !reflect.DeepEqual(got, []copiedIssue{{20, 41, 20, "open", "owner"}}) {
		t.Errorf("issues = %+v", got)
	}
	waitForWorkstreams(t, changes)
}

type workstreamDetails struct {
	CreatedAt      string `json:"createdAt"`
	CompletedTasks int64  `json:"completedTasks"`
	OpenTasks      int64  `json:"openTasks"`
	Evergreen      bool   `json:"evergreen"`
	Maintenance    bool   `json:"maintenance"`
	Skills         bool   `json:"skills"`
}

func details(t *testing.T, server *testserver.Server, number int64) (int, workstreamDetails) {
	t.Helper()
	status, text := send(t, server, http.MethodGet, "/api/workstreams/owner/shop/"+itoa(number)+"/details", "")
	var body struct {
		Data workstreamDetails `json:"data"`
	}
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(text), &body); err != nil {
			t.Fatalf("details: %v: %s", err, text)
		}
	}
	return status, body.Data
}

func TestTheDetailsCountTheTasksOfTheWorkstreamAtEachDepth(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.SetCreatedAt(shop, 12, 1790000000)
	fake.CloseIssueAs(shop, 41, "completed")
	fake.AddIssue(shop, 42, "Price table")
	fake.AddSubIssue(shop, 12, 42)
	fake.AddIssue(shop, 43, "Price column")
	fake.AddSubIssue(shop, 42, 43)
	fake.AddIssue(shop, 44, "Price index")
	fake.AddSubIssue(shop, 43, 44)
	fake.CloseIssueAs(shop, 44, "not_planned")
	fake.AddIssue(shop, 50, "Nested Workstream")
	fake.AddLabel(shop, 50, "mobius:workstream", "owner")
	fake.AddSubIssue(shop, 12, 50)
	fake.AddIssue(shop, 51, "Task of the nested Workstream")
	fake.AddSubIssue(shop, 50, 51)
	fake.AddIssue("other/repo", 77, "A task in another repository")
	fake.AddIssue("other/repo", 78, "A child in another repository")
	fake.AddForeignSubIssue(shop, 12, "other/repo", 77)
	fake.AddForeignSubIssue("other/repo", 77, "other/repo", 78)
	server := startCopied(t, fake)

	status, got := details(t, server, 12)

	want := workstreamDetails{CreatedAt: "2026-09-21T14:13:20Z", CompletedTasks: 2, OpenTasks: 2}
	if status != http.StatusOK || got != want {
		t.Errorf("details = %d %+v, want %+v", status, got, want)
	}
}

func TestTheDetailsAndTheClosureMakeNoGitHubCallForTheIssuesOfTheTree(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.CloseIssueAs(shop, 41, "completed")
	fake.AddIssue(shop, 42, "Price table")
	fake.AddSubIssue(shop, 12, 42)
	fake.AddIssue(shop, 43, "Price column")
	fake.AddSubIssue(shop, 42, 43)
	server := startCopied(t, fake)
	for _, number := range []int64{12, 41, 42, 43} {
		fake.FailIssueReads(shop, number, 1000)
		fake.FailSubIssues(shop, number, true)
	}

	status, got := details(t, server, 12)
	items := closure(t, server)

	if status != http.StatusOK || got.CompletedTasks != 1 || got.OpenTasks != 2 {
		t.Errorf("details = %d %+v", status, got)
	}
	if len(items) != 3 || items[0].Number != 42 || items[1].Number != 43 || items[2].Number != 12 || items[2].Title != "Integrate loyalty plans" {
		t.Errorf("items = %+v", items)
	}
}

func TestTheDetailsRefuseAClosedWorkstream(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.CloseIssue(shop, 41)
	fake.CloseIssue(shop, 12)
	server := startCopied(t, fake)

	if status, _ := details(t, server, 12); status != http.StatusConflict {
		t.Errorf("status = %d, want %d", status, http.StatusConflict)
	}
}

func TestTheDetailsRefuseAnIssueThatIsNotAWorkstream(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	if status, _ := details(t, server, 41); status != http.StatusConflict {
		t.Errorf("status = %d, want %d", status, http.StatusConflict)
	}
}

func TestAFailedBodyWriteUpdatesWorkstreamsNowAndWritesToGitHubLater(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, approvalSeen+callEditBrief("Change it.", "Ship loyalty plans to all shops.", "Ships everywhere.")+seenScript, withUIURL)
	fake.FailBodyWrites(true)

	run(t, server, leadSpec(t), "Change it.")
	done := approveNext(t, server, 1)

	if want := "Changed the Brief of #12 Integrate loyalty plans. Mobius writes it to GitHub later."; done.State != "done" || done.Result != want {
		t.Fatalf("action = %+v, want %q", done, want)
	}
	if got := workstreams(t, server)[0].Brief; got != "Ship loyalty plans to all shops." {
		t.Errorf("Brief = %q", got)
	}
	if _, body := fake.Issue(shop, 12); body != "" {
		t.Errorf("body on GitHub while failing = %q", body)
	}

	waitForPoll(t, server, fake)
	if got := workstreams(t, server)[0].Brief; got != "Ship loyalty plans to all shops." {
		t.Errorf("Brief after poll = %q", got)
	}

	fake.FailBodyWrites(false)
	wantBody := "Ship loyalty plans to all shops.\n\n[Open the Lead chat in Mobius](https://mobius.example.com/workstreams/owner/shop/12)"
	testkit.WaitFor(t, func() bool {
		_, body := fake.Issue(shop, 12)
		return body == wantBody
	})
}

func TestAFirstPollAddsRowsWithLevelOffAndStripsLevelLabels(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(shop)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.SetBody(shop, 12, "Ship loyalty plans.")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	fake.AddLabel(shop, 12, "mobius:auto-merge", "owner")
	server := testserver.StartWith(t, testserver.Config(t, t.TempDir()), fake.URL)
	if _, err := server.DB.Exec("INSERT INTO github_apps (app_id, slug, private_key, client_id, client_secret) VALUES (?, ?, ?, ?, ?)",
		testkit.AppID, testkit.AppSlug, testkit.AppPrivateKey, testkit.AppClientID, testkit.AppClientSecret); err != nil {
		t.Fatal(err)
	}
	server.WaitForFirstPoll(t, shop)

	want := []workstream{{Repository: shop, Number: 12, Title: "Integrate loyalty plans", Brief: "Ship loyalty plans.", Autopilot: "off"}}
	if list := workstreams(t, server); !reflect.DeepEqual(list, want) {
		t.Errorf("workstreams = %+v, want %+v", list, want)
	}
	if got := settingsOf(t, server, 12); len(got) != 1 || got[0].Autopilot || got[0].AutoMerge {
		t.Errorf("settings = %+v", got)
	}
	testkit.WaitFor(t, func() bool {
		return slices.Equal(fake.Labels(shop, 12), []string{"mobius:workstream"})
	})
}

func finishedAtOf(t *testing.T, server *testserver.Server) sql.NullString {
	t.Helper()
	var finishedAt sql.NullString
	if err := server.DB.QueryRow("SELECT finished_at FROM workstreams WHERE repository = ? AND number = 12", shop).Scan(&finishedAt); err != nil {
		t.Fatal(err)
	}
	return finishedAt
}

const reportFinishedPrompts = "[[prompts]]\nwhen = \"Report finished.\"\ncall = { tool = \"report_finished\" }\n\n[[prompts]]\nwhen = \"Second report.\"\ncall = { tool = \"report_finished\" }\n\n"

func TestACompletionWithoutSubIssuesClosesAfterReportFinished(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, reportFinishedPrompts)

	if got := workstreams(t, server); len(got) != 1 || got[0].ReportedFinished || got[0].AllTasksClosed {
		t.Fatalf("workstreams before report = %+v", got)
	}
	status, body := complete(t, server, 12)
	if status != http.StatusConflict || !strings.Contains(body, "The Workstream has no task, and its Lead did not report it as finished.") {
		t.Fatalf("status = %d: %s", status, body)
	}
	if state, _ := fake.State(shop, 12); state != "open" {
		t.Fatalf("state before report = %s", state)
	}

	changes := listen(t, server)
	session := run(t, server, leadSpec(t), "Report finished.")
	if got := reply(t, server, session); got != "Reported the Workstream as finished." {
		t.Fatalf("reply = %q", got)
	}
	waitForWorkstreams(t, changes)

	first := finishedAtOf(t, server)
	if !first.Valid {
		t.Fatal("finished_at is NULL after report_finished")
	}
	firstTime, err := time.Parse(time.RFC3339Nano, first.String)
	if err != nil {
		t.Fatalf("parse finished_at %q: %v", first.String, err)
	}
	if got := workstreams(t, server); len(got) != 1 || !got[0].ReportedFinished || got[0].AllTasksClosed {
		t.Fatalf("workstreams after report = %+v", got)
	}

	session2 := run(t, server, leadSpec(t), "Second report.")
	if got := reply(t, server, session2); got != "Reported the Workstream as finished." {
		t.Fatalf("second reply = %q", got)
	}
	second := finishedAtOf(t, server)
	if !second.Valid || second.String == first.String {
		t.Fatalf("finished_at after second report = %+v, first = %+v", second, first)
	}
	secondTime, err := time.Parse(time.RFC3339Nano, second.String)
	if err != nil || secondTime.Before(firstTime) {
		t.Fatalf("second finished_at = %q (%v), first = %q", second.String, err, first.String)
	}

	status, body = complete(t, server, 12)
	if status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	if state, reason := fake.State(shop, 12); state != "closed" || reason != "completed" {
		t.Errorf("state = %s, reason = %s", state, reason)
	}
	if got := finishedAtOf(t, server); got.Valid {
		t.Errorf("finished_at after complete = %+v, want NULL", got)
	}
}

func TestReportFinishedRefusesAnEvergreenWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Maintenance")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddLabel(shop, 12, "mobius:evergreen", "owner")
	server, _ := connect(t, fake, reportFinishedPrompts)

	session := run(t, server, leadSpec(t), "Report finished.")

	if got := reply(t, server, session); got != "error: An evergreen Workstream has no end." {
		t.Errorf("reply = %q", got)
	}
	if got := finishedAtOf(t, server); got.Valid {
		t.Errorf("finished_at = %+v, want NULL", got)
	}
	if got := workstreams(t, server); len(got) != 1 || got[0].ReportedFinished {
		t.Errorf("workstreams = %+v", got)
	}
}

func TestCloseClearsFinishedAtAndReopenNeedsANewReport(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, reportFinishedPrompts)

	run(t, server, leadSpec(t), "Report finished.")
	if got := finishedAtOf(t, server); !got.Valid {
		t.Fatal("finished_at is NULL after report_finished")
	}

	if status, body := closeWontDo(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("close status = %d: %s", status, body)
	}
	if got := finishedAtOf(t, server); got.Valid {
		t.Errorf("finished_at after close won't do = %+v, want NULL", got)
	}

	fake.ReopenIssue(shop, 12)
	testkit.WaitFor(t, func() bool { return len(workstreams(t, server)) == 1 })
	if got := workstreams(t, server); got[0].ReportedFinished {
		t.Errorf("workstreams after reopen = %+v", got)
	}
	if status, body := complete(t, server, 12); status != http.StatusConflict || !strings.Contains(body, "The Workstream has no task, and its Lead did not report it as finished.") {
		t.Fatalf("complete after reopen status = %d: %s", status, body)
	}

	run(t, server, leadSpec(t), "Second report.")
	if got := finishedAtOf(t, server); !got.Valid {
		t.Fatal("finished_at is NULL after second report")
	}
	fake.CloseIssueBy(shop, 12, "owner")
	testkit.WaitFor(t, func() bool { return len(workstreams(t, server)) == 0 })
	if got := finishedAtOf(t, server); got.Valid {
		t.Errorf("finished_at after GitHub close = %+v, want NULL", got)
	}
}
