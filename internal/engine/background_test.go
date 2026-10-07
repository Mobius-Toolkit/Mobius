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
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	startsTask    = `updates = ['{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "_meta": {"claudeCode": {"toolName": "Bash", "toolResponse": {"backgroundTaskId": "b1"}}}}']` + "\n"
	workUpdate    = `{"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "The task ended."}}`
	endUpdate     = `{"sessionUpdate": "usage_update", "used": 1, "size": 2, "_meta": {"_claude/origin": {"kind": "task-notification"}}}`
	taskEnds      = `later = { after = "500ms", updates = ['` + workUpdate + `', '` + endUpdate + `'] }` + "\n"
	absorbedLater = `later = { after = "1s", updates = ['` + workUpdate + `', '` + endUpdate + `'], absorb = true }` + "\n"
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

func TestAnImplementerWithALiveBackgroundTaskStartsTheCheckAfterTheAutonomousEnd(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\n"+startsTask+commitCents+taskEnds, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	session := roleSessions(t, server, engine.ImplementerRole)[0].ID
	ended := lineID(t, server, session, "update", "task-notification")
	started := lineID(t, server, session, "check", ".mobius/check started.")
	if ended == 0 || started == 0 || started < ended {
		t.Errorf("the autonomous end is line %d, the check starts at line %d", ended, started)
	}
	if prompts := promptTexts(t, server, session); len(prompts) != 1 {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAnAgentGetsNoPromptWhileAnAutonomousTurnRuns(t *testing.T) {
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

func TestAnEndOfAnAutonomousTurnWhileATaskRunsIsNoAbsorbedPrompt(t *testing.T) {
	shortAbsorb(t)
	fake := testkit.NewFakeGitHub(t)
	starts := []string{
		`{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "_meta": {"claudeCode": {"toolName": "Agent", "toolResponse": {"isAsync": true, "agentId": "s1"}}}}`,
		`{"sessionUpdate": "tool_call_update", "toolCallId": "t2", "_meta": {"claudeCode": {"toolName": "Agent", "toolResponse": {"isAsync": true, "agentId": "s2"}}}}`,
		endUpdate,
	}
	server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+strings.Join(starts, "', '")+"']\nhang = true\n")
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

func TestMonitorEventsDoNotEndABackgroundTask(t *testing.T) {
	shortHang(t)
	for name, persistent := range map[string]string{"a Monitor": "false", "a persistent Monitor": "true"} {
		t.Run(name, func(t *testing.T) {
			fake := testkit.NewFakeGitHub(t)
			bash := `{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "_meta": {"claudeCode": {"toolName": "Bash", "toolResponse": {"backgroundTaskId": "b1"}}}}`
			monitor := `{"sessionUpdate": "tool_call_update", "toolCallId": "t2", "_meta": {"claudeCode": {"toolName": "Monitor", "toolResponse": {"taskId": "m1", "timeoutMs": 300000, "persistent": ` + persistent + `}}}}`
			starts := "updates = ['" + bash + "', '" + monitor + "']\n"
			events := `later = { after = "100ms", updates = ['` + workUpdate + `', '` + endUpdate + `', '` + workUpdate + `', '` + endUpdate + `'] }` + "\n"
			server, _ := connect(t, fake, "[[prompts]]\n"+starts+"reply = [\"Started\"]\n"+events+"\n[[prompts]]\nreply = [\"Done\"]\n")
			agent := start(t, server, leadSpec(t))

			if err := agent.Prompt(t.Context(), "One", nil); err != nil {
				t.Fatal(err)
			}

			notes := noteTexts(t, server, agent.ID())
			if len(notes) != 1 || !strings.Contains(notes[0], "retry 1 of 3") {
				t.Errorf("notes = %q", notes)
			}
			if err := agent.End(t.Context(), "done"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestALostEndSignalGetsARetryPromptInTheSameSession(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\n"+startsTask+"reply = [\"Started\"]\n\n[[prompts]]\nreply = [\"Done\"]\n")
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	prompts := promptTexts(t, server, agent.ID())
	if len(prompts) != 2 || !strings.Contains(prompts[1], "You had no activity for 15 minutes. You are probably stuck.") {
		t.Errorf("prompts = %q", prompts)
	}
	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 1 || !strings.Contains(notes[0], "retry 1 of 3") {
		t.Errorf("notes = %q", notes)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

func TestAHangBeforeAPromptSendsTheTextOfTheCallerAfterTheRetryText(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Done\"]\n")
	agent := start(t, server, leadSpec(t))
	agent.Update(fmt.Appendf(nil, agentUpdate, `{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "_meta": {"claudeCode": {"toolName": "Bash", "toolResponse": {"backgroundTaskId": "b1"}}}}`))

	if err := agent.Prompt(t.Context(), "The check failed with exit code 7", nil); err != nil {
		t.Fatal(err)
	}

	prompts := promptTexts(t, server, agent.ID())
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	retry := strings.Index(prompts[0], "You had no activity for 15 minutes.")
	text := strings.Index(prompts[0], "The check failed with exit code 7")
	if retry != 0 || text < 0 {
		t.Errorf("prompt = %q", prompts[0])
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

func TestALostEndSignalAfterTheThirdRetryStopsTheSession(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, strings.Repeat("[[prompts]]\n"+startsTask+"reply = [\"Started\"]\n\n", 4))
	agent := start(t, server, leadSpec(t))

	err := agent.Prompt(t.Context(), "One", nil)

	if !errors.Is(err, engine.ErrHung) {
		t.Fatalf("Prompt = %v", err)
	}
	if prompts := promptTexts(t, server, agent.ID()); len(prompts) != 4 {
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

func TestTwoTasksThatEndInTheOppositeOrderAreNotWaitedTasks(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	updates := []string{
		`{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "_meta": {"claudeCode": {"toolName": "Bash", "toolResponse": {"backgroundTaskId": "b1"}}}}`,
		`{"sessionUpdate": "tool_call_update", "toolCallId": "t2", "_meta": {"claudeCode": {"toolName": "Bash", "toolResponse": {"backgroundTaskId": "b2"}}}}`,
		endUpdate,
		`{"sessionUpdate": "tool_call_update", "toolCallId": "t3", "_meta": {"claudeCode": {"toolName": "BashOutput", "toolResponse": {"shellId": "b1", "status": "completed"}}}}`,
	}
	server, _ := connect(t, fake, "[[prompts]]\nupdates = ['"+strings.Join(updates, "', '")+"']\nreply = [\"Done\"]\n")
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

func TestAStoppedTaskIsNotAWaitedTask(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	stops := `'{"sessionUpdate": "tool_call_update", "toolCallId": "t2", "_meta": {"claudeCode": {"toolName": "TaskStop", "toolResponse": {"task_id": "b1"}}}}'`
	starts := strings.Replace(strings.TrimSpace(startsTask), "]", ", "+stops+"]", 1)
	server, _ := connect(t, fake, "[[prompts]]\n"+starts+"\nreply = [\"Stopped\"]\n")
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

func TestATaskThatEndsInTheSameTurnIsNotAWaitedTask(t *testing.T) {
	shortHang(t)
	reads := map[string]string{
		"BashOutput": `{"shellId": "b1", "status": "completed", "exitCode": 0}`,
		"TaskOutput": `{"retrieval_status": "success", "task": {"task_id": "b1", "status": "completed"}}`,
	}
	for tool, response := range reads {
		t.Run(tool, func(t *testing.T) {
			fake := testkit.NewFakeGitHub(t)
			read := `'{"sessionUpdate": "tool_call_update", "toolCallId": "t2", "_meta": {"claudeCode": {"toolName": "` + tool + `", "toolResponse": ` + response + `}}}'`
			starts := strings.Replace(strings.TrimSpace(startsTask), "]", ", "+read+"]", 1)
			server, _ := connect(t, fake, "[[prompts]]\n"+starts+"\nreply = [\"Done\"]\n")
			agent := start(t, server, leadSpec(t))

			if err := agent.Prompt(t.Context(), "One", nil); err != nil {
				t.Fatal(err)
			}

			if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
				t.Errorf("notes = %q", notes)
			}
			if err := agent.End(t.Context(), "done"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func monitorStarts(tool, response string) string {
	return `updates = ['{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "_meta": {"claudeCode": {"toolName": "` + tool + `", "toolResponse": ` + response + `}}}']` + "\n"
}

func TestAMonitorThatIsNotPersistentIsAWaitedTask(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	starts := monitorStarts("Monitor", `{"taskId": "m1", "timeoutMs": 300000, "persistent": false}`)
	server, _ := connect(t, fake, "[[prompts]]\n"+starts+"reply = [\"Started\"]\n\n[[prompts]]\nreply = [\"Done\"]\n")
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	notes := noteTexts(t, server, agent.ID())
	if len(notes) != 1 || !strings.Contains(notes[0], "retry 1 of 3") {
		t.Errorf("notes = %q", notes)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
}

func TestAPersistentMonitorOrATaskIdOfAnotherToolIsNotAWaitedTask(t *testing.T) {
	shortHang(t)
	starts := map[string]string{
		"a persistent Monitor":  monitorStarts("Monitor", `{"taskId": "m1", "timeoutMs": 0, "persistent": true}`),
		"a TaskUpdate response": monitorStarts("TaskUpdate", `{"success": true, "taskId": "1", "updatedFields": ["status"]}`),
	}
	for name, toolStart := range starts {
		t.Run(name, func(t *testing.T) {
			fake := testkit.NewFakeGitHub(t)
			server, _ := connect(t, fake, "[[prompts]]\n"+toolStart+"reply = [\"Done\"]\n")
			agent := start(t, server, leadSpec(t))

			if err := agent.Prompt(t.Context(), "One", nil); err != nil {
				t.Fatal(err)
			}

			if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
				t.Errorf("notes = %q", notes)
			}
			if err := agent.End(t.Context(), "done"); err != nil {
				t.Fatal(err)
			}
		})
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
