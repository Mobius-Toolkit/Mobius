package engine_test

import (
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// waitForRepositories waits for a change of the repositories of the Apps. The test fails after one minute.
func waitForRepositories(t *testing.T, changes <-chan engine.Change) {
	t.Helper()
	deadline := time.After(time.Minute)
	for {
		select {
		case change, ok := <-changes:
			if !ok {
				t.Fatal("the engine closed the listener")
			}
			if change.Repositories {
				return
			}
		case <-deadline:
			t.Fatal("no change of the repositories after one minute")
		}
	}
}

// noRepositories fails the test when changes has a change of the repositories of the Apps.
func noRepositories(t *testing.T, changes <-chan engine.Change) {
	t.Helper()
	for {
		select {
		case change, ok := <-changes:
			if !ok {
				t.Fatal("the engine closed the listener")
			}
			if change.Repositories {
				t.Fatal("a change of the repositories")
			}
		default:
			return
		}
	}
}

func TestANewInstallationGivesAChangeOfTheRepositories(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	server := startCopied(t, fake)
	changes := listen(t, server)

	fake.AddRepository("acme/garden")

	waitForRepositories(t, changes)
}

func TestAChangeOfTheSelectedRepositoriesOfAnInstallationGivesAChangeOfTheRepositories(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	server := startCopied(t, fake)
	changes := listen(t, server)

	fake.AddRepository("owner/cafe")
	waitForRepositories(t, changes)
	fake.RemoveRepository("owner/cafe")
	waitForRepositories(t, changes)
}

func TestARemovedInstallationGivesAChangeOfTheRepositories(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	server := startCopied(t, fake)
	changes := listen(t, server)
	fake.AddRepository("acme/garden")
	waitForRepositories(t, changes)

	fake.RemoveRepository("acme/garden")

	waitForRepositories(t, changes)
}

func TestAPollWithNoChangeOfTheRepositoriesGivesNoChange(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	server := startCopied(t, fake)
	changes := listen(t, server)

	waitForPolls(t, fake)

	noRepositories(t, changes)
}
