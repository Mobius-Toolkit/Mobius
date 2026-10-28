package engine_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	workUpdate    = `{"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "The task ended."}}`
	endUpdate     = `{"sessionUpdate": "usage_update", "used": 1, "size": 2, "_meta": {"_claude/origin": {"kind": "task-notification"}}}`
	absorbedLater = `later = { updates = ['` + workUpdate + `', '` + endUpdate + `'], absorb = true }` + "\n"
	agentUpdate   = `{"sessionId": "s", "update": %s}`
)

// shortAbsorb makes the time that Mobius waits for an absorbed prompt short for the test.
func shortAbsorb(t *testing.T) {
	t.Helper()
	before := *engine.AbsorbTimeout
	*engine.AbsorbTimeout = 300 * time.Millisecond
	t.Cleanup(func() { *engine.AbsorbTimeout = before })
}

// lineID gives the id of the first line of the session of kind that has part in its raw JSON, or 0.
func lineID(t *testing.T, server *testserver.Server, session int64, kind, part string) int64 {
	t.Helper()
	for _, line := range transcript(t, server, session) {
		if line.Kind == kind && strings.Contains(line.Raw, part) {
			return line.ID
		}
	}
	return 0
}

func TestAnAgentGetsNoPromptWhileAnAutonomousTurnRuns(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"First\"]\n\n[[prompts]]\nreply = [\"Second\"]\n")
	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	agent.Update(fmt.Appendf(nil, agentUpdate, workUpdate))
	waiting, stopWaiting := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stopWaiting()
	if err := agent.Prompt(waiting, "Two", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Prompt = %v", err)
	}
	if line := lineID(t, server, agent.ID(), "prompt", "Two"); line != 0 {
		t.Fatalf("the prompt is line %d while the autonomous turn runs", line)
	}
	agent.Update(fmt.Appendf(nil, agentUpdate, endUpdate))
	if err := agent.Prompt(t.Context(), "Two", nil); err != nil {
		t.Fatal(err)
	}
	ended := lineID(t, server, agent.ID(), "update", "task-notification")
	second := lineID(t, server, agent.ID(), "prompt", "Two")
	if ended == 0 || second < ended {
		t.Errorf("the autonomous end is line %d, the second prompt is line %d", ended, second)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

func TestACannotDoInAnAutonomousTurnEndsTheWaitAndSendsNoPrompt(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	later := `later = { after = "10ms", call = { tool = "cannot_do", arguments = { reason = "The plan table does not exist." } }, updates = ['` + workUpdate + `'] }`
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"First\"]\n"+later+"\n\n[[prompts]]\nreply = [\"Second\"]\n")
	spec := implementerSpec(t, server, fake, 41)
	spec.Dir = t.TempDir()
	testkit.Git(t, spec.Dir, "clone", fake.Remote(shop), ".")
	agent := start(t, server, spec)
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return lineID(t, server, agent.ID(), "update", "The task ended.") != 0 })

	if err := agent.Prompt(t.Context(), "Two", nil); err != nil {
		t.Fatal(err)
	}
	if got := agent.CannotDo(); got != "The plan table does not exist." {
		t.Errorf("cannot_do = %q", got)
	}
	if got := promptTexts(t, server, agent.ID()); len(got) != 1 {
		t.Errorf("prompts = %q", got)
	}
}

func TestAStopWhileAnAutonomousTurnRunsEndsTheWaitAndSendsNoPrompt(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"First\"]\nlater = { after = \"10ms\", updates = ['"+workUpdate+"'] }\n", keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the loyalty API")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return lineID(t, server, session.ID, "update", "The task ended.") != 0
	})
	sendChat(t, server, leadChat, "Also add a plan price")
	testkit.WaitFor(t, func() bool { return server.Engine.TurnRunsForMessage(leadChat) })
	testkit.WaitFor(t, func() bool {
		stopChat(t, server, leadChat)
		return !chatView(t, server, leadChat).Writing
	})
	if got := promptTexts(t, server, session.ID); len(got) != 1 {
		t.Errorf("prompts = %q", got)
	}
}

