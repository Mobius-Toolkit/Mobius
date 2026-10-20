package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const garden = "owner/garden"

// foreignTask starts a server with the Workstream #12 of owner/shop and the issue #7 of owner/garden below it. The
// issue has no mobius:ready yet.
func foreignTask(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	server := connectSeen(t, fake)
	fake.AddForeignSubIssue(shop, 12, garden, 7)
	server.WaitForFirstPoll(t, garden)
	return server
}

func gardenTask(t *testing.T, server *testserver.Server) store.Task {
	t.Helper()
	return testkit.WaitForValue(t, func() (store.Task, bool) {
		task, err := store.New(server.DB).GetLiveTask(t.Context(), store.GetLiveTaskParams{Repository: garden, Issue: 7})
		return task, err == nil
	})
}

func TestAnIssueBelowAWorkstreamInAnotherRepositoryGetsATaskOfThatWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := foreignTask(t, fake)

	fake.AddLabel(garden, 7, "mobius:ready", "owner")

	task := gardenTask(t, server)
	if task.Repository != garden || task.WorkstreamRepository != shop || task.Workstream != 12 || task.State != "dispatched" {
		t.Errorf("task = %+v", task)
	}
	testkit.WaitFor(t, func() bool { return slices.Equal(fake.Labels(garden, 7), []string{"mobius:working"}) })
	event := testkit.WaitForValue(t, func() (store.LeadEvent, bool) {
		var event store.LeadEvent
		err := server.DB.QueryRow("SELECT repository, workstream, issue, kind FROM lead_events").Scan(&event.Repository, &event.Workstream, &event.Issue, &event.Kind)
		return event, err == nil
	})
	if event.Repository != shop || event.Workstream != 12 || event.Issue.Int64 != 7 || event.Kind != "dispatch" {
		t.Errorf("event = %+v", event)
	}
	testkit.WaitFor(t, func() bool {
		for _, line := range chatLines(t, server, leadChat) {
			if line.Author == "Event" && strings.Contains(line.Text, "Water the plants") {
				return true
			}
		}
		return false
	})
}

func TestAnOwnerCommentOnTheIssueOfATaskInAnotherRepositoryGoesToTheLeadOfTheWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := foreignTask(t, fake)
	fake.AddLabel(garden, 7, "mobius:ready", "owner")
	gardenTask(t, server)
	testkit.WaitFor(t, func() bool { return slices.Equal(fake.Labels(garden, 7), []string{"mobius:working"}) })

	fake.AddComment(garden, 7, "owner", "Water them twice a day.")

	testkit.WaitFor(t, func() bool {
		for _, line := range chatLines(t, server, leadChat) {
			if line.Author == "Event" && strings.Contains(line.Text, "Water them twice a day.") {
				return true
			}
		}
		return false
	})
}

func TestAnIssueWithAParentInAnUnreadableRepositoryGetsNoTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	fake.AddIssue("other/hidden", 5, "A Workstream that the App cannot read")
	fake.AddLabel("other/hidden", 5, "mobius:workstream", "owner")
	fake.AddIssue(garden, 7, "Water the plants")
	fake.AddForeignSubIssue("other/hidden", 5, garden, 7)
	server := connectSeen(t, fake)
	server.WaitForFirstPoll(t, garden)

	fake.AddLabel(garden, 7, "mobius:ready", "owner")

	waitForPolls(t, fake)
	var tasks int
	if err := server.DB.QueryRow("SELECT count(*) FROM tasks").Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 || !slices.Equal(fake.Labels(garden, 7), []string{"mobius:ready"}) {
		t.Errorf("tasks = %d, labels = %v", tasks, fake.Labels(garden, 7))
	}
}
