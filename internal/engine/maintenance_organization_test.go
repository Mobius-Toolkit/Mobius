package engine_test

import (
	"reflect"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestAHardBreakInAnotherRepositoryGetsAnIssueThereBelowTheMaintenanceWorkstreamOfTheIssueRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(garden, failedMainRun(7))
	fake.AddWorkflowJob(garden, run, 1, testkit.WorkflowJob{ID: 901, Name: "test", Conclusion: "failure", Steps: []testkit.WorkflowStep{{Name: "Run go test", Conclusion: "failure"}}})
	fake.AddJobLog(901, "--- FAIL: TestPlan\n")
	addMaintenanceWorkstream(fake, maintenance)
	server := issueRepositoryShop(t, fake, "")
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "failure")

	numbers := testkit.WaitForValue(t, func() ([]int64, bool) {
		numbers := fake.SubIssueNumbers(shop, maintenance)
		return numbers, len(numbers) == 1
	})
	title, _ := fake.Issue(garden, numbers[0])
	if title != `Fix the failed workflow "ci" on main` {
		t.Errorf("title of the issue in owner/garden = %q", title)
	}
	task := testkit.WaitForValue(t, func() (store.Task, bool) {
		task, err := store.New(server.DB).GetLiveTask(t.Context(), store.GetLiveTaskParams{Repository: garden, Issue: numbers[0]})
		return task, err == nil
	})
	if task.WorkstreamRepository != shop || task.Workstream != maintenance {
		t.Errorf("task = %+v", task)
	}
	if fake.HasIssue(shop, numbers[0]) {
		t.Errorf("owner/shop has the issue #%d", numbers[0])
	}
}

func TestACuratorCreatesTheIssueInItsRepositoryBelowTheMaintenanceWorkstreamOfTheIssueRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addMaintenanceWorkstream(fake, maintenance)
	server := issueRepositoryShop(t, fake, call("create_issue", `{ title = "Fix the flaky test", body = "The test fails in CI.", blocked_by = [40] }`))
	fake.AddIssue(garden, 40, "Plan table")
	spec := roleSpec(t, engine.CuratorRole)
	spec.Repository = garden

	session := run(t, server, spec, "Create the issue")

	if got := reply(t, server, session); got != "Created owner/garden#41." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.SubIssueNumbers(shop, maintenance); !reflect.DeepEqual(got, []int64{41}) {
		t.Errorf("sub-issues = %v", got)
	}
	if title, _ := fake.Issue(garden, 41); title != "Fix the flaky test" {
		t.Errorf("title of owner/garden#41 = %q", title)
	}
	if got := fake.Labels(garden, 41); !reflect.DeepEqual(got, []string{"mobius:triage"}) {
		t.Errorf("labels = %v", got)
	}
	if got := fake.BlockerNumbers(garden, 41); !reflect.DeepEqual(got, []int64{40}) {
		t.Errorf("blockers = %v", got)
	}
	if fake.HasIssue(shop, 41) {
		t.Error("owner/shop has the issue #41")
	}
}

func TestABreakGetsNoIssueWithoutAnIssueRepositoryAndTheLaterPollStepsStillRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	run := fake.AddWorkflowRun(garden, failedMainRun(7))
	addMaintenanceWorkstream(fake, maintenance)
	server := foreignTask(t, fake)
	waitForCIBreak(t, server, ciBreak{runID: run, rerun: true})

	fake.SetWorkflowRunStatus(run, "completed", "success")
	waitForCIBreak(t, server, ciBreak{runID: run, endedRunID: run, rerun: true, result: "flaky"})
	fake.AddLabel(garden, 7, "mobius:ready", "owner")

	if task := gardenTask(t, server); task.Workstream != 12 {
		t.Errorf("task = %+v", task)
	}
	waitForPolls(t, fake)
	if numbers := fake.SubIssueNumbers(shop, maintenance); len(numbers) != 0 {
		t.Errorf("sub-issues = %v", numbers)
	}
	if fake.HasIssue(garden, 8) {
		t.Error("owner/garden has the issue #8")
	}
}
