package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const leadNote = "- Plans store the price in cents."

// curatorScript plays a Curator that makes no edit, and a Lead that greets.
const curatorScript = `
[[prompts]]
when = "You are a Curator"
reply = ["The memory file is up to date."]

[[prompts]]
when = "Plan the loyalty API"
reply = ["Hello"]

[[prompts]]
when = "Add the lesson"
call = { tool = "edit_memory", arguments = { old = "", new = "Run make fmt before each commit.\n", reason = "Add: Workstream 12 repeats the format failure." } }

[[prompts]]
when = "Edit a missing text"
call = { tool = "edit_memory", arguments = { old = "Absent.", new = "Present.\n", reason = "Change: issue 41." } }

[[prompts]]
when = "Edit a repeated text"
call = { tool = "edit_memory", arguments = { old = "Same.", new = "Different.", reason = "Change: issue 41." } }

[[prompts]]
when = "Add without a reason"
call = { tool = "edit_memory", arguments = { old = "", new = "No reason.\n", reason = " " } }

[[prompts]]
when = "Add a line"
call = { tool = "edit_memory", arguments = { old = "", new = "One more.\n", reason = "Add: issue 41." } }
`

func curatorSessions(t *testing.T, server *testserver.Server) []store.Session {
	t.Helper()
	rows, err := server.DB.Query("SELECT id FROM sessions WHERE role = 'curator' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var sessions []store.Session
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		session, err := store.New(server.DB).GetSession(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return sessions
}

// endLeads starts and ends count Lead sessions of the Workstream #12.
func endLeads(t *testing.T, server *testserver.Server, count int) {
	t.Helper()
	for range count {
		end(t, start(t, server, leadSpec(t)), "done")
	}
}

func memoryReasons(t *testing.T, server *testserver.Server) (reasons []string) {
	t.Helper()
	rows, err := server.DB.Query("SELECT reason FROM memory_versions WHERE repository = ? ORDER BY id", shop)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var reason string
		if err := rows.Scan(&reason); err != nil {
			t.Fatal(err)
		}
		reasons = append(reasons, reason)
	}
	return reasons
}

func memoryVersions(t *testing.T, server *testserver.Server) (authors []string) {
	t.Helper()
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

func TestTheTenthEndedSessionStartsACurator(t *testing.T) {
	server, _ := connect(t, testkit.NewFakeGitHub(t), curatorScript)

	endLeads(t, server, 9)
	if sessions := curatorSessions(t, server); len(sessions) != 0 {
		t.Fatalf("Curators after 9 sessions = %+v", sessions)
	}
	endLeads(t, server, 1)

	sessions := curatorSessions(t, server)
	if len(sessions) != 1 || sessions[0].Repository != shop {
		t.Fatalf("Curators after 10 sessions = %+v", sessions)
	}
	testkit.WaitFor(t, func() bool { return curatorSessions(t, server)[0].EndReason.String == "done" })
	endLeads(t, server, 9)
	if sessions := curatorSessions(t, server); len(sessions) != 1 {
		t.Errorf("Curators after 9 more sessions = %+v", sessions)
	}
}

func TestTheCloseOfAWorkstreamStartsACurator(t *testing.T) {
	for name, close := range map[string]func(*testing.T, *testserver.Server, *testkit.FakeGitHub){
		"completed": func(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) {
			fake.AddIssue(shop, 41, "Add plan model")
			fake.AddSubIssue(shop, 12, 41)
			fake.CloseIssue(shop, 41)
			if err := server.Engine.CompleteWorkstream(t.Context(), shop, 12); err != nil {
				t.Fatal(err)
			}
		},
		"won't do": func(t *testing.T, server *testserver.Server, _ *testkit.FakeGitHub) {
			if err := server.Engine.CloseWorkstreamWontDo(t.Context(), shop, 12); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			fake := testkit.NewFakeGitHub(t)
			server, dataDir := connect(t, fake, curatorScript)
			lead := filepath.Join(dataDir, "leads", "owner", "shop", "12")
			if err := os.MkdirAll(lead, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(lead, "MEMORY.md"), []byte(leadNote+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := server.Engine.SaveMemory(t.Context(), shop, "owner", "", lesson+"\n"); err != nil {
				t.Fatal(err)
			}

			close(t, server, fake)

			session := testkit.WaitForValue(t, func() (store.Session, bool) {
				for _, session := range curatorSessions(t, server) {
					if session.EndedAt.Valid {
						return session, true
					}
				}
				return store.Session{}, false
			})
			prompts := promptTexts(t, server, session.ID)
			if session.EndReason.String != "done" || len(prompts) != 1 {
				t.Fatalf("session = %+v, prompts = %q", session, prompts)
			}
			for _, part := range []string{
				"You are a Curator",
				"a maximum of 200 lines",
				"# Memory\n\n" + lesson + "\n\n",
				"# Notes of the Lead of Workstream 12\n\n## " + filepath.Join(lead, "MEMORY.md") + "\n\n" + leadNote + "\n\n",
			} {
				if !strings.Contains(prompts[0], part) {
					t.Errorf("%q is not in %s", part, prompts[0])
				}
			}
			waitForPolls(t, fake)
			if sessions := curatorSessions(t, server); len(sessions) != 1 {
				t.Errorf("Curators after the close = %+v", sessions)
			}
		})
	}
}

func TestAnEditMemoryCallAddsAVersionOfTheCuratorAndTheNextAgentGetsTheText(t *testing.T) {
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), curatorScript)

	session := run(t, server, roleSpec(t, engine.CuratorRole), "Add the lesson")

	data, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "memory", shop+".md")))
	if got := memoryVersions(t, server); err != nil || string(data) != lesson+"\n" || len(got) != 1 || got[0] != "curator" {
		t.Errorf("file = %q, %v, versions = %v", data, err, got)
	}
	if got := memoryReasons(t, server); len(got) != 1 || got[0] != "Add: Workstream 12 repeats the format failure." {
		t.Errorf("reasons = %q", got)
	}
	if calls := mcpCalls(t, server, session); len(calls) != 1 || calls[0]["result"] != "Saved the memory file. It has 1 of 200 lines." {
		t.Errorf("calls = %v", calls)
	}
	sendChat(t, server, leadChat, "Plan the loyalty API")
	prompt := promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]
	if !strings.Contains(prompt, "# Memory\n\n"+lesson+"\n\n") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestAnEditMemoryCallRefusesAnOldTextThatDoesNotOccurOneTimeAndAResultOfMoreThan200Lines(t *testing.T) {
	server, _ := connect(t, testkit.NewFakeGitHub(t), curatorScript)
	if err := server.Engine.SaveMemory(t.Context(), shop, "owner", "", "Same.\nSame.\n"+strings.Repeat("A lesson.\n", 198)); err != nil {
		t.Fatal(err)
	}

	session := run(t, server, roleSpec(t, engine.CuratorRole), "Edit a missing text", "Edit a repeated text", "Add without a reason", "Add a line")

	want := []string{
		"The old text does not occur in the memory file.",
		"The old text occurs 2 times in the memory file. Make it longer, so that it occurs one time.",
		"The reason is empty. Name the type of change and the evidence.",
		"the memory file has too many lines: the text has 201 lines and the maximum is 200",
	}
	calls := mcpCalls(t, server, session)
	if len(calls) != len(want) {
		t.Fatalf("calls = %v", calls)
	}
	for i, call := range calls {
		if call["error"] != want[i] {
			t.Errorf("error of call %d = %v, want %s", i, call["error"], want[i])
		}
	}
	if got := memoryVersions(t, server); len(got) != 1 || got[0] != "owner" {
		t.Errorf("versions = %v", got)
	}
}

