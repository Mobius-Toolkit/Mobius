package engine_test

import (
	"errors"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// sameNumberTasks starts a server with the Workstream #12 of owner/shop, autopilot, and the task issues owner/shop#7
// and owner/garden#7 below it. Both have the title "Water the plants". It waits for the two dispatched tasks.
func sameNumberTasks(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	addGardenTask(fake)
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	addTaskIssue(fake, 7, "Water the plants")
	server := startAutopilot(t, fake, func(*config.Config) {}, "")
	liveTaskOf(t, server, 7)
	gardenTask(t, server)
	return server
}

// stopWorker makes the Worker of the task of the issue number of repository fail with no restart left.
func stopWorker(t *testing.T, server *testserver.Server, repository string, number int64) {
	t.Helper()
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'working', worker = NULL, worker_restarts = 3 WHERE repository = ? AND issue = ?", repository, number); err != nil {
		t.Fatal(err)
	}
	var task = gardenTask(t, server)
	if repository == shop {
		task = liveTask(t, server, number)
	}
	if restart, _ := server.Engine.RestartWorker(t.Context(), task, "Water the plants", errors.New("the Worker died")); restart {
		t.Fatal("the Worker restarted")
	}
}

func stopReasons(t *testing.T, server *testserver.Server) map[string]string {
	t.Helper()
	reasons := map[string]string{}
	for _, issue := range apiData[[]needsHuman](t, server, "/api/needs-human") {
		reasons[issue.URL] = issue.Reason
	}
	return reasons
}

func TestAHeldEventOfATaskGivesTheWaitStateOnlyToThatTaskAndNotToATaskOfAnotherRepositoryWithTheSameNumber(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	script := "[[prompts]]\nwhen = \"Add plan model\"\n" + holdEvent + "\n\n" + seen
	server, _ := connectWith(t, fake, script, keepSessionOpen)
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	fake.AddForeignSubIssue(shop, 12, garden, 7)
	server.WaitForFirstPoll(t, garden)
	fake.AddLabel(garden, 7, "mobius:ready", "owner")
	gardenTask(t, server)
	testkit.WaitFor(t, func() bool {
		var delivered int
		if err := server.DB.QueryRow("SELECT count(*) FROM lead_events WHERE issue_repository = ? AND delivered_at IS NOT NULL", garden).Scan(&delivered); err != nil {
			t.Fatal(err)
		}
		return delivered == 1
	})
	dispatchTask(fake, 7, "Add plan model")
	testkit.WaitFor(t, func() bool { return held(t, server, 1) })
	if _, err := server.DB.Exec(`UPDATE tasks SET state = 'approval', state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE issue = 7`); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool {
		states := map[string]string{}
		for _, line := range taskTab(t, server) {
			if line.Number == 7 {
				states[line.Repository] = line.State
			}
		}
		return states[shop] == "waits for Owner" && states[garden] == "waits for Lead"
	})
}

func TestAStopOfATaskGivesItsReasonOnlyToThatTaskAndNotToATaskOfAnotherRepositoryWithTheSameNumber(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := sameNumberTasks(t, fake)
	fake.AddLabel(garden, 7, "mobius:needs-human", "owner")

	stopWorker(t, server, shop, 7)

	testkit.WaitFor(t, func() bool {
		reasons := stopReasons(t, server)
		return len(reasons) == 2 && reasons["https://github.com/owner/shop/issues/7"] != ""
	})
	if reasons := stopReasons(t, server); reasons["https://github.com/owner/garden/issues/7"] != "" {
		t.Errorf("reasons = %v", reasons)
	}
}

func TestAStopOfATaskAddsOneStopEventAlsoWhenATaskOfAnotherRepositoryWithTheSameNumberHasAStopEventWithTheSameText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := sameNumberTasks(t, fake)

	stopWorker(t, server, shop, 7)
	stopWorker(t, server, garden, 7)

	testkit.WaitFor(t, func() bool { return countLeadEvents(t, server, "stop") == 2 })
	waitForPolls(t, fake)
	counts := map[string]int{}
	rows, err := server.DB.Query("SELECT issue_repository FROM lead_events WHERE kind = 'stop' AND issue = 7")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var repository string
		if err := rows.Scan(&repository); err != nil {
			t.Fatal(err)
		}
		counts[repository]++
	}
	if counts[shop] != 1 || counts[garden] != 1 || len(counts) != 2 {
		t.Errorf("stop events = %v", counts)
	}
}
