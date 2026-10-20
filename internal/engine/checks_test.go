package engine_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	// fixes is an Implementer that commits plan.txt with cents, and fixes the check in a fix round.
	fixes = "[[prompts]]\nwhen = \"Action: fix\"\nshell = \"echo 'cents per month' > plan.txt && git commit -q -am 'Fix the check'\"\n\n" + commits
	// fixesNothing is an Implementer that commits plan.txt with cents, and makes no commit in a fix round.
	fixesNothing = "[[prompts]]\nwhen = \"Action: fix\"\nshell = \"true\"\n\n" + commits
	// fixesInANewFile is an Implementer that commits a new file in a fix round.
	fixesInANewFile = "[[prompts]]\nwhen = \"Action: fix\"\nshell = \"echo fix > fix.txt && git add fix.txt && git commit -q -m 'Fix the check'\"\n\n" + commits
)

// approvalHead dispatches #41, waits until its task waits for the Lead, and gives the head of its branch.
func approvalHead(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) string {
	t.Helper()
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	return head(t, fake, "mobius/41")
}

// roundPrompt waits for the second Implementer session, and gives its first prompt.
func roundPrompt(t *testing.T, server *testserver.Server) string {
	t.Helper()
	return testkit.WaitForValue(t, func() (string, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) < 2 {
			return "", false
		}
		prompts := promptTexts(t, server, sessions[1].ID)
		if len(prompts) == 0 {
			return "", false
		}
		return prompts[0], true
	})
}

// roundEnded tells if the state is one that a task has after a fix round for a failed check run that the round did not
// fix. The task stays in checks for one poll, and then goes to a human.
func roundEnded(state string) bool { return state == "checks" || state == "needs_human" }

func checkRun(name, sha, status, conclusion string) testkit.CheckRun {
	return testkit.CheckRun{Name: name, HeadSHA: sha, Status: status, Conclusion: conclusion, Output: &testkit.CheckRunOutput{Title: name + " title", Summary: name + " summary"}}
}

// mobiusCheckRuns gives the Mobius check runs of the shop, in the order of their creation.
func mobiusCheckRuns(fake *testkit.FakeGitHub) []testkit.CheckRun {
	return slices.DeleteFunc(fake.CheckRuns(shop), func(run testkit.CheckRun) bool { return run.Name != "Mobius" })
}

// sentinelRound adds a failed check run sentinel on the head, and gives the prompt of the fix round that it starts.
func sentinelRound(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, sha string) string {
	t.Helper()
	fake.AddCheckRun(shop, checkRun("sentinel", sha, "completed", "failure"))
	return roundPrompt(t, server)
}

func implementers(t *testing.T, server *testserver.Server) int {
	t.Helper()
	return len(roleSessions(t, server, engine.ImplementerRole))
}

func TestAFailedCheckRunOnTheHeadStartsAFixRoundWithTheCheckAndItsAnnotations(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := approvalHead(t, server, fake)

	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.AddAnnotation(id, "plan.txt", 1, "Store the unit.")

	round := roundPrompt(t, server)
	for _, part := range []string{
		"# Open items\n",
		fmt.Sprintf("Check run \"build\", https://github.com/owner/shop/runs/%d:\nbuild title\n\nbuild summary\n", id),
		"- plan.txt line 1: Store the unit.\n",
		"Action: fix\n",
	} {
		if !strings.Contains(round, part) {
			t.Errorf("%q is not in %s", part, round)
		}
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" && head(t, fake, "mobius/41") != sha })
	if task := liveTask(t, server, 41); task.FixRounds != 1 {
		t.Errorf("fix rounds = %d", task.FixRounds)
	}
	if count := implementers(t, server); count != 2 {
		t.Errorf("Implementers = %d", count)
	}
}

func TestAFailedCheckRunMakesTheReadyPullRequestADraftUntilTheEndOfTheRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	goFile := filepath.Join(dataDir, "go")
	round := fmt.Sprintf("[[prompts]]\nwhen = \"Action: fix\"\nshell = \"while [ ! -e '%s' ]; do sleep 0.05; done\"\n\n", goFile)
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, round+commits, noChange)
	sha := approvalHead(t, server, fake)
	fake.SetDraft(shop, 42, false)
	waitForPolls(t, fake)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return fake.PullRequests(shop)[0].Draft })
	if state := taskState(t, server); state != "working" {
		t.Errorf("state = %s", state)
	}

	touch(t, goFile)

	testkit.WaitFor(t, func() bool { return roundEnded(taskState(t, server)) })
	if !fake.PullRequests(shop)[0].Draft {
		t.Error("the pull request is ready for review before the approval of the Lead")
	}
}

