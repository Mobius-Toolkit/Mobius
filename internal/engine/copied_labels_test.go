package engine_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const taskInNeedsHuman = `INSERT INTO tasks (repository, issue, workstream, state, dispatched_at)
	VALUES ('owner/shop', 41, 12, 'needs_human', '2026-09-30T00:00:00Z')`

// startLabeled starts a server with the Workstream #12 and its sub-issue #41. The poll interval is longer than the test,
// so only the first poll runs. #41 has mobius:needs-human when stopped is true. The SQL statement before runs before the server starts.
func startLabeled(t *testing.T, fake *testkit.FakeGitHub, stopped bool, before string) (*testserver.Server, <-chan engine.Change) {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	if stopped {
		fake.AddLabel(shop, 41, "mobius:needs-human", "owner")
	}
	dataDir := t.TempDir()
	// The App exists before the server starts, so the first poll of the server reads the repository, and no other poll runs.
	db, err := store.Open(t.Context(), filepath.Join(dataDir, "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		fmt.Sprintf("INSERT INTO github_apps (app_id, slug, private_key, client_id, client_secret) VALUES (%d, '%s', '%s', '%s', '%s')",
			testkit.AppID, testkit.AppSlug, testkit.AppPrivateKey, testkit.AppClientID, testkit.AppClientSecret),
		before,
	} {
		if statement == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := testserver.Config(t, dataDir)
	cfg.PollInterval = time.Hour
	fake.AddRepository(shop)
	server := testserver.StartWith(t, cfg, fake.URL)
	server.WaitForFirstPoll(t, shop)
	authorize(t, server)
	return server, listen(t, server)
}

func postIssue(t *testing.T, server *testserver.Server, action string) (int, string) {
	t.Helper()
	return send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/"+action, "")
}

func needsHumanRows(t *testing.T, server *testserver.Server) int {
	t.Helper()
	return len(apiData[[]needsHuman](t, server, "/api/needs-human"))
}

func checkTasks(t *testing.T, server *testserver.Server, state string) {
	t.Helper()
	want := []taskLine{line(41, "Add plan model", state, 0)}
	if got := apiData[[]taskLine](t, server, "/api/workstreams/owner/shop/12/tasks"); !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %+v, want %+v", got, want)
	}
}

func TestResumeOfALiveTaskChangesTheCopyBeforeTheNextPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, changes := startLabeled(t, fake, true, taskInNeedsHuman)
	if rows := needsHumanRows(t, server); rows != 1 {
		t.Fatalf("rows = %d", rows)
	}
	checkTasks(t, server, "needs-human")

	if status, body := postIssue(t, server, "resume"); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if rows := needsHumanRows(t, server); rows != 0 {
		t.Errorf("rows = %d", rows)
	}
	checkTasks(t, server, "ready")
	waitForWorkstreams(t, changes)
}

func TestResumeOfAnIssueWithNoTaskChangesTheCopyBeforeTheNextPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, changes := startLabeled(t, fake, true, "")

	if status, body := postIssue(t, server, "resume"); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if rows := needsHumanRows(t, server); rows != 0 {
		t.Errorf("rows = %d", rows)
	}
	checkTasks(t, server, "ready")
	waitForWorkstreams(t, changes)
}

func TestStartChangesTheCopyBeforeTheNextPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, changes := startLabeled(t, fake, false, "")
	checkTasks(t, server, "open")

	if status, body := postIssue(t, server, "start"); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	checkTasks(t, server, "ready")
	waitForWorkstreams(t, changes)
}

func TestAFailedLabelWriteOfResumeKeepsTheCopy(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, changes := startLabeled(t, fake, true, "")
	fake.FailAddLabels(shop, 41, true)

	if status, _ := postIssue(t, server, "resume"); status == http.StatusNoContent {
		t.Fatal("status = 204")
	}

	if rows := needsHumanRows(t, server); rows != 1 {
		t.Errorf("rows = %d", rows)
	}
	checkTasks(t, server, "needs-human")
	noWorkstreams(t, changes)
}

func TestAFailedLabelWriteOfStartKeepsTheCopy(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, changes := startLabeled(t, fake, false, "")
	fake.FailAddLabels(shop, 41, true)

	if status, _ := postIssue(t, server, "start"); status == http.StatusNoContent {
		t.Fatal("status = 204")
	}

	checkTasks(t, server, "open")
	noWorkstreams(t, changes)
}

func TestThePollShowsTheRowAgainWhenGitHubStillHasNeedsHuman(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := startLabeled(t, fake, true, taskInNeedsHuman)
	if status, body := postIssue(t, server, "resume"); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	if rows := needsHumanRows(t, server); rows != 0 {
		t.Fatalf("rows = %d", rows)
	}

	fake.AddLabel(shop, 41, "mobius:needs-human", "owner")
	server.Engine.Poll(t.Context())

	if rows := needsHumanRows(t, server); rows != 1 {
		t.Errorf("rows = %d", rows)
	}
}
