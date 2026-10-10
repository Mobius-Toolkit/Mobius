package engine_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func judgePromptHas(t *testing.T, server *testserver.Server, texts ...string) bool {
	t.Helper()
	return slices.ContainsFunc(judgePrompts(t, server), func(prompt string) bool {
		return !slices.ContainsFunc(texts, func(text string) bool { return !strings.Contains(prompt, text) })
	})
}

func TestAPollReadsTheReviewsAndThreadsOfManyChangedPullRequestsWithOneCall(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithStoppedTask(t, fake)
	fake.AddIssue(shop, 43, "Plan API")
	fake.AddSubIssue(shop, 12, 43)
	fake.PushCommit(shop, "mobius/43", "Plan API")
	fake.OpenPullRequest(shop, "Plan API", "mobius/43")
	if _, err := server.DB.Exec(`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, pull_request, branch, fix_rounds)
		VALUES ('owner/shop', 43, 12, 'stopped', '2026-10-04T10:00:00Z', 44, 'mobius/43', 3)`); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)
	reached, release := fake.HoldIssueEvents(shop, 12)
	fake.AddLabel(shop, 12, "priority", "owner")
	<-reached
	reads := fake.PullRequestReads()

	fake.AddComment(shop, 42, "owner", "Use cents.")
	fake.AddReviewComment(shop, 42, 0, "owner", "Rename plan to tier.")
	fake.AddReviewComment(shop, 44, 0, "owner", "Rename tier to plan.")
	release()

	testkit.WaitFor(t, func() bool { return eventLines(t, server) == 3 })
	waitForPolls(t, fake)
	// The poll after the one that read the pull requests reads the last changed pull request again.
	if got := fake.PullRequestReads() - reads; got != 2 {
		t.Errorf("reads of pull requests = %d", got)
	}
}

func TestAPollMakesNoJudgeCallForAPullRequestThatDidNotChange(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, "shell = \"true\"\n", "", noChange)
	endedChatSession(t, server, 1)
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	single, repositories := fake.CommentReads()
	reads := fake.PullRequestReads()

	waitForPolls(t, fake)

	afterSingle, afterRepositories := fake.CommentReads()
	if afterSingle != single || afterRepositories != repositories || fake.PullRequestReads() != reads {
		t.Errorf("reads of one issue = %d, reads of the repository = %d, reads of pull requests = %d",
			afterSingle-single, afterRepositories-repositories, fake.PullRequestReads()-reads)
	}
}

func TestAPollReadsThePullRequestsOfManyReviewedTasksWithOneCall(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithStoppedTask(t, fake)
	fake.AddIssue(shop, 43, "Plan API")
	fake.AddSubIssue(shop, 12, 43)
	fake.PushCommit(shop, "mobius/43", "Plan API")
	fake.OpenPullRequest(shop, "Plan API", "mobius/43")
	if _, err := server.DB.Exec(`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, pull_request, branch, fix_rounds)
		VALUES ('owner/shop', 43, 12, 'stopped', '2026-10-04T10:00:00Z', 44, 'mobius/43', 3)`); err != nil {
		t.Fatal(err)
	}
	app := testkit.AppSlug + "[bot]"
	for _, issue := range []int64{41, 43} {
		fake.AddLabel(shop, issue, "mobius:working", app)
	}
	fake.AddReviewComment(shop, 42, 0, app, "Use cents.")
	fake.AddReviewComment(shop, 44, 0, app, "Use tiers.")
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'reviewed'"); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	reads, notModified := fake.PullRequestReads(), fake.NotModifiedCount()

	waitForPolls(t, fake)

	// Each poll makes two requests that GitHub answers with Not Modified.
	if got, polls := fake.PullRequestReads()-reads, (fake.NotModifiedCount()-notModified)/2; got > polls+1 {
		t.Errorf("reads of pull requests = %d in %d polls", got, polls)
	}
	if states := []string{liveTaskState(t, server, 41), liveTaskState(t, server, 43)}; states[0] != "reviewed" || states[1] != "reviewed" {
		t.Errorf("states = %q", states)
	}
}

func TestAnEditedCommentGivesTheJudgeItsNewText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, "shell = \"true\"\n", "", func(cfg *config.Config) { cfg.ReviewQuietPeriod = 2 * time.Second })

	id := fake.AddComment(shop, 42, "owner", "Use dollars.")
	fake.AddComment(shop, 42, "owner", "Why cents?")
	waitForPolls(t, fake)
	fake.EditComment(shop, id, "Use cents.")

	testkit.WaitFor(t, func() bool { return judgePromptHas(t, server, "Use cents.", "Why cents?") })
	if judgePromptHas(t, server, "Use dollars.") {
		t.Errorf("prompts = %q", judgePrompts(t, server))
	}
}

func TestTheItemsOfAPollStartTheJudgeInALaterPollAfterTheQuietPeriod(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, "shell = \"true\"\n", "", func(cfg *config.Config) { cfg.ReviewQuietPeriod = 2 * time.Second })

	fake.AddReviewComment(shop, 42, 0, "owner", "Use price_cents.")
	fake.AddComment(shop, 42, "owner", "Why cents?")
	waitForPolls(t, fake)
	if judges := judgePrompts(t, server); len(judges) != 0 {
		t.Fatalf("prompts = %q", judges)
	}

	testkit.WaitFor(t, func() bool { return judgePromptHas(t, server, "Use price_cents.", "Why cents?") })
}

func TestARestartKeepsTheItemsOfTheJudge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fake.AddRepository(shop)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
	fake.PushCommit(shop, "mobius/41", "Add plan model")
	if number := fake.OpenPullRequest(shop, "Add plan model", "mobius/41"); number != 42 {
		t.Fatalf("pull request = %d", number)
	}
	fake.AddReviewComment(shop, 42, 0, "owner", "Use price_cents.")
	fake.AddComment(shop, 42, "owner", "Why cents?")
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nwhen = \"You are the Judge\"\nshell = \"true\"\n")
	seed(t, dataDir,
		`INSERT INTO tasks (id, repository, issue, workstream, state, dispatched_at, state_at, branch, pull_request)
		 VALUES (1, 'owner/shop', 41, 12, 'approval', '2026-10-04T10:00:00Z', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), 'mobius/41', 42)`)
	cfg := testserver.Config(t, dataDir)
	cfg.ReviewQuietPeriod = 200 * time.Millisecond

	server := testserver.StartWith(t, cfg, fake.URL)
	_, err := server.DB.Exec("INSERT INTO github_apps (app_id, slug, private_key, client_id, client_secret) VALUES (?, ?, ?, ?, ?)",
		testkit.AppID, testkit.AppSlug, testkit.AppPrivateKey, testkit.AppClientID, testkit.AppClientSecret)
	if err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return judgePromptHas(t, server, "Use price_cents.", "Why cents?") })
}
