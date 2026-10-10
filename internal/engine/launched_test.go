package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// connectGatedLead starts a server whose Lead turn for the event "First comment." ends when the gate opens. The next
// turns answer "Seen". The gate opens at the end of the test too.
func connectGatedLead(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, func()) {
	t.Helper()
	gate := filepath.Join(t.TempDir(), "gate")
	script := fmt.Sprintf("[[prompts]]\nwhen = \"First comment.\"\nshell = \"until [ -e %s ]; do sleep 0.05; done\"\n\n%s", gate, seen)
	server, _ := connectWith(t, fake, script, func(cfg *config.Config) { cfg.LeadIdleTimeout = 5 * time.Second })
	open := func() {
		if err := os.WriteFile(gate, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(open)
	return server, open
}

func TestACommentOnAWorkstreamIssueGetsARocketWhenTheLeadTurnOfItsEventStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	_, release := connectGatedLead(t, fake)

	first := fake.AddComment(shop, 12, "owner", "First comment.")
	second := fake.AddComment(shop, 12, "owner", "Second comment.")

	waitForReactions(t, fake, first, reactions("rocket"))
	waitForReactions(t, fake, second, reactions("eyes"))
	waitForPolls(t, fake)
	if got := fake.Reactions(shop, second); !slices.Equal(got, reactions("eyes")) {
		t.Errorf("reactions = %+v", got)
	}

	release()

	waitForReactions(t, fake, second, reactions("rocket"))
}

func TestARocketRemovesOnlyTheEyesOfTheApp(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	_, release := connectGatedLead(t, fake)

	fake.AddComment(shop, 12, "owner", "First comment.")
	second := fake.AddComment(shop, 12, "owner", "Second comment.")
	waitForReactions(t, fake, second, reactions("eyes"))
	fake.AddReaction(shop, second, "owner", "eyes")

	release()

	waitForReactions(t, fake, second, []testkit.Reaction{{User: "owner", Content: "eyes"}, reactions("rocket")[0]})
}

func TestACommentOnATaskIssueOrItsPullRequestGetsARocketWhenTheLeadTurnOfItsEventStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, release := connectGatedLead(t, fake)
	addStoppedTask(t, fake, server)

	first := fake.AddComment(shop, 41, "owner", "First comment.")
	conversation := fake.AddComment(shop, 42, "owner", "Second comment.")
	review := fake.AddReviewComment(shop, 42, 0, "owner", "Third comment.")

	waitForReactions(t, fake, first, reactions("rocket"))
	waitForReactions(t, fake, conversation, reactions("eyes"))
	waitForReactions(t, fake, review, reactions("eyes"))
	waitForPolls(t, fake)
	for _, id := range []int64{conversation, review} {
		if got := fake.Reactions(shop, id); !slices.Equal(got, reactions("eyes")) {
			t.Errorf("reactions of %d = %+v", id, got)
		}
	}

	release()

	waitForReactions(t, fake, conversation, reactions("rocket"))
	waitForReactions(t, fake, review, reactions("rocket"))
}

func TestAFailedRocketDoesNotStopTheLeadTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, seen, func(cfg *config.Config) { cfg.LeadIdleTimeout = 5 * time.Second })
	fake.FailReactions("rocket", true)

	failed := fake.AddComment(shop, 12, "owner", "Round down.")

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool { return strings.Contains(prompt, "Round down.") })
	})
	waitForReactions(t, fake, failed, reactions("eyes"))

	fake.FailReactions("rocket", false)
	next := fake.AddComment(shop, 12, "owner", "Round up.")

	waitForReactions(t, fake, next, reactions("rocket"))
	if got := fake.Reactions(shop, failed); !slices.Equal(got, reactions("eyes")) {
		t.Errorf("reactions = %+v", got)
	}
}

