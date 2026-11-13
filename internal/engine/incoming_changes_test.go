package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	evergreenLabel   = "mobius:evergreen"
	maintenanceLabel = "mobius:maintenance"
)

func incomingChanges(t *testing.T, server *testserver.Server, status string) int {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE status = ?", status)
}

// personChanges counts the incoming changes with the status of an actor that is not the Mobius App. A row of the App is
// the record of an own write.
func personChanges(t *testing.T, server *testserver.Server, status string) int {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE status = ? AND actor <> ?", status, testkit.AppSlug+"[bot]")
}

func ownWrites(t *testing.T, server *testserver.Server) int {
	t.Helper()
	return countRows(t, server, "SELECT count(*) FROM own_writes")
}

func TestAChangeOfAutopilotByATrustedUserChangesTheWorkstreamAndAddsAnEventForTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)

	fake.AddLabel(shop, 12, "mobius:autopilot", "owner")

	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "start" })
	testkit.WaitFor(t, func() bool { return len(autopilotEvents(t, server)) == 1 })
	testkit.WaitFor(t, func() bool { return personChanges(t, server, "done") == 1 })
	waitForPolls(t, fake)
	got := autopilotEvents(t, server)
	if len(got) != 1 || !strings.HasSuffix(got[0], ` Autopilot of Workstream #12 "Integrate loyalty plans" is "Start tasks", by @owner.`) {
		t.Errorf("events = %q", got)
	}
	if !slices.Contains(fake.Labels(shop, 12), "mobius:autopilot") {
		t.Errorf("labels = %v", fake.Labels(shop, 12))
	}
}

func TestAChangeOfEvergreenByAPersonWhoIsNotATrustedUserIsWrittenBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)

	fake.AddLabel(shop, 12, evergreenLabel, "mallory")

	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 12), evergreenLabel) })
	testkit.WaitFor(t, func() bool { return personChanges(t, server, "done") == 1 })
	waitForPolls(t, fake)
	if workstreams(t, server)[0].Evergreen {
		t.Error("the Workstream is evergreen")
	}
	if !fake.LabelRemovedBy(shop, 12, evergreenLabel, testkit.AppSlug+"[bot]") {
		t.Error("the App did not remove the label")
	}
}

func TestTheLabelWritesOfAChangeOfTheOwnerAddNoIncomingChangeAndTheirOwnWritesGoAway(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)

	setLevelOf(t, server, 12, "start_and_merge")

	testkit.WaitFor(t, func() bool {
		return slices.Contains(fake.Labels(shop, 12), "mobius:autopilot") && slices.Contains(fake.Labels(shop, 12), "mobius:auto-merge")
	})
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if got := personChanges(t, server, "done"); got != 0 {
		t.Errorf("incoming changes of a person = %d", got)
	}
}

func TestAChangeOfMaintenanceByATrustedUserMakesTheWorkstreamEvergreenAndWritesTheEvergreenLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)

	fake.AddLabel(shop, 12, maintenanceLabel, "owner")

	testkit.WaitFor(t, func() bool { return fake.LabelAddedBy(shop, 12, evergreenLabel, testkit.AppSlug+"[bot]") })
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 && personChanges(t, server, "done") == 1 })
	waitForPolls(t, fake)
	var maintenance, evergreen bool
	if err := server.DB.QueryRow("SELECT maintenance, evergreen FROM workstreams WHERE repository = ? AND number = 12", shop).Scan(&maintenance, &evergreen); err != nil {
		t.Fatal(err)
	}
	if !maintenance || !evergreen {
		t.Errorf("maintenance = %t, evergreen = %t", maintenance, evergreen)
	}
	if got := fake.Labels(shop, 12); !slices.Contains(got, maintenanceLabel) || !slices.Contains(got, evergreenLabel) {
		t.Errorf("labels = %v", got)
	}
}

