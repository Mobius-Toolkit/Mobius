package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// claudeCodeSlot makes the first binding of the Implementer one slot of Claude Code.
func claudeCodeSlot(cfg *config.Config) {
	cfg.Roles.Implementer = config.RoleBinding{Harness: config.ClaudeCode, Model: "sonnet", Effort: "high", Max: 1}
}

func TestAPausedSessionWithASettingKeepsNoSlotFromALaterSessionAndStartsAfterThePauseEnds(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, usageLimit, claudeCodeSlot)
	addCopiedTask(t, server, fake, 41)
	addCopiedTask(t, server, fake, 43)
	setBinding(t, server, "implementer", 41, config.Devin, "opus", "low")
	setBinding(t, server, "implementer", 43, config.Devin, "opus", "low")
	limited := start(t, server, taskSpec(t, shop, 41))
	go func() { _ = limited.Prompt(t.Context(), "Store plans in cents.", nil) }()
	testkit.WaitFor(t, func() bool { _, found := devinPause(t, server); return found })
	end(t, limited, "stopped")
	earlier := startLater(t.Context(), server, taskSpec(t, shop, 43))
	waiting := queued(t, server, engine.ImplementerRole)
	if !strings.HasPrefix(waiting.QueueReason.String, "paused until ") || waiting.Model != "opus" {
		t.Fatalf("waiting session = %+v, want the pause of Devin", waiting)
	}

	later := awaitWithin(t, startLater(t.Context(), server, implementerSpec(t, server, fake, 45)))

	requireSession(t, server, later, "claude-code sonnet")
	end(t, later, "done")
	if err := server.Engine.Resume(t.Context(), "devin"); err != nil {
		t.Fatal(err)
	}
	agent := awaitWithin(t, earlier)
	defer end(t, agent, "done")
	requireSession(t, server, agent, "devin opus")
}

func TestASessionWithASettingThatTheAccessCheckHoldsKeepsNoSlotFromALaterSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, answersDone, claudeCodeSlot)
	addCopiedTask(t, server, fake, 43)
	setBinding(t, server, "implementer", 43, config.Devin, "opus", "low")
	server.Stop()
	testkit.InstallFakeHarness(t, dataDir, "devin", options+noKey)
	cfg := testserver.Config(t, dataDir)
	claudeCodeSlot(cfg)
	restarted := testserver.StartWith(t, cfg, fake.URL)
	restarted.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, restarted.Engine.AccessChecked)
	earlier := startLater(t.Context(), restarted, taskSpec(t, shop, 43))
	held := testkit.WaitForValue(t, func() (string, bool) {
		for _, node := range tree(t, restarted) {
			if node.Session.Role == engine.ImplementerRole && strings.HasPrefix(node.Session.QueueReason.String, "access check failed:") {
				return node.Session.QueueReason.String, true
			}
		}
		return "", false
	})
	if !strings.Contains(held, "no API key for provider route") {
		t.Fatalf("queue reason = %q, want the failed access check of Devin", held)
	}

	later := awaitWithin(t, startLater(t.Context(), restarted, implementerSpec(t, restarted, fake, 45)))
	defer end(t, later, "done")

	requireSession(t, restarted, later, "claude-code sonnet")
	select {
	case got := <-earlier:
		t.Errorf("the held session started: %+v", got)
	default:
	}
}

func TestEarlierSessionsThatCanStartKeepTheirOrderBeforeALaterSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, answersDone, firstSlots(1))
	holder := start(t, server, implementerSpec(t, server, fake, 41))
	specs := []engine.Spec{implementerSpec(t, server, fake, 43), implementerSpec(t, server, fake, 45), implementerSpec(t, server, fake, 47)}
	var results []<-chan started
	for i := len(specs) - 1; i >= 0; i-- {
		results = append([]<-chan started{startLater(t.Context(), server, specs[i])}, results...)
	}
	testkit.WaitFor(t, func() bool {
		count := 0
		for _, node := range tree(t, server) {
			if node.Session.Role == engine.ImplementerRole && node.Session.QueueReason.Valid {
				count++
			}
		}
		return count == len(specs)
	})

	end(t, holder, "done")

	for i, result := range results {
		agent := awaitWithin(t, result)
		for _, other := range results[i+1:] {
			select {
			case got := <-other:
				t.Errorf("a later session started with the slot of session %d: %+v", agent.ID(), got)
			default:
			}
		}
		end(t, agent, "done")
	}
}

func TestASessionThatFollowsTheRoleAndWaitsForAFullBindingKeepsItsPlaceWhileTheFirstBindingIsPaused(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, usageLimit, firstSlots(1))
	addBinding(t, server, config.ClaudeCode, "sonnet", "high", 1)
	limited := start(t, server, implementerSpec(t, server, fake, 41))
	full := start(t, server, implementerSpec(t, server, fake, 43))
	requireSession(t, server, limited, "devin swe-1.5")
	requireSession(t, server, full, "claude-code sonnet")
	go func() { _ = limited.Prompt(t.Context(), "Store plans in cents.", nil) }()
	testkit.WaitFor(t, func() bool { _, found := devinPause(t, server); return found })
	end(t, limited, "stopped")
	earlierSpec := implementerSpec(t, server, fake, 45)
	laterSpec := implementerSpec(t, server, fake, 47)
	later := startLater(t.Context(), server, laterSpec)
	queued(t, server, engine.ImplementerRole)
	earlier := startLater(t.Context(), server, earlierSpec)
	testkit.WaitFor(t, func() bool {
		count := 0
		for _, node := range tree(t, server) {
			if node.Session.Role == engine.ImplementerRole && node.Session.QueueReason.Valid {
				count++
			}
		}
		return count == 2
	})

	end(t, full, "done")

	agent := awaitWithin(t, earlier)
	defer end(t, agent, "done")
	requireSession(t, server, agent, "claude-code sonnet")
	select {
	case got := <-later:
		t.Errorf("the later session started before the earlier session: %+v", got)
	default:
	}
}
