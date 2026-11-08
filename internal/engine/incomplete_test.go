package engine_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func incompleteScript(reason string) string {
	return fmt.Sprintf("[[prompts]]\nstop_reason = %q\n\n", reason)
}

func incompleteText(reason, prompt string) string {
	return fmt.Sprintf("Your previous turn ended with the stop reason `%s` before your work was complete. Read the state of your work and continue. The prompt of that turn follows.\n\n%s", reason, prompt)
}

func TestATurnWithAnIncompleteStopReasonGetsTheIncompletePromptWithTheSamePromptText(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"max_tokens", "max_turn_requests", "refusal"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := connect(t, fake, incompleteScript(reason)+"[[prompts]]\nreply = [\"Done\"]\n")

			session := run(t, server, leadSpec(t), "Plan the loyalty API")

			want := []string{"Plan the loyalty API", incompleteText(reason, "Plan the loyalty API")}
			if prompts := promptTexts(t, server, session); !reflect.DeepEqual(prompts, want) {
				t.Errorf("prompts = %q, want %q", prompts, want)
			}
			notes := noteTexts(t, server, session)
			if len(notes) != 1 || !strings.Contains(notes[0], reason) || !strings.Contains(notes[0], "retry 1 of 3") {
				t.Errorf("notes = %q", notes)
			}
			if text := reply(t, server, session); text != "Done" {
				t.Errorf("reply = %q", text)
			}
		})
	}
}

func TestASecondIncompleteTurnGetsTheIncompletePromptOfItsOwnStopReasonOnTheOriginalText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, incompleteScript("max_tokens")+incompleteScript("refusal")+"[[prompts]]\nreply = [\"Done\"]\n")

	session := run(t, server, leadSpec(t), "Plan the loyalty API")

	want := []string{"Plan the loyalty API", incompleteText("max_tokens", "Plan the loyalty API"), incompleteText("refusal", "Plan the loyalty API")}
	if prompts := promptTexts(t, server, session); !reflect.DeepEqual(prompts, want) {
		t.Errorf("prompts = %q, want %q", prompts, want)
	}
}

func TestATurnWithEndTurnGetsNoIncompletePrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Done\"]\n")

	session := run(t, server, leadSpec(t), "Plan the loyalty API")

	if prompts := promptTexts(t, server, session); len(prompts) != 1 {
		t.Errorf("prompts = %q", prompts)
	}
	if notes := noteTexts(t, server, session); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
}

func TestAnImplementerRunsItsLocalCheckAfterTheEndTurnThatFollowsAnIncompleteTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\n"+commitCents+"stop_reason = \"max_tokens\"\n\n[[prompts]]\nreply = [\"Done\"]\n", noChange)
	fake.SetCheck(shop, "exit 0")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" })
	session := roleSessions(t, server, engine.ImplementerRole)[0].ID
	prompts := promptTexts(t, server, session)
	if len(prompts) != 2 || !strings.HasPrefix(prompts[1], incompleteText("max_tokens", "")) || !strings.HasSuffix(prompts[1], prompts[0]) {
		t.Fatalf("prompts = %q", prompts)
	}
	var starts int
	for _, line := range transcript(t, server, session) {
		if line.Kind == "check" && line.Text == ".mobius/check started." {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("checks = %d", starts)
	}
}

func TestAnImplementerWithAnIncompleteStopReasonOnEachTurnRunsNoLocalCheckAndFailsWithTheStopReason(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\n"+commitCents+"stop_reason = \"refusal\"\n\n"+strings.Repeat(incompleteScript("refusal"), 3), noChange)
	fake.SetCheck(shop, "exit 0")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	sessions := endedImplementers(t, server, 1)
	session := sessions[0]
	if session.EndReason.String != "failed" {
		t.Errorf("end reason = %q", session.EndReason.String)
	}
	if prompts := promptTexts(t, server, session.ID); len(prompts) != 4 {
		t.Errorf("prompts = %q", prompts)
	}
	errorRows := rows(t, server, session.ID, "error")
	if len(errorRows) != 1 || !strings.Contains(errorRows[0]["message"].(string), "refusal") {
		t.Errorf("error rows = %v", errorRows)
	}
	notes := noteTexts(t, server, session.ID)
	if len(notes) != 4 || !strings.Contains(notes[3], "after 3 retries") {
		t.Errorf("notes = %q", notes)
	}
	for _, line := range transcript(t, server, session.ID) {
		if line.Kind == "check" && line.Text == ".mobius/check started." {
			t.Errorf("the local check ran")
		}
	}
}

func TestAnAntigravityLimitReplyWithAnIncompleteStopReasonStaysOnTheUsageLimitPath(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	limit := strings.Replace(antigravityLimitReply, "[[prompts]]\nreply", "[[prompts]]\nstop_reason = \"max_tokens\"\nreply", 1)
	server, _ := connectWith(t, fake, limit, func(cfg *config.Config) {
		cfg.Roles.Implementer = config.RoleBinding{Harness: config.Antigravity, Model: "gemini-3-pro", Max: 2}
	})
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Store plans in cents.", nil) }()

	testkit.WaitFor(t, func() bool {
		var count int
		err := server.DB.QueryRow("SELECT count(*) FROM harness_pauses WHERE harness = 'antigravity'").Scan(&count)
		return err == nil && count == 1
	})
	if err := server.Engine.Resume(t.Context(), "antigravity"); err != nil {
		t.Fatal(err)
	}
	if err := <-prompted; err != nil {
		t.Fatal(err)
	}

	want := []string{"Store plans in cents.", "Store plans in cents."}
	if prompts := promptTexts(t, server, agent.ID()); !reflect.DeepEqual(prompts, want) {
		t.Errorf("prompts = %q, want %q", prompts, want)
	}
	if notes := noteTexts(t, server, agent.ID()); len(notes) != 0 {
		t.Errorf("notes = %q", notes)
	}
}
