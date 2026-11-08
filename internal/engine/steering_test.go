package engine_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	checkRequest = "Tell Mobius your state"
	checkNote    = "asks the agent for its state"
	openNote     = "a tool call is open"
	injected     = "steering_outcome = \"injected\"\n\n"

	openCall          = `{"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "Run", "status": "in_progress"}`
	backgroundRunning = "call = { tool = \"background_state\", arguments = { state = \"running\" } }\n"
	movedCommand      = `{"sessionUpdate": "tool_call_update", "toolCallId": "m1", "_meta": {"claudeCode": {"toolName": "Bash", "toolResponse": {"backgroundTaskId": "b1", "backgroundedToDeliverMessage": true}}}}`
	movedSubagentCall = `{"sessionUpdate": "tool_call_update", "toolCallId": "m2", "_meta": {"claudeCode": {"parentToolUseId": "p1", "toolName": "Bash", "toolResponse": {"backgroundTaskId": "b2", "backgroundedToDeliverMessage": true}}}}`
)

func endedCall(status string) string {
	return `{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "` + status + `"}`
}

// shortBackgroundTimeout makes the time with no activity that Mobius waits for a tool call, and the hour of the checks,
// short for the test. It changes a package variable, so the test that calls it stays serial.
func shortBackgroundTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	before := *engine.BackgroundTimeout
	*engine.BackgroundTimeout = timeout
	t.Cleanup(func() { *engine.BackgroundTimeout = before })
}

// shortStartTimeout makes the limit of a steering request short for the test. It changes a package variable, so the
// test that calls it stays serial.
func shortStartTimeout(t *testing.T, limit time.Duration) {
	t.Helper()
	before := runner.StartTimeout
	runner.StartTimeout = limit
	t.Cleanup(func() { runner.StartTimeout = before })
}

// connectSteering starts a server with a fake Harness that has the script keys of header and plays prompts.
func connectSteering(t *testing.T, fake *testkit.FakeGitHub, header, prompts string) *testserver.Server {
	t.Helper()
	server, _ := connectScript(t, fake, header+options+prompts, func(*config.Config) {})
	return server
}

// promptWithin sends text to agent, and gives the time of the prompt and its error.
func promptWithin(t *testing.T, agent *engine.Agent, text string) (time.Duration, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	started := time.Now()
	err := agent.Prompt(ctx, text, nil)
	return time.Since(started), err
}

func countNotes(t *testing.T, server *testserver.Server, session int64, part string) int {
	t.Helper()
	count := 0
	for _, note := range noteTexts(t, server, session) {
		if strings.Contains(note, part) {
			count++
		}
	}
	return count
}

func endHungAgent(t *testing.T, agent *engine.Agent) {
	t.Helper()
	if err := agent.End(t.Context(), "hung"); err != nil {
		t.Fatal(err)
	}
}

