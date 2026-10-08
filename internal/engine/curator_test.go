package engine_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func stamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339Nano)
}

// addSession adds a session of role in the Workstream #12 that starts at startedAt. A session with a reason ends one
// second later. An issue of 0 is no issue.
func addSession(t *testing.T, server *testserver.Server, role string, issue int64, startedAt time.Time, reason string) int64 {
	t.Helper()
	var endedAt, endReason sql.NullString
	if reason != "" {
		endedAt = sql.NullString{String: stamp(startedAt.Add(time.Second)), Valid: true}
		endReason = sql.NullString{String: reason, Valid: true}
	}
	result, err := server.DB.Exec(
		"INSERT INTO sessions (role, harness, model, organization, repository, workstream, issue, started_at, ended_at, end_reason) VALUES (?, 'fake', 'fake', 'owner', ?, 12, ?, ?, ?, ?)",
		role, shop, sql.NullInt64{Int64: issue, Valid: issue != 0}, stamp(startedAt), endedAt, endReason)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addTranscript(t *testing.T, server *testserver.Server, session int64, at time.Time, kind string, row any) {
	t.Helper()
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.DB.Exec("INSERT INTO transcript (session, time, kind, json) VALUES (?, ?, ?, ?)", session, stamp(at), kind, string(data)); err != nil {
		t.Fatal(err)
	}
}

func addCall(t *testing.T, server *testserver.Server, session int64, at time.Time, tool string, arguments any) {
	t.Helper()
	addTranscript(t, server, session, at, "mcp_call", map[string]any{"tool": tool, "arguments": arguments, "result": "ok"})
}

func addChat(t *testing.T, server *testserver.Server, author, text string, at time.Time) {
	t.Helper()
	if _, err := server.DB.Exec("INSERT INTO chat_messages (organization, repository, workstream, author, time, text) VALUES ('owner', ?, 12, ?, ?, ?)", shop, author, stamp(at), text); err != nil {
		t.Fatal(err)
	}
}

// addProblems adds one item of each kind at the time at: an Owner message after a Lead reply, a cannot_do, a hung
// session with a retry prompt, a task with a fix round of the Lead and a review with findings. The texts have the marker.
func addProblems(t *testing.T, server *testserver.Server, marker string, at time.Time) {
	t.Helper()
	addChat(t, server, "Lead", "Lead reply "+marker, at)
	addChat(t, server, "Owner", "Owner correction "+marker, at.Add(time.Second))
	implementer := addSession(t, server, engine.ImplementerRole, 41, at, "hung")
	addCall(t, server, implementer, at, "cannot_do", map[string]string{"reason": "Cannot do " + marker})
	addTranscript(t, server, implementer, at, "prompt", map[string]string{"text": "You had no activity for 15 minutes. You are probably stuck.\n\nRetry " + marker})
	lead := addSession(t, server, engine.LeadRole, 0, at, "done")
	addCall(t, server, lead, at, "start_fix_round", map[string]any{"n": 41, "findings": "Fix round " + marker})
	reviewer := addSession(t, server, engine.ReviewerRole, 41, at, "done")
	addCall(t, server, reviewer, at, "submit_review", map[string]any{
		"body":     "Review summary " + marker,
		"comments": []map[string]any{{"path": "plan.go", "line": 7, "body": "Finding " + marker}},
	})
}

// nextCuratorPrompt ends 10 Lead sessions, waits for the count-th Curator session to end, and gives its first prompt.
func nextCuratorPrompt(t *testing.T, server *testserver.Server, count int) string {
	t.Helper()
	endLeads(t, server, 10)
	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := curatorSessions(t, server)
		return sessions[count-1], len(sessions) >= count && sessions[count-1].EndedAt.Valid
	})
	return promptTexts(t, server, session.ID)[0]
}

func TestThePromptOfTheCuratorHasEachKindOfItemOfTheSessionsOfTheRepository(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), curatorScript)
	addProblems(t, server, "alpha", time.Now().Add(-time.Hour))

	prompt := nextCuratorPrompt(t, server, 1)

	for _, part := range []string{
		"# Messages of the Owner in the Lead chats",
		"Lead:\nLead reply alpha\n\nOwner:\nOwner correction alpha",
		"# Results of cannot_do",
		"implementer, issue #41, ",
		"Cannot do alpha",
		"# Hung sessions",
		"Session ",
		"# Retry prompts after a hang",
		"Retry alpha",
		"# Fix rounds that repeat",
		"Issue #41, 2 fix rounds",
		"Findings of the Lead, ",
		"Fix round alpha",
		"Findings of the Reviewer, ",
		"# Review findings",
		"Review summary alpha\n- plan.go:7: Finding alpha",
	} {
		if !strings.Contains(prompt, part) {
			t.Errorf("%q is not in %s", part, prompt)
		}
	}
}

