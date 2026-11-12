package engine_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestQueuedRunNoJobsOlderThanThirtyMinutesIsCancelledAndRerunOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	runID := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.SetQueuedRunNoJobs(runID)

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	waitForPolls(t, fake)

	if calls := fake.CancelRequests(); calls != 1 {
		t.Errorf("cancel calls = %d, want 1", calls)
	}
	if calls := fake.RerunRequests(); calls != 1 {
		t.Errorf("rerun calls = %d, want 1", calls)
	}
	if attempt := fake.WorkflowRunAttempt(runID); attempt != 2 {
		t.Errorf("attempt = %d, want 2", attempt)
	}

	waitForPolls(t, fake)
	if calls := fake.CancelRequests(); calls != 1 {
		t.Errorf("cancel calls = %d, want 1", calls)
	}
	if calls := fake.RerunRequests(); calls != 1 {
		t.Errorf("rerun calls = %d, want 1", calls)
	}
}

func TestQueuedRunNoJobsRefusedBehindMainUpdatesBranchOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		refuse func(*testkit.FakeGitHub)
	}{
		{"cancel refused", func(f *testkit.FakeGitHub) { f.RefuseCancel(http.StatusConflict) }},
		{"rerun refused", func(f *testkit.FakeGitHub) { f.RefuseRerun(http.StatusUnprocessableEntity) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
			sha := checksHead(t, server, fake)
			start := time.Now()
			fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})
			tc.refuse(fake)
			fake.SetBehind(shop, 42)
			fake.CommitFile(shop, "price.txt", "dollars\n", "Add price")

			server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
			testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() == 1 })

			waitForPolls(t, fake)
			if calls := fake.UpdateBranchCalls(); calls != 1 {
				t.Errorf("update branch calls = %d, want 1", calls)
			}
		})
	}
}

func TestQueuedRunNoJobsRefusedNotBehindGivesLongCIWaitWithQueuedNoJob(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "build", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseCancel(http.StatusConflict)

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	waitForPolls(t, fake)

	if calls := fake.UpdateBranchCalls(); calls != 0 {
		t.Errorf("update branch calls = %d, want 0", calls)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })
	prompt := waitForLeadPrompt(t, server, " the CI of #41 \"Add plan model\" runs for more than 2h0m0s")
	if !strings.Contains(prompt, "build (queued with no job)") {
		t.Errorf("prompt = %s, want build (queued with no job)", prompt)
	}
	if calls := fake.UpdateBranchCalls(); calls != 0 {
		t.Errorf("update branch calls = %d, want 0", calls)
	}
}

func TestQueuedRunNoJobsAfterRefusalLaterPollsDoNotCallCancelRerunOrUpdateBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseCancel(http.StatusConflict)
	fake.SetBehind(shop, 42)
	fake.CommitFile(shop, "price.txt", "dollars\n", "Add price")

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	waitForPolls(t, fake)

	if calls := fake.CancelRequests(); calls != 1 {
		t.Fatalf("cancel calls = %d, want 1", calls)
	}
	if calls := fake.UpdateBranchCalls(); calls != 1 {
		t.Fatalf("update branch calls = %d, want 1", calls)
	}

	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if got := fake.CancelRequests(); got != 1 {
		t.Errorf("cancel calls = %d, want 1", got)
	}
	if got := fake.RerunRequests(); got != 0 {
		t.Errorf("rerun calls = %d, want 0", got)
	}
	if got := fake.UpdateBranchCalls(); got != 1 {
		t.Errorf("update branch calls = %d, want 1", got)
	}
}