// Serial: the test changes hangTimeout.
func TestACheckThatTheAgentAnswersWithRunningEndsTheTurnWithNoCancelAndStartsTheWait(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server := connectSteering(t, fake, injected, "[[prompts]]\nhang = true\n\n"+
		"[[prompts]]\nwhen = \""+checkRequest+"\"\n"+backgroundRunning+"\n"+
		"[[prompts]]\nwhen = \""+waitPrompt+"\"\n"+backgroundDone)
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.WaitBackground(t.Context(), waitPrompt); err != nil {
		t.Fatal(err)
	}

	prompts := promptTexts(t, server, agent.ID())
	if len(prompts) != 3 || prompts[0] != "One" || !strings.Contains(prompts[1], checkRequest) || prompts[2] != waitPrompt {
		t.Errorf("prompts = %q", prompts)
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 1 || !strings.Contains(notes[0], checkNote) {
		t.Errorf("notes = %q", notes)
	}
	if sent := rows(t, server, agent.ID(), "prompt"); sent[1]["check"] != true || sent[0]["check"] != nil {
		t.Errorf("prompt rows = %v", sent)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

// Serial: the test changes hangTimeout.
func TestAnAgentThatAnswersACheckWithWorkUpdatesContinuesTheSameTurn(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server := connectSteering(t, fake, injected, "[[prompts]]\nhang = true\n\n[[prompts]]\nwhen = \""+checkRequest+"\"\nreply = [\"Still at work.\"]\n")
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	prompts := promptTexts(t, server, agent.ID())
	if len(prompts) != 2 || !strings.Contains(prompts[1], checkRequest) {
		t.Errorf("prompts = %q", prompts)
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 1 || !strings.Contains(notes[0], checkNote) {
		t.Errorf("notes = %q", notes)
	}
	if text := reply(t, server, agent.ID()); text != "Still at work." {
		t.Errorf("reply = %q", text)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

// Serial: the test changes hangTimeout.
func TestAnAgentWithSteeringThatGivesNoAnswerGetsOneCheckThenTheCancelAndTheThreeRetriesAndEndsAsHung(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server := connectSteering(t, fake, injected, "[[prompts]]\nhang = true\n\n[[prompts]]\nwhen = \""+checkRequest+"\"\nhang = true\n")
	agent := start(t, server, leadSpec(t))

	_, err := promptWithin(t, agent, "One")

	if !errors.Is(err, engine.ErrHung) {
		t.Fatalf("Prompt = %v", err)
	}
	if prompts := promptTexts(t, server, agent.ID()); len(prompts) != 5 {
		t.Errorf("prompts = %q", prompts)
	}
	if count := countNotes(t, server, agent.ID(), checkNote); count != 1 {
		t.Errorf("checks = %d", count)
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 5 || !strings.Contains(notes[4], "Mobius stops the session") {
		t.Errorf("notes = %q", notes)
	}
	endHungAgent(t, agent)
}

// Serial: the test changes hangTimeout.
func TestAnAgentWithNoSteeringAndNoOpenToolCallGetsTheCancelAndTheRetryPromptAtTheHangTimeAndEndsAsHung(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, strings.Repeat(hang, 4))
	agent := start(t, server, leadSpec(t))

	_, err := promptWithin(t, agent, "One")

	if !errors.Is(err, engine.ErrHung) {
		t.Fatalf("Prompt = %v", err)
	}
	if prompts := promptTexts(t, server, agent.ID()); len(prompts) != 4 {
		t.Errorf("prompts = %q", prompts)
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 4 || !strings.Contains(notes[0], "retry 1 of 3") || !strings.Contains(notes[3], "Mobius stops the session") {
		t.Errorf("notes = %q", notes)
	}
	endHungAgent(t, agent)
}

// Serial: the test changes hangTimeout and backgroundTimeout.
func TestAnAgentWithNoSteeringAndAnOpenToolCallGetsTheCancelOnlyAtTheBackgroundTimeout(t *testing.T) {
	shortHang(t)
	shortBackgroundTimeout(t, time.Second)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+openCall+"']\nhang = true\n\n"+strings.Repeat(hang, 3))
	agent := start(t, server, leadSpec(t))

	elapsed, err := promptWithin(t, agent, "One")

	if !errors.Is(err, engine.ErrHung) {
		t.Fatalf("Prompt = %v", err)
	}
	if want := time.Second + 3*300*time.Millisecond; elapsed < want {
		t.Errorf("the prompt took %s, want %s or more", elapsed, want)
	}
	if count := countNotes(t, server, agent.ID(), openNote); count != 1 {
		t.Errorf("notes of the wait = %d, notes = %q", count, noteTexts(t, server, agent.ID()))
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 5 || !strings.Contains(notes[1], "retry 1 of 3") || !strings.Contains(notes[4], "Mobius stops the session") {
		t.Errorf("notes = %q", notes)
	}
	endHungAgent(t, agent)
}

// Serial: the test changes hangTimeout and backgroundTimeout.
func TestAnAgentWithNoSteeringWhoseToolCallEndedGetsTheCancelAtTheHangTime(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		t.Run(status, func(t *testing.T) {
			shortHang(t)
			shortBackgroundTimeout(t, 10*time.Second)
			fake := testkit.NewFakeGitHub(t)
			server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+openCall+"', '"+endedCall(status)+"']\nhang = true\n\n"+strings.Repeat(hang, 3))
			agent := start(t, server, leadSpec(t))

			elapsed, err := promptWithin(t, agent, "One")

			if !errors.Is(err, engine.ErrHung) {
				t.Fatalf("Prompt = %v", err)
			}
			if elapsed >= 10*time.Second {
				t.Errorf("the prompt took %s", elapsed)
			}
			if count := countNotes(t, server, agent.ID(), openNote); count != 0 {
				t.Errorf("notes of the wait = %d", count)
			}
			endHungAgent(t, agent)
		})
	}
}

// Serial: the test changes hangTimeout and backgroundTimeout.
func TestAnUpdateOfAnAgentWithNoSteeringAndAnOpenToolCallGivesANewTimeWithNoActivity(t *testing.T) {
	shortHang(t)
	shortBackgroundTimeout(t, 10*time.Second)
	fake := testkit.NewFakeGitHub(t)
	gate := filepath.Join(t.TempDir(), "gate")
	server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+openCall+"']\nshell = \"while [ ! -e '"+gate+"' ]; do sleep 0.05; done\"\n")
	agent := start(t, server, leadSpec(t))
	done := make(chan error, 1)
	go func() { done <- agent.Prompt(t.Context(), "One", nil) }()
	testkit.WaitFor(t, func() bool { return countNotes(t, server, agent.ID(), openNote) == 1 })

	agent.Update(fmt.Appendf(nil, agentUpdate, workUpdate))

	// The wait adds its note again only after new activity, so the second note shows that the update reset the idle time.
	testkit.WaitFor(t, func() bool { return countNotes(t, server, agent.ID(), openNote) == 2 })
	touch(t, gate)

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if prompts := promptTexts(t, server, agent.ID()); len(prompts) != 1 {
		t.Errorf("prompts = %q", prompts)
	}
	for _, note := range noteTexts(t, server, agent.ID()) {
		if strings.Contains(note, "retry") {
			t.Errorf("note = %q", note)
		}
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

// Serial: the test changes hangTimeout and backgroundTimeout.
func TestARetryTurnWithAnOpenToolCallGetsTheCancelAtTheHangTime(t *testing.T) {
	shortHang(t)
	shortBackgroundTimeout(t, 10*time.Second)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nhang = true\n\n"+strings.Repeat("[[prompts]]\nupdates = ['"+openCall+"']\nhang = true\n\n", 3))
	agent := start(t, server, leadSpec(t))

	elapsed, err := promptWithin(t, agent, "One")

	if !errors.Is(err, engine.ErrHung) {
		t.Fatalf("Prompt = %v", err)
	}
	if elapsed >= 5*time.Second {
		t.Errorf("the prompt took %s", elapsed)
	}
	if count := countNotes(t, server, agent.ID(), openNote); count != 0 {
		t.Errorf("notes of the wait = %d", count)
	}
	endHungAgent(t, agent)
}

// Serial: the test changes hangTimeout and StartTimeout.
func TestASteeringRequestThatGetsNoResponseGivesTheCancelAndTheRetryPromptAfterTheStartTimeout(t *testing.T) {
	shortHang(t)
	shortStartTimeout(t, 300*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	server := connectSteering(t, fake, "steering_hang = true\n\n", "[[prompts]]\nhang = true\n\n[[prompts]]\nwhen = \""+checkRequest+"\"\nreply = [\"Done\"]\n")
	agent := start(t, server, leadSpec(t))

	elapsed, err := promptWithin(t, agent, "One")

	if err != nil {
		t.Fatal(err)
	}
	if elapsed < 600*time.Millisecond {
		t.Errorf("the prompt took %s", elapsed)
	}
	prompts := promptTexts(t, server, agent.ID())
	if len(prompts) != 3 || !strings.Contains(prompts[1], checkRequest) || !strings.Contains(prompts[2], checkRequest) {
		t.Errorf("prompts = %q", prompts)
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 2 || !strings.Contains(notes[0], checkNote) || !strings.Contains(notes[1], "retry 1 of 3") {
		t.Errorf("notes = %q", notes)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

// Serial: the test changes hangTimeout.
func TestATurnThatEndsWhileASteeringRequestWaitsGetsNoCancelAndNoRetryNote(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server := connectSteering(t, fake, "steering_hang = true\n\n", "[[prompts]]\nlinger = \"900ms\"\n")
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 1 || !strings.Contains(notes[0], checkNote) {
		t.Errorf("notes = %q", notes)
	}
	if prompts := promptTexts(t, server, agent.ID()); len(prompts) != 2 {
		t.Errorf("prompts = %q", prompts)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

// Serial: the test changes hangTimeout.
func TestASteeringRequestWithAnOutcomeOtherThanInjectedGivesTheCancelAndTheRetryPrompt(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server := connectSteering(t, fake, "steering_outcome = \"promptRequired\"\n\n", "[[prompts]]\nhang = true\n\n[[prompts]]\nwhen = \""+checkRequest+"\"\nreply = [\"Done\"]\n")
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	if prompts := promptTexts(t, server, agent.ID()); len(prompts) != 3 {
		t.Errorf("prompts = %q", prompts)
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 2 || !strings.Contains(notes[0], checkNote) || !strings.Contains(notes[1], "retry 1 of 3") {
		t.Errorf("notes = %q", notes)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

// Serial: the test changes hangTimeout and backgroundTimeout.
func TestAnAgentThatAnswersEachCheckAndStaysInTheTurnGetsTheCancelWhenTheFirstCheckIsOneBackgroundTimeoutOld(t *testing.T) {
	shortHang(t)
	shortBackgroundTimeout(t, time.Second)
	fake := testkit.NewFakeGitHub(t)
	server := connectSteering(t, fake, injected, "[[prompts]]\nhang = true\n\n[[prompts]]\nwhen = \""+checkRequest+"\"\nupdates = ['"+workUpdate+"']\nhang = true\n")
	agent := start(t, server, leadSpec(t))

	elapsed, err := promptWithin(t, agent, "One")

	if !errors.Is(err, engine.ErrHung) {
		t.Fatalf("Prompt = %v", err)
	}
	if want := 300*time.Millisecond + time.Second + 3*300*time.Millisecond; elapsed < want {
		t.Errorf("the prompt took %s, want %s or more", elapsed, want)
	}
	if count := countNotes(t, server, agent.ID(), checkNote); count < 2 {
		t.Errorf("checks = %d", count)
	}
	endHungAgent(t, agent)
}

// Serial: the test changes hangTimeout and backgroundTimeout.
func TestWorkOfTheHangTimeOrMoreAfterACheckStartsANewHourOfChecks(t *testing.T) {
	shortHang(t)
	shortBackgroundTimeout(t, time.Second)
	fake := testkit.NewFakeGitHub(t)
	server := connectSteering(t, fake, injected, "[[prompts]]\nhang = true\n\n[[prompts]]\nwhen = \""+checkRequest+"\"\nbusy = \"1500ms\"\nhang = true\n")
	agent := start(t, server, leadSpec(t))
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- agent.Prompt(ctx, "One", nil) }()

	testkit.WaitFor(t, func() bool {
		return countNotes(t, server, agent.ID(), checkNote) >= 2 || countNotes(t, server, agent.ID(), "retry") > 0
	})
	stop()
	<-done

	if count := countNotes(t, server, agent.ID(), checkNote); count < 2 {
		t.Errorf("checks = %d", count)
	}
	if count := countNotes(t, server, agent.ID(), "retry"); count != 0 {
		t.Errorf("notes = %q", noteTexts(t, server, agent.ID()))
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

// Serial: the test changes hangTimeout.
func TestAnAgentOnTheCancelPathThatCallsRunningAndEndsTheTurnCanGetThreeNewRetriesAfterALaterHang(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, hang+"[[prompts]]\n"+backgroundRunning+"\n"+strings.Repeat(hang, 4))
	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	_, err := promptWithin(t, agent, "Two")

	if !errors.Is(err, engine.ErrHung) {
		t.Fatalf("Prompt = %v", err)
	}
	if prompts := promptTexts(t, server, agent.ID()); len(prompts) != 6 {
		t.Errorf("prompts = %q", prompts)
	}
	endHungAgent(t, agent)
}

// Serial: the test changes hangTimeout and backgroundCheckInterval.
func TestAnAgentThatAnswersTheRetryPromptWithDoneContinuesTheRoleWithNoWaitPrompt(t *testing.T) {
	shortHang(t)
	shortBackgroundCheck(t, 50*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+asyncAgentStart+"']\nhang = true\n\n[[prompts]]\nwhen = \""+checkRequest+"\"\n"+backgroundDone)
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.WaitBackground(t.Context(), waitPrompt); err != nil {
		t.Fatal(err)
	}

	prompts := promptTexts(t, server, agent.ID())
	if len(prompts) != 2 || !strings.Contains(prompts[1], checkRequest) {
		t.Errorf("prompts = %q", prompts)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

// Serial: the test changes backgroundTimeout and backgroundCheckInterval.
func TestAfterTheTimeoutOfWaitBackgroundTheNextPromptGetsNoWaitPrompt(t *testing.T) {
	shortBackgroundTimeout(t, time.Nanosecond)
	shortBackgroundCheck(t, time.Hour)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+asyncAgentStart+"']\nreply = [\"One\"]\n\n[[prompts]]\n"+backgroundRunning+"\n[[prompts]]\nreply = [\"Two\"]\n")
	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.WaitBackground(t.Context(), waitPrompt); err != nil {
		t.Fatal(err)
	}
	if notes := noteTexts(t, server, agent.ID()); len(notes) != 1 || !strings.Contains(notes[0], "Mobius ends the wait for the background tasks") {
		t.Errorf("notes = %q", notes)
	}

	if err := agent.Prompt(t.Context(), "Two", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.WaitBackground(t.Context(), waitPrompt); err != nil {
		t.Fatal(err)
	}

	prompts := promptTexts(t, server, agent.ID())
	if !slices.Equal(prompts, []string{"One", waitPrompt, "Two"}) {
		t.Errorf("prompts = %q", prompts)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

func TestACommandThatTheCLIMovedToTheBackgroundMakesWaitBackgroundSendItsPromptButTheSameUpdateOfASubagentDoesNot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, update string
		prompts      int
	}{
		{"command", movedCommand, 2},
		{"subagent", movedSubagentCall, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+test.update+"']\nreply = [\"One\"]\n\n[[prompts]]\n"+backgroundDone)
			agent := start(t, server, leadSpec(t))
			if err := agent.Prompt(t.Context(), "One", nil); err != nil {
				t.Fatal(err)
			}

			if err := agent.WaitBackground(t.Context(), waitPrompt); err != nil {
				t.Fatal(err)
			}

			if prompts := promptTexts(t, server, agent.ID()); len(prompts) != test.prompts {
				t.Errorf("prompts = %q", prompts)
			}
			if err := agent.End(t.Context(), "done"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Serial: the test changes backgroundCheckInterval.
func TestAnImplementerWithNoAsyncMarkThatCallsRunningAndEndsTheTurnGetsTheWaitPrompt(t *testing.T) {
	shortBackgroundCheck(t, 50*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	implementer := "[[prompts]]\nreply = [\"A command still runs.\"]\n" + backgroundRunning + "\n" +
		"[[prompts]]\nwhen = \"" + implementerWaitPrompt + "\"\n" + commitCents + backgroundDone
	server, _ := connectTask(t, fake, leadStarts, implementer, noChange)
	fake.SetCheck(shop, "grep -q cents plan.txt")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)
	session := endedImplementers(t, server, 1)[0]
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 2 || !strings.Contains(prompts[1], implementerWaitPrompt) {
		t.Errorf("prompts = %q", prompts)
	}
}

// Serial: the test changes backgroundCheckInterval.
func TestAResearcherWithNoAsyncMarkThatCallsRunningAndEndsTheTurnGetsTheWaitPrompt(t *testing.T) {
	shortBackgroundCheck(t, 50*time.Millisecond)
	fake := testkit.NewFakeGitHub(t)
	researcher := "[[prompts]]\nreply = [\"A command still runs.\"]\n" + backgroundRunning + "\n" +
		"[[prompts]]\nreply = [\"The final report.\"]\n" + backgroundDone
	server, _ := connectResearcher(t, fake, researchLead, researcher)

	sendChat(t, server, leadChat, "Plan the price model.")

	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := roleSessions(t, server, engine.ResearcherRole)
		if len(sessions) == 0 {
			return store.Session{}, false
		}
		return sessions[0], sessions[0].EndedAt.Valid
	})
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 2 || !strings.Contains(prompts[1], waitPrompt) {
		t.Errorf("prompts = %q", prompts)
	}
	if session.EndReason.String != "done" {
		t.Errorf("session = %+v", session)
	}
}

// Serial: the test changes backgroundTimeout.
func TestAReviewerThatAnswersTheDeploymentNeedsPromptWithRunningGetsTheWaitPromptAndReportsTheNeedsInALaterTurn(t *testing.T) {
	shortBackgroundTimeout(t, time.Nanosecond)
	fake := testkit.NewFakeGitHub(t)
	reviewer := "[[prompts]]\nwhen = \"You are the Reviewer\"\nshell = \"true\"\n\n" +
		"[[prompts]]\nwhen = \"" + waitPrompt + "\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [] } }\n\n" +
		"[[prompts]]\nwhen = \"Call `report_deployment_needs` one time\"\n" + backgroundRunning + "\n"
	server, _ := connectTask(t, fake, reviewer+leadStarts, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\"")
	session := endedReviewers(t, server, 1)[0]
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 3 || prompts[1] != needsRequest || !strings.HasPrefix(prompts[2], needsRequest) || !strings.Contains(prompts[2], waitPrompt) {
		t.Fatalf("prompts = %q", prompts)
	}
	if needs := deploymentNeeds(t, server); len(needs) != 0 {
		t.Errorf("needs = %+v", needs)
	}
	if state := taskState(t, server); state != "approval" {
		t.Errorf("state = %s", state)
	}
}
