package engine_test

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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

	if len(items) != 2 || items[0].Number != 7 || items[0].Repository != garden || !strings.Contains(items[0].URL, "/owner/garden/issues/7") || items[1].Number != 12 || items[1].Repository != shop {
		t.Errorf("items = %+v", items)
	}
	if status, got := details(t, server, 12); status != http.StatusOK || got.OpenTasks != 1 || got.CompletedTasks != 0 {
		t.Errorf("details = %d %+v", status, got)
	}
}

func TestAWontDoCloseWritesToATaskIssueAndAPullRequestOfAnotherRepositoryAndNotToTheIssuesOfTheWorkstreamRepositoryWithTheSameNumbers(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addGardenTask(fake)
	fake.AddPullRequest(garden, 8, "Water the plants")
	fake.AddIssue(shop, 7, "Price table")
	fake.AddIssue(shop, 8, "Plan names")
	server := startServer(t, fake, t.TempDir(), `INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, pull_request)
		VALUES ('owner/garden', 'owner/shop', 7, 12, 'working', '2026-10-04T10:00:00Z', 8)`)
	server.WaitForFirstPoll(t, shop)
	server.WaitForFirstPoll(t, garden)

	if status, body := closeWontDo(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	for _, number := range []int64{7, 8} {
		if state, _ := fake.State(garden, number); state != "closed" {
			t.Errorf("state of %s#%d = %s", garden, number, state)
		}
		if comments := fake.Comments(garden, number); !slices.Contains(comments, wontDoComment) {
			t.Errorf("comments of %s#%d = %v", garden, number, comments)
		}
		if !slices.Contains(fake.Labels(garden, number), wontDo) {
			t.Errorf("labels of %s#%d = %v", garden, number, fake.Labels(garden, number))
		}
		unchanged(t, fake, shop, number, "open")
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
		INSERT INTO lead_events (repository, workstream, issue_repository, issue, kind, payload, time)
		VALUES ('owner/shop', 12, 'owner/garden', 7, 'stop', '2026-10-04 10:00 UTC stop of #7 "Water the plants": no push.', '2026-10-04T10:00:00Z')`)

	pullRequest := int64(8)
	pullRequestURL := "https://github.com/owner/garden/pull/8"
	want := []needsHuman{{Repository: shop, Workstream: 12, Number: 7, Title: "Water the plants", URL: "https://github.com/owner/garden/issues/7", Stopped: true, PullRequest: &pullRequest, PullRequestURL: &pullRequestURL, Reason: "no push."}}
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
	server, _ := connectWith(t, fake, call("search_issues", `{ query = "repo:owner/garden flaky" }`), func(cfg *config.Config) {
		cfg.Organizations = map[string]config.Organization{"owner": {IssueRepository: shop}}
	})
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

func TestAStopOfATaskInAnotherRepositoryWhoseCheckWriteFailsAddsNoSecondStopEventToTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addGardenTask(fake)
	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")
	fake.PushCommit(garden, "mobius/7", "Water the plants")
	sha := testkit.Git(t, fake.Remote(garden), "rev-parse", "mobius/7")
	if number := fake.OpenPullRequest(garden, "Water the plants", "mobius/7"); number != 8 {
		t.Fatalf("pull request = %d", number)
	}
	fake.AddCheckRun(garden, checkRun("Mobius", sha, "in_progress", ""))
	server := startAutopilot(t, fake, func(*config.Config) {}, "")
	gardenTask(t, server)
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'working', worker = NULL, worker_restarts = 3, pull_request = 8 WHERE repository = ? AND issue = 7", garden); err != nil {
		t.Fatal(err)
	}
	fake.FailUpdateCheckRuns(true)

	restart, _ := server.Engine.RestartWorker(t.Context(), gardenTask(t, server), "Water the plants", errors.New("the Worker died"))

	if restart {
		t.Fatal("the Worker restarted")
	}
	testkit.WaitFor(t, func() bool { return countLeadEvents(t, server, "stop") == 1 })
	waitForPolls(t, fake)
	if events := countLeadEvents(t, server, "stop"); events != 1 {
		t.Errorf("stop events = %d", events)
	}
}

func TestAFollowUpThreadOfATaskInAnotherRepositoryWhoseResolveFailsAddsNoSecondFollowUpEventToTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addGardenTask(fake)
	fake.AddLabel(garden, 7, "mobius:needs-human", testkit.AppSlug+"[bot]")
	fake.PushCommit(garden, "mobius/7", "Water the plants")
	if number := fake.OpenPullRequest(garden, "Water the plants", "mobius/7"); number != 8 {
		t.Fatalf("pull request = %d", number)
	}
	fake.AddLabel(garden, 8, "mobius:needs-human", testkit.AppSlug+"[bot]")
	fake.AddReviewComment(garden, 8, 0, testkit.AppSlug+"[bot]", "Name the unit.\n\n<!-- mobius:follow-up -->")
	fake.FailResolveThreads(true)
	server := startServer(t, fake, t.TempDir(), `INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, state_at, pull_request)
		VALUES ('owner/garden', 'owner/shop', 7, 12, 'needs_human', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z', 8)`)
	server.WaitForFirstPoll(t, shop)
	server.WaitForFirstPoll(t, garden)

	fake.RemoveLabel(garden, 7, "mobius:needs-human", "owner")

	testkit.WaitFor(t, func() bool { return countLeadEvents(t, server, "follow_up") == 1 })
	waitForPolls(t, fake)
	if events := countLeadEvents(t, server, "follow_up"); events != 1 {
		t.Errorf("follow-up events = %d", events)
	}
}

func TestTheFullSlotsActivityNamesATaskIssueOfAnotherRepositoryWithItsRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Add plan model")
	server := startAutopilot(t, fake, func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 }, "")
	liveTaskOf(t, server, 41)
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	fake.AddForeignSubIssue(shop, 12, garden, 7)
	since := fullSlotsSince(t, server)

	setClock(server, since.Add(31*time.Minute))
	testkit.WaitFor(t, func() bool { return len(fullSlotsActivities(t, server)) == 1 })

	want := `Autopilot could not start ` + garden + `#7 "Water the plants".`
	if item := fullSlotsActivities(t, server)[0]; item.Repository != shop || !strings.Contains(item.Text, want) {
		t.Errorf("activity = %+v, want text with %q", item, want)
	}
}