func TestAStopBeforeTheChatLoopTakesAMessageEndsTheWriting(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"First\"]\n", keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the loyalty API")
	session := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		return lineID(t, server, session.ID, "update", "First") != 0
	})
	testkit.WaitFor(t, func() bool { return !chatView(t, server, leadChat).Writing })
	if err := server.Engine.QueueOwnerMessageWithoutWake(t.Context(), leadChat, "Also add a plan price"); err != nil {
		t.Fatal(err)
	}

	stopChat(t, server, leadChat)

	testkit.WaitFor(t, func() bool { return !chatView(t, server, leadChat).Writing })
	var owner []store.ChatMessage
	for _, message := range chatView(t, server, leadChat).Messages {
		if message.Author == "Owner" {
			owner = append(owner, message)
		}
	}
	if len(owner) != 2 || !owner[1].StoppedAt.Valid || owner[1].DeliveredAt.Valid {
		t.Errorf("owner messages = %+v", owner)
	}
	if got := promptTexts(t, server, session.ID); len(got) != 1 {
		t.Errorf("prompts = %q", got)
	}
}

func TestAnAbsorbedPromptGetsACancelAfterTheGraceTimeAndTheWorkContinues(t *testing.T) {
	shortAbsorb(t)
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, "[[prompts]]\n"+commitCents+absorbedLater, noChange)
	flag := dataDir + "/checked"
	fake.SetCheck(shop, fmt.Sprintf("if [ -e '%s' ]; then exit 0; fi; touch '%s'; exit 1", flag, flag))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	session := roleSessions(t, server, engine.ImplementerRole)[0].ID
	prompts := promptTexts(t, server, session)
	if len(prompts) != 2 || !strings.Contains(prompts[1], "The local check `.mobius/check` failed.") {
		t.Fatalf("prompts = %q", prompts)
	}
	notes := noteTexts(t, server, session)
	absorbed := slices.IndexFunc(notes, func(note string) bool { return strings.Contains(note, "Mobius cancelled the prompt") })
	if absorbed < 0 {
		t.Errorf("notes = %q", notes)
	}
	if slices.ContainsFunc(notes, func(note string) bool { return strings.Contains(note, "retry") }) {
		t.Errorf("notes = %q", notes)
	}
	var starts int
	for _, line := range transcript(t, server, session) {
		if line.Kind == "check" && line.Text == ".mobius/check started." {
			starts++
		}
	}
	if starts != 2 {
		t.Errorf("checks = %d", starts)
	}
}

func TestAUserTurnEndWhileAPromptRunsGetsNoCancel(t *testing.T) {
	shortAbsorb(t)
	fake := testkit.NewFakeGitHub(t)
	userEnd := strings.Replace(endUpdate, "task-notification", "channel", 1)
	server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+userEnd+"']\nhang = true\n")
	agent := start(t, server, leadSpec(t))
	waiting, stopWaiting := context.WithTimeout(t.Context(), 1200*time.Millisecond)
	defer stopWaiting()

	if err := agent.Prompt(waiting, "One", nil); err == nil {
		t.Fatal("the prompt got a cancel and ended before the timeout")
	}
	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
}

func TestAnEndOfAnAutonomousTurnWhileASubagentRunsIsNoAbsorbedPrompt(t *testing.T) {
	shortAbsorb(t)
	fake := testkit.NewFakeGitHub(t)
	subagent := `{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "_meta": {"claudeCode": {"toolName": "Agent", "toolResponse": {"isAsync": true, "agentId": "s1"}}}}`
	server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+subagent+"', '"+endUpdate+"']\nhang = true\n")
	agent := start(t, server, leadSpec(t))
	waiting, stopWaiting := context.WithTimeout(t.Context(), 1200*time.Millisecond)
	defer stopWaiting()

	if err := agent.Prompt(waiting, "One", nil); err == nil {
		t.Fatal("the prompt got a cancel and ended before the timeout")
	}
	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
}

