package engine_test

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// addGardenTask adds the Workstream #12 of owner/shop with the issue #7 of owner/garden below it.
func addGardenTask(fake *testkit.FakeGitHub) {
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	fake.AddForeignSubIssue(shop, 12, garden, 7)
}

func TestAutopilotDispatchesATaskIssueOfAnotherRepositoryWithTheKeyOfTheWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addGardenTask(fake)
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")

	server := startAutopilot(t, fake, func(*config.Config) {}, "")

	task := gardenTask(t, server)
	if task.WorkstreamRepository != shop || task.Workstream != 12 || task.State != "dispatched" {
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
}

func TestACloseOfAWorkstreamClosesATaskIssueOfAnotherRepositoryWithItsPullRequestAndRemovesItsLabels(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addGardenTask(fake)
	fake.AddLabel(garden, 7, "mobius:working", testkit.AppSlug+"[bot]")
	fake.AddPullRequest(garden, 8, "Water the plants")
	server := startServer(t, fake, t.TempDir(), `INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, pull_request)
		VALUES ('owner/garden', 'owner/shop', 7, 12, 'working', '2026-10-04T10:00:00Z', 8)`)
	server.WaitForFirstPoll(t, shop)
	server.WaitForFirstPoll(t, garden)

	fake.CloseIssue(shop, 12)

	testkit.WaitFor(t, func() bool { state, _ := fake.State(garden, 7); return state == "closed" })
	if state, reason := fake.State(garden, 7); state != "closed" || reason != "not_planned" {
		t.Errorf("state of the issue = %s, %s", state, reason)
	}
	testkit.WaitFor(t, func() bool { state, _ := fake.State(garden, 8); return state == "closed" })
	if labels := fake.Labels(garden, 7); len(labels) != 0 {
		t.Errorf("labels = %v", labels)
	}
}

func TestTheWontDoClosureAndTheDetailsOfAWorkstreamHaveATaskIssueOfAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addGardenTask(fake)
	server := startCopied(t, fake)

	items := closure(t, server)

	if len(items) != 2 || items[0].Number != 7 || !strings.Contains(items[0].URL, "/owner/garden/issues/7") || items[1].Number != 12 {
		t.Errorf("items = %+v", items)
	}
	if status, got := details(t, server, 12); status != http.StatusOK || got.OpenTasks != 1 || got.CompletedTasks != 0 {
		t.Errorf("details = %d %+v", status, got)
	}
}

func TestTheNeedsHumanListHasAStoppedTaskInAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addGardenTask(fake)
	fake.AddLabel(garden, 7, "mobius:needs-human", "owner")
	fake.AddPullRequest(garden, 8, "Water the plants")
	server := startServer(t, fake, t.TempDir(), `INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, pull_request)
		VALUES ('owner/garden', 'owner/shop', 7, 12, 'needs_human', '2026-10-04T10:00:00Z', 8);
		INSERT INTO inbox_items (kind, repository, workstream, issue, text, link, time)
		VALUES ('stopped', 'owner/garden', 12, 7, 'Task #7 "Water the plants" stopped: no push.', '', '2026-10-04T10:00:00Z')`)

	pullRequest := int64(8)
	pullRequestURL := "https://github.com/owner/garden/pull/8"
	want := []needsHuman{{Repository: shop, Workstream: 12, Number: 7, Title: "Water the plants", URL: "https://github.com/owner/garden/issues/7", Stopped: true, PullRequest: &pullRequest, PullRequestURL: &pullRequestURL, Reason: `Task #7 "Water the plants" stopped: no push.`}}
	testkit.WaitFor(t, func() bool { return reflect.DeepEqual(apiData[[]needsHuman](t, server, "/api/needs-human"), want) })
}

func TestAPollRemovesTheStateLabelsOfATaskIssueOfAnotherRepositoryWhenItsTaskEnded(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addGardenTask(fake)
	server := startServer(t, fake, t.TempDir(), `INSERT INTO tasks (id, repository, workstream_repository, issue, workstream, state, dispatched_at, state_at)
		VALUES (1, 'owner/garden', 'owner/shop', 7, 12, 'ended', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z')`)
	server.WaitForFirstPoll(t, shop)
	server.WaitForFirstPoll(t, garden)

	for _, label := range []string{"mobius:working", "mobius:review", "mobius:question", "mobius:needs-human"} {
		fake.AddLabel(garden, 7, label, testkit.AppSlug+"[bot]")
	}

	testkit.WaitFor(t, func() bool { return len(fake.Labels(garden, 7)) == 0 })
}

func TestSearchIssuesGivesAnIssueOfAnotherRepositoryWithItsRepositoryAndItsOwnComments(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(garden)
	server, _ := connect(t, fake, call("search_issues", `{ query = "repo:owner/garden flaky" }`))
	addMaintenanceWorkstream(fake, 20)
	fake.AddIssue(shop, 40, "Flaky CI of the shop")
	fake.AddIssue(garden, 40, "Flaky CI of the garden")
	fake.CloseIssueAs(garden, 40, "not_planned")
	fake.AddComment(garden, 40, "owner", "Comment of the garden.")

	session := run(t, server, roleSpec(t, engine.CuratorRole), "1. ")

	text := reply(t, server, session)
	for _, part := range []string{"owner/garden#40 Flaky CI of the garden (closed, not_planned)", "Comment of the garden."} {
		if !strings.Contains(text, part) {
			t.Errorf("%q is not in %q", part, text)
		}
	}
	if strings.Contains(text, "of the shop") {
		t.Errorf("text = %q", text)
	}
}
