package engine_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const lesson = "Run make fmt before each commit."

func TestEachRoleGetsTheSameMemorySectionAfterTheRoleInstructions(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"" + question + "\"\ncall = { tool = \"start_researcher\", arguments = { repository = \"owner/shop\", question = \"" + question + "\" } }\n\n" + leadStarts
	server, dataDir := connectTask(t, fake, lead, commits, noChange)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+"[[prompts]]\nreply = [\"Plans store the price in cents.\"]\n")
	fake.CommitFile(shop, ".mobius/roles/implementer.md", "Commit small steps.", "Add the role file")
	fake.CommitFile(shop, ".mobius/roles/researcher.md", "Name each source.", "Add the role file")
	if err := server.Engine.SaveMemory(t.Context(), shop, "shared", "owner", "", lesson+"\n"); err != nil {
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
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	prompt := firstLeadPrompt(t, fake, [2]string{"AGENTS.md", fact})

	if strings.Contains(prompt, "# Memory") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestASaveAddsOneVersionAndASaveOfTheSameTextAddsNone(t *testing.T) {
	t.Parallel()
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
		if err := server.Engine.SaveMemory(t.Context(), shop, "shared", author, "", lesson); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop+".md")))
	if got := versions(); err != nil || string(data) != lesson || len(got) != 1 || got[0] != "curator" {
		t.Errorf("file = %q, %v, versions = %v", data, err, got)
	}
}

func TestASaveRefusesATextOf201Lines(t *testing.T) {
	t.Parallel()
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), "")

	if err := server.Engine.SaveMemory(t.Context(), shop, "shared", "curator", "", strings.Repeat("A lesson.\n", 200)); err != nil {
		t.Fatal(err)
	}
	err := server.Engine.SaveMemory(t.Context(), shop, "shared", "curator", "", strings.Repeat("A lesson.\n", 201))

	var count int
	if scanErr := server.DB.QueryRow("SELECT count(*) FROM memory_versions").Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	data, _ := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop+".md")))
	if err == nil || count != 1 || strings.Count(string(data), "\n") != 200 {
		t.Errorf("err = %v, versions = %d, lines = %d", err, count, strings.Count(string(data), "\n"))
	}
}

func TestTwoEditsFromTheSameVersionSaveOnlyOne(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), "")
	if err := server.Engine.SaveMemory(t.Context(), shop, "shared", "curator", "", lesson+"\n"); err != nil {
		t.Fatal(err)
	}
	var base int64
	if err := server.DB.QueryRow("SELECT max(id) FROM memory_versions").Scan(&base); err != nil {
		t.Fatal(err)
	}

	const edits = 8
	errs := make(chan error, edits)
	for i := range edits {
		go func() {
			errs <- server.Engine.EditMemory(t.Context(), shop, "shared", base, fmt.Sprintf("Edit %d.\n", i))
		}()
	}
	saved := 0
	for range edits {
		if err := <-errs; err == nil {
			saved++
		} else if !engine.Refused(err) {
			t.Fatal(err)
		}
	}

	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM memory_versions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if saved != 1 || count != 2 {
		t.Errorf("saved = %d, versions = %d", saved, count)
	}
}

func TestEachMemoryFileHasItsOwn200LineLimit(t *testing.T) {
	t.Parallel()
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), "")
	shared := strings.Repeat("Shared lesson.\n", 200)
	role := strings.Repeat("Role lesson.\n", 200)
	if err := server.Engine.SaveMemory(t.Context(), shop, engine.MemoryFileShared, "curator", "", shared); err != nil {
		t.Fatal(err)
	}
	if err := server.Engine.SaveMemory(t.Context(), shop, "implementer", "curator", "", role); err != nil {
		t.Fatal(err)
	}

	err := server.Engine.SaveMemory(t.Context(), shop, "implementer", "curator", "", strings.Repeat("Role lesson.\n", 201))

	var count int
	if scanErr := server.DB.QueryRow("SELECT count(*) FROM memory_versions WHERE repository = ? AND file = 'implementer'", shop).Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	data, readErr := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop, "implementer.md")))
	if !errors.Is(err, engine.ErrMemoryTooLong) || count != 1 || readErr != nil || string(data) != role {
		t.Errorf("err = %v, versions = %d, file = %q, %v", err, count, data, readErr)
	}
}

func TestAnEditOfOneFileUsesTheNewestVersionOfThatFile(t *testing.T) {
	t.Parallel()
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), "")
	if err := server.Engine.SaveMemory(t.Context(), shop, engine.MemoryFileShared, "owner", "", "Shared one.\n"); err != nil {
		t.Fatal(err)
	}
	var sharedBase int64
	if err := server.DB.QueryRow("SELECT max(id) FROM memory_versions WHERE repository = ? AND file = 'shared'", shop).Scan(&sharedBase); err != nil {
		t.Fatal(err)
	}
	if err := server.Engine.SaveMemory(t.Context(), shop, "implementer", "owner", "", "Role one.\n"); err != nil {
		t.Fatal(err)
	}

	if err := server.Engine.EditMemory(t.Context(), shop, "implementer", sharedBase, "Role two.\n"); err == nil || !engine.Refused(err) {
		t.Errorf("stale edit of another file: err = %v", err)
	}
	if err := server.Engine.EditMemory(t.Context(), shop, engine.MemoryFileShared, sharedBase, "Shared two.\n"); err != nil {
		t.Fatal(err)
	}

	shared, sharedErr := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop+".md")))
	role, roleErr := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop, "implementer.md")))
	if sharedErr != nil || roleErr != nil || string(shared) != "Shared two.\n" || string(role) != "Role one.\n" {
		t.Errorf("shared = %q, %v, role = %q, %v", shared, sharedErr, role, roleErr)
	}
}

