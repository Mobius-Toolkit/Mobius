package engine_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// antigravityLimit gives the script of an Antigravity agent whose prompt with text hits a usage limit.
func antigravityLimit(text string) string {
	return fmt.Sprintf("\n[[prompts]]\nwhen = %q\nerror = { code = -32603, message = \"Usage Limit Reached. Your quota will reset in 2 days, 3 hours.\" }\n", text)
}

// waitForPausedWorker waits until sessions gives one session and it waits for a pause. It gives the session.
func waitForPausedWorker(t *testing.T, sessions func() []store.Session) store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() (store.Session, bool) {
		found := sessions()
		if len(found) != 1 {
			return store.Session{}, false
		}
		return found[0], strings.HasPrefix(found[0].QueueReason.String, "paused until ")
	})
}

// moveRole sets the Harness, the model and the effort of the Role role. The other fields of the binding stay.
func moveRole(t *testing.T, server *testserver.Server, role string, harness config.Harness, model, effort string) {
	t.Helper()
	settings, err := server.Engine.Roles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var binding config.RoleBinding
	for _, setting := range settings {
		if setting.Role == role {
			binding = setting.Binding
		}
	}
	binding.Harness, binding.Model, binding.Effort = harness, model, effort
	if err := server.Engine.SetRole(t.Context(), role, binding); err != nil {
		t.Fatal(err)
	}
}

func TestATriagerThatWaitsForAPauseMovesToTheNewHarnessAndKeepsItsTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	limited := "[[prompts]]\nwhen = \"# Issue\\n\\nowner/shop#52 \"\nerror = { code = -32603, message = \"Rate limited\", data = { errorKind = \"rate_limit\" } }\n"
	server, dataDir := connect(t, fake, limited)
	gate := filepath.Join(t.TempDir(), "go")
	installDevin(t, dataDir, fmt.Sprintf(`
[[prompts]]
when = "# Issue\n\nowner/shop#52 "
shell = "while [ ! -e '%s' ]; do sleep 0.05; done"
reply = ["Create the Workstream \"Loyalty points\" for this issue."]
`, gate))
	triagerSessions := func() []store.Session { return chatSessions(t, server, issueTriagers, engine.TriagerRole) }
	fake.CreateIssue(shop, 52, "Add loyalty points", "Points for each order.", "owner")
	waitForPausedWorker(t, triagerSessions)

	setHarness(t, server, "triager", config.Devin, "swe-1.5")

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		found := triagerSessions()
		return found, len(found) == 2
	})
	if sessions[0].EndReason.String != "moved" || sessions[1].Harness != "devin" {
		t.Errorf("sessions = %+v", sessions)
	}
	if turns := triagerTurns(t, server); !slices.Equal(turns, []int64{52}) {
		t.Errorf("turns while the new session runs = %v", turns)
	}
	touch(t, gate)
	testkit.WaitFor(t, func() bool {
		proposed := slices.ContainsFunc(fake.Comments(shop, 52), func(comment testkit.Comment) bool {
			return strings.HasPrefix(comment.Body, `Create the Workstream "Loyalty points" for this issue.`)
		})
		return proposed && len(triagerTurns(t, server)) == 0
	})
	prompts := promptTexts(t, server, sessions[1].ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "# Issue\n\nowner/shop#52 Add loyalty points\n\nPoints for each order.") {
		t.Errorf("prompts of the new session = %q", prompts)
	}
	if sessions := triagerSessions(); len(sessions) != 2 || sessions[1].EndReason.String != "done" {
		t.Errorf("sessions = %+v", sessions)
	}
}

