package engine_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// readSessionFiles is the entry of the Curator of the skill repository that reads its session files. The worktree of
// the Curator is four levels below the data directory.
const readSessionFiles = `
[[prompts]]
when = "# Session files"
shell = "tail -n +1 ../../../../scratch/*/sessions/*.txt"
`

// runCommand gives the entry that sends the updates of one tool call when the prompt has when. The call has the
// command in rawInput, and an output and a rawOutput that must not reach a session file.
func runCommand(when, command string) string {
	updates := []map[string]any{
		{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Bash", "kind": "execute", "status": "pending", "rawInput": map[string]any{}},
		{
			"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed",
			"rawInput":  map[string]any{"command": command},
			"rawOutput": "secret-raw",
			"content":   []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "secret-output"}}},
		},
	}
	var list []string
	for _, update := range updates {
		data, err := json.Marshal(update)
		if err != nil {
			panic(err)
		}
		list = append(list, fmt.Sprintf("'''%s'''", data))
	}
	return fmt.Sprintf("[[prompts]]\nwhen = %q\nupdates = [%s]\nreply = [\"Ran\"]\n", when, strings.Join(list, ", "))
}

// endSessions runs and ends count sessions of spec, each with the prompt.
func endSessions(t *testing.T, server *testserver.Server, count int, spec func() engine.Spec, prompt string) []int64 {
	t.Helper()
	var ids []int64
	for range count {
		ids = append(ids, run(t, server, spec(), prompt))
	}
	return ids
}

func skillsCurators(t *testing.T, server *testserver.Server, repository string, count int) []store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]store.Session, bool) {
		var found []store.Session
		for _, session := range curatorSessions(t, server) {
			if session.Repository == repository && session.EndReason.String == "done" {
				found = append(found, session)
			}
		}
		return found, len(found) == count
	})
}

func skillsServer(t *testing.T, script string) (*testserver.Server, string) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	return connect(t, fake, script)
}

func TestCuratorEverySessionsOfTheOrganizationStartTheCuratorOfTheSkillRepository(t *testing.T) {
	t.Parallel()
	server, _ := skillsServer(t, readSessionFiles+runCommand("Run the command", "grep -n FAIL build.log")+curatorScript)

	ids := endSessions(t, server, 9, func() engine.Spec { return leadSpec(t) }, "Run the command")
	if sessions := curatorSessions(t, server); len(sessions) != 0 {
		t.Fatalf("Curators after 9 sessions = %+v", sessions)
	}
	ids = append(ids, endSessions(t, server, 1, func() engine.Spec { return leadSpec(t) }, "Run the command")...)

	curators := skillsCurators(t, server, skills, 1)
	text := reply(t, server, curators[0].ID)
	for _, id := range ids {
		if want := fmt.Sprintf("==> ../../../../scratch/%d/sessions/%d.txt <==", curators[0].ID, id); !strings.Contains(text, want) {
			t.Errorf("reply %q does not have %q", text, want)
		}
	}
	want := "Role: lead_chat\nRepository: owner/shop\n\nTitle: Bash\nKind: execute\nInput: {\"command\":\"grep -n FAIL build.log\"}\n"
	if got := strings.Count(text, want); got != len(ids) {
		t.Errorf("reply has %d files with the command, want %d: %q", got, len(ids), text)
	}
}

func TestASessionFileHasNoToolOutputAndNoPromptTextAndCutsALongInput(t *testing.T) {
	t.Parallel()
	command := strings.Repeat("a", 5000)
	server, _ := skillsServer(t, readSessionFiles+runCommand("Run the long command", command)+curatorScript)
	spec := func() engine.Spec {
		spec := roleSpec(t, engine.ImplementerRole)
		spec.Issue = sql.NullInt64{Int64: 41, Valid: true}
		return spec
	}

	endSessions(t, server, 10, spec, "Run the long command prompt-marker")

	text := reply(t, server, skillsCurators(t, server, skills, 1)[0].ID)
	for _, absent := range []string{"secret-output", "secret-raw", "prompt-marker"} {
		if strings.Contains(text, absent) {
			t.Errorf("reply has %q", absent)
		}
	}
	if !strings.Contains(text, "Role: implementer\nRepository: owner/shop\nIssue: 41\n") {
		t.Errorf("reply = %q", text)
	}
	const prefix, cut = "Input: ", " (cut)"
	cutLines := 0
	for line := range strings.SplitSeq(text, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		cutLines++
		if got, want := len([]rune(line)), len(prefix)+4000+len(cut); got != want || !strings.HasSuffix(line, cut) {
			t.Errorf("input line has %d characters, want %d, and ends with %q: %q", got, want, cut, line[len(line)-10:])
		}
	}
	if cutLines != 10 {
		t.Errorf("reply has %d input lines, want 10", cutLines)
	}
}

func TestTheNextRunOfTheCuratorOfTheSkillRepositoryGetsNoFileOfAnEarlierSession(t *testing.T) {
	t.Parallel()
	script := readSessionFiles + runCommand("Run the second command", "second-command") + runCommand("Run the first command", "first-command") + curatorScript
	server, _ := skillsServer(t, script)
	lead := func() engine.Spec { return leadSpec(t) }
	endSessions(t, server, 10, lead, "Run the first command")
	skillsCurators(t, server, skills, 1)

	second := endSessions(t, server, 10, lead, "Run the second command")

	text := reply(t, server, skillsCurators(t, server, skills, 2)[1].ID)
	if strings.Contains(text, "first-command") || strings.Count(text, "second-command") != len(second) || strings.Count(text, "==> ") != len(second) {
		t.Errorf("reply = %q", text)
	}
}

func TestTheCuratorOfAnotherRepositoryGetsNoSessionFiles(t *testing.T) {
	t.Parallel()
	server, dataDir := skillsServer(t, readSessionFiles+runCommand("Run the command", "grep -n FAIL build.log")+curatorScript)

	endSessions(t, server, 10, func() engine.Spec { return leadSpec(t) }, "Run the command")

	skillsCurators(t, server, skills, 1)
	shopCurator := skillsCurators(t, server, shop, 1)[0]
	if prompt := rows(t, server, shopCurator.ID, "prompt")[0]["text"].(string); strings.Contains(prompt, "# Session files") {
		t.Errorf("prompt of the Curator of %s has the section of the session files", shop)
	}
	if text := reply(t, server, shopCurator.ID); strings.Contains(text, "==> ") {
		t.Errorf("reply = %q", text)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "scratch", fmt.Sprint(shopCurator.ID))); !os.IsNotExist(err) {
		t.Errorf("scratch directory of the Curator of %s: %v", shop, err)
	}
}

func TestACuratorOfTheSkillRepositoryProposesASkillToTheLeadOfTheWorkstreamSkills(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	addSkillsWorkstream(fake, 20)
	script := callProposeSkill("# Session files", "failed-tests", "3 sessions grep FAIL in build.log.", [2]string{"SKILL.md", "Run grep -n FAIL on the log.\n"}) + curatorScript
	server, _ := connect(t, fake, script)

	endSessions(t, server, 10, func() engine.Spec { return leadSpec(t) }, "Run the command")

	events := testkit.WaitForValue(t, func() ([]leadEvent, bool) {
		events := skillProposals(leadEvents(t, server))
		return events, len(events) == 1
	})
	if events[0].Workstream != 20 || !strings.Contains(events[0].Payload, `"failed-tests"`) || !strings.Contains(events[0].Payload, "Reason: 3 sessions grep FAIL in build.log.") {
		t.Errorf("event = %+v", events[0])
	}
}
