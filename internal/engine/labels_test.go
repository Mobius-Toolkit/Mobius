package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
)

const shop = "owner/shop"

// repository gives owner/shop of the fake GitHub, with the client of the installation of the App.
func repository(t *testing.T, fake *testkit.FakeGitHub) github.Repository {
	t.Helper()
	fake.AddRepository(shop)
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	queries := store.New(db)
	err = queries.AddGitHubApp(t.Context(), store.AddGitHubAppParams{
		AppID:        testkit.AppID,
		Slug:         testkit.AppSlug,
		PrivateKey:   testkit.AppPrivateKey,
		ClientID:     testkit.AppClientID,
		ClientSecret: testkit.AppClientSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	gh, err := github.New(queries, fake.URL, fake.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := gh.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	return gh.Repositories()[0]
}

type status struct {
	name, status, found string
}

func checkLabels(t *testing.T, repository github.Repository) []status {
	t.Helper()
	checks, err := engine.CheckLabels(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	var found []status
	for _, check := range checks {
		found = append(found, status{check.Label.Name, check.Status, check.Found})
	}
	return found
}

func TestFixLabelsCreatesTheMissingLabelsAndSetsTheFixedColors(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	// mobius:ready has the fixed color in lowercase and its own description.
	fake.AddRepositoryLabel(shop, "mobius:ready", "0e8a16", "Ready, says the Owner")
	fake.AddRepositoryLabel(shop, "mobius:working", "ededed", "Custom description")
	fake.AddRepositoryLabel(shop, "bug", "d73a4a", "Something is wrong")
	repository := repository(t, fake)

	want := []status{
		{"mobius:workstream", engine.Missing, ""},
		{"mobius:autopilot", engine.Missing, ""},
		{"mobius:ready", engine.Present, ""},
		{"mobius:working", engine.WrongColor, "ededed"},
		{"mobius:needs-human", engine.Missing, ""},
		{"mobius:no-workstream", engine.Missing, ""},
	}
	if got := checkLabels(t, repository); !reflect.DeepEqual(got, want) {
		t.Errorf("status = %v, want %v", got, want)
	}

	if err := engine.FixLabels(t.Context(), repository); err != nil {
		t.Fatal(err)
	}

	wantLabels := []testkit.Label{
		{Name: "bug", Color: "d73a4a", Description: "Something is wrong"},
		{Name: "mobius:autopilot", Color: "1D76DB", Description: "Mobius dispatches the ready tasks of this Workstream"},
		{Name: "mobius:needs-human", Color: "D93F0B", Description: "Mobius waits for an answer from a human"},
		{Name: "mobius:no-workstream", Color: "BFD4F2", Description: "The Triager found no Workstream for this issue"},
		{Name: "mobius:ready", Color: "0e8a16", Description: "Ready, says the Owner"},
		{Name: "mobius:working", Color: "FBCA04", Description: "Custom description"},
		{Name: "mobius:workstream", Color: "5319E7", Description: "Mobius Workstream: a parent issue for a group of tasks"},
	}
	if got := fake.RepositoryLabels(shop); !reflect.DeepEqual(got, wantLabels) {
		t.Errorf("labels = %v", got)
	}
	if got := fake.LabelPatches(shop); !reflect.DeepEqual(got, []string{"mobius:working"}) {
		t.Errorf("patches = %v", got)
	}
	for _, got := range checkLabels(t, repository) {
		if got.status != engine.Present {
			t.Errorf("after the fix: %v", got)
		}
	}
}

// GitHub compares label names with no regard to case, so a POST for a label in a different case fails.
func TestFixLabelsSkipsALabelWithANameInADifferentCase(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepositoryLabel(shop, "Mobius:Ready", "0E8A16", "Ready")
	fake.AddRepositoryLabel(shop, "MOBIUS:WORKING", "ededed", "Working")
	repository := repository(t, fake)

	want := []status{
		{"mobius:workstream", engine.Missing, ""},
		{"mobius:autopilot", engine.Missing, ""},
		{"mobius:ready", engine.WrongCase, "Mobius:Ready"},
		{"mobius:working", engine.WrongCase, "MOBIUS:WORKING"},
		{"mobius:needs-human", engine.Missing, ""},
		{"mobius:no-workstream", engine.Missing, ""},
	}
	if got := checkLabels(t, repository); !reflect.DeepEqual(got, want) {
		t.Errorf("status = %v, want %v", got, want)
	}

	if err := engine.FixLabels(t.Context(), repository); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, label := range fake.RepositoryLabels(shop) {
		names = append(names, label.Name+" "+label.Color+" "+label.Description)
	}
	wantNames := []string{
		"MOBIUS:WORKING ededed Working",
		"Mobius:Ready 0E8A16 Ready",
		"mobius:autopilot 1D76DB Mobius dispatches the ready tasks of this Workstream",
		"mobius:needs-human D93F0B Mobius waits for an answer from a human",
		"mobius:no-workstream BFD4F2 The Triager found no Workstream for this issue",
		"mobius:workstream 5319E7 Mobius Workstream: a parent issue for a group of tasks",
	}
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("labels = %v", names)
	}
	if got := fake.LabelPatches(shop); len(got) != 0 {
		t.Errorf("patches = %v", got)
	}
}
