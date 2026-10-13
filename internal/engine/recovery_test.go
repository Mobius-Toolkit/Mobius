package engine_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const recoveryLead = options + "[[prompts]]\nreply = [\"Seen\"]\n"

// seedEnded starts a server with an ended task of #41 that has the branch mobius/41, the pull request #42 and the state
// before the end checks, with a check run that runs. The issue #41 has the labels. The pull request is open, unless
// prepare changes it.
func seedEnded(t *testing.T, fake *testkit.FakeGitHub, labels []string, recoveries int, prepare func(), more ...string) *testserver.Server {
	t.Helper()
	dataDir := t.TempDir()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	for _, label := range labels {
		fake.AddLabel(shop, 41, label, testkit.AppSlug+"[bot]")
	}
	testkit.InstallFakeAgent(t, dataDir, recoveryLead)
	seed(t, dataDir, append([]string{`INSERT INTO tasks (id, repository, issue, workstream, state, dispatched_at, state_at, branch, pull_request, recoveries, fix_rounds, check_head)
		 VALUES (1, 'owner/shop', 41, 12, 'ended', '2026-10-04T10:00:00Z', '2026-10-04T11:00:00Z', 'mobius/41', 42, ` + strconv.Itoa(recoveries) + `, 2, 'abc')`,
		`INSERT INTO task_states (task, state, issue, workstream, organization, repository, started_at, ended_at)
		 VALUES (1, 'working', 41, 12, 'owner', 'owner/shop', '2026-10-04T10:00:00Z', '2026-10-04T10:30:00Z'),
		        (1, 'checks', 41, 12, 'owner', 'owner/shop', '2026-10-04T10:30:00Z', '2026-10-04T11:00:00Z')`}, more...)...)
	cfg := testserver.Config(t, dataDir)
	cfg.MaxWorkerRestarts = 2
	cfg.ReviewQuietPeriod = 0
	fake.AddRepository(shop)
	fake.PushCommit(shop, "mobius/41", "Add plan model")
	fake.OpenPullRequest(shop, "Add plan model", "mobius/41")
	fake.AddCheckRun(shop, checkRun("build", head(t, fake, "mobius/41"), "in_progress", ""))
	if prepare != nil {
		prepare()
	}
	server := testserver.StartWith(t, cfg, fake.URL)
	if _, err := server.DB.Exec("INSERT INTO github_apps (app_id, slug, private_key, client_id, client_secret) VALUES (?, ?, ?, ?, ?)",
		testkit.AppID, testkit.AppSlug, testkit.AppPrivateKey, testkit.AppClientID, testkit.AppClientSecret); err != nil {
		t.Fatal(err)
	}
	server.WaitForFirstPoll(t, shop)
	return server
}

func recoveryEvents(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var texts []string
	for _, event := range leadEvents(t, server) {
		if event.Kind == "recovery" {
			texts = append(texts, event.Payload)
		}
	}
	return texts
}

func taskRow(t *testing.T, server *testserver.Server, id int64) (state, branch string, pullRequest int64, recoveries int, needsHumanAt sql.NullString) {
	t.Helper()
	err := server.DB.QueryRow("SELECT state, branch, pull_request, recoveries, needs_human_at FROM tasks WHERE id = ?", id).Scan(&state, &branch, &pullRequest, &recoveries, &needsHumanAt)
	if err != nil {
		t.Fatal(err)
	}
	return state, branch, pullRequest, recoveries, needsHumanAt
}

func TestAStartContinuesTheEndedTaskOfAWorkingIssueWithItsBranchAndPullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	server := seedEnded(t, fake, []string{"mobius:working"}, 0, nil)

	testkit.WaitFor(t, func() bool { return len(recoveryEvents(t, server)) == 1 })
	state, branch, pullRequest, recoveries, _ := taskRow(t, server, 1)
	if state != "checks" || branch != "mobius/41" || pullRequest != 42 || recoveries != 1 {
		t.Errorf("task = %s %s %d %d", state, branch, pullRequest, recoveries)
	}
	if task := liveTaskOf(t, server, 41); task.ID != 1 || task.FixRounds != 2 {
		t.Errorf("task = %+v", task)
	}
	event := recoveryEvents(t, server)[0]
	for _, part := range []string{`recovery of #41 "Add plan model": Mobius restarted and found no live task.`, "in checks with the branch mobius/41 and the pull request #42 (recovery 1 of 2).", "are lost."} {
		if !strings.Contains(event, part) {
			t.Errorf("event = %q", event)
		}
	}
	if items := inbox(t, server); len(items) != 0 {
		t.Errorf("inbox = %+v", items)
	}
	if labels := fake.Labels(shop, 41); !reflect.DeepEqual(labels, []string{"mobius:working"}) {
		t.Errorf("labels = %q", labels)
	}
	if len(fake.PullRequests(shop)) != 1 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
}

