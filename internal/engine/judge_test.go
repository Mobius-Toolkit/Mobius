package engine_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/testserver"
)

const (
	bot = "coderabbitai[bot]"
	// verdicts is a Judge with one action of each verdict for the items 2 to 5.
	verdicts = `call = { tool = "submit_verdicts", arguments = { items = [
    { item = 2, actions = [{ verdict = "fix", text = "Rename the field." }] },
    { item = 3, actions = [{ verdict = "question", text = "Explain why the plan stores cents." }] },
    { item = 4, actions = [{ verdict = "follow-up", text = "Move the parser to its own crate." }] },
    { item = 5, actions = [{ verdict = "reject", text = "The API needs this name." }] },
] } }
`
	question2 = `call = { tool = "submit_verdicts", arguments = { items = [
    { item = 2, actions = [{ verdict = "question", text = "Explain why the plan stores cents." }] },
] } }
`
	// renames is an Implementer that renames the field for the fix of item 2 and replies in the thread 2.
	renames = "[[prompts]]\nwhen = \"Action: fix: Rename the field.\"\nshell = \"echo 'price_cents' > plan.txt && git commit -q -am 'Rename the field' && git rev-parse HEAD\"\ncall = { tool = \"reply_thread\", arguments = { thread = 2, text = \"Fixed in {shell}.\" } }\n\n"
	// answers is an Implementer that answers the question of item 2 with no commit.
	answers = "[[prompts]]\nwhen = \"Action: question: Explain why the plan stores cents.\"\ncall = { tool = \"reply_thread\", arguments = { thread = 2, text = \"Cents avoid rounding.\" } }\n\n"
	// fixesEach is an Implementer that commits a change in each fix round.
	fixesEach = "[[prompts]]\nwhen = \"Action: fix\"\nshell = \"echo x >> plan.txt && git commit -q -am Fix\"\n\n"
)

// connectJudge starts a server with the task #41 of the Workstream #12, the trusted bot coderabbitai[bot] and a short
// review_quiet_period. The Judge plays judge, the Reviewer finds nothing, and the Lead replies to the follow-up of
// item 4 and starts the Implementer, which plays implementer.
func connectJudge(t *testing.T, fake *testkit.FakeGitHub, judge, implementer string, adjust func(*config.Config)) *testserver.Server {
	t.Helper()
	lead := "[[prompts]]\nwhen = \"You are the Judge\"\n" + judge + "\n" + noFinding +
		"[[prompts]]\nwhen = \"follow-up on pull request #42\"\ncall = { tool = \"reply_thread\", arguments = { thread = 4, text = \"Follow-up: #99.\" } }\n\n" + leadStarts
	server, _ := connectTask(t, fake, lead, implementer+commits, func(cfg *config.Config) {
		cfg.TrustedBots = []string{bot}
		cfg.ReviewQuietPeriod = 200 * time.Millisecond
		adjust(cfg)
	})
	readyWithItem(t, server, fake)
	return server
}

// judgePrompts gives the prompts of the Judge sessions of the Workstream #12, the oldest first.
func judgePrompts(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var texts []string
	for _, session := range roleSessions(t, server, engine.JudgeRole) {
		texts = append(texts, promptTexts(t, server, session.ID)...)
	}
	return texts
}

// replied waits until the review thread of #42 that starts with the comment root has a reply, and gives the thread.
func replied(t *testing.T, fake *testkit.FakeGitHub, root int64) testkit.Thread {
	t.Helper()
	return testkit.WaitForValue(t, func() (testkit.Thread, bool) {
		thread := fake.ReviewThread(shop, 42, root)
		return thread, len(thread.Comments) == 2
	})
}

// resolved waits until the review thread of #42 that starts with the comment root is resolved, and gives the thread.
func resolved(t *testing.T, fake *testkit.FakeGitHub, root int64) testkit.Thread {
	t.Helper()
	return testkit.WaitForValue(t, func() (testkit.Thread, bool) {
		thread := fake.ReviewThread(shop, 42, root)
		return thread, thread.Resolved
	})
}