func TestQueuedRunUnderThirtyMinutesOrWithJobDoesNotCancelRerunOrUpdateBranch(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	runID := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})

	// Less than 30 minutes:
	server.Engine.SetClock(func() time.Time { return start.Add(29 * time.Minute) })
	waitForPolls(t, fake)

	if calls := fake.CancelRequests(); calls != 0 {
		t.Errorf("cancel calls = %d, want 0", calls)
	}
	if calls := fake.RerunRequests(); calls != 0 {
		t.Errorf("rerun calls = %d, want 0", calls)
	}
	if calls := fake.UpdateBranchCalls(); calls != 0 {
		t.Errorf("update branch calls = %d, want 0", calls)
	}

	// More than 30 minutes, but has a job:
	fake.AddWorkflowJob(shop, runID, 1, testkit.WorkflowJob{ID: 800, Name: "test", Status: "queued"})
	server.Engine.SetClock(func() time.Time { return start.Add(35 * time.Minute) })
	waitForPolls(t, fake)

	if calls := fake.CancelRequests(); calls != 0 {
		t.Errorf("cancel calls = %d, want 0", calls)
	}
	if calls := fake.RerunRequests(); calls != 0 {
		t.Errorf("rerun calls = %d, want 0", calls)
	}
	if calls := fake.UpdateBranchCalls(); calls != 0 {
		t.Errorf("update branch calls = %d, want 0", calls)
	}

	// Positive control: more than 30 minutes and no jobs cancels and runs again.
	fake.SetQueuedRunNoJobs(runID)
	waitForPolls(t, fake)

	if calls := fake.CancelRequests(); calls != 1 {
		t.Errorf("cancel calls = %d, want 1", calls)
	}
	if calls := fake.RerunRequests(); calls != 1 {
		t.Errorf("rerun calls = %d, want 1", calls)
	}
}

func TestQueuedRunWorkflowJobsRefusalDoesNotFailPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseWorkflowJobs(http.StatusForbidden)

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })
	prompt := waitForLeadPrompt(t, server, " the CI of #41 \"Add plan model\" runs for more than 2h0m0s")
	if !strings.Contains(prompt, "did not complete: ci.") {
		t.Errorf("prompt = %s, want did not complete: ci.", prompt)
	}
	if calls := fake.CancelRequests(); calls != 0 {
		t.Errorf("cancel calls = %d, want 0", calls)
	}

	fake.RefuseWorkflowJobs(0)
	testkit.WaitFor(t, func() bool { return fake.CancelRequests() == 1 })
	waitForPolls(t, fake)
	if calls := fake.CancelRequests(); calls != 1 {
		t.Errorf("cancel calls = %d, want 1", calls)
	}
}

func TestQueuedRunRerunRefusedOnceAfterCancelRunsAgainAndTaskStaysInChecks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	runID := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseRerunOnce(http.StatusUnprocessableEntity)

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	testkit.WaitFor(t, func() bool { return fake.RerunRequests() == 2 })
	waitForPolls(t, fake)

	if calls := fake.CancelRequests(); calls != 1 {
		t.Errorf("cancel calls = %d, want 1", calls)
	}
	if calls := fake.RerunRequests(); calls != 2 {
		t.Errorf("rerun calls = %d, want 2", calls)
	}
	if attempt := fake.WorkflowRunAttempt(runID); attempt != 2 {
		t.Errorf("attempt = %d, want 2", attempt)
	}
	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s, want checks", state)
	}
}

func TestQueuedRunRerunRefusedAfterCancelNotBehindKeepsTaskInChecksAndNamesRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "build", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseRerun(http.StatusUnprocessableEntity)

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	testkit.WaitFor(t, func() bool { return fake.RerunRequests() == 2 })
	waitForPolls(t, fake)

	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s, want checks", state)
	}
	if calls := fake.UpdateBranchCalls(); calls != 0 {
		t.Errorf("update branch calls = %d, want 0", calls)
	}

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })
	prompt := waitForLeadPrompt(t, server, " the CI of #41 \"Add plan model\" runs for more than 2h0m0s")
	if !strings.Contains(prompt, "build (queued with no job)") {
		t.Errorf("prompt = %s, want build (queued with no job)", prompt)
	}
	if calls := fake.RerunRequests(); calls != 2 {
		t.Errorf("rerun calls = %d, want 2", calls)
	}
}

