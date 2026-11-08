package engine_test

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const answersDone = "[[prompts]]\nreply = [\"Done.\"]\n"

// waitForCopy waits until the local copy has the issue number of repository.
func waitForCopy(t *testing.T, server *testserver.Server, repository string, number int64) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		_, _, err := server.Engine.NextBinding(t.Context(), "implementer", repository, number)
		return err == nil
	})
}

// addCopiedTask adds the issue number below the Workstream #12, and waits until the local copy has it.
func addCopiedTask(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, number int64) {
	t.Helper()
	fake.AddIssue(shop, number, "Task")
	fake.AddSubIssue(shop, 12, number)
	waitForCopy(t, server, shop, number)
}

func setBinding(t *testing.T, server *testserver.Server, role string, issue int64, harness config.Harness, model, effort string) {
	t.Helper()
	if err := server.Engine.SetIssueBinding(t.Context(), role, shop, issue, harness, model, effort); err != nil {
		t.Fatal(err)
	}
}

func clearBinding(t *testing.T, server *testserver.Server, role, repository string, issue int64) {
	t.Helper()
	if err := server.Engine.ClearIssueBinding(t.Context(), role, repository, issue); err != nil {
		t.Fatal(err)
	}
}

// taskSpec gives the spec of an Implementer session of the issue number of repository in the Workstream #12.
func taskSpec(t *testing.T, repository string, number int64) engine.Spec {
	t.Helper()
	spec := roleSpec(t, engine.ImplementerRole)
	spec.Repository, spec.WorkstreamRepository = repository, shop
	spec.Issue = sql.NullInt64{Int64: number, Valid: true}
	return spec
}

// taskSession runs an Implementer session of the issue number of repository, and gives its row.
func taskSession(t *testing.T, server *testserver.Server, repository string, number int64) store.Session {
	t.Helper()
	agent := start(t, server, taskSpec(t, repository, number))
	end(t, agent, "done")
	return session(t, server, agent.ID())
}

func requireBinding(t *testing.T, got store.Session, harness, model, effort string) {
	t.Helper()
	if got.Harness != harness || got.Model != model || got.Effort.String != effort {
		t.Errorf("session %d has %s %s %s, want %s %s %s", got.ID, got.Harness, got.Model, got.Effort.String, harness, model, effort)
	}
}

func requireNext(t *testing.T, server *testserver.Server, role, repository string, issue int64, level, harness, model, effort string) {
	t.Helper()
	binding, got, err := server.Engine.NextBinding(t.Context(), role, repository, issue)
	if err != nil {
		t.Fatal(err)
	}
	if got != level || string(binding.Harness) != harness || binding.Model != model || binding.Effort != effort {
		t.Errorf("next binding of %s for %s#%d = %s %s %s at %s, want %s %s %s at %s", role, repository, issue, binding.Harness, binding.Model, binding.Effort, got, harness, model, effort, level)
	}
}

// awaitWithin gives the agent of the result when Start ends, and fails the test when it does not end in time.
func awaitWithin(t *testing.T, result <-chan started) *engine.Agent {
	t.Helper()
	var agent *engine.Agent
	testkit.WaitFor(t, func() bool {
		select {
		case r := <-result:
			if r.err != nil {
				t.Fatal(r.err)
			}
			agent = r.agent
			return true
		default:
			return false
		}
	})
	return agent
}

func oneSlot(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 }

func TestATaskWithNoSettingStartsItsImplementerWithTheBindingOfTheRole(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, answersDone)
	addCopiedTask(t, server, fake, 41)

	requireBinding(t, taskSession(t, server, shop, 41), "devin", "swe-1.5", "high")
	requireNext(t, server, "implementer", shop, 41, engine.LevelRole, "devin", "swe-1.5", "high")
}

