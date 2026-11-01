package engine_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	leadReruns = "[[prompts]]\nwhen = \"Rerun #41\"\ncall = { tool = \"rerun_failed_jobs\", arguments = { repository = \"owner/shop\", n = 41 } }\n\n"

	leadRerunsAndDispatches = "[[prompts]]\nwhen = \"Rerun #41\"\ncall = { tool = \"rerun_failed_jobs\", arguments = { repository = \"owner/shop\", n = 41 } }\n\n" +
		"[[prompts]]\nwhen = \"dispatch of #41\"\nreply = [\"Seen\"]\n\n"
)

func TestRerunFailedJobsStartsAFailedRunInItsFirstAttemptAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadReruns+leadStarts, commits, noChange)
	sha := checksHead(t, server, fake)
	run := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "completed", Conclusion: "failure"})
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })

	sendChat(t, server, leadChat, "Rerun #41")

	want := fmt.Sprintf("ci (run %d, attempt 1): started again", run)
	waitForChat(t, server, leadChat, "Lead", want)

	testkit.WaitFor(t, func() bool { return rerunHead(t, server) == sha })
	if got := liveTask(t, server, 41).RerunRuns.String; got != fmt.Sprint(run) {
		t.Errorf("rerun_runs = %q, want %q", got, fmt.Sprint(run))
	}
	if attempt := fake.WorkflowRunAttempt(run); attempt != 2 {
		t.Errorf("attempt = %d, want 2", attempt)
	}
}

func TestRerunFailedJobsStartsAFailedRunInItsSecondAttemptAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadReruns+leadStarts, commits, noChange)
	sha := checksHead(t, server, fake)
	run := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "completed", Conclusion: "failure"})
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })

	fake.RerunWorkflowRun(run, "failure")

	sendChat(t, server, leadChat, "Rerun #41")

	want := fmt.Sprintf("ci (run %d, attempt 2): started again", run)
	waitForChat(t, server, leadChat, "Lead", want)

	if attempt := fake.WorkflowRunAttempt(run); attempt != 3 {
		t.Errorf("attempt = %d, want 3", attempt)
	}
}

func TestRerunFailedJobsReportsARunningJobAndMakesNoRerunCall(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadReruns+leadStarts, commits, noChange)
	sha := checksHead(t, server, fake)
	run := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "completed", Conclusion: "failure"})
	fake.AddWorkflowJob(shop, run, 1, testkit.WorkflowJob{ID: 700, Name: "e2e", Status: "in_progress"})
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })

	callsBefore := fake.RerunRequests()

	sendChat(t, server, leadChat, "Rerun #41")

	want := fmt.Sprintf("ci (run %d, attempt 1): a job still runs: e2e", run)
	waitForChat(t, server, leadChat, "Lead", want)

	if got := fake.RerunRequests(); got != callsBefore {
		t.Errorf("rerun requests = %d, want %d", got, callsBefore)
	}
	if rerunHead(t, server) != "" {
		t.Errorf("rerun_head = %q, want empty", rerunHead(t, server))
	}
}

func TestRerunFailedJobsReportsARefusalOfGitHub(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadReruns+leadStarts, commits, noChange)
	sha := checksHead(t, server, fake)
	run := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, WorkflowID: 5, Status: "completed", Conclusion: "failure"})
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })

	fake.RefuseRerun(http.StatusForbidden)

	sendChat(t, server, leadChat, "Rerun #41")

	want := fmt.Sprintf("ci (run %d, attempt 1): GitHub refused: Resource not accessible by integration", run)
	waitForChat(t, server, leadChat, "Lead", want)

	if rerunHead(t, server) != "" {
		t.Errorf("rerun_head = %q, want empty", rerunHead(t, server))
	}
}

func TestRerunFailedJobsRefusesATaskWithNoPullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadRerunsAndDispatches, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })

	sendChat(t, server, leadChat, "Rerun #41")

	waitForChat(t, server, leadChat, "Lead", "error: The task of #41 has no open pull request.")
}