func TestTheFixRoundsThatRepeatCanComeOnlyFromReviews(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), curatorScript)
	at := time.Now().Add(-time.Hour)
	reviewer := addSession(t, server, engine.ReviewerRole, 52, at, "done")
	for round := 1; round <= 2; round++ {
		addCall(t, server, reviewer, at, "submit_review", map[string]any{
			"body":     fmt.Sprintf("Review %d", round),
			"comments": []map[string]any{{"path": "plan.go", "line": round, "body": fmt.Sprintf("Finding %d", round)}},
		})
	}
	other := addSession(t, server, engine.ReviewerRole, 53, at, "done")
	addCall(t, server, other, at, "submit_review", map[string]any{
		"body":     "Review of the other issue",
		"comments": []map[string]any{{"path": "plan.go", "line": 1, "body": "Other finding"}},
	})

	prompt := nextCuratorPrompt(t, server, 1)

	for _, part := range []string{"Issue #52, 2 fix rounds", "- plan.go:1: Finding 1", "- plan.go:2: Finding 2"} {
		if !strings.Contains(prompt, part) {
			t.Errorf("%q is not in %s", part, prompt)
		}
	}
	if strings.Contains(prompt, "Issue #53, ") {
		t.Errorf("a task with one fix round is in %s", prompt)
	}
}

func TestThePromptOfTheCuratorHasNoItemOfASessionBeforeTheLastDoneCurator(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), curatorScript)
	now := time.Now()
	addProblems(t, server, "stale", now.Add(-3*time.Hour))
	addSession(t, server, engine.CuratorRole, 0, now.Add(-2*time.Hour), "done")
	addProblems(t, server, "new", now.Add(-time.Hour))

	prompt := nextCuratorPrompt(t, server, 2)

	if !strings.Contains(prompt, "Owner correction new") {
		t.Errorf("the items after the Curator are not in %s", prompt)
	}
	if strings.Contains(prompt, "stale") {
		t.Errorf("an item before the Curator is in %s", prompt)
	}
}

func TestAFailedCuratorDoesNotHideTheItemsFromTheNextCurator(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), curatorScript)
	now := time.Now()
	addProblems(t, server, "stale", now.Add(-5*time.Hour))
	addSession(t, server, engine.CuratorRole, 0, now.Add(-4*time.Hour), "done")
	addProblems(t, server, "between", now.Add(-3*time.Hour))
	addSession(t, server, engine.CuratorRole, 0, now.Add(-2*time.Hour), "failed")

	prompt := nextCuratorPrompt(t, server, 3)

	if !strings.Contains(prompt, "Owner correction between") {
		t.Errorf("the items between the done Curator and the failed Curator are not in %s", prompt)
	}
	if strings.Contains(prompt, "stale") {
		t.Errorf("an item before the done Curator is in %s", prompt)
	}
}

func TestThePromptOfTheCuratorCutsALongTextAndLeavesOutTheOldestItems(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), curatorScript)
	now := time.Now().Add(-time.Hour)
	addChat(t, server, "Owner", strings.Repeat("a", 1000)+"b", now)
	implementer := addSession(t, server, engine.ImplementerRole, 41, now, "done")
	for i := range 21 {
		addCall(t, server, implementer, now.Add(time.Duration(i)*time.Second), "cannot_do", map[string]string{"reason": fmt.Sprintf("Reason %02d", i)})
	}

	prompt := nextCuratorPrompt(t, server, 1)

	if !strings.Contains(prompt, "Owner:\n"+strings.Repeat("a", 1000)+" (cut)\n") {
		t.Errorf("the long text is not cut in %s", prompt)
	}
	if !strings.Contains(prompt, "Mobius left out the 1 oldest items.") || strings.Contains(prompt, "Reason 00") || !strings.Contains(prompt, "Reason 20") {
		t.Errorf("the oldest item is not left out in %s", prompt)
	}
}