func TestAnAutonomousTurnWithNoActivityGetsARetryPromptWithTheTextOfTheCaller(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Done\"]\n")
	agent := start(t, server, leadSpec(t))
	agent.Update(fmt.Appendf(nil, agentUpdate, workUpdate))

	if err := agent.Prompt(t.Context(), "The check failed with exit code 7", nil); err != nil {
		t.Fatal(err)
	}

	prompts := promptTexts(t, server, agent.ID())
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	retry := strings.Index(prompts[0], "You had no activity for 15 minutes. You are probably stuck.")
	text := strings.Index(prompts[0], "The check failed with exit code 7")
	if retry != 0 || text < 0 {
		t.Errorf("prompt = %q", prompts[0])
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 1 || !strings.Contains(notes[0], "retry 1 of 3") {
		t.Errorf("notes = %q", notes)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

func TestAnAutonomousTurnThatHangsAfterTheThirdRetryStopsTheSession(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, strings.Repeat("[[prompts]]\nhang = true\n\n", 3))
	agent := start(t, server, leadSpec(t))
	agent.Update(fmt.Appendf(nil, agentUpdate, workUpdate))

	err := agent.Prompt(t.Context(), "One", nil)

	if !errors.Is(err, engine.ErrHung) {
		t.Fatalf("Prompt = %v", err)
	}
	if prompts := promptTexts(t, server, agent.ID()); len(prompts) != 3 {
		t.Errorf("prompts = %q", prompts)
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 4 || !strings.Contains(notes[3], "Mobius stops the session") {
		t.Errorf("notes = %q", notes)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

func TestAHarnessWithNoSignalsHasNoWaitAndNoNote(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	work := `updates = ['{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Terminal"}', '{"sessionUpdate": "plan", "entries": []}']` + "\n"
	server, _ := connect(t, fake, "[[prompts]]\n"+work+"busy = \"100ms\"\nreply = [\"One\"]\n\n[[prompts]]\n"+work+"reply = [\"Two\"]\n")
	agent := start(t, server, leadSpec(t))

	for _, text := range []string{"One", "Two"} {
		if err := agent.Prompt(t.Context(), text, nil); err != nil {
			t.Fatal(err)
		}
	}

	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

const (
	asyncAgentUpdate = `{"sessionUpdate": "tool_call_update", "toolCallId": "%s", "_meta": {"claudeCode": {"toolName": "Agent", "toolResponse": {"isAsync": true, "agentId": "s1"}}}}`
	commandCall      = `{"sessionUpdate": "tool_call", "toolCallId": "b1", "name": "Bash", "rawInput": {"command": "sleep 60", "run_in_background": true}}`
	commandSpawned   = `{"sessionUpdate": "async_task_spawned", "asyncTaskId": "b1", "name": "sleep 60", "taskType": "local_bash"}`
	commandEnded     = `{"sessionUpdate": "async_task_state_update", "asyncTaskId": "b1", "state": "completed"}`
	commandStopped   = `{"sessionUpdate": "async_task_state_update", "asyncTaskId": "b1", "state": "stopped"}`
	finalReport      = `{"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "The final report."}}`
)

func TestTheEndOfABackgroundCommandDoesNotEndTheWaitForABackgroundAgent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"One\"]\n")
	agent := start(t, server, leadSpec(t))
	peerEnd := strings.Replace(endUpdate, "task-notification", "peer", 1)
	for _, update := range []string{fmt.Sprintf(asyncAgentUpdate, "a1"), commandSpawned, peerEnd, commandEnded, workUpdate, endUpdate} {
		agent.Update(fmt.Appendf(nil, agentUpdate, update))
	}

	waiting, stopWaiting := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stopWaiting()
	if err := agent.WaitBackground(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBackground = %v while the background agent runs", err)
	}

	agent.Update(fmt.Appendf(nil, agentUpdate, finalReport))
	agent.Update(fmt.Appendf(nil, agentUpdate, endUpdate))
	if err := agent.WaitBackground(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reply := agent.ReplyText(); reply != "The final report." {
		t.Errorf("reply = %q", reply)
	}
}

func TestACommandThatEndsWhileTheAgentIsIdleKeepsTheWaitUntilItsTaskNotification(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"One\"]\n")
	agent := start(t, server, leadSpec(t))
	for _, update := range []string{commandSpawned, commandEnded} {
		agent.Update(fmt.Appendf(nil, agentUpdate, update))
	}

	waiting, stopWaiting := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stopWaiting()
	if err := agent.WaitBackground(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBackground = %v before the task-notification turn of the command", err)
	}

	agent.Update(fmt.Appendf(nil, agentUpdate, finalReport))
	agent.Update(fmt.Appendf(nil, agentUpdate, endUpdate))
	if err := agent.WaitBackground(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reply := agent.ReplyText(); reply != "The final report." {
		t.Errorf("reply = %q", reply)
	}
}

func TestTwoCommandsThatEndWhileTheAgentIsIdleDoNotEndTheWaitForABackgroundAgent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"One\"]\n")
	agent := start(t, server, leadSpec(t))
	secondSpawned := strings.Replace(commandSpawned, `"b1"`, `"b2"`, 1)
	secondEnded := strings.Replace(commandEnded, `"b1"`, `"b2"`, 1)
	peerEnd := strings.Replace(endUpdate, "task-notification", "peer", 1)
	updates := []string{fmt.Sprintf(asyncAgentUpdate, "a1"), peerEnd, commandSpawned, secondSpawned, commandEnded, secondEnded, workUpdate, endUpdate, workUpdate, endUpdate}
	for _, update := range updates {
		agent.Update(fmt.Appendf(nil, agentUpdate, update))
	}

	waiting, stopWaiting := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stopWaiting()
	if err := agent.WaitBackground(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBackground = %v while the background agent runs", err)
	}
}

func TestACommandThatTheAdapterReportsStoppedAndThenCompletedDoesNotEndTheWaitForABackgroundAgent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"One\"]\n")
	agent := start(t, server, leadSpec(t))
	peerEnd := strings.Replace(endUpdate, "task-notification", "peer", 1)
	updates := []string{fmt.Sprintf(asyncAgentUpdate, "a1"), peerEnd, commandSpawned, commandStopped, commandEnded, workUpdate, endUpdate}
	for _, update := range updates {
		agent.Update(fmt.Appendf(nil, agentUpdate, update))
	}

	waiting, stopWaiting := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stopWaiting()
	if err := agent.WaitBackground(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBackground = %v while the background agent runs", err)
	}
}

