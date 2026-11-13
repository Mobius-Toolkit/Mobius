package engine_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// endedHardBreakSQL is a hard break of the workflow 7 that a passed run ended. Its task issue is the number issue.
func endedHardBreakSQL(issue int64) string {
	return fmt.Sprintf(`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, run_event, rerun_at, result, ended_run_id, ended_at, issue, started_at)
		VALUES ('owner/shop', 7, 'ci', 1, '', 1, 'push', '2026-10-04T10:00:00Z', 'hard', 2, '2026-10-05T10:00:00Z', %d, '2026-10-04T10:00:00Z')`, issue)
}

// flakyBreakSQL is a flaky break of the workflow 7. Its task issue is the number issue.
func flakyBreakSQL(issue int64) string {
	return fmt.Sprintf(`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, run_event, rerun_at, result, ended_run_id, ended_at, issue, started_at)
		VALUES ('owner/shop', 7, 'ci', 1, '', 1, 'push', '2026-10-04T10:00:00Z', 'flaky', 2, '2026-10-04T10:05:00Z', %d, '2026-10-04T10:00:00Z')`, issue)
}

// openCIFailureSQL is an open CI failure record. Its issue is the number issue.
func openCIFailureSQL(issue int64) string {
	return fmt.Sprintf(`INSERT INTO ci_failures (repository, reason, test, location, issue, created_at)
		VALUES ('owner/shop', 'The plan test expects cents.', 'TestPlan', 'plan_test.go:12', %d, '2026-10-04T10:00:00Z')`, issue)
}

func addRecord(t *testing.T, server *testserver.Server, statement string) {
	t.Helper()
	if _, err := server.DB.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

// waitQueued waits until the session of role for the issue number waits in the queue.
func waitQueued(t *testing.T, server *testserver.Server, role string, number int64) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM sessions WHERE role = ? AND issue = ? AND queue_reason IS NOT NULL", role, number) == 1
	})
}

// nextToStart waits for the first result of Start in results, and gives its index and its agent.
func nextToStart(t *testing.T, results []<-chan started) (int, *engine.Agent) {
	t.Helper()
	cases := make([]reflect.SelectCase, 0, len(results)+1)
	for _, result := range results {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(result)})
	}
	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(time.After(time.Minute))})
	index, value, _ := reflect.Select(cases)
	if index == len(results) {
		t.Fatal("no session took the slot after one minute")
	}
	result := value.Interface().(started)
	if result.err != nil {
		t.Fatal(result.err)
	}
	return index, result.agent
}

// slotOrder holds the only slot of role with the session of the issue 41 and queues the sessions of issues, in this
// order. When the session of the issue 41 ends, each queued session takes the slot in its turn. slotOrder gives the
// order of the issues. spec adds the queued task of an issue, when the role has one, and gives the spec of its session.
func slotOrder(t *testing.T, server *testserver.Server, role string, issues []int64, spec func(issue int64) engine.Spec) []int64 {
	t.Helper()
	holder := start(t, server, spec(41))
	results := make([]<-chan started, 0, len(issues))
	for _, issue := range issues {
		results = append(results, startLater(t.Context(), server, spec(issue)))
		waitQueued(t, server, role, issue)
	}
	end(t, holder, "done")

	order := make([]int64, 0, len(issues))
	for range issues {
		index, agent := nextToStart(t, results)
		order = append(order, issues[index])
		results[index] = make(chan started)
		end(t, agent, "done")
	}
	return order
}

func implementerSlotOrder(t *testing.T, fake *testkit.FakeGitHub, records []string, issues []int64) []int64 {
	t.Helper()
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 })
	for _, record := range records {
		addRecord(t, server, record)
	}
	return slotOrder(t, server, engine.ImplementerRole, issues, func(issue int64) engine.Spec {
		return implementerSpec(t, server, fake, issue)
	})
}

// testACIFixTaskTakesTheSlotBeforeAFixRound queues the task #45 whose issue has the record, behind the fix round for a
// failed check of the task #41. The task #43 holds the only slot while the two wait.
func testACIFixTaskTakesTheSlotBeforeAFixRound(t *testing.T, record func(issue int64) string) {
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectThree(t, fake, fixes)
	addRecord(t, server, record(45))
	sha, pullRequest := readyPullRequest(t, server, fake)
	fake.SetCreatedAt(shop, pullRequest, 1000)
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForState(t, server, 43, "working")
	fake.AddLabel(shop, 45, "mobius:ready", "owner")
	waitForState(t, server, 45, "queued")
	waitQueued(t, server, engine.ImplementerRole, 45)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	waitForState(t, server, 41, "queued")
	// The poll after the start of the round gives the pull request its work.
	waitForPolls(t, fake)

	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	waitForImplementerEnd(t, server, 45, 1)
	waitForImplementerEnd(t, server, 41, 2)
	startsAfter(t, issueImplementers(t, server, 41)[1], issueImplementers(t, server, 45)[0])
}

func TestATaskOfAnEndedHardBreakTakesTheImplementerSlotBeforeAFixRound(t *testing.T) {
	t.Parallel()
	testACIFixTaskTakesTheSlotBeforeAFixRound(t, endedHardBreakSQL)
}

func TestATaskOfAFlakyBreakTakesTheImplementerSlotBeforeAFixRound(t *testing.T) {
	t.Parallel()
	testACIFixTaskTakesTheSlotBeforeAFixRound(t, flakyBreakSQL)
}

func TestATaskOfACIFailureRecordTakesTheImplementerSlotBeforeAFixRound(t *testing.T) {
	t.Parallel()
	testACIFixTaskTakesTheSlotBeforeAFixRound(t, openCIFailureSQL)
}

func TestATaskOfACIFailureRecordTakesTheImplementerSlotBeforeAnEarlierTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	order := implementerSlotOrder(t, fake, []string{openCIFailureSQL(45)}, []int64{43, 45})

	if want := []int64{45, 43}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestTwoCIFixTasksTakeTheImplementerSlotInTheOrderOfTheirTimeInTheQueue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	order := implementerSlotOrder(t, fake, []string{openCIFailureSQL(45), flakyBreakSQL(47)}, []int64{43, 45, 47})

	if want := []int64{45, 47, 43}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestTasksWithNoCIFixIssueKeepTheOrderOfTheirTimeInTheQueue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	order := implementerSlotOrder(t, fake, nil, []int64{43, 45, 47})

	if want := []int64{43, 45, 47}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestAReviewerOfACIFixTaskTakesTheReviewerSlotBeforeAnEarlierReviewer(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Reviewer.Max = 1 })
	addRecord(t, server, openCIFailureSQL(45))

	order := slotOrder(t, server, engine.ReviewerRole, []int64{43, 45}, func(issue int64) engine.Spec {
		spec := implementerSpec(t, server, fake, issue)
		spec.Role = engine.ReviewerRole
		return spec
	})

	if want := []int64{45, 43}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestAJudgeOfACIFixTaskTakesTheJudgeSlotBeforeAnEarlierJudge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Judge.Max = 1 })
	addRecord(t, server, openCIFailureSQL(45))

	order := slotOrder(t, server, engine.JudgeRole, []int64{43, 45}, func(issue int64) engine.Spec {
		spec := roleSpec(t, engine.JudgeRole)
		spec.Issue = sql.NullInt64{Int64: issue, Valid: true}
		return spec
	})

	if want := []int64{45, 43}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}