func TestACuratorThatWaitsForAPauseMovesToTheNewHarnessAndKeepsItsRequests(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, requestScript)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+antigravityLimit("You are a Curator"))
	installDevin(t, dataDir, "[[prompts]]\nwhen = \"# Requests of the Owner\"\nreply = [\"The memory file is up to date.\"]\n")
	sessions := func() []store.Session { return curatorSessions(t, server) }
	sendChat(t, server, coordinatorChat, "Remember the format rule.")
	approveNext(t, server, 1)
	waitForPausedWorker(t, sessions)

	moveRole(t, server, "curator", config.Devin, "swe-1.5", "high")

	moved := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		found := sessions()
		return found, len(found) == 2 && found[1].EndedAt.Valid
	})
	if moved[0].EndReason.String != "moved" || moved[1].Harness != "devin" || moved[1].EndReason.String != "done" {
		t.Errorf("sessions = %+v", moved)
	}
	prompts := promptTexts(t, server, moved[1].ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "# Requests of the Owner\n\n## Request 1\n\nAdd the lesson: run make fmt before each commit.\n\n") {
		t.Errorf("prompts of the new session = %q", prompts)
	}
	result := fmt.Sprintf("# Curator message\n\nResult of the Curator %d on the requests of the Owner:", moved[1].ID)
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(coordinatorPrompts(t, server), func(prompt string) bool { return strings.Contains(prompt, result) })
	})
	for _, prompt := range coordinatorPrompts(t, server) {
		if strings.Contains(prompt, "The Curator failed") || strings.Contains(prompt, fmt.Sprintf("Result of the Curator %d ", moved[0].ID)) {
			t.Errorf("the Coordinator got an answer of the moved Curator: %s", prompt)
		}
	}
	if n := rowCount(t, server, "SELECT count(*) FROM curator_requests"); n != 0 {
		t.Errorf("requests = %d", n)
	}
}

func TestADiagnoserThatWaitsForAPauseMovesToTheNewHarnessAndTheRowEndsWithItsReasons(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+antigravityLimit("You are a Diagnoser"))
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts+reportsPlan, fixes, func(cfg *config.Config) {
		cfg.Roles.Diagnoser = config.RoleBinding{Harness: config.Antigravity, Model: "gemini-3-pro", Max: 2}
	})
	sha := approvalHead(t, server, fake)
	failActions(fake, sha)
	waitForPausedWorker(t, func() []store.Session { return diagnoserSessions(t, server) })
	var row int64
	if err := server.DB.QueryRow("SELECT id FROM ci_diagnoses").Scan(&row); err != nil {
		t.Fatal(err)
	}

	moveRole(t, server, "diagnoser", config.ClaudeCode, "sonnet", "low")

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		found := diagnoserSessions(t, server)
		return found, len(found) == 2 && found[1].EndedAt.Valid
	})
	if sessions[0].EndReason.String != "moved" || sessions[1].Harness != "claude-code" || sessions[1].EndReason.String != "done" {
		t.Errorf("sessions = %+v", sessions)
	}
	var session int64
	var reasons, ended string
	query := fmt.Sprintf("SELECT coalesce(session, 0), reasons, ended_at FROM ci_diagnoses WHERE id = %d", row)
	testkit.WaitFor(t, func() bool {
		if err := server.DB.QueryRow(query).Scan(&session, &reasons, &ended); err != nil {
			t.Fatal(err)
		}
		return ended != ""
	})
	if session != sessions[1].ID || !strings.Contains(reasons, "The plan test expects cents.") {
		t.Errorf("row = session %d, reasons %q", session, reasons)
	}
	if n := rowCount(t, server, "SELECT count(*) FROM ci_diagnoses"); n != 1 {
		t.Errorf("rows = %d", n)
	}
	if prompts := promptTexts(t, server, sessions[1].ID); len(prompts) != 1 || !strings.Contains(prompts[0], "Head commit: "+sha+"\n") {
		t.Errorf("prompts of the new session = %q", prompts)
	}
}