func TestTheEndOfAnAutonomousTurnOfAPeerDoesNotEndTheWaitForABackgroundAgent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"One\"]\n")
	agent := start(t, server, leadSpec(t))
	peerEnd := strings.Replace(endUpdate, "task-notification", "peer", 1)
	for _, update := range []string{fmt.Sprintf(asyncAgentUpdate, "a1"), workUpdate, peerEnd} {
		agent.Update(fmt.Appendf(nil, agentUpdate, update))
	}

	waiting, stopWaiting := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stopWaiting()
	if err := agent.WaitBackground(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBackground = %v while the background agent runs", err)
	}

	agent.Update(fmt.Appendf(nil, agentUpdate, finalReport))
	agent.Update(fmt.Appendf(nil, agentUpdate, endUpdate))
	if err := agent.WaitBackground(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reply := agent.ReplyText(); reply != "The final report." {
		t.Errorf("reply = %q", reply)
	}
}

func TestTheEndOfAWakeupDoesNotEndTheWaitForABackgroundAgent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"One\"]\n")
	agent := start(t, server, leadSpec(t))
	wakeupEnd := strings.Replace(endUpdate, `{"kind": "task-notification"}`, `{"kind": "task-notification", "subkind": "scheduled-trigger"}`, 1)
	for _, update := range []string{fmt.Sprintf(asyncAgentUpdate, "a1"), workUpdate, wakeupEnd} {
		agent.Update(fmt.Appendf(nil, agentUpdate, update))
	}

	waiting, stopWaiting := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stopWaiting()
	if err := agent.WaitBackground(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBackground = %v while the background agent runs", err)
	}

	agent.Update(fmt.Appendf(nil, agentUpdate, finalReport))
	agent.Update(fmt.Appendf(nil, agentUpdate, endUpdate))
	if err := agent.WaitBackground(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reply := agent.ReplyText(); reply != "The final report." {
		t.Errorf("reply = %q", reply)
	}
}