func TestATaskWithASettingStartsItsImplementerWithTheSettingAndItsReviewerWithTheBindingOfTheRole(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, noChange)
	waitForCopy(t, server, shop, 41)
	setBinding(t, server, "implementer", 41, config.Devin, "opus", "low")

	implementer := firstRound(t, server, fake)

	requireBinding(t, implementer, "devin", "opus", "low")
	reviewers := roleSessions(t, server, engine.ReviewerRole)
	if len(reviewers) != 1 {
		t.Fatalf("reviewers = %+v", reviewers)
	}
	requireBinding(t, reviewers[0], "claude-code", "opus", "high")
}

func TestALeadOfAWorkstreamWithNoSettingAndWithASettingGetsEachBinding(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	sendChat(t, server, leadChat, "Plan the API")
	first := endedChatSession(t, server, 0)
	setBinding(t, server, "lead", 12, config.ClaudeCode, "sonnet", "low")
	sendChat(t, server, leadChat, "Plan the tests")

	second := endedChatSession(t, server, 1)
	requireBinding(t, first, "claude-code", "opus", "high")
	requireBinding(t, second, "claude-code", "sonnet", "low")
	requireNext(t, server, "lead", shop, 12, engine.LevelWorkstream, "claude-code", "sonnet", "low")
}

func TestTheTaskSettingWinsOverTheWorkstreamSettingAndTheBindingOfTheRole(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, answersDone)
	addCopiedTask(t, server, fake, 41)
	addCopiedTask(t, server, fake, 43)

	setBinding(t, server, "implementer", 12, config.Devin, "opus", "medium")
	requireBinding(t, taskSession(t, server, shop, 41), "devin", "opus", "medium")
	requireNext(t, server, "implementer", shop, 41, engine.LevelWorkstream, "devin", "opus", "medium")
	requireNext(t, server, "implementer", shop, 12, engine.LevelWorkstream, "devin", "opus", "medium")

	setBinding(t, server, "implementer", 41, config.Devin, "sonnet", "low")
	requireBinding(t, taskSession(t, server, shop, 41), "devin", "sonnet", "low")
	requireBinding(t, taskSession(t, server, shop, 43), "devin", "opus", "medium")
	requireNext(t, server, "implementer", shop, 41, engine.LevelTask, "devin", "sonnet", "low")
	row, found, err := server.Engine.IssueBinding(t.Context(), "implementer", shop, 41)
	if err != nil || !found || row.Harness != "devin" || row.Model != "sonnet" || row.Effort != "low" {
		t.Errorf("row = %+v, found = %v, err = %v", row, found, err)
	}

	clearBinding(t, server, "implementer", shop, 41)
	requireBinding(t, taskSession(t, server, shop, 41), "devin", "opus", "medium")
	if _, found, err := server.Engine.IssueBinding(t.Context(), "implementer", shop, 41); err != nil || found {
		t.Errorf("found = %v, err = %v after the clear", found, err)
	}

	clearBinding(t, server, "implementer", shop, 12)
	requireBinding(t, taskSession(t, server, shop, 41), "devin", "swe-1.5", "high")
	requireNext(t, server, "implementer", shop, 12, engine.LevelRole, "devin", "swe-1.5", "high")
}

func TestATaskInAnotherRepositoryThanItsWorkstreamUsesTheSettingOfTheWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := foreignTask(t, fake)
	waitForCopy(t, server, garden, 7)

	setBinding(t, server, "implementer", 12, config.Devin, "opus", "medium")

	requireNext(t, server, "implementer", garden, 7, engine.LevelWorkstream, "devin", "opus", "medium")
	requireBinding(t, taskSession(t, server, garden, 7), "devin", "opus", "medium")
}

func TestARoundAfterAClearOfTheTaskSettingUsesTheBindingOfTheRole(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, noChange)
	waitForCopy(t, server, shop, 41)
	setBinding(t, server, "implementer", 41, config.Devin, "opus", "low")
	first := firstRound(t, server, fake)
	waitForKept(t, server, 41)

	clearBinding(t, server, "implementer", shop, 41)
	startFixRound(t, server)

	second := waitForImplementerSessions(t, server, 2)[1]
	requireBinding(t, first, "devin", "opus", "low")
	requireBinding(t, second, "devin", "swe-1.5", "high")
	if second.Continued != 0 {
		t.Errorf("session = %+v, want a new process for the other binding", second)
	}
}

