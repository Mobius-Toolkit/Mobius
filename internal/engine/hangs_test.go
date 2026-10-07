package engine_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const hang = "[[prompts]]\nhang = true\n\n"

// shortHang makes the time with no activity of a hung turn short for the test.
func shortHang(t *testing.T) {
	t.Helper()
	before := *engine.HangTimeout
	*engine.HangTimeout = 300 * time.Millisecond
	t.Cleanup(func() { *engine.HangTimeout = before })
}

func noteTexts(t *testing.T, server *testserver.Server, session int64) []string {
	t.Helper()
	var texts []string
	for _, row := range rows(t, server, session, "check") {
		texts = append(texts, row["text"].(string))
	}
	return texts
}

func TestATurnWithNoActivityGetsARetryPromptInTheSameSession(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, hang+"[[prompts]]\nreply = [\"Done\"]\n")

	session := run(t, server, leadSpec(t), "Plan the loyalty API")

	prompts := promptTexts(t, server, session)
	if len(prompts) != 2 || prompts[0] != "Plan the loyalty API" {
		t.Fatalf("prompts = %q", prompts)
	}
	for _, part := range []string{
		"You had no activity for 15 minutes. You are probably stuck.",
		"Continue your work in a different way.",
		"If you cannot do the work, say so in your reply and give the reason.",
		"`TaskStop`",
	} {
		if !strings.Contains(prompts[1], part) {
			t.Errorf("%q is not in %s", part, prompts[1])
		}
	}
	if text := reply(t, server, session); text != "Done" {
		t.Errorf("reply = %q", text)
	}
	notes := noteTexts(t, server, session)
	if len(notes) != 1 || !strings.Contains(notes[0], "had no activity") || !strings.Contains(notes[0], "retry 1 of 3") {
		t.Errorf("notes = %q", notes)
	}
}

func TestTheRetryPromptOfAnImplementerTellsItToCallCannotDo(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, hang+commits, longGrace)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	sessions := roleSessions(t, server, engine.ImplementerRole)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	prompts := promptTexts(t, server, sessions[0].ID)
	if len(prompts) != 2 || !strings.Contains(prompts[1], "If you cannot do the work, call `cannot_do` with the reason.") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestATurnThatSendsUpdatesGetsNoRetryPrompt(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nbusy = \"1s\"\nreply = [\"Done\"]\n")

	session := run(t, server, leadSpec(t), "Plan the loyalty API")

	if prompts := promptTexts(t, server, session); len(prompts) != 1 {
		t.Errorf("prompts = %q", prompts)
	}
	if notes := noteTexts(t, server, session); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
}

func TestAnUpdateThatIsNoWorkDoesNotEndTheHang(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	updates := `updates = ['{"sessionUpdate": "available_commands_update", "availableCommands": []}', '{"sessionUpdate": "usage_update", "used": 1, "size": 2}']`
	server, _ := connect(t, fake, "[[prompts]]\n"+updates+"\nhang = true\n\n[[prompts]]\nreply = [\"Done\"]\n")

	session := run(t, server, leadSpec(t), "Plan the loyalty API")

	if prompts := promptTexts(t, server, session); len(prompts) != 2 {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAnImplementerThatChecksItsWorkLongerThanTheHangTimeGetsNoRetryPrompt(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, commits, longGrace)
	goFile := filepath.Join(dataDir, "go")
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) == 1 && slices.ContainsFunc(transcript(t, server, sessions[0].ID), func(line engine.Line) bool { return line.Kind == "check" })
	})

	waitForPolls(t, fake)
	touch(t, goFile)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	session := roleSessions(t, server, engine.ImplementerRole)[0]
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 1 {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestASessionThatWaitsForASlotLongerThanTheHangTimeGetsNoRetryPrompt(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Work\"\nbusy = \"1s\"\n", func(cfg *config.Config) { cfg.MaxAgents = 1 })
	first := start(t, server, implementerSpec(t, server, fake, 41))
	working := make(chan error, 1)
	go func() { working <- first.Prompt(t.Context(), "Work", nil) }()
	second := startLater(t.Context(), server, roleSpec(t, engine.JudgeRole))
	queued(t, server, engine.JudgeRole)

	if err := <-working; err != nil {
		t.Fatal(err)
	}
	end(t, first, "done")
	agent := await(t, second)
	defer end(t, agent, "done")
	if err := agent.Prompt(t.Context(), "Judge", nil); err != nil {
		t.Fatal(err)
	}

	for _, id := range []int64{first.ID(), agent.ID()} {
		if prompts := promptTexts(t, server, id); len(prompts) != 1 {
			t.Errorf("prompts of session %d = %q", id, prompts)
		}
	}
}

func TestTheHangAfterTheThirdRetryStopsTheSessionAndHandsTheTaskToAHuman(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, strings.Repeat(hang, 5), noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": the implementer session ")
	sessions := endedImplementers(t, server, 1)
	if len(sessions) != 1 || sessions[0].EndReason.String != "hung" {
		t.Fatalf("sessions = %+v", sessions)
	}
	session := sessions[0]
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 4 {
		t.Errorf("prompts = %q", prompts)
	}
	notes := noteTexts(t, server, session.ID)
	if len(notes) != 4 || !strings.Contains(notes[3], "Mobius stops the session") {
		t.Errorf("notes = %q", notes)
	}
	var found []inboxItem
	for _, item := range inbox(t, server) {
		if item.Kind == "stopped" {
			found = append(found, item)
		}
	}
	text := fmt.Sprintf("Mobius stopped the implementer session %d. It had no activity after 3 retries. The session worked on #41.", session.ID)
	if len(found) != 1 || found[0].Text != text || found[0].Issue != 41 || found[0].Repository != shop {
		t.Errorf("Inbox items = %+v", found)
	}
	if state := taskState(t, server); state != "needs_human" {
		t.Errorf("state = %s", state)
	}
	if !hasLabel(fake, "mobius:needs-human") || hasLabel(fake, "mobius:working") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if task := liveTask(t, server, 41); task.WorkerRestarts != 0 {
		t.Errorf("Worker restarts = %d", task.WorkerRestarts)
	}
	waitForPolls(t, fake)
	if count := implementers(t, server); count != 1 {
		t.Errorf("Implementers = %d", count)
	}
}

func TestALeadThatHangsAfterTheThirdRetryEndsWithNoNewSessionAndSendsTheEventToTheInbox(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nwhen = \"dispatch of #41\"\nhang = true\n\n"+strings.Repeat(hang, 3))

	dispatchTask(fake, 41, "Add plan model")

	testkit.WaitFor(t, func() bool {
		kinds := map[string]bool{}
		for _, item := range inbox(t, server) {
			kinds[item.Kind] = true
		}
		return kinds["stopped"] && kinds["Lead failed"]
	})
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 1 || sessions[0].EndReason.String != "hung" {
		t.Errorf("sessions = %+v", sessions)
	}
	if got := undelivered(t, server); len(got) != 0 {
		t.Errorf("undelivered = %+v", got)
	}
}
