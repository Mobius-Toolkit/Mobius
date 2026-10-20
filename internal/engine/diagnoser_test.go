package engine_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	planFailure = `{ reason = "The plan test expects cents.", test = "TestPlan", location = "plan_test.go:12", lines = "--- FAIL: TestPlan", caused_by_pull_request = false`
	// reportsPlan is a Diagnoser that reports the failure of TestPlan.
	reportsPlan = "[[prompts]]\nwhen = \"You are a Diagnoser\"\ncall = { tool = \"report_failure\", arguments = " + planFailure + " } }\n"
	// reportsPlanOfThePullRequest is a Diagnoser that reports a failure that the pull request caused.
	reportsPlanOfThePullRequest = "[[prompts]]\nwhen = \"You are a Diagnoser\"\ncall = { tool = \"report_failure\", arguments = { reason = \"The change breaks TestPlan.\", test = \"TestPlan\", location = \"plan_test.go:12\", lines = \"--- FAIL: TestPlan\", caused_by_pull_request = true } }\n"
	// findsNothing is a Diagnoser that calls no tool.
	findsNothing = "[[prompts]]\nwhen = \"You are a Diagnoser\"\nreply = [\"I found no cause.\"]\n"
)

// connectDiagnoser is connectTask with a Diagnoser that plays diagnoser on Antigravity.
func connectDiagnoser(t *testing.T, fake *testkit.FakeGitHub, lead, diagnoser string) *testserver.Server {
	t.Helper()
	return connectDiagnoserFixing(t, fake, lead, fixes, diagnoser)
}

// connectDiagnoserFixing is connectDiagnoser with the Implementer script implementer.
func connectDiagnoserFixing(t *testing.T, fake *testkit.FakeGitHub, lead, implementer, diagnoser string) *testserver.Server {
	t.Helper()
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+diagnoser)
	server, _ := connectTaskIn(t, fake, dataDir, lead, implementer, func(cfg *config.Config) {
		cfg.Roles.Diagnoser = config.RoleBinding{Harness: config.Antigravity, Model: "gemini-3-pro", Max: 2}
	})
	return server
}

func diagnoserSessions(t *testing.T, server *testserver.Server) []store.Session {
	t.Helper()
	rows, err := server.DB.Query("SELECT id FROM sessions WHERE role = 'diagnoser' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var sessions []store.Session
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		session, err := store.New(server.DB).GetSession(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return sessions
}

func rowCount(t *testing.T, server *testserver.Server, query string) int {
	t.Helper()
	var n int
	if err := server.DB.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// failActions adds a failed check run of GitHub Actions with a job log on the head sha.
func failActions(fake *testkit.FakeGitHub, sha string) int64 {
	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.SetCheckRunApp(id, "github-actions")
	fake.AddJobLog(id, "ok  internal/store\n--- FAIL: TestPlan\nFAIL\n")
	return id
}

func TestADiagnoserRunsBeforeTheFixRoundAndTheRoundGetsItsReason(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStarts, reportsPlan)
	sha := approvalHead(t, server, fake)

	id := failActions(fake, sha)

	round := roundPrompt(t, server)
	inOrder(t, round, "Check run \"build\"", "Action: fix\n",
		"\nDiagnosis of the failed jobs:\n- The plan test expects cents. Test: TestPlan. Location: plan_test.go:12. The change of the pull request did not cause this failure.\n")
	sessions := diagnoserSessions(t, server)
	if len(sessions) != 1 {
		t.Fatalf("Diagnosers = %d", len(sessions))
	}
	prompt := promptTexts(t, server, sessions[0].ID)[0]
	for _, part := range []string{
		"#42 https://github.com/owner/shop/pull/42\nHead commit: " + sha + "\n",
		fmt.Sprintf("- Job \"build\"\n  Link: https://github.com/owner/shop/runs/%d\n  Log file: ", id),
	} {
		if !strings.Contains(prompt, part) {
			t.Errorf("%q is not in %s", part, prompt)
		}
	}
	testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).CheckHead.String == sha })
	if n := implementers(t, server); n != 2 {
		t.Errorf("Implementers = %d", n)
	}
	if n := rowCount(t, server, "SELECT count(*) FROM ci_failures WHERE reason = 'The plan test expects cents.' AND test = 'TestPlan' AND location = 'plan_test.go:12' AND issue = 0 AND ended_at = ''"); n != 1 {
		t.Errorf("records = %d", n)
	}
	var urls, lines string
	if err := server.DB.QueryRow("SELECT job_urls, lines FROM ci_failure_places WHERE pull_request = 42 AND ci_break = 0 AND head = ?", sha).Scan(&urls, &lines); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("https://github.com/owner/shop/runs/%d", id); urls != want || lines != "--- FAIL: TestPlan" {
		t.Errorf("place = %q, %q", urls, lines)
	}
}

func TestTheFixRoundWaitsForTheDiagnoser(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	goFile := filepath.Join(t.TempDir(), "go")
	slow := fmt.Sprintf("[[prompts]]\nwhen = \"You are a Diagnoser\"\nshell = \"while [ ! -e '%s' ]; do sleep 0.05; done\"\nreply = [\"Done.\"]\n", goFile)
	server := connectDiagnoser(t, fake, leadStarts, slow)
	sha := approvalHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return len(diagnoserSessions(t, server)) == 1 })
	waitForPolls(t, fake)
	if task := liveTask(t, server, 41); task.CheckHead.Valid || task.State != "approval" {
		t.Errorf("task = %s, check head %v", task.State, task.CheckHead)
	}
	if n := implementers(t, server); n != 1 {
		t.Errorf("Implementers = %d", n)
	}
	for _, request := range fake.Requests() {
		if strings.HasSuffix(request.Path, "/annotations") {
			t.Errorf("a poll read %s while the Diagnoser runs", request.Path)
		}
	}

	touch(t, goFile)

	roundPrompt(t, server)
	testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).CheckHead.String == sha })
}