func TestThePromptOfTheCuratorHasTheLastVersionsWithTheirAuthorAndReason(t *testing.T) {
	server, _ := connect(t, testkit.NewFakeGitHub(t), curatorScript)
	if err := server.Engine.SaveMemory(t.Context(), shop, "owner", "", lesson+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := server.Engine.SaveMemory(t.Context(), shop, "curator", "Add: issue 41.", lesson+"\nA second lesson.\n"); err != nil {
		t.Fatal(err)
	}

	endLeads(t, server, 10)

	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := curatorSessions(t, server)
		return sessions[0], len(sessions) == 1 && sessions[0].EndedAt.Valid
	})
	prompt := promptTexts(t, server, session.ID)[0]
	_, versions, found := strings.Cut(prompt, "newest first.\n\n")
	versions, _, _ = strings.Cut(versions, "\n\n")
	lines := strings.Split(versions, "\n")
	if !found || len(lines) != 2 || !strings.HasSuffix(lines[0], ", curator: Add: issue 41.") || !strings.HasSuffix(lines[1], ", owner: ") {
		t.Errorf("versions = %q", versions)
	}
}

func TestTwoStartsDuringACuratorSessionGiveOneMoreCuratorSession(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, curatorScript, func(cfg *config.Config) { cfg.Roles.Curator.Max = 1 })
	holder := start(t, server, roleSpec(t, engine.CuratorRole))

	endLeads(t, server, 10)
	if sessions := curatorSessions(t, server); len(sessions) != 2 {
		t.Fatalf("Curators after 10 sessions = %+v", sessions)
	}
	endLeads(t, server, 12)
	if err := server.Engine.CloseWorkstreamWontDo(t.Context(), shop, 12); err != nil {
		t.Fatal(err)
	}
	if sessions := curatorSessions(t, server); len(sessions) != 2 {
		t.Fatalf("Curators during the first session = %+v", sessions)
	}
	end(t, holder, "done")

	testkit.WaitFor(t, func() bool {
		sessions := curatorSessions(t, server)
		return len(sessions) == 3 && sessions[2].EndedAt.Valid
	})
	waitForPolls(t, fake)
	if sessions := curatorSessions(t, server); len(sessions) != 3 {
		t.Errorf("Curators = %+v", sessions)
	}
}

