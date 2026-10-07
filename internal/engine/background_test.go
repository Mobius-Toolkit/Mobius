package engine_test

import (
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
	if err := agent.Prompt(t.Context(), "One"); err != nil {
		t.Fatal(err)
	}
	agent.Update(fmt.Appendf(nil, agentUpdate, workUpdate))
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Two") }()

	time.Sleep(100 * time.Millisecond)
	agent.Update(fmt.Appendf(nil, agentUpdate, endUpdate))

	if err := <-prompted; err != nil {
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

func TestALostEndSignalEndsTheWaitWithANoteAndNoRetry(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\n"+startsTask+"reply = [\"Started\"]\n\n[[prompts]]\nreply = [\"Done\"]\n")
	agent := start(t, server, leadSpec(t))

	for _, text := range []string{"One", "Two"} {
		if err := agent.Prompt(t.Context(), text); err != nil {
			t.Fatal(err)
		}
	}

	notes := noteTexts(t, server, agent.ID())
	want := "The agent had no activity for 300ms while Mobius waited for its background tasks. Mobius continues."
	if !slices.Equal(notes, []string{want}) {
		t.Errorf("notes = %q", notes)
	}
	if prompts := promptTexts(t, server, agent.ID()); len(prompts) != 2 {
		t.Errorf("prompts = %q", prompts)
	}
	if text := reply(t, server, agent.ID()); text != "StartedDone" {
		t.Errorf("reply = %q", text)
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

	if err := agent.Prompt(t.Context(), "One"); err != nil {
		t.Fatal(err)
	}

	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
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
		if err := agent.Prompt(t.Context(), text); err != nil {
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
