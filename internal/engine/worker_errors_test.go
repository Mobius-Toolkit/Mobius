package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	// failReviewerEnd makes the end of a Reviewer session fail.
	failReviewerEnd = "CREATE TRIGGER fail_update BEFORE UPDATE OF end_reason ON sessions WHEN NEW.role = 'reviewer' BEGIN SELECT RAISE(ABORT, 'forced failure'); END"
	// failHandToHuman makes the move of a task to needs_human fail.
	failHandToHuman = "CREATE TRIGGER fail_update BEFORE UPDATE OF state ON tasks WHEN NEW.state = 'needs_human' BEGIN SELECT RAISE(ABORT, 'forced failure'); END"
	// failFixRoundWorker makes the change of the Worker of a task to the Implementer of a fix round fail.
	failFixRoundWorker = "CREATE TRIGGER fail_update BEFORE UPDATE OF worker_input ON tasks WHEN NEW.worker_input LIKE '%# Open items%' BEGIN SELECT RAISE(ABORT, 'forced failure'); END"
)

// failTaskUpdates adds the trigger, which fails the updates of task rows until allowTaskUpdates.
func failTaskUpdates(t *testing.T, server *testserver.Server, trigger string) {
	t.Helper()
	if _, err := server.DB.Exec(trigger); err != nil {
		t.Fatal(err)
	}
}

func allowTaskUpdates(t *testing.T, server *testserver.Server) {
	t.Helper()
	if _, err := server.DB.Exec("DROP TRIGGER fail_update"); err != nil {
		t.Fatal(err)
	}
}

func workerRestarts(t *testing.T, server *testserver.Server) int64 {
	t.Helper()
	var restarts int64
	if err := server.DB.QueryRow("SELECT coalesce(max(worker_restarts), 0) FROM tasks WHERE repository = ? AND issue = 41", shop).Scan(&restarts); err != nil {
		t.Fatal(err)
	}
	return restarts
}

// seedReviewer starts a server with the task of #41 in the Reviewer step. The pull request #42 does not exist yet, so
// the restart of the Reviewer fails until openPullRequest.
func seedReviewer(t *testing.T, fake *testkit.FakeGitHub, adjust func(*config.Config)) *testserver.Server {
	t.Helper()
	return seedReviewerAs(t, fake, options+noFinding, adjust)
}

// seedReviewerWith is seedReviewer with the agent script script.
func seedReviewerWith(t *testing.T, fake *testkit.FakeGitHub, script string) *testserver.Server {
	t.Helper()
	return seedReviewerAs(t, fake, options+script, func(*config.Config) {})
}

func seedReviewerAs(t *testing.T, fake *testkit.FakeGitHub, script string, adjust func(*config.Config)) *testserver.Server {
	t.Helper()
	dataDir := t.TempDir()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
	testkit.InstallFakeAgent(t, dataDir, script)
	seed(t, dataDir,
		`INSERT INTO tasks (id, repository, issue, workstream, state, dispatched_at, queued_at, branch, pull_request, worker, worker_input)
		 VALUES (1, 'owner/shop', 41, 12, 'working', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z', 'mobius/41', 42, 'reviewer', NULL)`)
	cfg := testserver.Config(t, dataDir)
	adjust(cfg)
	server := startServerWith(t, fake, cfg, "")
	fake.PushCommit(shop, "mobius/41", "Add plan model")
	server.WaitForFirstPoll(t, shop)
	return server
}

func TestAnErrorInTheReviewerWhileTheTaskIsQueuedStartsTheReviewerAgain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	implementer := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo more >> plan.txt && git commit -q -am 'Remove the lines'\"\n\n" + commits
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, implementer, func(cfg *config.Config) { cfg.MaxFixRounds = 1 })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	readyForReview(t, server, fake)
	// At the review limit, the Reviewer hands the task to a human before its session, while the task is queued.
	failTaskUpdates(t, server, failHandToHuman)

	sendChat(t, server, leadChat, "Send the findings to #41")

	testkit.WaitFor(t, func() bool { return workerRestarts(t, server) >= 1 })
	if state := taskState(t, server); state != "queued" {
		t.Errorf("state = %s", state)
	}
	allowTaskUpdates(t, server)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	testkit.WaitFor(t, func() bool {
		comments := roundComments(fake)
		return len(comments) == 2 && strings.HasPrefix(comments[1], "Review not started. Limit reached (1 of 1).")
	})
	if restarts := workerRestarts(t, server); restarts < 1 {
		t.Errorf("worker restarts = %d", restarts)
	}
}