func TestARevertOfARoleMemoryVersionChangesOnlyThatFile(t *testing.T) {
	t.Parallel()
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), "")
	for _, text := range []string{"Shared one.\n", "Shared two.\n"} {
		if err := server.Engine.SaveMemory(t.Context(), shop, engine.MemoryFileShared, "owner", "", text); err != nil {
			t.Fatal(err)
		}
	}
	var sharedNewest int64
	if err := server.DB.QueryRow("SELECT max(id) FROM memory_versions WHERE repository = ? AND file = 'shared'", shop).Scan(&sharedNewest); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"One.\n", "Two.\n"} {
		if err := server.Engine.SaveMemory(t.Context(), shop, "implementer", "owner", "", text); err != nil {
			t.Fatal(err)
		}
	}
	var roleNewest int64
	if err := server.DB.QueryRow("SELECT max(id) FROM memory_versions WHERE repository = ? AND file = 'implementer'", shop).Scan(&roleNewest); err != nil {
		t.Fatal(err)
	}

	if err := server.Engine.RevertMemory(t.Context(), shop, "implementer", roleNewest); err != nil {
		t.Fatal(err)
	}
	role, roleErr := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop, "implementer.md")))
	shared, sharedErr := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop+".md")))
	if roleErr != nil || sharedErr != nil || string(role) != "One.\n" || string(shared) != "Shared two.\n" {
		t.Errorf("role = %q, %v, shared = %q, %v", role, roleErr, shared, sharedErr)
	}
	for _, other := range []struct {
		file string
		id   int64
	}{{"shared", roleNewest}, {"implementer", sharedNewest}} {
		if err := server.Engine.RevertMemory(t.Context(), shop, other.file, other.id); !errors.Is(err, engine.ErrNoMemoryVersion) {
			t.Errorf("revert of %s with version %d of another file: err = %v", other.file, other.id, err)
		}
	}
}

func TestTheEditOfTheOwnerGivesErrMemoryTooLongAboveTheByteLimit(t *testing.T) {
	t.Parallel()
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), "")

	err := server.Engine.EditMemory(t.Context(), shop, engine.MemoryFileShared, 0, strings.Repeat("A", 16385))
	if !errors.Is(err, engine.ErrMemoryTooLong) {
		t.Fatalf("err = %v, want ErrMemoryTooLong", err)
	}
	want := "the memory file has too many lines: the memory file shared has 16385 bytes and the limit is 16384. Remove or shorten a lesson. Do not make a line longer."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	var count int
	if scanErr := server.DB.QueryRow("SELECT count(*) FROM memory_versions WHERE repository = ?", shop).Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "memory", shop+".md")); count != 0 || !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("versions = %d, statErr = %v", count, statErr)
	}
}

func TestARevertAboveTheByteLimitWithMoreBytesThanTheCurrentTextIsRefused(t *testing.T) {
	t.Parallel()
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), "")
	sharedPath := filepath.Join(dataDir, "memory", shop+".md")
	if err := os.MkdirAll(filepath.Dir(sharedPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sharedPath, []byte("Start.\n"+strings.Repeat("A", 19987)+"\nEnd.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v1Text := "Start.\n" + strings.Repeat("A", 17987) + "\nEnd.\n"
	if err := server.Engine.SaveMemory(t.Context(), shop, engine.MemoryFileShared, "owner", "v1", v1Text); err != nil {
		t.Fatal(err)
	}
	var v1ID int64
	if err := server.DB.QueryRow("SELECT max(id) FROM memory_versions WHERE repository = ? AND file = 'shared'", shop).Scan(&v1ID); err != nil {
		t.Fatal(err)
	}

	v2Text := "Start.\n" + strings.Repeat("A", 16987) + "\nEnd.\n"
	if err := server.Engine.SaveMemory(t.Context(), shop, engine.MemoryFileShared, "owner", "v2", v2Text); err != nil {
		t.Fatal(err)
	}
	var v2ID int64
	if err := server.DB.QueryRow("SELECT max(id) FROM memory_versions WHERE repository = ? AND file = 'shared'", shop).Scan(&v2ID); err != nil {
		t.Fatal(err)
	}

	problem, err := server.Engine.MemoryRevertProblem(t.Context(), shop, engine.MemoryFileShared, v2ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "The revert gives 18000 bytes and the limit is 16384. Edit the file."
	if problem != want {
		t.Errorf("problem = %q, want %q", problem, want)
	}

	err = server.Engine.RevertMemory(t.Context(), shop, engine.MemoryFileShared, v2ID)
	if err == nil || !engine.Refused(err) || err.Error() != want {
		t.Errorf("err = %v, want refusal %q", err, want)
	}
}
