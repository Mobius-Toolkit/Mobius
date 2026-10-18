package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func TestAQuestionThatClosesWithNoCommentFreesTheHeldEventsInIdOrder(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := heldAfterQuestion(t, fake)

	fake.RemoveLabel(shop, 41, "mobius:question", "owner")

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := onlySession(t, server)
		return prompts, len(prompts) >= 5 && eventDelivered(t, server)
	})
	if len(prompts) != 5 || !strings.Contains(prompts[3], "Held follow-up.") || !strings.Contains(prompts[4], "Ready for approval.") {
		t.Errorf("prompts = %q", prompts)
	}
	assertDismissed(t, server, item.ID)
}

func TestAStartWithAHeldEventWhoseQuestionIsClosedDeliversTheEventInTheFirstPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	first, _, dataDir := heldAfterQuestionIn(t, fake)
	first.Stop()
	fake.RemoveLabel(shop, 41, "mobius:question", "owner")
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nreply = [\"Seen\"]\n\n[[prompts]]\nreply = [\"Seen\"]\n")

	server := testserver.Start(t, dataDir, fake.URL)

	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	joined := strings.Join(promptTexts(t, server, sessions[len(sessions)-1].ID), "\n")
	if !strings.Contains(joined, "Held follow-up.") || strings.Index(joined, "Held follow-up.") > strings.Index(joined, "Ready for approval.") {
		t.Errorf("prompts = %q", joined)
	}
}

func TestAHeldEventStaysHeldWhileTheQuestionIsOpen(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := heldAfterQuestion(t, fake)

	waitForPolls(t, fake)

	if got := onlySession(t, server); len(got) != 3 || !held(t, server, 2) || !slices.Contains(fake.Labels(shop, 41), "mobius:question") {
		t.Errorf("prompts = %q, labels = %v", got, fake.Labels(shop, 41))
	}
}

func TestAHeldEventWithNoQuestionStaysHeldUntilAMessageOfTheOwner(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\n"+holdEvent+"\n\n[[prompts]]\nreply = [\"Owner one.\"]\n\n[[prompts]]\nreply = [\"Seen\"]\n", keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return held(t, server, 1) })

	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if got := onlySession(t, server); len(got) != 1 || !held(t, server, 1) {
		t.Fatalf("prompts = %q", got)
	}
	sendChat(t, server, leadChat, "Go on")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) && len(onlySession(t, server)) == 3 })
}