func TestAnErrorInAFixRoundAfterTheQueueStepStartsTheImplementerOfTheRoundAgain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, finding+leadStarts, fixReplies+commits, func(cfg *config.Config) {
		cfg.MaxFixRounds = 2
		cfg.MaxWorkerRestarts = 10
	})
	failTaskUpdates(t, server, failFixRoundWorker)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { return workerRestarts(t, server) >= 1 })
	reviewers := endedReviewers(t, server, 1)
	if reviewers[0].EndReason.String != "done" {
		t.Errorf("Reviewers = %+v", reviewers)
	}
	if task := liveTask(t, server, 41); task.State != "queued" || task.FixRounds != 1 {
		t.Errorf("task = %+v", task)
	}
	allowTaskUpdates(t, server)

	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.ImplementerRole)) >= 2 })
	implementers := roleSessions(t, server, engine.ImplementerRole)
	if implementers[1].Parent.Int64 != reviewers[0].ID {
		t.Errorf("Implementers = %+v, Reviewers = %+v", implementers, reviewers)
	}
	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := promptTexts(t, server, implementers[1].ID)
		return prompts, len(prompts) > 0
	})
	if len(prompts) != 1 || !strings.Contains(prompts[0], "# Open items\n") {
		t.Errorf("prompts = %q", prompts)
	}
	if task := liveTask(t, server, 41); task.FixRounds != 1 {
		t.Errorf("task = %+v", task)
	}
}

func TestAReviewerThatFailsAfterAFixRoundStartedDoesNotStartAgain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, finding+leadStarts, fixReplies+commits, func(cfg *config.Config) {
		cfg.MaxFixRounds = 2
		cfg.MaxWorkerRestarts = 10
	})
	failTaskUpdates(t, server, failReviewerEnd)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	endedImplementers(t, server, 2)
	if restarts := workerRestarts(t, server); restarts != 0 {
		t.Errorf("worker restarts = %d", restarts)
	}
}

func TestAFixRoundThatFailsAfterTheQueueStepGoesToAHumanAtMaxWorkerRestarts(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, finding+leadStarts, fixReplies+commits, func(cfg *config.Config) {
		cfg.MaxWorkerRestarts = 1
	})
	failTaskUpdates(t, server, failFixRoundWorker)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": the Worker failed after 1 restarts.")
	task := liveTask(t, server, 41)
	if task.State != "needs_human" || task.WorkerRestarts != 1 {
		t.Errorf("task = %+v", task)
	}
	if !hasLabel(fake, "mobius:needs-human") || hasLabel(fake, "mobius:working") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if implementers := roleSessions(t, server, engine.ImplementerRole); len(implementers) != 1 {
		t.Errorf("Implementers = %+v", implementers)
	}
}

func TestAnErrorInTheRestartOfTheReviewerStartsTheReviewerAgain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := seedReviewer(t, fake, func(*config.Config) {})

	testkit.WaitFor(t, func() bool { return workerRestarts(t, server) >= 1 })
	if reviewers := roleSessions(t, server, engine.ReviewerRole); len(reviewers) != 0 {
		t.Errorf("Reviewers = %+v", reviewers)
	}
	if number := fake.OpenPullRequest(shop, "Add plan model", "mobius/41"); number != 42 {
		t.Fatalf("pull request = %d", number)
	}

	reviewers := endedReviewers(t, server, 1)
	if reviewers[0].EndReason.String != "done" {
		t.Errorf("Reviewers = %+v", reviewers)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "checks" || taskState(t, server) == "approval" })
}

func TestARestartOfTheReviewerThatFailsEachTimeGoesToAHumanAtMaxWorkerRestarts(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddPullRequest(shop, 42, "Add plan model")
	server := seedReviewer(t, fake, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": the Worker failed after 1 restarts.")
	task := liveTask(t, server, 41)
	if task.State != "needs_human" || task.WorkerRestarts != 1 {
		t.Errorf("task = %+v", task)
	}
	if !hasLabel(fake, "mobius:needs-human") || hasLabel(fake, "mobius:working") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
}