func TestAFailedGitHubActionsCheckRunGivesTheLast200LinesOfItsJobLog(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := approvalHead(t, server, fake)

	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.SetCheckRunApp(id, "github-actions")
	fake.AddAnnotation(id, "plan.txt", 1, "Store the unit.")
	var log []string
	for line := 1; line <= 250; line++ {
		log = append(log, fmt.Sprintf("log line %d", line))
	}
	fake.AddJobLog(id, strings.Join(log, "\n"))

	round := roundPrompt(t, server)
	inOrder(t, round, "- plan.txt line 1: Store the unit.\n", "\n"+strings.Join(log[50:], "\n")+"\n", "Action: fix\n")
	if strings.Contains(round, "log line 50\n") {
		t.Errorf("round = %s", round)
	}
}

func TestAFailedCheckRunOfADifferentAppGivesNoJobLog(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := approvalHead(t, server, fake)

	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.SetCheckRunApp(id, "other-ci")
	fake.AddJobLog(id, "secret log line")

	round := roundPrompt(t, server)
	if !strings.Contains(round, "Check run \"build\"") || strings.Contains(round, "secret log line") {
		t.Errorf("round = %s", round)
	}
}

func TestAFailedJobLogDownloadStillStartsTheFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := approvalHead(t, server, fake)

	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.SetCheckRunApp(id, "github-actions")
	fake.AddAnnotation(id, "plan.txt", 1, "Store the unit.")

	round := roundPrompt(t, server)
	for _, part := range []string{"Check run \"build\"", "- plan.txt line 1: Store the unit.\n", "Action: fix\n"} {
		if !strings.Contains(round, part) {
			t.Errorf("%q is not in %s", part, round)
		}
	}
	if strings.Contains(round, "job log") {
		t.Errorf("round = %s", round)
	}
}

func TestAFailedCheckRunOnTheSameHeadStartsOneFixRoundAndThenHandsTheTaskToAHuman(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, noChange)
	sha := approvalHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
	endedImplementers(t, server, 2)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	if got := head(t, fake, "mobius/41"); got != sha {
		t.Errorf("head = %s", got)
	}
	waitForPolls(t, fake)

	if count := implementers(t, server); count != 2 {
		t.Errorf("Implementers = %d", count)
	}
}

func TestARemovalOfNeedsHumanFromATaskThatStoppedOnFailedCIStartsANewCIFixRoundOnTheSameHead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, noChange)
	sha := approvalHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
	endedImplementers(t, server, 2)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")

	testkit.WaitFor(t, func() bool { return implementers(t, server) == 3 })
	endedImplementers(t, server, 3)
	if got := head(t, fake, "mobius/41"); got != sha {
		t.Errorf("head = %s", got)
	}
	prompts := promptTexts(t, server, roleSessions(t, server, engine.ImplementerRole)[2].ID)
	if len(prompts) == 0 || !strings.Contains(prompts[0], "Check run \"build\"") || strings.Contains(prompts[0], "The human continued the task") {
		t.Errorf("prompts = %q", prompts)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	waitForPolls(t, fake)
	if got := implementers(t, server); got != 3 {
		t.Errorf("implementers = %d", got)
	}
}

func TestACancelledOrTimedOutCheckRunOnTheHeadStartsAFixRound(t *testing.T) {
	t.Parallel()
	for _, conclusion := range []string{"cancelled", "timed_out"} {
		t.Run(conclusion, func(t *testing.T) {
			fake := testkit.NewFakeGitHub(t)
			server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
			sha := approvalHead(t, server, fake)

			fake.AddCheckRun(shop, checkRun("build", sha, "completed", conclusion))

			testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
		})
	}
}

