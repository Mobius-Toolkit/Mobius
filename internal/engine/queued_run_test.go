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
			waitForPolls(t, fake)

			if calls := fake.UpdateBranchCalls(); calls != 1 {
				t.Errorf("update branch calls = %d, want 1", calls)
			}

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

	cancels := fake.CancelRequests()
	reruns := fake.RerunRequests()
	updates := fake.UpdateBranchCalls()

	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if got := fake.CancelRequests(); got != cancels {
		t.Errorf("cancel calls = %d, want %d", got, cancels)
	}
	if got := fake.RerunRequests(); got != reruns {
		t.Errorf("rerun calls = %d, want %d", got, reruns)
	}
	if got := fake.UpdateBranchCalls(); got != updates {
		t.Errorf("update branch calls = %d, want %d", got, updates)
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
}