func TestACommentOfATrustedUserGetsARocketWhenTheJudgeSessionStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, "shell = \"true\"\n", "", func(cfg *config.Config) { cfg.ReviewQuietPeriod = 2 * time.Second })

	review := fake.AddReviewComment(shop, 42, 0, "owner", "Use price_cents.")
	conversation := fake.AddComment(shop, 42, "owner", "Why cents?")
	fromBot := fake.AddReviewComment(shop, 42, 0, bot, "Rename plan to tier.")

	waitForReactions(t, fake, review, reactions("eyes"))
	waitForReactions(t, fake, conversation, reactions("eyes"))

	waitForReactions(t, fake, review, reactions("rocket"))
	waitForReactions(t, fake, conversation, reactions("rocket"))
	if got := fake.Reactions(shop, fromBot); len(got) != 0 {
		t.Errorf("reactions of the comment of the bot = %+v", got)
	}
}

func TestEachNewCommentOfATrustedUserInAThreadGetsARocketWhenTheJudgeSessionStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, "shell = \"true\"\n", "", func(cfg *config.Config) { cfg.ReviewQuietPeriod = 2 * time.Second })

	first := fake.AddReviewComment(shop, 42, 0, "owner", "Use price_cents.")
	second := fake.AddReviewComment(shop, 42, first, "owner", "Also rename plan to tier.")
	fromBot := fake.AddReviewComment(shop, 42, first, bot, "Rename plan to tier.")

	waitForReactions(t, fake, first, reactions("eyes"))
	waitForReactions(t, fake, second, reactions("eyes"))

	waitForReactions(t, fake, first, reactions("rocket"))
	waitForReactions(t, fake, second, reactions("rocket"))
	if got := fake.Reactions(shop, fromBot); len(got) != 0 {
		t.Errorf("reactions of the comment of the bot = %+v", got)
	}
}

func TestACommentThatTheLeadWroteThroughTheAppGetsNoRocketWhenTheJudgeSessionStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, "shell = \"true\"\n", "", func(cfg *config.Config) { cfg.ReviewQuietPeriod = 2 * time.Second })

	fromLead := fake.AddAppComment(shop, 42, "owner", "I asked the Lead in the chat.")
	conversation := fake.AddComment(shop, 42, "owner", "Why cents?")

	waitForReactions(t, fake, conversation, reactions("eyes"))
	waitForReactions(t, fake, conversation, reactions("rocket"))
	if got := fake.Reactions(shop, fromLead); len(got) != 0 {
		t.Errorf("reactions of the comment of the Lead = %+v", got)
	}
}

func TestACommentThatTheDrainHeldGetsARocketWhenTheTriagerStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 56, "Add points")
	fake.AddLabel(shop, 56, "mobius:ready", "owner")
	waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	testkit.WaitFor(t, func() bool { return len(fake.Comments(shop, 56)) == 1 })
	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}

	id := fake.AddComment(shop, 56, "owner", "Please use points.")

	waitForReactions(t, fake, id, reactions("eyes"))
	waitForPolls(t, fake)
	if got := fake.Reactions(shop, id); !slices.Equal(got, reactions("eyes")) {
		t.Errorf("reactions = %+v", got)
	}

	cancelDrain(t, server)

	waitForReactions(t, fake, id, reactions("rocket"))
}

func TestACommentThatTheFirstRunOfTheTriagerReadsGetsARocketWhenTheRunStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}
	fake.AddIssue(shop, 57, "Add pricing")
	fake.AddLabel(shop, 57, "mobius:ready", "owner")
	waitForPolls(t, fake)

	id := fake.AddComment(shop, 57, "owner", "Please cover pricing.")

	waitForReactions(t, fake, id, reactions("eyes"))
	waitForPolls(t, fake)
	if got := fake.Reactions(shop, id); !slices.Equal(got, reactions("eyes")) {
		t.Errorf("reactions = %+v", got)
	}

	cancelDrain(t, server)

	waitForReactions(t, fake, id, reactions("rocket"))
}

func TestACommentThatIsOlderThanAProposalOfTheTriagerGetsARocketWhenTheTriagerReadsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 56, "Add points")
	fake.AddLabel(shop, 56, "mobius:ready", "owner")
	waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	testkit.WaitFor(t, func() bool { return len(fake.Comments(shop, 56)) == 1 })
	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}

	id := fake.AddComment(shop, 56, "owner", "Please use points.")
	fake.AddComment(shop, 56, testkit.AppSlug+"[bot]", "Second proposal.")

	waitForReactions(t, fake, id, reactions("eyes"))
	cancelDrain(t, server)

	waitForReactions(t, fake, id, reactions("rocket"))
}
