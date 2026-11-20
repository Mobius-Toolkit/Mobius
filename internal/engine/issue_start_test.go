package engine_test

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func startRowOf(t *testing.T, server *testserver.Server, repository string, number int64) (sql.NullString, string) {
	t.Helper()
	var at sql.NullString
	var by string
	err := server.DB.QueryRow(`SELECT start_requested_at, start_requested_by FROM issues WHERE repository = ? AND number = ?`, repository, number).Scan(&at, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return at, by
}

func hasStart(t *testing.T, server *testserver.Server, number int64) bool {
	t.Helper()
	at, by := startRowOf(t, server, shop, number)
	return at.Valid && by != ""
}

func startByOf(t *testing.T, server *testserver.Server, repository string, number int64) string {
	t.Helper()
	at, by := startRowOf(t, server, repository, number)
	if !at.Valid {
		return ""
	}
	return by
}

func TestATrustedAddOfMobiusReadySetsTheStartColumnsAndTheSendRemovesTheLabelWithNoNewIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 40, "Add the price table")
	fake.AddBlockedBy(shop, 41, 40)
	server := startCopied(t, fake)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool {
		return startByOf(t, server, shop, 41) == "owner" &&
			!slices.Contains(fake.Labels(shop, 41), "mobius:ready") &&
			doneChangesOfPersons(t, server) == 1 &&
			incomingChanges(t, server, "pending") == 0 &&
			outgoingChanges(t, server, "pending") == 0 &&
			ownWrites(t, server) == 0
	})
	changes := countRows(t, server, "SELECT count(*) FROM incoming_changes")
	waitForPolls(t, fake)
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes"); got != changes {
		t.Errorf("incoming_changes = %d, want %d", got, changes)
	}
	if got := startByOf(t, server, shop, 41); got != "owner" {
		t.Errorf("start_requested_by = %q", got)
	}
}

func TestAnUntrustedAddOfMobiusReadySetsNoStartColumnAndTheSendRemovesTheLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)

	fake.AddLabel(shop, 41, "mobius:ready", "mallory")

	testkit.WaitFor(t, func() bool {
		return !slices.Contains(fake.Labels(shop, 41), "mobius:ready") &&
			doneChangesOfPersons(t, server) == 1 &&
			incomingChanges(t, server, "pending") == 0 &&
			outgoingChanges(t, server, "pending") == 0 &&
			ownWrites(t, server) == 0
	})
	waitForPolls(t, fake)
	if hasStart(t, server, 41) {
		t.Errorf("start_requested_by = %q", startByOf(t, server, shop, 41))
	}
	if hasLiveTask(t, server, 41) {
		t.Error("the untrusted add started a task")
	}
}

func TestAStartOfAnIssueWithAnOpenBlockerKeepsItsColumnsAndDispatchesAfterTheBlockerCloses(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 40, "Add the price table")
	fake.AddSubIssue(shop, 12, 40)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 40)
	testkit.WaitFor(t, func() bool { return blockersOf41(t, server, "open") == 1 })

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/start", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if got := startByOf(t, server, shop, 41); got != "ui" {
		t.Errorf("start_requested_by = %q", got)
	}
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 41) {
		t.Fatal("the blocked issue started a task")
	}
	if !hasStart(t, server, 41) {
		t.Fatal("the waiting Start was cleared")
	}

	fake.CloseIssue(shop, 40)

	if task := liveTaskOf(t, server, 41); task.Workstream != 12 {
		t.Errorf("task = %+v", task)
	}
	if at, by := startRowOf(t, server, shop, 41); at.Valid || by != "" {
		t.Errorf("start = %+v, %q", at, by)
	}
}

func TestAStartOfAnIssueWithAStoppedTaskAndAPullRequestResumesAndKeepsTheTaskRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithStoppedTask(t, fake)
	before := liveTask(t, server, 41)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/start", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool { return implementerRounds(t, server) == 1 })
	after := liveTask(t, server, 41)
	if after.ID != before.ID || after.FixRounds != 1 {
		t.Errorf("task = %+v (before %+v)", after, before)
	}
	if at, by := startRowOf(t, server, shop, 41); at.Valid || by != "" {
		t.Errorf("start = %+v, %q", at, by)
	}
}

func TestStopOfAWaitingStartClearsTheStartColumns(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectSeen(t, fake)
	fake.AddIssue(shop, 40, "Add the price table")
	fake.AddSubIssue(shop, 12, 40)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 40)
	testkit.WaitFor(t, func() bool { return blockersOf41(t, server, "open") == 1 })

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/start", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	if !hasStart(t, server, 41) {
		t.Fatal("start was not stored")
	}

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/stop", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if at, by := startRowOf(t, server, shop, 41); at.Valid || by != "" {
		t.Errorf("start = %+v, %q", at, by)
	}
	if issueRowOf(t, server, shop, 41).Triage {
		t.Error("Stop set triage")
	}
	fake.CloseIssue(shop, 40)
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 41) {
		t.Error("a task started after Stop")
	}
}
