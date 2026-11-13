package engine_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const other = "other/thing"

// startRelease starts a server on owner/shop and other/thing with the program release tag. Each test of the changelog
// sets Release, so it stays serial.
func startRelease(t *testing.T, fake *testkit.FakeGitHub, dataDir, tag string, statements ...string) *testserver.Server {
	t.Helper()
	engine.Release = tag
	t.Cleanup(func() { engine.Release = "" })
	fake.AddRepository(other)
	seed(t, dataDir, statements...)
	testkit.InstallFakeAgent(t, dataDir, options+seenScript)
	return startServer(t, fake, dataDir, "")
}

// restartRelease stops server and starts a server on the same data with the program release tag.
func restartRelease(t *testing.T, fake *testkit.FakeGitHub, server *testserver.Server, dataDir, tag string) *testserver.Server {
	t.Helper()
	server.Stop()
	engine.Release = tag
	testkit.InstallFakeAgent(t, dataDir, options+seenScript)
	return testserver.Start(t, dataDir, fake.URL)
}

func lastRelease(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var tag string
	err := server.DB.QueryRow("SELECT tag FROM last_release").Scan(&tag)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return tag
}

// changelogs gives the text of each message of the Mobius author in the Coordinator chat of each organization.
func changelogs(t *testing.T, server *testserver.Server) map[string][]string {
	t.Helper()
	rows, err := server.DB.Query("SELECT organization, text FROM chat_messages WHERE repository = '' AND workstream = 0 AND author = 'Mobius' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	texts := map[string][]string{}
	for rows.Next() {
		var organization, text string
		if err := rows.Scan(&organization, &text); err != nil {
			t.Fatal(err)
		}
		texts[organization] = append(texts[organization], text)
	}
	return texts
}

func messageCount(texts map[string][]string) int {
	count := 0
	for _, organization := range texts {
		count += len(organization)
	}
	return count
}

func TestTheFirstStartOfAReleaseKeepsTheTagAndSendsNoChangelog(t *testing.T) {
	// Serial: the test sets the package variable Release.
	fake := testkit.NewFakeGitHub(t)
	fake.SetComparedCommits("Show the usage (#10)")

	server := startRelease(t, fake, t.TempDir(), "v0.1.4")

	testkit.WaitFor(t, func() bool { return lastRelease(t, server) == "v0.1.4" })
	waitForPolls(t, fake)
	if texts := changelogs(t, server); len(texts) != 0 {
		t.Errorf("changelogs = %q", texts)
	}
}

func TestALocalBuildKeepsNoTagAndSendsNoChangelog(t *testing.T) {
	// Serial: the test sets the package variable Release.
	fake := testkit.NewFakeGitHub(t)
	fake.SetComparedCommits("Show the usage (#10)")

	server := startRelease(t, fake, t.TempDir(), "")

	waitForPolls(t, fake)
	var rows int
	if err := server.DB.QueryRow("SELECT COUNT(*) FROM last_release").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("rows of last_release = %d", rows)
	}
	if texts := changelogs(t, server); len(texts) != 0 {
		t.Errorf("changelogs = %q", texts)
	}
}

func TestANewReleaseSendsTheChangelogToTheCoordinatorChatOfEachOrganization(t *testing.T) {
	// Serial: the test sets the package variable Release.
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	memory := filepath.Join(dataDir, "memory", "owner", "shop", "lead.md")
	if err := os.MkdirAll(filepath.Dir(memory), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memory, []byte("Use the old flag.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := startRelease(t, fake, dataDir, "v0.1.4")
	testkit.WaitFor(t, func() bool { return lastRelease(t, first) == "v0.1.4" })
	fake.SetComparedCommits("Show the usage (#10)\n\nThe body of the commit.", "Fix the flag (#11)", "Release the notes")
	fake.SetClosingIssues(10, 7, 8)

	server := restartRelease(t, fake, first, dataDir, "v0.1.6")

	testkit.WaitFor(t, func() bool { return messageCount(changelogs(t, server)) == 2 })
	testkit.WaitFor(t, func() bool { return lastRelease(t, server) == "v0.1.6" })
	texts := changelogs(t, server)
	want := "Changelog from v0.1.4 to v0.1.6\n\n2 pull requests.\n\n- #10 Show the usage (closes #7, #8)\n- #11 Fix the flag\n- Release the notes\n"
	if len(texts["owner"]) != 1 || texts["owner"][0] != want+"\nMemory files:\n- "+memory+"\n" {
		t.Errorf("changelog of owner = %q", texts["owner"])
	}
	if !slices.Equal(texts["other"], []string{want}) {
		t.Errorf("changelog of other = %q", texts["other"])
	}
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(coordinatorPrompts(t, server), func(prompt string) bool {
			return strings.Contains(prompt, "Changelog from v0.1.4 to v0.1.6")
		})
	})
}