func TestAStartContinuesTheEndedTaskOfAnIssueForHumanReviewInReadyForReview(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	server := seedEnded(t, fake, []string{"mobius:review"}, 0, nil)

	testkit.WaitFor(t, func() bool { return len(recoveryEvents(t, server)) == 1 })
	if state, _, _, recoveries, _ := taskRow(t, server, 1); state != "ready_for_review" || recoveries != 1 {
		t.Errorf("task = %s %d", state, recoveries)
	}
}

func TestAStartContinuesTheEndedTaskOfAnIssueWithNeedsHumanInNeedsHumanAndKeepsItsInboxItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := seedEnded(t, fake, []string{"mobius:needs-human"}, 0, nil,
		`INSERT INTO inbox_items (kind, organization, repository, workstream, issue, title, reason, text, link, time)
		 VALUES ('stopped', 'owner', 'owner/shop', 12, 41, 'Add plan model', 'Stopped', 'The task stopped.', '', '2026-10-04T11:00:00Z')`)

	testkit.WaitFor(t, func() bool { return hasLiveTask(t, server, 41) })

	state, _, _, _, needsHumanAt := taskRow(t, server, 1)
	if state != "needs_human" || !needsHumanAt.Valid {
		t.Errorf("task = %s %v", state, needsHumanAt)
	}
	if items := itemsOf(t, server, "stopped"); len(items) != 1 {
		t.Errorf("stopped items = %+v", items)
	}
	if items := itemsOf(t, server, "lost"); len(items) != 0 {
		t.Errorf("lost items = %+v", items)
	}
}

func TestAStartHandsAnEndedTaskAtTheLimitOfRecoveriesToAHuman(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	server := seedEnded(t, fake, []string{"mobius:working"}, 2, nil)

	item := waitForItem(t, server, "lost")
	if item.Issue != 41 {
		t.Errorf("item = %+v", item)
	}
	if labels := fake.Labels(shop, 41); !reflect.DeepEqual(labels, []string{"mobius:needs-human"}) {
		t.Errorf("labels = %q", labels)
	}
	if hasLiveTask(t, server, 41) {
		t.Error("the task is live")
	}
}

func TestAStartHandsAnEndedTaskWithAMergedPullRequestToAHuman(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	server := seedEnded(t, fake, []string{"mobius:working"}, 0, func() { fake.MergePullRequest(shop, 42) })

	waitForItem(t, server, "lost")
	if hasLiveTask(t, server, 41) {
		t.Error("the task is live")
	}
	if events := recoveryEvents(t, server); len(events) != 0 {
		t.Errorf("events = %q", events)
	}
}

func TestResumeContinuesTheEndedTaskAfterTheLimitOfRecoveriesWithItsBranchAndPullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := seedEnded(t, fake, []string{"mobius:working"}, 2, nil)
	waitForItem(t, server, "lost")
	authorize(t, server)

	resume(t, server)

	testkit.WaitFor(t, func() bool { return len(recoveryEvents(t, server)) == 1 })
	task := liveTaskOf(t, server, 41)
	if task.ID != 1 || task.Branch.String != "mobius/41" || task.PullRequest.Int64 != 42 || task.Recoveries != 0 {
		t.Errorf("task = %+v", task)
	}
	if event := recoveryEvents(t, server)[0]; !strings.Contains(event, "the Owner continued the issue, and it had no live task. Mobius continued the earlier task with the branch mobius/41 and the pull request #42.") {
		t.Errorf("event = %q", event)
	}
	if pulls := fake.PullRequests(shop); len(pulls) != 1 {
		t.Errorf("pull requests = %+v", pulls)
	}
	var tasks int
	if err := server.DB.QueryRow("SELECT count(*) FROM tasks").Scan(&tasks); err != nil || tasks != 1 {
		t.Errorf("tasks = %d, %v", tasks, err)
	}
}

func TestAReadyIssueWithAnEndedTaskWhosePullRequestMergedGetsANewTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := seedEnded(t, fake, nil, 0, func() { fake.MergePullRequest(shop, 42) })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	task := liveTaskOf(t, server, 41)

	if task.ID == 1 || task.Branch.String == "mobius/41" {
		t.Errorf("task = %+v", task)
	}
}