func TestTheJudgeRoutesOneItemOfEachVerdict(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, verdicts, renames, noChange)

	fix := fake.AddReviewComment(shop, 42, 0, "owner", "Use price_cents.")
	question := fake.AddComment(shop, 42, "owner", "Why cents?")
	followUp := fake.AddReviewComment(shop, 42, 0, "owner", "Split the parser.")
	reject := fake.AddReviewComment(shop, 42, 0, bot, "Rename plan to tier.")
	if ids := []int64{fix, question, followUp, reject}; !slices.Equal(ids, []int64{2, 3, 4, 5}) {
		t.Fatalf("ids = %v", ids)
	}

	if rejected := replied(t, fake, reject); rejected.Comments[1] != (testkit.Comment{Author: app, Body: "The API needs this name."}) {
		t.Errorf("thread = %+v", rejected)
	}
	resolved(t, fake, reject)
	judge := judgePrompts(t, server)
	if len(judge) != 1 {
		t.Fatalf("prompts = %q", judge)
	}
	for _, part := range []string{
		"You are the Judge",
		"# Issue\n\n#41 Add plan model\n\nPlans have a price.\n",
		"# Items\n",
		"Thread 2, src/plan.rs line 12:\n\n@owner, ",
		"Thread 4, src/plan.rs line 12:",
		"Thread 5, src/plan.rs line 12:\n\n@coderabbitai[bot], ",
		"Comment 3:\n\n@owner, ",
	} {
		if !strings.Contains(judge[0], part) {
			t.Errorf("%q is not in %s", part, judge[0])
		}
	}
	if followed := replied(t, fake, followUp); followed.Comments[1] != (testkit.Comment{Author: app, Body: "Follow-up: #99."}) {
		t.Errorf("thread = %+v", followed)
	}
	resolved(t, fake, followUp)
	waitForLeadPrompt(t, server, " follow-up on pull request #42 of #41 \"Add plan model\", item 4:\n\n> Move the parser to its own crate.\n")
	fixed := resolved(t, fake, fix)
	want := testkit.Thread{Resolved: true, Comments: []testkit.Comment{{Author: "owner", Body: "Use price_cents."}, {Author: app, Body: "Fixed in " + head(t, fake, "mobius/41") + "."}}}
	if !reflect.DeepEqual(fixed, want) {
		t.Errorf("thread = %+v", fixed)
	}
	prompts := implementerPrompts(t, server)
	round := prompts[len(prompts)-1]
	for _, part := range []string{"# Open items\n", "Thread 2, src/plan.rs line 12:", "Action: fix: Rename the field.\n", "Comment 3:", "Action: question: Explain why the plan stores cents.\n"} {
		if !strings.Contains(round, part) {
			t.Errorf("%q is not in %s", part, round)
		}
	}
	if strings.Contains(round, "Thread 4") || strings.Contains(round, "Thread 5") {
		t.Errorf("round = %s", round)
	}
	if task := liveTask(t, server, 41); task.FixRounds != 1 {
		t.Errorf("task = %+v", task)
	}
}

func TestAnImplementerReplyWithAnAnswerAndNoCommitResolvesTheThread(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, question2, answers, noChange)

	question := fake.AddReviewComment(shop, 42, 0, "owner", "Why cents?")

	want := testkit.Thread{Resolved: true, Comments: []testkit.Comment{{Author: "owner", Body: "Why cents?"}, {Author: app, Body: "Cents avoid rounding."}}}
	if answered := resolved(t, fake, question); !reflect.DeepEqual(answered, want) {
		t.Errorf("thread = %+v", answered)
	}
}

func TestAnImplementerReplyToAConversationCommentResolvesNothing(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, question2, answers, noChange)

	question := fake.AddComment(shop, 42, "owner", "Why cents?")

	if question != 2 {
		t.Fatalf("id = %d", question)
	}
	answer := testkit.Comment{Author: app, Body: "> Why cents?\n\nCents avoid rounding."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 42), answer) })
	if fake.ReviewThread(shop, 42, question).Resolved {
		t.Error("the comment is resolved")
	}
}