func TestAFailedCheckRunMobiusHasNoEffect(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := approvalHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("Mobius", sha, "completed", "failure"))
	round := sentinelRound(t, server, fake, sha)

	if !strings.Contains(round, "Check run \"sentinel\"") || strings.Contains(round, "Check run \"Mobius\"") {
		t.Errorf("round = %s", round)
	}
}

func TestAPassedCheckRunHasNoEffect(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := approvalHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("passed", sha, "completed", "success"))
	round := sentinelRound(t, server, fake, sha)

	if !strings.Contains(round, "Check run \"sentinel\"") || strings.Contains(round, "Check run \"passed\"") {
		t.Errorf("round = %s", round)
	}
}

func TestAFailedCheckRunWaitsForARunningCheckRunOfTheHeadAndThenOneFixRoundGetsBothFailedCheckRuns(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	sha := checksHead(t, server, fake)
	running := fake.AddCheckRun(shop, checkRun("check", sha, "in_progress", ""))
	fake.AddCheckRun(shop, checkRun("e2e", sha, "completed", "failure"))
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s", state)
	}
	if hasLabel(fake, "mobius:needs-human") {
		t.Error("the issue has mobius:needs-human")
	}
	if count := implementers(t, server); count != 1 {
		t.Errorf("Implementers = %d", count)
	}
	if task := liveTask(t, server, 41); task.CheckHead.Valid || task.FixRounds != 0 {
		t.Errorf("check head = %+v, fix rounds = %d", task.CheckHead, task.FixRounds)
	}

	fake.SetCheckRunStatus(running, "completed", "failure")

	round := roundPrompt(t, server)
	for _, part := range []string{"Check run \"e2e\"", "Check run \"check\""} {
		if !strings.Contains(round, part) {
			t.Errorf("%q is not in %s", part, round)
		}
	}
	testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).CheckHead.String == sha })
	if task := liveTask(t, server, 41); task.FixRounds != 1 {
		t.Errorf("fix rounds = %d", task.FixRounds)
	}
}

func TestAFailedCheckRunWaitsForARunningWorkflowRunOfTheHead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	sha := checksHead(t, server, fake)
	workflow := fake.AddWorkflowRun(shop, testkit.WorkflowRun{HeadSHA: sha, Status: "in_progress"})
	fake.AddCheckRun(shop, checkRun("e2e", sha, "completed", "failure"))
	waitForPolls(t, fake)

	if count := implementers(t, server); count != 1 {
		t.Errorf("Implementers = %d", count)
	}

	fake.SetWorkflowRunStatus(workflow, "completed", "failure")

	round := roundPrompt(t, server)
	if !strings.Contains(round, "Check run \"e2e\"") {
		t.Errorf("round = %s", round)
	}
}

func TestAFailedCheckRunAtMaxFixRoundsHandsTheTaskToAHuman(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, func(cfg *config.Config) { cfg.MaxFixRounds = 1 })
	sha := approvalHead(t, server, fake)
	if _, err := server.DB.Exec("UPDATE tasks SET fix_rounds = 1 WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") })
	if hasLabel(fake, "mobius:working") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if count := implementers(t, server); count != 1 {
		t.Errorf("Implementers = %d", count)
	}
	runs := testkit.WaitForValue(t, func() ([]testkit.CheckRun, bool) {
		runs := mobiusCheckRuns(fake)
		last := runs[len(runs)-1]
		return runs, last.Output != nil && last.Output.Title == "Round limit"
	})
	if last := runs[len(runs)-1]; last.Conclusion != "failure" || last.Output.Summary != "The pull request has open items after 1 fix rounds." {
		t.Errorf("check runs = %+v", runs)
	}
	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": the pull request has open items after 1 fix rounds.")
	wantStopNotice(t, server, fake, "Round limit", "The pull request has open items after 1 fix rounds.",
		"Read the open items of PR #42. Resolve the items that need no change. Then Resume, or close the pull request. Resume resets the round count.")
}