func TestAResearcherThatWaitsForAPauseMovesToTheNewHarnessWithItsQuestionAndDetails(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectResearcher(t, fake, researchLeadOfDetails, antigravityLimit("# Question"))
	installDevin(t, dataDir, `
[[prompts]]
when = "The Owner gave new details"
reply = ["Discounts are in cents.\n"]

[[prompts]]
when = "# Question"
reply = ["Plans store the price in cents.\n"]
`)
	sessions := func() []store.Session { return roleSessions(t, server, engine.ResearcherRole) }
	sendChat(t, server, leadChat, "Begin alpha.")
	waitForPausedWorker(t, sessions)
	sendChat(t, server, leadChat, "Details for alpha.")
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadCalls(t, server), func(call map[string]any) bool {
			return call["result"] == "Sent the details to the Researcher 2."
		})
	})

	moveRole(t, server, "researcher", config.Devin, "swe-1.5", "high")

	report := waitForLeadPrompt(t, server, "# Researcher message\n\nReport of the Researcher 2")
	if want := "# Researcher message\n\nReport of the Researcher 2 on \"Alpha question\":\n\nDiscounts are in cents.\n"; report != want {
		t.Errorf("report = %q", report)
	}
	moved := sessions()
	if len(moved) != 2 || moved[0].EndReason.String != "moved" || moved[1].Harness != "devin" || moved[1].Parent != moved[0].Parent {
		t.Fatalf("sessions = %+v", moved)
	}
	prompts := promptTexts(t, server, moved[1].ID)
	if len(prompts) != 2 || !strings.Contains(prompts[0], "# Question\n\nAlpha question") ||
		prompts[1] != "The Owner gave new details for the question. They replace the old text where they differ.\n\nAsk about the discount too." {
		t.Errorf("prompts of the new session = %q", prompts)
	}
}

func TestAResearcherThatMovesWhileItsDetailsPromptWaitsGivesTheDetailsToTheNewSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectResearcher(t, fake, researchLeadOfDetails, antigravityLimit("The Owner gave new details")+`
[[prompts]]
when = "# Question"
reply = ["Looking."]
hang = true
`)
	installDevin(t, dataDir, `
[[prompts]]
when = "The Owner gave new details"
reply = ["Discounts are in cents.\n"]

[[prompts]]
when = "# Question"
reply = ["Plans store the price in cents.\n"]
`)
	sessions := func() []store.Session { return roleSessions(t, server, engine.ResearcherRole) }
	sendChat(t, server, leadChat, "Begin alpha.")
	testkit.WaitFor(t, func() bool {
		found := sessions()
		return len(found) == 1 && reply(t, server, found[0].ID) == "Looking."
	})
	sendChat(t, server, leadChat, "Details for alpha.")
	waitForPausedWorker(t, sessions)

	moveRole(t, server, "researcher", config.Devin, "swe-1.5", "high")

	report := waitForLeadPrompt(t, server, "# Researcher message\n\nReport of the Researcher 2")
	if want := "# Researcher message\n\nReport of the Researcher 2 on \"Alpha question\":\n\nDiscounts are in cents.\n"; report != want {
		t.Errorf("report = %q", report)
	}
	moved := sessions()
	if len(moved) != 2 {
		t.Fatalf("sessions = %+v", moved)
	}
	prompts := promptTexts(t, server, moved[1].ID)
	if len(prompts) != 2 || prompts[1] != "The Owner gave new details for the question. They replace the old text where they differ.\n\nAsk about the discount too." {
		t.Errorf("prompts of the new session = %q", prompts)
	}
}

func TestAChangeOfOnlyTheModelOfTheResearcherKeepsTheWaitingResearcher(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearcher(t, fake, researchLeadOfDetails, antigravityLimit("# Question"))
	sessions := func() []store.Session { return roleSessions(t, server, engine.ResearcherRole) }
	sendChat(t, server, leadChat, "Begin alpha.")
	waitForPausedWorker(t, sessions)

	setHarness(t, server, "researcher", config.Antigravity, "haiku")
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	waiting := waitForPausedWorker(t, sessions)
	if waiting.EndedAt.Valid {
		t.Errorf("session = %+v", waiting)
	}
}
