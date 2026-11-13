package engine_test

import (
	"net/http"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	maintenanceWorkstream = 14
	skillsWorkstream      = 15
)

// missingItems gives the open Inbox items of a missing Workstream.
func missingItems(t *testing.T, server *testserver.Server) []inboxItem {
	t.Helper()
	return append(itemsOf(t, server, "missing maintenance"), itemsOf(t, server, "missing skills")...)
}

// waitForOpenWorkstreams waits until the copy has count open Workstreams.
func waitForOpenWorkstreams(t *testing.T, server *testserver.Server, count int) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		var found int
		if err := server.DB.QueryRow("SELECT count(*) FROM copied_workstreams").Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found == count
	})
}

func TestAnOrganizationWithAnOpenWorkstreamAndNoMaintenanceAndNoSkillsWorkstreamGetsOneItemOfEachKind(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startOrganization(t, fake, func(*config.Config) {}, nil, shop)

	maintenance := waitForItem(t, server, "missing maintenance")
	skills := waitForItem(t, server, "missing skills")
	if _, err := server.Engine.SetIssueRepository(t.Context(), "owner", shop); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)

	got := missingItems(t, server)
	if len(got) != 2 || got[0].ID != maintenance.ID || got[1].ID != skills.ID {
		t.Fatalf("items = %+v", got)
	}
	if maintenance.Title != "Maintenance Workstream is missing" || skills.Title != "Skills Workstream is missing" {
		t.Errorf("titles = %q, %q", maintenance.Title, skills.Title)
	}
	for _, item := range got {
		if item.Repository != "" || item.WorkstreamRepository != "" || item.Workstream != 0 || item.Issue != 0 {
			t.Errorf("item = %+v", item)
		}
	}
}

func TestAnOrganizationWithNoOpenWorkstreamGetsNoItemOfAMissingWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startOrganization(t, fake, func(*config.Config) {}, nil)

	if _, err := server.Engine.SetIssueRepository(t.Context(), "owner", shop); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)

	if got := missingItems(t, server); len(got) != 0 {
		t.Errorf("items = %+v", got)
	}
}

func TestAMaintenanceWorkstreamAndASkillsWorkstreamThatAPersonCreatesDismissTheItemsWithNoStoredDismissal(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startOrganization(t, fake, func(*config.Config) {}, nil, shop)
	waitForItem(t, server, "missing maintenance")
	waitForItem(t, server, "missing skills")

	addMaintenanceWorkstream(fake, maintenanceWorkstream)
	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "missing maintenance")) == 0 })
	if got := itemsOf(t, server, "missing skills"); len(got) != 1 {
		t.Errorf("skills items = %+v", got)
	}
	addSkillsWorkstream(fake, skillsWorkstream)
	testkit.WaitFor(t, func() bool { return len(missingItems(t, server)) == 0 })

	var dismissals int
	if err := server.DB.QueryRow("SELECT count(*) FROM missing_workstream_dismissals").Scan(&dismissals); err != nil || dismissals != 0 {
		t.Errorf("dismissals = %d, %v", dismissals, err)
	}
}

func TestAnOrganizationWithNoIssueRepositoryGetsAMissingMaintenanceItemThatStaysAfterTheChoiceOfARepositoryWithNoMaintenanceWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startOrganization(t, fake, func(*config.Config) {}, nil, shop, garden)
	item := waitForItem(t, server, "missing maintenance")

	if _, err := server.Engine.SetIssueRepository(t.Context(), "owner", shop); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)

	got := itemsOf(t, server, "missing maintenance")
	if len(got) != 1 || got[0].ID != item.ID {
		t.Errorf("items = %+v, want %+v", got, item)
	}
}

func TestAfterTheOwnerDismissesAnItemACheckAddsNoNewItemOfThatKindAlsoAfterTheWorkstreamExistsAndClosesAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startOrganization(t, fake, func(*config.Config) {}, nil, shop)
	item := waitForItem(t, server, "missing maintenance")
	waitForItem(t, server, "missing skills")

	if status, body := send(t, server, http.MethodPost, "/api/inbox/"+itoa(item.ID)+"/dismiss", ""); status != http.StatusNoContent {
		t.Fatalf("dismiss: status %d: %s", status, body)
	}
	if _, err := server.Engine.SetIssueRepository(t.Context(), "owner", shop); err != nil {
		t.Fatal(err)
	}
	addMaintenanceWorkstream(fake, maintenanceWorkstream)
	waitForOpenWorkstreams(t, server, 2)
	fake.CloseIssue(shop, maintenanceWorkstream)
	waitForOpenWorkstreams(t, server, 1)
	if _, err := server.Engine.SetIssueRepository(t.Context(), "owner", shop); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)

	if got := itemsOf(t, server, "missing maintenance"); len(got) != 0 {
		t.Errorf("maintenance items = %+v", got)
	}
	if got := itemsOf(t, server, "missing skills"); len(got) != 1 {
		t.Errorf("skills items = %+v", got)
	}
}