func TestACommentInAnUnresolvedThreadMakesTheJudgeRunAgain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, `call = { tool = "submit_verdicts", arguments = { items = [
    { item = 2, actions = [{ verdict = "reject", text = "The API needs this name." }] },
] } }
`, "", noChange)

	thread := fake.AddReviewComment(shop, 42, 0, bot, "Rename plan to tier.")
	resolved(t, fake, thread)
	if prompts := judgePrompts(t, server); len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}

	fake.UnresolveReviewThread(thread)
	fake.AddReviewComment(shop, 42, thread, "owner", "Rename it anyway.")

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := judgePrompts(t, server)
		return prompts, len(prompts) == 2
	})
	if !strings.Contains(prompts[1], "Rename it anyway.") {
		t.Errorf("prompt = %s", prompts[1])
	}
}

// judgeToHuman starts a server whose Judge gives no verdicts, with max_fix_rounds 1. Two comments of the trusted bot
// start a fix round, and the round limit then hands the task to a human.
func judgeToHuman(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	server := connectJudge(t, fake, "shell = \"true\"\n", fixesEach, func(cfg *config.Config) { cfg.MaxFixRounds = 1 })
	fake.AddReviewComment(shop, 42, 0, bot, "Rename plan to tier.")
	fake.AddReviewComment(shop, 42, 0, bot, "Rename tier to plan.")
	testkit.WaitFor(t, func() bool {
		return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") && !hasLabel(fake, "mobius:working")
	})
	return server
}

func TestAFixRoundOfTheJudgeFromNeedsHumanPutsTheWorkingLabelBack(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	judgeToHuman(t, fake)

	fake.AddComment(shop, 42, "owner", "Continue.")

	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") && !hasLabel(fake, "mobius:needs-human") })
}

func TestMobiusReadyOnATaskInNeedsHumanStartsAFixRoundOnTheSamePullRequest(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := judgeToHuman(t, fake)
	stopped := liveTask(t, server, 41)
	sha := head(t, fake, "mobius/41")

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	fixed := testkit.WaitForValue(t, func() (string, bool) {
		fixed := head(t, fake, "mobius/41")
		return fixed, fixed != sha
	})
	isAncestor(t, fake, sha, fixed)
	task := liveTask(t, server, 41)
	if task.ID != stopped.ID || task.Branch != stopped.Branch || task.PullRequest != stopped.PullRequest || task.State == "stopped" {
		t.Errorf("task = %+v", task)
	}
	if len(fake.PullRequests(shop)) != 1 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
	if !hasLabel(fake, "mobius:working") || hasLabel(fake, "mobius:ready") || hasLabel(fake, "mobius:needs-human") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	prompts := implementerPrompts(t, server)
	if last := prompts[len(prompts)-1]; !strings.Contains(last, "Rename tier to plan.") || !strings.Contains(last, "Action: fix") {
		t.Errorf("prompt = %s", last)
	}
}

func TestARemovalOfTheWorkingLabelStopsATaskWhileTheJudgeRunsFromAStateOtherThanNeedsHuman(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, "hang = true\n", "", noChange)
	fake.AddComment(shop, 42, "owner", "Why cents?")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "working" })

	fake.RemoveLabel(shop, 41, "mobius:working", "mallory")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "stopped" })
	testkit.WaitFor(t, func() bool {
		judges := roleSessions(t, server, engine.JudgeRole)
		return len(judges) == 1 && judges[0].EndReason.String == "stopped"
	})
}

// A comment on the pull request of a task in ready_for_review goes to the Judge, and not to the Lead (Mobius#274).
func TestACommentOnThePullRequestOfATaskInReadyForReviewGoesOnlyToTheJudge(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, "shell = \"true\"\n", "", noChange)

	fake.AddComment(shop, 42, "owner", "Why cents?")

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(judgePrompts(t, server), func(prompt string) bool { return strings.Contains(prompt, "Why cents?") })
	})
	waitForPolls(t, fake)
	if slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool { return strings.Contains(prompt, "Why cents?") }) {
		t.Errorf("Lead prompts = %q", leadPrompts(t, server))
	}
}
