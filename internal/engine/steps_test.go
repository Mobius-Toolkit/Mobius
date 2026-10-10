package engine_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type stepRow struct {
	Kind                                    string
	Session, Task, Issue                    sql.NullInt64
	Workstream                              int64
	Organization, Repository, Role, Harness string
	Model                                   string
	Effort                                  sql.NullString
	StartedAt, EndedAt                      string
	Attempt                                 sql.NullInt64
	Result                                  sql.NullString
}

func stepRows(t *testing.T, server *testserver.Server, kind string) []stepRow {
	t.Helper()
	rows, err := server.DB.QueryContext(t.Context(), `SELECT kind, session, task, issue, workstream, organization, repository, role,
		harness, model, effort, started_at, ended_at, attempt, result FROM step_times WHERE kind = ? ORDER BY id`, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var found []stepRow
	for rows.Next() {
		var r stepRow
		if err := rows.Scan(&r.Kind, &r.Session, &r.Task, &r.Issue, &r.Workstream, &r.Organization, &r.Repository, &r.Role,
			&r.Harness, &r.Model, &r.Effort, &r.StartedAt, &r.EndedAt, &r.Attempt, &r.Result); err != nil {
			t.Fatal(err)
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

// waitForSteps waits until the step rows of kind have count rows, and gives them.
func waitForSteps(t *testing.T, server *testserver.Server, kind string, count int) []stepRow {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]stepRow, bool) {
		rows := stepRows(t, server, kind)
		return rows, len(rows) == count
	})
}

// stepOf checks the group values of the step row of #41 against session, and the order of its times.
func stepOf(t *testing.T, server *testserver.Server, row stepRow, session store.Session) {
	t.Helper()
	if row.Session != nullInt(session.ID) || row.Task != nullInt(liveTask(t, server, 41).ID) || row.Issue != nullInt(41) || row.Workstream != 12 ||
		row.Organization != "owner" || row.Repository != shop || row.Role != engine.ImplementerRole || row.Harness != session.Harness ||
		row.Model != session.Model || row.Effort != session.Effort {
		t.Errorf("row = %+v, session = %+v", row, session)
	}
	if parseTime(t, row.EndedAt).Before(parseTime(t, row.StartedAt)) {
		t.Errorf("row = %+v ends before it starts", row)
	}
}

func stepResults(rows []stepRow) []string {
	var found []string
	for _, row := range rows {
		found = append(found, row.Result.String)
	}
	return found
}

func stepAttempts(rows []stepRow) []int64 {
	var found []int64
	for _, row := range rows {
		found = append(found, row.Attempt.Int64)
	}
	return found
}

func TestEachRunOfTheLocalCheckHasACheckRowWithItsAttemptAndResult(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\n"+commitDollar+"\n[[prompts]]\n"+fixCents, noChange)
	fake.SetCheck(shop, "grep -q cents plan.txt")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	rows := waitForSteps(t, server, "check", 2)
	session := endedImplementers(t, server, 1)[0]
	if !slices.Equal(stepResults(rows), []string{"fail", "pass"}) || !slices.Equal(stepAttempts(rows), []int64{1, 2}) {
		t.Errorf("rows = %+v", rows)
	}
	for _, row := range rows {
		stepOf(t, server, row, session)
	}
	if parseTime(t, rows[1].StartedAt).Before(parseTime(t, rows[0].EndedAt)) {
		t.Errorf("the second check starts at %s, before the first ends at %s", rows[1].StartedAt, rows[0].EndedAt)
	}
}

// Serial: the check must not end before CheckTimeout.
func TestACheckThatDoesNotEndInCheckTimeoutHasACheckRowWithTheResultTimeout(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, func(cfg *config.Config) {
		cfg.CheckTimeout = 300 * time.Millisecond
		cfg.MaxCheckAttempts = 1
	})
	fake.SetCheck(shop, "sleep 10")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	rows := waitForSteps(t, server, "check", 1)
	if !slices.Equal(stepResults(rows), []string{"timeout"}) || !slices.Equal(stepAttempts(rows), []int64{1}) {
		t.Errorf("rows = %+v", rows)
	}
	if got := parseTime(t, rows[0].EndedAt).Sub(parseTime(t, rows[0].StartedAt)); got < 300*time.Millisecond {
		t.Errorf("duration = %s", got)
	}
}

func TestACheckOnAFullDiskHasACheckRowForEachRunWithTheSameAttempt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	testkit.SetFreeSpace(t, dataDir, 0)
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, commits, func(cfg *config.Config) {
		cfg.MaxCheckAttempts = 1
		cfg.HousekeeperInterval = 100 * time.Millisecond
	})
	free := filepath.Join(dataDir, "harnesses", "df.free")
	fake.SetCheck(shop, fmt.Sprintf("if [ \"$(cat '%s')\" = 0 ]; then echo 'error: No space left on device (os error 28)'; exit 1; fi", free))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForSteps(t, server, "check", 1)

	testkit.SetFreeSpace(t, dataDir, 20<<20)

	rows := waitForSteps(t, server, "check", 2)
	if !slices.Equal(stepResults(rows), []string{"fail", "pass"}) || !slices.Equal(stepAttempts(rows), []int64{1, 1}) {
		t.Errorf("rows = %+v", rows)
	}
}