func TestAStoppedBackgroundCommandEndsTheWaitWithNoBackgroundTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"One\"]\n")
	agent := start(t, server, leadSpec(t))
	for _, update := range []string{commandCall, commandSpawned, commandStopped, finalReport, endUpdate} {
		agent.Update(fmt.Appendf(nil, agentUpdate, update))
	}

	waiting, stopWaiting := context.WithTimeout(t.Context(), 10*time.Second)
	defer stopWaiting()
	if err := agent.WaitBackground(waiting); err != nil {
		t.Fatalf("WaitBackground = %v after the agent stopped its command", err)
	}
	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
}

func TestABackgroundCommandThatEndsInsideATurnEndsTheWaitWithNoBackgroundTime(t *testing.T) {
	shortBackground(t)
	fake := testkit.NewFakeGitHub(t)
	updates := "['" + commandCall + "', '" + commandSpawned + "', '" + commandEnded + "']"
	server, _ := connect(t, fake, "[[prompts]]\nupdates = "+updates+"\nreply = [\"The final report.\"]\n")
	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	if err := agent.WaitBackground(t.Context()); err != nil {
		t.Fatal(err)
	}
	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
}

func TestABackgroundAgentOfASubagentDoesNotHoldTheWait(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"One\"]\n")
	agent := start(t, server, leadSpec(t))
	subAsync := strings.Replace(fmt.Sprintf(asyncAgentUpdate, "a1"), `"_meta": {"claudeCode": {`, `"_meta": {"claudeCode": {"parentToolUseId": "p1", `, 1)
	for _, update := range []string{subAsync, finalReport, endUpdate} {
		agent.Update(fmt.Appendf(nil, agentUpdate, update))
	}

	waiting, stopWaiting := context.WithTimeout(t.Context(), 10*time.Second)
	defer stopWaiting()
	if err := agent.WaitBackground(waiting); err != nil {
		t.Fatalf("WaitBackground = %v while only a subagent started an agent", err)
	}
	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
}

// shortBackground makes the time that Mobius waits for idle background tasks short for the test.
func shortBackground(t *testing.T) {
	t.Helper()
	before := *engine.BackgroundTimeout
	*engine.BackgroundTimeout = 600 * time.Millisecond
	t.Cleanup(func() { *engine.BackgroundTimeout = before })
}

func TestOneEndOfTwoBackgroundAgentsEndsTheWaitAfterTheBackgroundTimeWithANote(t *testing.T) {
	shortBackground(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"One\"]\n")
	agent := start(t, server, leadSpec(t))
	for _, update := range []string{fmt.Sprintf(asyncAgentUpdate, "a1"), fmt.Sprintf(asyncAgentUpdate, "a2"), finalReport, endUpdate} {
		agent.Update(fmt.Appendf(nil, agentUpdate, update))
	}

	if err := agent.WaitBackground(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reply := agent.ReplyText(); reply != "The final report." {
		t.Errorf("reply = %q", reply)
	}
	if notes := noteTexts(t, server, agent.ID()); len(notes) != 1 || !strings.Contains(notes[0], "while a background task ran") {
		t.Errorf("notes = %q", notes)
	}
}

func TestABackgroundAgentThatEndsAfterTheHangTimeStillGivesTheFinalReport(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+fmt.Sprintf(asyncAgentUpdate, "a1")+"']\nreply = [\"One helper still runs.\"]\n")
	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	waiting, stopWaiting := context.WithTimeout(t.Context(), 1200*time.Millisecond)
	defer stopWaiting()
	if err := agent.WaitBackground(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBackground = %v after the hang time while the background agent runs", err)
	}

	agent.Update(fmt.Appendf(nil, agentUpdate, finalReport))
	agent.Update(fmt.Appendf(nil, agentUpdate, endUpdate))
	if err := agent.WaitBackground(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reply := agent.ReplyText(); reply != "The final report." {
		t.Errorf("reply = %q", reply)
	}
	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
}