var replacedOutput = &testkit.CheckRunOutput{Title: "Replaced by a new head", Summary: "A new head of the pull request replaced this commit."}

func TestAFailedCheckRunOfTheOldHeadDoesNotStopATaskAfterAFixRoundPushed(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, sha := seedWaitingWith(t, fake, "checks", fixesInANewFile)
	waitForPolls(t, fake)
	fake.UnlistPullRequests(true)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return head(t, fake, "mobius/41") != sha && roundEnded(taskState(t, server)) })
	waitForPolls(t, fake)
	task := liveTask(t, server, 41)
	if task.State != "checks" || task.CiFailedHead.Valid {
		t.Errorf("state = %s, ci_failed_head = %v", task.State, task.CiFailedHead)
	}
}

// rerunHead gives the re-run head of the live task of #41.
func rerunHead(t *testing.T, server *testserver.Server) string {
	t.Helper()
	return liveTask(t, server, 41).RerunHead.String
}

// failedFirstAttempt dispatches #41, adds a failed workflow run and a failed check run on its head, and waits until
// Mobius ran the failed jobs again. The fix round of the failed check run makes no commit.
func failedFirstAttempt(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) (workflow, check int64) {
	t.Helper()
	sha := checksHead(t, server, fake)
	workflow = fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "completed", Conclusion: "failure"})
	check = fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return rerunHead(t, server) == sha })
	return workflow, check
}

func TestAFailedCIAfterTheFixRoundRunsTheFailedJobsAgainOneTimeAndThePassedSecondAttemptMovesTheTaskToApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	workflow, check := failedFirstAttempt(t, server, fake)
	waitForPolls(t, fake)

	if state, attempt := taskState(t, server), fake.WorkflowRunAttempt(workflow); state != "checks" || attempt != 2 || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("state = %s, attempt = %d", state, attempt)
	}

	fake.SetCheckRunStatus(check, "completed", "success")
	fake.SetWorkflowRunStatus(workflow, "completed", "success")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	if attempt := fake.WorkflowRunAttempt(workflow); attempt != 2 {
		t.Errorf("attempt = %d", attempt)
	}
}

func TestTheTaskWaitsWhileTheFailedCheckRunOfTheFirstAttemptStaysAndTheJobsRunOnlyOneTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	workflow, _ := failedFirstAttempt(t, server, fake)
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if state, attempt := taskState(t, server), fake.WorkflowRunAttempt(workflow); state != "checks" || attempt != 2 || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("state = %s, attempt = %d", state, attempt)
	}
}

func TestTheTaskWaitsWhileTheWorkflowRunsStillShowTheFailedFirstAttemptAndTheJobsRunOnlyOneTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	workflow, check := failedFirstAttempt(t, server, fake)
	fake.SetWorkflowRunAttempt(workflow, 1, "completed", "failure")
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if state, attempt := taskState(t, server), fake.WorkflowRunAttempt(workflow); state != "checks" || attempt != 1 || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("state = %s, attempt = %d", state, attempt)
	}

	fake.SetCheckRunStatus(check, "completed", "success")
	fake.SetWorkflowRunAttempt(workflow, 2, "completed", "success")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
}