func TestASubjectWithTheNumberOfAnIssueKeepsTheOtherLinesOfTheChangelog(t *testing.T) {
	// Serial: the test sets the package variable Release.
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	first := startRelease(t, fake, dataDir, "v0.1.4")
	testkit.WaitFor(t, func() bool { return lastRelease(t, first) == "v0.1.4" })
	fake.SetComparedCommits("Show the usage (#10)", "Fix the flag (#99)")
	fake.SetClosingIssues(10, 7)
	fake.SetNotPullRequest(99)

	server := restartRelease(t, fake, first, dataDir, "v0.1.6")

	testkit.WaitFor(t, func() bool { return lastRelease(t, server) == "v0.1.6" })
	texts := changelogs(t, server)["owner"]
	if len(texts) != 1 || !strings.Contains(texts[0], "\n- #10 Show the usage (closes #7)\n- #99 Fix the flag\n") {
		t.Errorf("changelog of owner = %q", texts)
	}
}

func TestOneChangelogCoversManyReleasesAndMoreThanOnePageOfTheComparison(t *testing.T) {
	// Serial: the test sets the package variable Release.
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	first := startRelease(t, fake, dataDir, "v0.1.4")
	testkit.WaitFor(t, func() bool { return lastRelease(t, first) == "v0.1.4" })
	var commits []string
	for number := 1; number <= 120; number++ {
		commits = append(commits, fmt.Sprintf("Change %d (#%d)", number, number))
	}
	fake.SetComparedCommits(commits...)

	server := restartRelease(t, fake, first, dataDir, "v0.1.9")

	testkit.WaitFor(t, func() bool { return messageCount(changelogs(t, server)) == 2 })
	texts := changelogs(t, server)["owner"]
	if len(texts) != 1 || !strings.Contains(texts[0], "Changelog from v0.1.4 to v0.1.9\n\n120 pull requests.") ||
		!strings.Contains(texts[0], "\n- #1 Change 1\n") || !strings.Contains(texts[0], "\n- #120 Change 120\n") {
		t.Errorf("changelog of owner = %q", texts)
	}
}

func TestARestartWithTheSameTagSendsNoSecondChangelog(t *testing.T) {
	// Serial: the test sets the package variable Release.
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	first := startRelease(t, fake, dataDir, "v0.1.4")
	testkit.WaitFor(t, func() bool { return lastRelease(t, first) == "v0.1.4" })
	fake.SetComparedCommits("Show the usage (#10)")
	second := restartRelease(t, fake, first, dataDir, "v0.1.6")
	testkit.WaitFor(t, func() bool { return messageCount(changelogs(t, second)) == 2 })
	testkit.WaitFor(t, func() bool { return lastRelease(t, second) == "v0.1.6" })

	third := restartRelease(t, fake, second, dataDir, "v0.1.6")

	waitForPolls(t, fake)
	if count := messageCount(changelogs(t, third)); count != 2 {
		t.Errorf("messages = %d", count)
	}
}

func TestAnOrganizationThatGotTheChangelogGetsNoSecondOne(t *testing.T) {
	// Serial: the test sets the package variable Release.
	fake := testkit.NewFakeGitHub(t)
	fake.SetComparedCommits("Show the usage (#10)")

	server := startRelease(t, fake, t.TempDir(), "v0.1.6",
		"INSERT INTO last_release (id, tag) VALUES (1, 'v0.1.4')",
		"INSERT INTO release_changelogs (organization, tag, sent_at) VALUES ('owner', 'v0.1.6', '2026-11-11T10:00:00Z')")

	testkit.WaitFor(t, func() bool { return lastRelease(t, server) == "v0.1.6" })
	texts := changelogs(t, server)
	if len(texts["owner"]) != 0 || len(texts["other"]) != 1 {
		t.Errorf("changelogs = %q", texts)
	}
}

func TestAFailedReadOfOneAppSendsNoChangelogUntilTheReadSucceeds(t *testing.T) {
	// Serial: the test sets the package variable Release.
	fake := testkit.NewFakeGitHub(t)
	fake.SetComparedCommits("Show the usage (#10)")
	fake.InstallSecondApp("other")
	fake.FailInstallations(testkit.SecondAppID, true)

	server := startRelease(t, fake, t.TempDir(), "v0.1.6",
		"INSERT INTO last_release (id, tag) VALUES (1, 'v0.1.4')",
		fmt.Sprintf("INSERT INTO github_apps (app_id, slug, private_key, client_id, client_secret) VALUES (%d, '%s', '%s', 'Iv23second', 'second-client-secret')",
			testkit.SecondAppID, testkit.SecondAppSlug, testkit.AppPrivateKey))

	refused := fake.InstallationReads()
	testkit.WaitFor(t, func() bool { return fake.InstallationReads() >= refused+6 })
	if texts := changelogs(t, server); len(texts) != 0 || lastRelease(t, server) != "v0.1.4" {
		t.Errorf("changelogs = %q, last release = %s", texts, lastRelease(t, server))
	}

	fake.FailInstallations(testkit.SecondAppID, false)

	testkit.WaitFor(t, func() bool { return lastRelease(t, server) == "v0.1.6" })
	texts := changelogs(t, server)
	if len(texts["owner"]) != 1 || len(texts["other"]) != 1 {
		t.Errorf("changelogs = %q", texts)
	}
}
