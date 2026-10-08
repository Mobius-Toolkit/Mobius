package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const lesson = "Run make fmt before each commit."

func TestEachRoleGetsTheSameMemorySectionAfterTheRoleInstructions(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"" + question + "\"\ncall = { tool = \"start_researcher\", arguments = { question = \"" + question + "\" } }\n\n" + leadStarts
	server, dataDir := connectTask(t, fake, lead, commits, noChange)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+"[[prompts]]\nreply = [\"Plans store the price in cents.\"]\n")
	fake.CommitFile(shop, ".mobius/roles/implementer.md", "Commit small steps.", "Add the role file")
	fake.CommitFile(shop, ".mobius/roles/researcher.md", "Name each source.", "Add the role file")
	if err := server.Engine.SaveMemory(t.Context(), shop, "owner", "", lesson+"\n"); err != nil {
		t.Fatal(err)
	}

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	sendChat(t, server, leadChat, question)

	for role, instructions := range map[string]string{"implementer": "Commit small steps.", "researcher": "Name each source."} {
		prompt := testkit.WaitForValue(t, func() (string, bool) {
			sessions := roleSessions(t, server, role)
			if len(sessions) == 0 {
				return "", false
			}
			prompts := promptTexts(t, server, sessions[0].ID)
			return strings.Join(prompts, ""), len(prompts) > 0
		})
		if !strings.Contains(prompt, "# Role instructions\n\n"+instructions+"\n\n# Memory\n\n"+lesson+"\n\n") {
			t.Errorf("%s: %s", role, prompt)
		}
	}
}

func TestAPromptHasNoMemorySectionWhenTheMemoryFileDoesNotExist(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)

	prompt := firstLeadPrompt(t, fake, [2]string{"AGENTS.md", fact})

	if strings.Contains(prompt, "# Memory") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestASaveAddsOneVersionAndASaveOfTheSameTextAddsNone(t *testing.T) {
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), "")
	versions := func() (authors []string) {
		rows, err := server.DB.Query("SELECT author FROM memory_versions WHERE repository = ? ORDER BY id", shop)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var author string
			if err := rows.Scan(&author); err != nil {
				t.Fatal(err)
			}
			authors = append(authors, author)
		}
		return authors
	}

	for _, author := range []string{"curator", "owner"} {
		if err := server.Engine.SaveMemory(t.Context(), shop, author, "", lesson); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop+".md")))
	if got := versions(); err != nil || string(data) != lesson || len(got) != 1 || got[0] != "curator" {
		t.Errorf("file = %q, %v, versions = %v", data, err, got)
	}
}

func TestASaveRefusesATextOf201Lines(t *testing.T) {
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), "")

	if err := server.Engine.SaveMemory(t.Context(), shop, "curator", "", strings.Repeat("A lesson.\n", 200)); err != nil {
		t.Fatal(err)
	}
	err := server.Engine.SaveMemory(t.Context(), shop, "curator", "", strings.Repeat("A lesson.\n", 201))

	var count int
	if scanErr := server.DB.QueryRow("SELECT count(*) FROM memory_versions").Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	data, _ := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop+".md")))
	if err == nil || count != 1 || strings.Count(string(data), "\n") != 200 {
		t.Errorf("err = %v, versions = %d, lines = %d", err, count, strings.Count(string(data), "\n"))
	}
}