func TestTheJobsRunAgainOnlyAfterEachRunOfTheHeadCompletedAndEachFailedRunRunsOneTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	goFile := filepath.Join(dataDir, "go")
	round := fmt.Sprintf("[[prompts]]\nwhen = \"Action: fix\"\nshell = \"while [ ! -e '%s' ]; do sleep 0.05; done\"\n\n", goFile)
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, round+commits, longGrace)
	sha := checksHead(t, server, fake)
	slow := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "e2e", HeadSHA: sha, WorkflowID: 6, Status: "in_progress"})
	fast := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "completed", Conclusion: "failure"})
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if state, attempt, count := taskState(t, server), fake.WorkflowRunAttempt(fast), implementers(t, server); state != "checks" || attempt != 1 || count != 1 || rerunHead(t, server) != "" || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("state = %s, attempt = %d, Implementers = %d, rerun_head = %s", state, attempt, count, rerunHead(t, server))
	}

	fake.SetWorkflowRunStatus(slow, "completed", "failure")

	testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
	late := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "screenshots", HeadSHA: sha, WorkflowID: 7, Status: "in_progress"})
	touch(t, goFile)
	endedImplementers(t, server, 2)
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if state, attempt := taskState(t, server), fake.WorkflowRunAttempt(fast); state != "checks" || attempt != 1 || rerunHead(t, server) != "" || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("state = %s, attempt = %d, rerun_head = %s", state, attempt, rerunHead(t, server))
	}

	fake.SetWorkflowRunStatus(late, "completed", "success")

	testkit.WaitFor(t, func() bool { return rerunHead(t, server) == sha })
	if state, first, second, third := taskState(t, server), fake.WorkflowRunAttempt(fast), fake.WorkflowRunAttempt(slow), fake.WorkflowRunAttempt(late); state != "checks" || first != 2 || second != 2 || third != 1 || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("state = %s, attempts = %d, %d and %d", state, first, second, third)
	}

	fake.SetWorkflowRunStatus(fast, "completed", "failure")
	fake.SetWorkflowRunStatus(slow, "completed", "failure")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	waitForPolls(t, fake)
	if first, second, third := fake.WorkflowRunAttempt(fast), fake.WorkflowRunAttempt(slow), fake.WorkflowRunAttempt(late); first != 2 || second != 2 || third != 1 {
		t.Errorf("attempts = %d, %d and %d", first, second, third)
	}
}

func TestAFailedRunThatMobiusDidNotRunAgainHandsTheTaskToAHumanAfterTheSecondAttemptPassed(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	workflow, check := failedFirstAttempt(t, server, fake)
	sha := head(t, fake, "mobius/41")
	other := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "screenshots", HeadSHA: sha, WorkflowID: 7, Status: "completed", Conclusion: "failure"})

	fake.SetCheckRunStatus(check, "completed", "success")
	fake.SetWorkflowRunStatus(workflow, "completed", "success")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	waitForPolls(t, fake)
	if first, second := fake.WorkflowRunAttempt(workflow), fake.WorkflowRunAttempt(other); first != 2 || second != 1 {
		t.Errorf("attempts = %d and %d", first, second)
	}
}

func TestAFailedSecondAttemptHandsTheTaskToAHumanAndTheJobsRunNoThirdTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	workflow, check := failedFirstAttempt(t, server, fake)

	fake.SetCheckRunStatus(check, "completed", "failure")
	fake.SetWorkflowRunStatus(workflow, "completed", "failure")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	waitForPolls(t, fake)
	if attempt := fake.WorkflowRunAttempt(workflow); attempt != 2 {
		t.Errorf("attempt = %d", attempt)
	}
}

func TestARefusedRerunHandsTheTaskToAHumanAtOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	sha := checksHead(t, server, fake)
	fake.RefuseRerun(403)
	workflow := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "completed", Conclusion: "failure"})
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	if attempt := fake.WorkflowRunAttempt(workflow); attempt != 1 || rerunHead(t, server) != "" {
		t.Errorf("attempt = %d, rerun_head = %s", attempt, rerunHead(t, server))
	}
}

func TestARerunThatGitHubAnswersWithA5xxStatusIsTriedAgainAtTheNextPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	sha := checksHead(t, server, fake)
	fake.RefuseRerun(500)
	workflow := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "completed", Conclusion: "failure"})
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
	endedImplementers(t, server, 2)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" })
	waitForPolls(t, fake)

	if state, attempt := taskState(t, server), fake.WorkflowRunAttempt(workflow); state != "checks" || attempt != 1 {
		t.Errorf("state = %s, attempt = %d", state, attempt)
	}

	fake.RefuseRerun(0)

	testkit.WaitFor(t, func() bool { return rerunHead(t, server) == sha })
	if attempt := fake.WorkflowRunAttempt(workflow); attempt != 2 || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("attempt = %d", attempt)
	}
}
