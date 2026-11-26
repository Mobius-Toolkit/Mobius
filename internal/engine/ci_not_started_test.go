package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	billingMessage = "The job was not started because recent account payments have failed or your spending limit needs to be increased."

	notStartedText = "GitHub did not start the failed jobs of the CI of the head commit. GitHub gave this message: \"" + billingMessage + "\"."
	notStartedNext = "Remove the cause, for example the billing block of the account. Then run the failed jobs of PR #42 again on GitHub. " +
		"Mobius continues the task when they pass. A Resume starts a fix round, which cannot change the cause."
)

// failUnstarted adds a failed workflow run on sha with a failed check run of GitHub Actions. Its job failed with no
// step in the first attempt, and its annotation holds billingMessage.
func failUnstarted(fake *testkit.FakeGitHub, sha string) (workflow, check int64) {
	workflow = fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "in_progress"})
	check = fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.SetCheckRunApp(check, "github-actions")
	fake.AddWorkflowJob(shop, workflow, 1, testkit.WorkflowJob{ID: check, Name: "build", Conclusion: "failure"})
	fake.AddAnnotation(check, ".github", 1, billingMessage)
	fake.SetWorkflowRunStatus(workflow, "completed", "failure")
	return workflow, check
}

// wantNotStartedStop waits for the stop of #41 with the text of a CI that GitHub did not start, and checks the Lead
// event, the Inbox item and the Mobius check.
func wantNotStartedStop(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	prompt := waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": "+notStartedText)
	if !strings.Contains(prompt, "call rerun_failed_jobs to start a new attempt.") {
		t.Errorf("Lead prompt = %q", prompt)
	}
	wantStopNotice(t, server, fake, "CI not started", notStartedText, notStartedNext)
	runs := testkit.WaitForValue(t, func() ([]testkit.CheckRun, bool) {
		runs := mobiusCheckRuns(fake)
		return runs, len(runs) > 0 && runs[len(runs)-1].Output != nil && runs[len(runs)-1].Output.Title == "CI not started"
	})
	if last := runs[len(runs)-1]; last.Conclusion != "failure" || last.Output.Summary != notStartedText {
		t.Errorf("check run = %+v", last)
	}
}

func wantNoRound(t *testing.T, server *testserver.Server) {
	t.Helper()
	if count := implementers(t, server); count != 1 {
		t.Errorf("Implementers = %d", count)
	}
	if count := len(roleSessions(t, server, engine.DiagnoserRole)); count != 0 {
		t.Errorf("Diagnosers = %d", count)
	}
	if count := rowCount(t, server, "SELECT count(*) FROM ci_diagnoses"); count != 0 {
		t.Errorf("diagnoses = %d", count)
	}
}

// stopAfterSecondAttempt fails the job with no step in two attempts, and waits for the stop.
func stopAfterSecondAttempt(t *testing.T) (*testserver.Server, *testkit.FakeGitHub, int64, int64) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	workflow, check := failUnstarted(fake, checksHead(t, server, fake))
	testkit.WaitFor(t, func() bool { return rerunHead(t, server) != "" })
	if attempt := fake.WorkflowRunAttempt(workflow); attempt != 2 {
		t.Errorf("attempt = %d", attempt)
	}
	wantNoRound(t, server)

	fake.AddWorkflowJob(shop, workflow, 2, testkit.WorkflowJob{ID: check, Name: "build", Conclusion: "failure"})
	fake.SetWorkflowRunStatus(workflow, "completed", "failure")

	wantNotStartedStop(t, server, fake)
	wantNoRound(t, server)
	return server, fake, workflow, check
}

func TestAFailedJobThatGitHubNeverStartedRunsAgainAndTheSecondFailureStopsTheTaskWithNoRound(t *testing.T) {
	t.Parallel()
	stopAfterSecondAttempt(t)
}

func TestAThirdAttemptThatPassesMovesTheStoppedTaskOnWithNoResumeAndNoRound(t *testing.T) {
	t.Parallel()
	server, fake, workflow, check := stopAfterSecondAttempt(t)

	fake.RerunWorkflowRun(workflow, "success")
	fake.SetCheckRunStatus(check, "completed", "success")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	wantNoRound(t, server)
}

func TestAFailedJobWithNoStepAndAFailedJobWithStepsStartAFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	sha := checksHead(t, server, fake)
	workflow := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "in_progress"})
	unstarted := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	started := fake.AddCheckRun(shop, checkRun("lint", sha, "completed", "failure"))
	for _, check := range []int64{unstarted, started} {
		fake.SetCheckRunApp(check, "github-actions")
	}
	fake.AddWorkflowJob(shop, workflow, 1, testkit.WorkflowJob{ID: unstarted, Name: "build", Conclusion: "failure"})
	fake.AddWorkflowJob(shop, workflow, 1, testkit.WorkflowJob{ID: started, Name: "lint", Conclusion: "failure", Steps: []testkit.WorkflowStep{{Name: "Run lint", Conclusion: "failure"}}})
	fake.SetWorkflowRunStatus(workflow, "completed", "failure")

	testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
	if count := rowCount(t, server, "SELECT count(*) FROM ci_diagnoses"); count != 1 {
		t.Errorf("diagnoses = %d", count)
	}
}

func TestARefusedRerunOfAJobWithNoStepStopsTheTaskWithTheTextOfAJobThatGitHubNeverStarted(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, longGrace)
	fake.RefuseRerun(403)
	workflow, _ := failUnstarted(fake, checksHead(t, server, fake))

	wantNotStartedStop(t, server, fake)
	wantNoRound(t, server)
	if attempt := fake.WorkflowRunAttempt(workflow); attempt != 1 || rerunHead(t, server) != "" {
		t.Errorf("attempt = %d, rerun_head = %s", attempt, rerunHead(t, server))
	}
}
