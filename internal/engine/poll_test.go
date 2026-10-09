package engine_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// startServer starts a server with the data in dataDir, runs the SQL statement before when it is not empty, and then adds
// the App that is installed on owner/shop. The first poll comes after the App.
func startServer(t *testing.T, fake *testkit.FakeGitHub, dataDir, before string) *testserver.Server {
	t.Helper()
	return startServerWith(t, fake, testserver.Config(t, dataDir), before)
}

// startServerWith is startServer with the config cfg.
func startServerWith(t *testing.T, fake *testkit.FakeGitHub, cfg *config.Config, before string) *testserver.Server {
	t.Helper()
	fake.AddRepository(shop)
	server := testserver.StartWith(t, cfg, fake.URL)
	if before != "" {
		if _, err := server.DB.Exec(before); err != nil {
			t.Fatal(err)
		}
	}
	_, err := server.DB.Exec("INSERT INTO github_apps (app_id, slug, private_key, client_id, client_secret) VALUES (?, ?, ?, ?, ?)",
		testkit.AppID, testkit.AppSlug, testkit.AppPrivateKey, testkit.AppClientID, testkit.AppClientSecret)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func cursor(t *testing.T, server *testserver.Server) (since, etag sql.NullString) {
	t.Helper()
	err := server.DB.QueryRow("SELECT since, etag FROM sync_cursors WHERE repository = ? AND endpoint = 'issues'", shop).Scan(&since, &etag)
	if err != nil {
		t.Fatal(err)
	}
	return since, etag
}

// The poll fixes the labels of a managed repository at its first sight, one time in a run of the server.
func TestAPollFixesTheLabelsOfAManagedRepositoryOneTime(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepositoryLabel(shop, "mobius:working", "ededed", "Custom description")
	fake.AddRepositoryLabel(shop, "bug", "d73a4a", "Something is wrong")
	startServer(t, fake, t.TempDir(), "")

	labels := testkit.WaitForValue(t, func() ([]testkit.Label, bool) {
		labels := fake.RepositoryLabels(shop)
		return labels, len(labels) == 10
	})

	want := []testkit.Label{
		{Name: "bug", Color: "d73a4a", Description: "Something is wrong"},
		{Name: "mobius:autopilot", Color: "1D76DB", Description: "Mobius dispatches the ready tasks of this Workstream"},
		{Name: "mobius:needs-human", Color: "D93F0B", Description: "The task stopped and needs a human"},
		{Name: "mobius:no-workstream", Color: "BFD4F2", Description: "The Triager found no Workstream for this issue"},
		{Name: "mobius:question", Color: "C5A3F5", Description: "Mobius waits for an answer from a human"},
		{Name: "mobius:ready", Color: "0E8A16", Description: "Mobius can dispatch this task"},
		{Name: "mobius:review", Color: "006B75", Description: "The pull request waits for a human review"},
		{Name: "mobius:wont-do", Color: "CFD3D7", Description: "The Owner closed the Workstream of this issue as \"won't do\""},
		{Name: "mobius:working", Color: "FBCA04", Description: "Custom description"},
		{Name: "mobius:workstream", Color: "5319E7", Description: "Mobius Workstream: a parent issue for a group of tasks"},
	}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v", labels)
	}
	if got := fake.LabelPatches(shop); !reflect.DeepEqual(got, []string{"mobius:working"}) {
		t.Errorf("patches = %v", got)
	}

	polls := fake.NotModifiedCount()
	fake.DeleteRepositoryLabel(shop, "mobius:ready")
	testkit.WaitFor(t, func() bool { return fake.NotModifiedCount() >= polls+2 })

	if got := fake.RepositoryLabels(shop); len(got) != 9 {
		t.Errorf("labels = %v", got)
	}
	if got := fake.LabelPatches(shop); !reflect.DeepEqual(got, []string{"mobius:working"}) {
		t.Errorf("patches = %v", got)
	}
}

func TestThePollMovesTheCursorToTheLastChangeAndSendsTheETag(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 1, "First")
	fake.AddIssue(shop, 2, "Second")
	server := startServer(t, fake, t.TempDir(), "")

	server.WaitForFirstPoll(t, shop)

	since, etag := cursor(t, server)
	first, err := time.Parse(time.RFC3339, since.String)
	if err != nil || !etag.Valid {
		t.Fatalf("cursor = %v, %v: %v", since, etag, err)
	}
	// The issue list has no change, so GitHub answers 304 to the ETag.
	polls := fake.NotModifiedCount()
	testkit.WaitFor(t, func() bool { return fake.NotModifiedCount() >= polls+2 })

	fake.AddComment(shop, 1, "owner", "A change")

	last := testkit.WaitForValue(t, func() (time.Time, bool) {
		since, _ := cursor(t, server)
		last, err := time.Parse(time.RFC3339, since.String)
		return last, err == nil && last.After(first)
	})
	if last.Sub(first) != time.Second {
		t.Errorf("the cursor moved from %v to %v, want the time of the comment", first, last)
	}
}

// The cursor rows of the Rust version have the same format.
func TestThePollContinuesFromTheCursorOfTheDatabase(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 1, "First")
	server := startServer(t, fake, t.TempDir(), `INSERT INTO sync_cursors (repository, endpoint, since, etag) VALUES ('owner/shop', 'issues', '2999-01-01T00:00:00Z', '"old"')`)

	testkit.WaitFor(t, func() bool {
		_, etag := cursor(t, server)
		return etag.String != `"old"`
	})

	// No issue changed after the cursor, so the cursor stays.
	if since, etag := cursor(t, server); since.String != "2999-01-01T00:00:00Z" || !etag.Valid {
		t.Errorf("cursor = %v, %v", since, etag)
	}
}

// A 304 for page 1 says nothing about the other pages.
func TestAnIssueListOfMoreThanOnePageGivesNoETag(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	for number := range int64(101) {
		fake.AddIssue(shop, number+1, "Issue")
	}
	server := startServer(t, fake, t.TempDir(), "")

	server.WaitForFirstPoll(t, shop)

	if since, etag := cursor(t, server); !since.Valid || etag.Valid {
		t.Errorf("cursor = %v, %v", since, etag)
	}
}
