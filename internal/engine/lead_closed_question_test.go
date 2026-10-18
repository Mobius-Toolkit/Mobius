package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
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
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n\n[[prompts]]\nreply = [\"Seen\"]\n", func(cfg *config.Config) {
		seed(t, cfg.DataDir,
			`INSERT INTO lead_events (repository, workstream, issue, kind, payload, time, held) VALUES ('owner/shop', 12, 41, 'follow_up', 'Held follow-up.', '2026-10-04T10:00:00Z', 1)`,
			`INSERT INTO lead_events (repository, workstream, issue, kind, payload, time) VALUES ('owner/shop', 12, 41, 'ready_for_approval', 'Ready for approval.', '2026-10-04T10:10:00Z')`,
			`INSERT INTO inbox_items (kind, organization, repository, workstream, issue, text, link, time, dismissed_at) VALUES ('question', 'owner', 'owner/shop', 12, 41, 'Cents or dollars?', '', '2026-10-04T10:05:00Z', '2026-10-04T11:00:00Z')`)
	})

	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	if joined := strings.Join(onlySession(t, server), "\n"); !strings.Contains(joined, "Held follow-up.") || strings.Index(joined, "Held follow-up.") > strings.Index(joined, "Ready for approval.") {
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
