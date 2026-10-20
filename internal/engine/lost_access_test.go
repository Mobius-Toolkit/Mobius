package engine_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func TestARestartDuringARefusedReadOfTheAppRepositoriesKeepsTheLiveTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
	testkit.InstallFakeAgent(t, dataDir, options+hangs)
	seed(t, dataDir,
		`INSERT INTO tasks (id, repository, workstream_repository, issue, workstream, state, dispatched_at, state_at, branch, pull_request)
		 VALUES (1, 'owner/shop', 'owner/shop', 41, 12, 'checks', '2026-10-04T10:00:00Z', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), 'mobius/41', 42)`)
	cfg := testserver.Config(t, dataDir)
	cfg.ReviewQuietPeriod = time.Hour
	fake.FailInstallations(testkit.AppID, true)
	server := startServerWith(t, fake, cfg, "")
	fake.PushCommit(shop, "mobius/41", "Add plan model")
	if number := fake.OpenPullRequest(shop, "Add plan model", "mobius/41"); number != 42 {
		t.Fatalf("pull request = %d", number)
	}
	refused := fake.InstallationReads()
	testkit.WaitFor(t, func() bool { return fake.InstallationReads() >= refused+3 })

	fake.FailInstallations(testkit.AppID, false)
	server.WaitForFirstPoll(t, shop)

	var id int64
	var state string
	if err := server.DB.QueryRow("SELECT id, state FROM tasks WHERE repository = ? AND issue = 41 AND state <> 'ended'", shop).Scan(&id, &state); err != nil {
		t.Fatal(err)
	}
	if id != 1 || state != "checks" {
		t.Errorf("task = %d in %s", id, state)
	}
	if labels := fake.Labels(shop, 41); !reflect.DeepEqual(labels, []string{"mobius:working"}) {
		t.Errorf("labels = %q", labels)
	}
}

func TestACompleteReadWithNoAccessToTheRepositoryEndsItsLiveTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := seedWaiting(t, fake, "checks")

	fake.RemoveRepository(shop)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
}
