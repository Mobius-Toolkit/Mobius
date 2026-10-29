package engine_test

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type heldWork struct {
	Kind                 string `json:"kind"`
	Repository           string `json:"repository"`
	Issue                int64  `json:"issue"`
	Title                string `json:"title"`
	WorkstreamRepository string `json:"workstreamRepository"`
	Workstream           int64  `json:"workstream"`
	WorkstreamTitle      string `json:"workstreamTitle"`
	HeldAt               string `json:"heldAt"`
}

type switchesState struct {
	Start   bool       `json:"start"`
	Merge   bool       `json:"merge"`
	Repairs bool       `json:"repairs"`
	Held    []heldWork `json:"held"`
}

func getSwitches(t *testing.T, server *testserver.Server) switchesState {
	t.Helper()
	response, err := server.Client.Get(server.URL + "/api/switches")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/switches: status %d", response.StatusCode)
	}
	var body struct {
		Data switchesState `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

// putSwitch sets the switch kind and gives the status of the answer.
func putSwitch(t *testing.T, server *testserver.Server, kind string, on bool) int {
	t.Helper()
	body := `{"on": false}`
	if on {
		body = `{"on": true}`
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, server.URL+"/api/switches/"+kind, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func pausedAt(t *testing.T, server *testserver.Server, kind string) []string {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, at *string) error {
		return rows.Scan(at)
	}, "SELECT paused_at FROM global_pauses WHERE kind = ?", kind)
}

// switchChanges counts the changes of the switches in the buffer of changes.
func switchChanges(changes <-chan engine.Change) int {
	count := 0
	for {
		select {
		case change := <-changes:
			if change.Switches {
				count++
			}
		default:
			return count
		}
	}
}

func TestEachSwitchIsOnWithEmptyTables(t *testing.T) {
	t.Parallel()
	server := startWithTasks(t, testkit.NewFakeGitHub(t))

	got := getSwitches(t, server)

	if !got.Start || !got.Merge || !got.Repairs || got.Held == nil || len(got.Held) != 0 {
		t.Errorf("switches = %+v", got)
	}
}

func TestAnOffSwitchKeepsItsFirstTimeAndTheOnDeletesIt(t *testing.T) {
	t.Parallel()
	server := startWithTasks(t, testkit.NewFakeGitHub(t))
	for _, kind := range []string{"start", "merge", "repairs"} {
		if status := putSwitch(t, server, kind, false); status != http.StatusNoContent {
			t.Fatalf("%s off: status %d", kind, status)
		}
		first := pausedAt(t, server, kind)
		got := getSwitches(t, server)
		on := map[string]bool{"start": got.Start, "merge": got.Merge, "repairs": got.Repairs}
		if len(first) != 1 || on[kind] {
			t.Fatalf("%s off: paused_at = %v, switches = %+v", kind, first, got)
		}
		if off := !got.Start || !got.Merge || !got.Repairs; off && (got.Start != (kind != "start") || got.Merge != (kind != "merge") || got.Repairs != (kind != "repairs")) {
			t.Errorf("%s off: switches = %+v, want only %s off", kind, got, kind)
		}

		if status := putSwitch(t, server, kind, false); status != http.StatusNoContent {
			t.Fatalf("%s off again: status %d", kind, status)
		}
		if second := pausedAt(t, server, kind); !slices.Equal(first, second) {
			t.Errorf("%s: paused_at = %v after the second off, want %v", kind, second, first)
		}

		if status := putSwitch(t, server, kind, true); status != http.StatusNoContent {
			t.Fatalf("%s on: status %d", kind, status)
		}
		if rows := pausedAt(t, server, kind); len(rows) != 0 {
			t.Errorf("%s on: paused_at = %v", kind, rows)
		}
		if got := getSwitches(t, server); !got.Start || !got.Merge || !got.Repairs {
			t.Errorf("%s on: switches = %+v", kind, got)
		}
	}
}

func TestASwitchWithAnUnknownKindGivesAClientError(t *testing.T) {
	t.Parallel()
	server := startWithTasks(t, testkit.NewFakeGitHub(t))

	status := putSwitch(t, server, "deploy", false)

	if status < 400 || status >= 500 {
		t.Errorf("status = %d", status)
	}
}

func TestEachSetOfASwitchSendsTheEventSwitchesWithTheFullState(t *testing.T) {
	t.Parallel()
	server := startWithTasks(t, testkit.NewFakeGitHub(t))
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	putSwitch(t, server, "merge", false)

	scanner := bufio.NewScanner(response.Body)
	event := ""
	var got switchesState
	for scanner.Scan() {
		line := scanner.Text()
		if name, found := strings.CutPrefix(line, "event: "); found {
			event = name
		}
		if data, found := strings.CutPrefix(line, "data: "); found && event == "switches" {
			if err := json.Unmarshal([]byte(data), &got); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !got.Start || got.Merge || !got.Repairs || got.Held == nil || len(got.Held) != 0 {
		t.Errorf("switches = %+v", got)
	}
}

func TestHeldWorkOfAnOpenIssueShowsItsWorkstreamAndTheNewHoldSendsOneEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	changes := listen(t, server)
	putSwitch(t, server, "start", false)
	waitForChange(t, changes, func(change engine.Change) bool { return change.Switches })

	if err := server.Engine.HoldWork(t.Context(), "start", shop, 41); err != nil {
		t.Fatal(err)
	}
	if switchChanges(changes) != 1 {
		t.Error("the first hold did not send one event")
	}
	first := getSwitches(t, server)
	want := heldWork{
		Kind: "start", Repository: shop, Issue: 41, Title: "Add plan model",
		WorkstreamRepository: shop, Workstream: 12, WorkstreamTitle: "Integrate loyalty plans",
	}
	if len(first.Held) != 1 || first.Held[0].HeldAt == "" {
		t.Fatalf("held = %+v", first.Held)
	}
	want.HeldAt = first.Held[0].HeldAt
	if first.Held[0] != want {
		t.Errorf("held = %+v, want %+v", first.Held[0], want)
	}

	if err := server.Engine.HoldWork(t.Context(), "start", shop, 41); err != nil {
		t.Fatal(err)
	}
	if err := server.Engine.HoldWork(t.Context(), "start", shop, 50); err != nil {
		t.Fatal(err)
	}
	if count := switchChanges(changes); count != 1 {
		t.Errorf("events after a second hold of #41 and a hold of #50 = %d, want 1", count)
	}
	second := getSwitches(t, server)
	if len(second.Held) != 2 || second.Held[0] != want || second.Held[1].Issue != 50 {
		t.Errorf("held = %+v", second.Held)
	}

	putSwitch(t, server, "start", true)
	if got := getSwitches(t, server); len(got.Held) != 0 {
		t.Errorf("held after the switch is on = %+v", got.Held)
	}
}

func TestHeldWorkOfASwitchThatIsOnAddsNoRowAndSendsNoEvent(t *testing.T) {
	t.Parallel()
	server := startWithTasks(t, testkit.NewFakeGitHub(t))
	changes := listen(t, server)

	if err := server.Engine.HoldWork(t.Context(), "start", shop, 41); err != nil {
		t.Fatal(err)
	}

	if count := switchChanges(changes); count != 0 {
		t.Errorf("events = %d, want 0", count)
	}
	rows := query(t, server, func(rows *sql.Rows, issue *int64) error {
		return rows.Scan(issue)
	}, "SELECT issue FROM held_work")
	if len(rows) != 0 {
		t.Errorf("held_work issues = %v, want none", rows)
	}
	putSwitch(t, server, "start", false)
	if got := getSwitches(t, server); len(got.Held) != 0 {
		t.Errorf("held after a later off = %+v", got.Held)
	}
}

func TestHeldWorkOfAClosedIssueIsNotListed(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	putSwitch(t, server, "start", false)
	if err := server.Engine.HoldWork(t.Context(), "start", shop, 41); err != nil {
		t.Fatal(err)
	}
	if err := server.Engine.HoldWork(t.Context(), "start", shop, 50); err != nil {
		t.Fatal(err)
	}

	fake.CloseIssue(shop, 41)

	testkit.WaitFor(t, func() bool {
		held := getSwitches(t, server).Held
		return len(held) == 1 && held[0].Issue == 50
	})
}

func TestTheSwitchesAndTheHeldWorkStayAfterANewStartOnTheSameDatabase(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	first := startServer(t, fake, dataDir, "")
	first.WaitForFirstPoll(t, shop)
	putSwitch(t, first, "merge", false)
	if err := first.Engine.HoldWork(t.Context(), "merge", shop, 41); err != nil {
		t.Fatal(err)
	}
	before := getSwitches(t, first)
	first.Stop()

	second := testserver.Start(t, dataDir, fake.URL)

	after := getSwitches(t, second)
	if !after.Start || after.Merge || !after.Repairs || len(after.Held) != 1 || after.Held[0] != before.Held[0] {
		t.Errorf("switches = %+v, want the state %+v", after, before)
	}
}