func TestARoundAfterAChangeOfTheTaskSettingToAnotherModelUsesItInANewProcessAndDoesNotContinueTheKeptOne(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := keptServer(t, fake, commits, noChange)
	first := firstRound(t, server, fake)
	waitForKept(t, server, 41)

	setBinding(t, server, "implementer", 41, config.Devin, "opus", "high")
	before := harnessStarts(t, dataDir)
	startFixRound(t, server)

	second := waitForImplementerSessions(t, server, 2)[1]
	requireBinding(t, second, "devin", "opus", "high")
	if second.Continued != 0 || second.AcpSessionID.String == first.AcpSessionID.String {
		t.Errorf("session = %+v", second)
	}
	if got := harnessStarts(t, dataDir); got != before+1 {
		t.Errorf("starts = %d, want %d", got, before+1)
	}
}

func TestAChangeOfTheLeadSettingMovesNoLeadThatWaitsForAPause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	installDevin(t, dataDir, movedReply)
	sendChat(t, server, leadChat, "Plan the API")
	waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)

	setBinding(t, server, "lead", 12, config.Devin, "swe-1.5", "high")
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 1 || sessions[0].EndedAt.Valid || sessions[0].EndReason.Valid {
		t.Fatalf("sessions = %+v", sessions)
	}
	requireBinding(t, sessions[0], "claude-code", "opus", "high")
}

func TestAChangeOfTheTaskSettingWhileTheImplementerWaitsForASlotGivesItTheNewBinding(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, answersDone, oneSlot)
	addCopiedTask(t, server, fake, 41)
	addCopiedTask(t, server, fake, 43)
	running := start(t, server, taskSpec(t, shop, 41))
	defer end(t, running, "done")
	waiting := startLater(t.Context(), server, taskSpec(t, shop, 43))
	queuedSession := queued(t, server, engine.ImplementerRole)
	requireBinding(t, queuedSession, "devin", "swe-1.5", "high")

	setBinding(t, server, "implementer", 43, config.Devin, "opus", "low")

	testkit.WaitFor(t, func() bool {
		changed := session(t, server, queuedSession.ID)
		return changed.Model == "opus" && changed.QueueReason.Valid
	})
	end(t, running, "done")
	agent := awaitWithin(t, waiting)
	defer end(t, agent, "done")
	requireBinding(t, session(t, server, agent.ID()), "devin", "opus", "low")
}

func TestAChangeOfTheHarnessOfTheImplementerRoleKeepsTheHarnessOfAWaitingSessionWithASetting(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, answersDone, oneSlot)
	addCopiedTask(t, server, fake, 41)
	addCopiedTask(t, server, fake, 43)
	addCopiedTask(t, server, fake, 44)
	setBinding(t, server, "implementer", 43, config.Devin, "opus", "low")
	running := start(t, server, taskSpec(t, shop, 41))
	defer end(t, running, "done")
	startLater(t.Context(), server, taskSpec(t, shop, 43))
	queued(t, server, engine.ImplementerRole)
	startLater(t.Context(), server, taskSpec(t, shop, 44))
	testkit.WaitFor(t, func() bool {
		count := 0
		for _, node := range tree(t, server) {
			if node.Session.Role == engine.ImplementerRole && node.Session.QueueReason.Valid {
				count++
			}
		}
		return count == 2
	})

	moveImplementerToClaudeCode(t, server)

	testkit.WaitFor(t, func() bool {
		harnesses := queuedHarnesses(t, server)
		return slices.Equal(harnesses, []string{"claude-code", "devin"})
	})
	waitForPolls(t, fake)
	if harnesses := queuedHarnesses(t, server); !slices.Equal(harnesses, []string{"claude-code", "devin"}) {
		t.Errorf("harnesses of the waiting sessions = %v", harnesses)
	}
}