func TestTheClaudeCodeMemoryOfALeadIsInTheDirectoryOfItsProject(t *testing.T) {
	got := engine.ClaudeMemoryDir("/Users/a", "/Users/a/.mobius/leads/O/R/230")
	if want := "/Users/a/.claude/projects/-Users-a--mobius-leads-O-R-230/memory"; got != want {
		t.Errorf("directory = %s, want %s", got, want)
	}
}

func TestTheClaudeCodeMemoryDirectoryReplacesEachCharacterThatIsNotALetterOrADigit(t *testing.T) {
	got := engine.ClaudeMemoryDir("/home/john_doe", "/home/john_doe/my data/leads/O/my_repo/12")
	if want := "/home/john_doe/.claude/projects/-home-john-doe-my-data-leads-O-my-repo-12/memory"; got != want {
		t.Errorf("directory = %s, want %s", got, want)
	}
}

func writeNotes(t *testing.T, dir, name, text string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestThePromptOfTheCuratorHasTheNotesOfTheLeadsThatChangedAfterTheLastDoneCurator(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), curatorScript)
	twelve := filepath.Join(dataDir, "leads", "owner", "shop", "12")
	thirteen := filepath.Join(dataDir, "leads", "owner", "shop", "13")
	index := writeNotes(t, twelve, "MEMORY.md", "- Index of twelve.")
	writeNotes(t, twelve, "ignored.txt", "- Not a note.")
	plans := writeNotes(t, engine.ClaudeMemoryDir(home, twelve), "plans.md", "- Plans use cents.")
	writeNotes(t, thirteen, "MEMORY.md", "- First of thirteen.")
	prompt := func(session store.Session) string {
		t.Helper()
		return promptTexts(t, server, session.ID)[0]
	}
	waitForEnd := func(count int) store.Session {
		t.Helper()
		return testkit.WaitForValue(t, func() (store.Session, bool) {
			sessions := curatorSessions(t, server)
			return sessions[count-1], len(sessions) == count && sessions[count-1].EndedAt.Valid
		})
	}

	endLeads(t, server, 10)

	first := prompt(waitForEnd(1))
	for _, part := range []string{
		"# Notes of the Lead of Workstream 12\n\n## " + index + "\n\n- Index of twelve.\n\n## " + plans + "\n\n- Plans use cents.\n\n",
		"# Notes of the Lead of Workstream 13\n\n## " + filepath.Join(thirteen, "MEMORY.md") + "\n\n- First of thirteen.\n\n",
	} {
		if !strings.Contains(first, part) {
			t.Errorf("%q is not in %s", part, first)
		}
	}
	if strings.Contains(first, "Not a note.") {
		t.Errorf("a file that is not .md is in %s", first)
	}

	writeNotes(t, thirteen, "MEMORY.md", "- Second of thirteen.")
	end(t, start(t, server, roleSpec(t, engine.CuratorRole)), "failed")
	endLeads(t, server, 9)
	if sessions := curatorSessions(t, server); len(sessions) != 2 {
		t.Fatalf("Curators after a failed Curator and 9 sessions = %+v", sessions)
	}
	endLeads(t, server, 1)

	second := prompt(waitForEnd(3))
	if !strings.Contains(second, "# Notes of the Lead of Workstream 13\n\n## "+filepath.Join(thirteen, "MEMORY.md")+"\n\n- Second of thirteen.\n\n") {
		t.Errorf("the changed notes are not in %s", second)
	}
	if strings.Contains(second, "Workstream 12") {
		t.Errorf("the notes of a Workstream with no change are in %s", second)
	}
}