func TestQueuedRunSecondAttemptCancelledByAPersonIsAFailedWorkflow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	runID := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseRerunOnce(http.StatusUnprocessableEntity)

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	testkit.WaitFor(t, func() bool { return fake.WorkflowRunAttempt(runID) == 2 })
	if state := taskState(t, server); state != "checks" {
		t.Fatalf("state = %s, want checks", state)
	}

	fake.SetWorkflowRunStatus(runID, "completed", "cancelled")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
}

func TestQueuedRunCancelRefusedThenCancelledByAPersonIsAFailedWorkflow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	runID := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseCancel(http.StatusConflict)

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	testkit.WaitFor(t, func() bool { return fake.CancelRequests() == 1 })
	waitForPolls(t, fake)
	if state := taskState(t, server); state != "checks" {
		t.Fatalf("state = %s, want checks", state)
	}

	fake.SetWorkflowRunStatus(runID, "completed", "cancelled")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
}

func TestQueuedRunCancelledByMobiusWhileABreakIsOpenGivesNoBreakWaitAndRunsAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, longGrace)
	sha := checksHead(t, server, fake)
	hardBreak(t, server, fake)
	start := time.Now()
	runID := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseRerunOnce(http.StatusUnprocessableEntity)

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	testkit.WaitFor(t, func() bool { return fake.WorkflowRunAttempt(runID) == 2 })
	waitForPolls(t, fake)

	if got := breakWaitEvents(t, server); len(got) != 0 {
		t.Errorf("events = %q", got)
	}
	if got := breakWaitHead(t, server); got != "" {
		t.Errorf("break_wait_head = %s", got)
	}
	if state := taskState(t, server); state != "checks" {
		t.Errorf("state = %s, want checks", state)
	}
}

func TestQueuedRunRefusedRunStartedLaterIsNotNamedQueuedNoJob(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	runID := fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "build", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseCancel(http.StatusConflict)

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	waitForPolls(t, fake)

	if calls := fake.CancelRequests(); calls != 1 {
		t.Fatalf("cancel calls = %d, want 1", calls)
	}

	// GitHub starts the run later:
	fake.SetWorkflowRunStatus(runID, "in_progress", "")
	fake.AddWorkflowJob(shop, runID, 1, testkit.WorkflowJob{ID: 900, Name: "build-job", Status: "in_progress"})

	server.Engine.SetClock(func() time.Time { return start.Add(2*time.Hour + time.Minute) })
	prompt := waitForLeadPrompt(t, server, " the CI of #41 \"Add plan model\" runs for more than 2h0m0s")
	if strings.Contains(prompt, "build (queued with no job)") {
		t.Errorf("prompt = %s, should not contain build (queued with no job)", prompt)
	}
	if !strings.Contains(prompt, "build") {
		t.Errorf("prompt = %s, want build", prompt)
	}
}

func TestQueuedRunTemporaryErrorRetriesBranchStepWithoutRepeatingCancel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	start := time.Now()
	fake.AddWorkflowRun(shop, testkit.WorkflowRun{Name: "ci", HeadSHA: sha, Status: "queued", CreatedAt: start})
	fake.RefuseCancel(http.StatusConflict)
	fake.SetBehind(shop, 42)
	fake.CommitFile(shop, "price.txt", "dollars\n", "Add price")
	fake.RefuseBranchUpdateOnce(http.StatusInternalServerError)

	server.Engine.SetClock(func() time.Time { return start.Add(31 * time.Minute) })
	testkit.WaitFor(t, func() bool { return fake.UpdateBranchCalls() >= 2 })
	waitForPolls(t, fake)

	if calls := fake.CancelRequests(); calls != 1 {
		t.Errorf("cancel calls = %d, want 1", calls)
	}
	if calls := fake.UpdateBranchCalls(); calls != 2 {
		t.Errorf("update branch calls = %d, want 2", calls)
	}

	waitForPolls(t, fake)

	if calls := fake.CancelRequests(); calls != 1 {
		t.Errorf("cancel calls after done = %d, want 1", calls)
	}
	if calls := fake.UpdateBranchCalls(); calls != 2 {
		t.Errorf("update branch calls after done = %d, want 2", calls)
	}
}