// queuedHarnesses gives the sorted Harnesses of the Implementer sessions that wait for a slot.
func queuedHarnesses(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var harnesses []string
	for _, node := range tree(t, server) {
		if node.Session.Role == engine.ImplementerRole && node.Session.QueueReason.Valid {
			harnesses = append(harnesses, node.Session.Harness)
		}
	}
	slices.Sort(harnesses)
	return harnesses
}

func TestALeadWithASettingThatWaitsForAPauseStaysAfterAChangeOfTheHarnessOfTheLeadRole(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	installDevin(t, dataDir, movedReply)
	setBinding(t, server, "lead", 12, config.ClaudeCode, "haiku", "low")
	sendChat(t, server, leadChat, "Plan the API")
	waitForPausedSession(t, server, leadChat, engine.LeadRole, 1)

	setHarness(t, server, "lead", config.Devin, "swe-1.5")
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 1 || sessions[0].EndedAt.Valid || sessions[0].EndReason.Valid {
		t.Fatalf("sessions = %+v", sessions)
	}
	requireBinding(t, sessions[0], "claude-code", "haiku", "low")
}

func TestASessionWithASettingPassesItsAccessCheckAfterARestart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, answersDone)
	addCopiedTask(t, server, fake, 41)
	setBinding(t, server, "implementer", 41, config.Devin, "opus", "low")
	server.Stop()

	restarted := testserver.Start(t, dataDir, fake.URL)
	restarted.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, restarted.Engine.AccessChecked)
	agent := awaitWithin(t, startLater(t.Context(), restarted, taskSpec(t, shop, 41)))
	defer end(t, agent, "done")

	requireBinding(t, session(t, restarted, agent.ID()), "devin", "opus", "low")
}

func TestASettingThatTheRolesPageWouldRefuseIsRefusedAndStoresNoRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, answersDone)
	addCopiedTask(t, server, fake, 41)
	cases := []struct {
		name          string
		harness       config.Harness
		model, effort string
	}{
		{"an unknown Harness", "gemini", "swe-1.5", "high"},
		{"an empty model", config.Devin, "", "high"},
		{"no effort for a Harness with effort levels", config.Devin, "swe-1.5", ""},
		{"an effort for a Harness with no effort levels", config.Antigravity, "gemini-3-pro", "high"},
		{"the Harness and the model of the Reviewer", config.ClaudeCode, "opus", "high"},
	}
	for _, c := range cases {
		err := server.Engine.SetIssueBinding(t.Context(), "implementer", shop, 41, c.harness, c.model, c.effort)
		if !engine.Refused(err) {
			t.Errorf("%s: err = %v, want a refusal", c.name, err)
		}
	}
	for _, refused := range []error{
		server.Engine.SetIssueBinding(t.Context(), "lead", shop, 41, config.ClaudeCode, "sonnet", "low"),
		server.Engine.SetIssueBinding(t.Context(), "judge", shop, 41, config.ClaudeCode, "sonnet", "low"),
		server.Engine.SetIssueBinding(t.Context(), "implementer", shop, 99, config.Devin, "opus", "low"),
	} {
		if !engine.Refused(refused) {
			t.Errorf("err = %v, want a refusal", refused)
		}
	}

	rows, err := store.New(server.DB).ListIssueBindings(t.Context())
	if err != nil || len(rows) != 0 {
		t.Errorf("rows = %+v, err = %v", rows, err)
	}
}

func TestAPauseOfAHarnessThatOnlyASettingUsesIsInUse(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageLimit)
	pausedDevinWithNoSession(t, server, fake)
	moveImplementerToClaudeCode(t, server)
	addCopiedTask(t, server, fake, 43)
	if devinInUse(t, apiPauses(t, server)) {
		t.Fatal("the pause of devin is in use before the setting")
	}

	setBinding(t, server, "implementer", 43, config.Devin, "opus", "low")

	if !devinInUse(t, apiPauses(t, server)) {
		t.Error("the pause of devin is not in use, but a task has a setting with devin")
	}
}