func TestTheCheckAfterTheMergeOfNewCommitsBeforeThePushHasACheckRowWithNoAttempt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, commits, noChange)
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	worktree := filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41")
	testkit.WaitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(worktree, "plan.txt"))
		return err == nil
	})

	fake.PushCommit(shop, "mobius/41", "Update the UI screenshots")
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	rows := waitForSteps(t, server, "check", 2)
	if !slices.Equal(stepResults(rows), []string{"pass", "pass"}) || rows[0].Attempt != nullInt(1) || rows[1].Attempt.Valid {
		t.Errorf("rows = %+v", rows)
	}
}

func TestASessionThatWaitsForItsSlotHasAQueueRowFromItsAddTimeToItsStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStartsTwo, commits, func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 })
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))
	dispatchTwo(fake)
	waiting := testkit.WaitForValue(t, func() (store.Session, bool) {
		for _, session := range roleSessions(t, server, engine.ImplementerRole) {
			if session.QueueReason.Valid && strings.HasPrefix(session.QueueReason.String, "no free") {
				return session, true
			}
		}
		return store.Session{}, false
	})

	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	ended := endedImplementers(t, server, 2)
	var row stepRow
	testkit.WaitFor(t, func() bool {
		for _, found := range stepRows(t, server, "queue") {
			if found.Session == nullInt(waiting.ID) {
				row = found
			}
		}
		return row.Session.Valid
	})
	started := ended[0]
	if started.ID != waiting.ID {
		started = ended[1]
	}
	if row.Session != nullInt(waiting.ID) || row.Role != engine.ImplementerRole || row.Harness != started.Harness || row.Model != started.Model ||
		row.Effort != started.Effort || row.Issue != started.Issue || row.Workstream != 12 || row.Organization != "owner" || row.Repository != shop ||
		!row.Task.Valid || row.Attempt.Valid || row.Result.Valid {
		t.Errorf("row = %+v, session = %+v", row, started)
	}
	if row.EndedAt != started.StartedAt || !parseTime(t, row.StartedAt).Before(parseTime(t, row.EndedAt)) {
		t.Errorf("row = %+v, session starts at %s", row, started.StartedAt)
	}
}

func TestTheWaitForTheCIHasACIRowWithTheResultFailureOrSuccess(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	sha := checksHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fixed := testkit.WaitForValue(t, func() (string, bool) {
		fixed := head(t, fake, "mobius/41")
		return fixed, fixed != sha && taskState(t, server) == "checks"
	})
	fake.AddCheckRun(shop, checkRun("build", fixed, "completed", "success"))

	rows := waitForSteps(t, server, "ci", 2)
	sessions := endedImplementers(t, server, 2)
	if !slices.Equal(stepResults(rows), []string{"failure", "success"}) {
		t.Errorf("rows = %+v", rows)
	}
	stepOf(t, server, rows[0], sessions[0])
	stepOf(t, server, rows[1], sessions[1])
	if rows[0].Attempt.Valid || rows[1].Attempt.Valid {
		t.Errorf("rows = %+v", rows)
	}
	if parseTime(t, rows[1].StartedAt).Before(parseTime(t, rows[0].EndedAt)) {
		t.Errorf("the second wait starts at %s, before the first ends at %s", rows[1].StartedAt, rows[0].EndedAt)
	}
}

func TestACIThatFailsAfterItsFixRoundHasACIRowWithTheResultFailureForTheNewestImplementer(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	sha := checksHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })

	rows := waitForSteps(t, server, "ci", 2)
	sessions := endedImplementers(t, server, 2)
	if !slices.Equal(stepResults(rows), []string{"failure", "failure"}) {
		t.Errorf("rows = %+v", rows)
	}
	stepOf(t, server, rows[0], sessions[0])
	stepOf(t, server, rows[1], sessions[1])
}

func TestTheCIRowOfAStopThatContinuesStartsWhenTheTaskEntersChecksAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, id := stoppedOnFailedCI(t, fake, noChange)
	stopped := liveTask(t, server, 41)

	fake.SetCheckRunStatus(id, "completed", "success")

	var successes []stepRow
	testkit.WaitFor(t, func() bool {
		successes = successes[:0]
		for _, found := range stepRows(t, server, "ci") {
			if found.Result.String == "success" {
				successes = append(successes, found)
			}
		}
		return len(successes) == 2
	})
	row := successes[1]
	if !parseTime(t, row.StartedAt).After(parseTime(t, stopped.StateAt)) {
		t.Errorf("row = %+v starts before the task entered checks again, stopped at %s", row, stopped.StateAt)
	}
}