func TestADiagnoserThatCallsNoToolDoesNotBlockTheFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStarts, findsNothing)
	sha := approvalHead(t, server, fake)

	round := sentinelRound(t, server, fake, sha)

	if strings.Contains(round, "Diagnosis of the failed jobs") {
		t.Errorf("round = %s", round)
	}
	if n := rowCount(t, server, "SELECT count(*) FROM ci_failures"); n != 0 {
		t.Errorf("records = %d", n)
	}
	testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).CheckHead.String == sha })
}

func TestAFailureThatThePullRequestCausedHasNoRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStarts, reportsPlanOfThePullRequest)
	sha := approvalHead(t, server, fake)

	round := sentinelRound(t, server, fake, sha)

	if want := "- The change breaks TestPlan. Test: TestPlan. Location: plan_test.go:12. The change of the pull request caused this failure.\n"; !strings.Contains(round, want) {
		t.Errorf("%q is not in %s", want, round)
	}
	if n := rowCount(t, server, "SELECT count(*) FROM ci_failures") + rowCount(t, server, "SELECT count(*) FROM ci_failure_places"); n != 0 {
		t.Errorf("records and places = %d", n)
	}
}

func TestTwoPullRequestsWithTheSameTestAndLocationShareOneRecord(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStartsTwo, reportsPlan)
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	first := approvalHead(t, server, fake)
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return liveTaskState(t, server, 43) == "approval" })
	second := head(t, fake, "mobius/43")

	fake.AddCheckRun(shop, checkRun("build", first, "completed", "failure"))
	fake.AddCheckRun(shop, checkRun("build", second, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return rowCount(t, server, "SELECT count(*) FROM ci_failure_places") == 2 })
	if n := rowCount(t, server, "SELECT count(*) FROM ci_failures"); n != 1 {
		t.Errorf("records = %d", n)
	}
	if n := rowCount(t, server, "SELECT count(DISTINCT pull_request) FROM ci_failure_places WHERE ci_break = 0 AND pull_request > 0"); n != 2 {
		t.Errorf("pull requests = %d", n)
	}
	if n := rowCount(t, server, "SELECT count(*) FROM ci_failure_places GROUP BY failure"); n != 2 {
		t.Errorf("places of the record = %d", n)
	}
}

func TestASecondFailedHeadOfThePullRequestWithTheSameFailureKeepsOnePlaceWithTheNewestHead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectDiagnoser(t, fake, leadStarts, reportsPlan)
	sha := approvalHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" && head(t, fake, "mobius/41") != sha })
	next := head(t, fake, "mobius/41")

	fake.AddCheckRun(shop, checkRun("build", next, "completed", "failure"))

	testkit.WaitFor(t, func() bool {
		return rowCount(t, server, "SELECT count(*) FROM ci_diagnoses WHERE ended_at <> ''") == 2
	})
	if n := rowCount(t, server, "SELECT count(*) FROM ci_failures"); n != 1 {
		t.Errorf("records = %d", n)
	}
	var head string
	if err := server.DB.QueryRow("SELECT head FROM ci_failure_places WHERE pull_request = 42").Scan(&head); err != nil || head != next {
		t.Errorf("place head = %s, %v, want %s", head, err, next)
	}
	if n := rowCount(t, server, "SELECT count(*) FROM ci_failure_places"); n != 1 {
		t.Errorf("places = %d", n)
	}
}

func TestReportFailureRefusesARecordThatIsNotOpenInTheRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	missing := strings.Replace(reportsPlan, "caused_by_pull_request = false", "caused_by_pull_request = false, same_record = 99", 1)
	server := connectDiagnoser(t, fake, leadStarts, missing)
	sha := approvalHead(t, server, fake)

	sentinelRound(t, server, fake, sha)

	calls := mcpCalls(t, server, diagnoserSessions(t, server)[0].ID)
	if want := "99 is not an open failure record of this repository."; len(calls) != 1 || calls[0]["error"] != want {
		t.Errorf("calls = %v, want the error %q", calls, want)
	}
	if n := rowCount(t, server, "SELECT count(*) FROM ci_failures"); n != 0 {
		t.Errorf("records = %d", n)
	}
}

func TestAResumeRoundOfATaskThatTheCIStoppedAfterADiagnosisGetsTheSameDiagnosis(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+reportsPlan)
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, fixesNothing, func(cfg *config.Config) {
		cfg.Roles.Diagnoser = config.RoleBinding{Harness: config.Antigravity, Model: "gemini-3-pro", Max: 2}
	})
	sha := approvalHead(t, server, fake)
	failActions(fake, sha)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	diagnosis := "\nDiagnosis of the failed jobs:\n- The plan test expects cents. Test: TestPlan. Location: plan_test.go:12. The change of the pull request did not cause this failure.\n"
	if round := roundPrompt(t, server); !strings.Contains(round, diagnosis) {
		t.Fatalf("%q is not in %s", diagnosis, round)
	}

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")

	resumed := testkit.WaitForValue(t, func() (string, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) < 3 {
			return "", false
		}
		prompts := promptTexts(t, server, sessions[2].ID)
		if len(prompts) == 0 {
			return "", false
		}
		return prompts[0], true
	})
	inOrder(t, resumed, "Check run \"build\"", "Action: fix\n", diagnosis)
	if n := len(diagnoserSessions(t, server)); n != 1 {
		t.Errorf("Diagnosers = %d", n)
	}
}