func TestTheHousekeeperDeletesADoneIncomingChangeAndAnOwnWriteOlderThan24HoursAndKeepsAPendingIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, seen, func(cfg *config.Config) {
		cfg.HousekeeperInterval = 100 * time.Millisecond
	})
	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	// The repository owner/gone has no App access, so no poll applies its pending change.
	for _, change := range []struct{ externalID, repository, status string }{{"1", shop, "done"}, {"2", "owner/gone", "pending"}} {
		_, err := server.DB.Exec(`INSERT INTO incoming_changes (integration, external_id, repository, number, field, change, actor, metadata, status, created_at)
			VALUES ('github', ?, ?, 12, 'evergreen', 'labeled', 'owner', '{"label":"mobius:evergreen"}', ?, ?)`, change.externalID, change.repository, change.status, old)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := server.DB.Exec(`INSERT INTO own_writes (integration, repository, number, field, metadata, created_at)
		VALUES ('github', ?, 12, 'evergreen', '{"label":"mobius:evergreen","change":"labeled"}', ?)`, shop, old)
	if err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return incomingChanges(t, server, "done") == 0 && ownWrites(t, server) == 0 })
	if got := incomingChanges(t, server, "pending"); got != 1 {
		t.Errorf("pending incoming changes = %d", got)
	}
}

func TestAFailedLabelWriteLeavesNoOwnWrite(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)
	setEvergreenOf(t, server, true)
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 12), evergreenLabel) })
	setEvergreenOf(t, server, false)
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 12), evergreenLabel) })
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	fake.FailAddLabels(shop, 12, true)

	setEvergreenOf(t, server, true)

	waitForPolls(t, fake)
	if got := ownWrites(t, server); got != 0 {
		t.Errorf("own writes = %d", got)
	}
	fake.FailAddLabels(shop, 12, false)
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 12), evergreenLabel) })
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if got := personChanges(t, server, "done"); got != 0 {
		t.Errorf("incoming changes of a person = %d", got)
	}
}

func TestATrustedChangeWaitsForTheCopyOfTheRepositoryAndThenChangesTheNewWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)
	fake.FailSubIssues(shop, 13, true)
	fake.AddIssue(shop, 13, "Price rounding")
	fake.AddLabel(shop, 13, "mobius:autopilot", "owner")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool { return incomingChanges(t, server, "pending") == 1 })
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 1 {
		t.Errorf("pending incoming changes = %d", got)
	}
	fake.FailSubIssues(shop, 13, false)

	testkit.WaitFor(t, func() bool { return personChanges(t, server, "done") == 1 })
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(workstreams(t, server), func(w workstream) bool { return w.Number == 13 && w.Autopilot == "start" })
	})
	waitForPolls(t, fake)
	if !slices.Contains(fake.Labels(shop, 13), "mobius:autopilot") {
		t.Errorf("labels = %v", fake.Labels(shop, 13))
	}
}

func setEvergreenOf(t *testing.T, server *testserver.Server, on bool) {
	t.Helper()
	body := `{"on":false}`
	if on {
		body = `{"on":true}`
	}
	if status, text := send(t, server, http.MethodPut, "/api/workstreams/owner/shop/12/evergreen", body); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, text)
	}
}

func TestAFailedPullRequestReadAfterATrustedLevelChangeStillGivesTheLeadTheEventAndMarksTheChangeDone(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvable(t, fake, "off", true)
	sendChat(t, server, leadChat, "Approve #41")
	waitForItem(t, server, "ready for review")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	waitForPolls(t, fake)
	fake.FailPullRequestReads(true)

	fake.AddLabel(shop, 12, "mobius:auto-merge", "owner")

	testkit.WaitFor(t, func() bool { return personChanges(t, server, "done") == 1 })
	testkit.WaitFor(t, func() bool { return len(autopilotEvents(t, server)) == 1 })
	got := autopilotEvents(t, server)
	if len(got) != 1 || !strings.HasSuffix(got[0], ` is "Merge only", by @owner.`) {
		t.Errorf("events = %q", got)
	}
	if got := workstreams(t, server)[0].Autopilot; got != "merge" {
		t.Errorf("level = %s", got)
	}
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
}

func TestASettingLabelEventOfATrustedUserInTheSecondOfTheIssueCursorChangesTheWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)
	fake.AddLabel(shop, 12, "bug", "owner")
	waitForPoll(t, server, fake)

	fake.AddLabelInLastSecond(shop, 12, "mobius:autopilot", "owner")

	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "start" })
	testkit.WaitFor(t, func() bool { return personChanges(t, server, "done") == 1 })
}

func TestASettingLabelOfTheAppWithNoOwnWriteInTheSecondOfTheIssueCursorIsWrittenBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopiedWithLead(t, fake)
	fake.AddLabel(shop, 12, "bug", "owner")
	waitForPoll(t, server, fake)

	fake.AddLabelInLastSecond(shop, 12, evergreenLabel, appActor)

	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 12), evergreenLabel) })
	if workstreams(t, server)[0].Evergreen {
		t.Error("the Workstream is evergreen")
	}
}
