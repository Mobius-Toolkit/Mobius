package engine_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	// fixes is an Implementer that commits plan.txt with cents, and fixes the check in a fix round.
	fixes = "[[prompts]]\nwhen = \"Action: fix\"\nshell = \"echo 'cents per month' > plan.txt && git commit -q -am 'Fix the check'\"\n\n" + commits
	// fixesNothing is an Implementer that commits plan.txt with cents, and makes no commit in a fix round.
	fixesNothing = "[[prompts]]\nwhen = \"Action: fix\"\nshell = \"true\"\n\n" + commits
)

// readyForReview dispatches #41, waits until its task is ready for review, and gives the head of its branch.
func readyForReview(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) string {
	t.Helper()
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	return head(t, fake, "mobius/41")
}

// roundPrompt waits for the second Implementer session, and gives its first prompt.
func roundPrompt(t *testing.T, server *testserver.Server) string {
	t.Helper()
	return testkit.WaitForValue(t, func() (string, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) < 2 {
			return "", false
		}
		prompts := promptTexts(t, server, sessions[1].ID)
		if len(prompts) == 0 {
			return "", false
		}
		return prompts[0], true
	})
}

func checkRun(name, sha, status, conclusion string) testkit.CheckRun {
	return testkit.CheckRun{Name: name, HeadSHA: sha, Status: status, Conclusion: conclusion, Output: &testkit.CheckRunOutput{Title: name + " title", Summary: name + " summary"}}
}

// sentinelRound adds a failed check run sentinel on the head, and gives the prompt of the fix round that it starts.
func sentinelRound(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, sha string) string {
	t.Helper()
	fake.AddCheckRun(shop, checkRun("sentinel", sha, "completed", "failure"))
	return roundPrompt(t, server)
}

func implementers(t *testing.T, server *testserver.Server) int {
	t.Helper()
	return len(roleSessions(t, server, engine.ImplementerRole))
}

func TestAFailedCheckRunOnTheHeadStartsAFixRoundWithTheCheckAndItsAnnotations(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := readyForReview(t, server, fake)

	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.AddAnnotation(id, "plan.txt", 1, "Store the unit.")

	round := roundPrompt(t, server)
	for _, part := range []string{
		"# Open items\n",
		fmt.Sprintf("Check run \"build\", https://github.com/owner/shop/runs/%d:\nbuild title\n\nbuild summary\n", id),
		"- plan.txt line 1: Store the unit.\n",
		"Action: fix\n",
	} {
		if !strings.Contains(round, part) {
			t.Errorf("%q is not in %s", part, round)
		}
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" && head(t, fake, "mobius/41") != sha })
	if task := liveTask(t, server, 41); task.FixRounds != 1 {
		t.Errorf("fix rounds = %d", task.FixRounds)
	}
	if count := implementers(t, server); count != 2 {
		t.Errorf("Implementers = %d", count)
	}
}

func TestAFailedCheckRunMakesTheReadyPullRequestADraftUntilTheEndOfTheRound(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	goFile := filepath.Join(dataDir, "go")
	round := fmt.Sprintf("[[prompts]]\nwhen = \"Action: fix\"\nshell = \"while [ ! -e '%s' ]; do sleep 0.05; done\"\n\n", goFile)
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, round+commits, noChange)
	sha := readyForReview(t, server, fake)
	testkit.WaitFor(t, func() bool { return !fake.PullRequests(shop)[0].Draft })

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return fake.PullRequests(shop)[0].Draft })
	if state := taskState(t, server); state != "working" {
		t.Errorf("state = %s", state)
	}

	touch(t, goFile)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	testkit.WaitFor(t, func() bool { return !fake.PullRequests(shop)[0].Draft })
}

func TestAFailedGitHubActionsCheckRunGivesTheLast200LinesOfItsJobLog(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := readyForReview(t, server, fake)

	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.SetCheckRunApp(id, "github-actions")
	fake.AddAnnotation(id, "plan.txt", 1, "Store the unit.")
	var log []string
	for line := 1; line <= 250; line++ {
		log = append(log, fmt.Sprintf("log line %d", line))
	}
	fake.AddJobLog(id, strings.Join(log, "\n"))

	round := roundPrompt(t, server)
	inOrder(t, round, "- plan.txt line 1: Store the unit.\n", "\n"+strings.Join(log[50:], "\n")+"\n", "Action: fix\n")
	if strings.Contains(round, "log line 50\n") {
		t.Errorf("round = %s", round)
	}
}

func TestAFailedCheckRunOfADifferentAppGivesNoJobLog(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := readyForReview(t, server, fake)

	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.SetCheckRunApp(id, "other-ci")
	fake.AddJobLog(id, "secret log line")

	round := roundPrompt(t, server)
	if !strings.Contains(round, "Check run \"build\"") || strings.Contains(round, "secret log line") {
		t.Errorf("round = %s", round)
	}
}

func TestAFailedJobLogDownloadStillStartsTheFixRound(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := readyForReview(t, server, fake)

	id := fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	fake.SetCheckRunApp(id, "github-actions")
	fake.AddAnnotation(id, "plan.txt", 1, "Store the unit.")

	round := roundPrompt(t, server)
	for _, part := range []string{"Check run \"build\"", "- plan.txt line 1: Store the unit.\n", "Action: fix\n"} {
		if !strings.Contains(round, part) {
			t.Errorf("%q is not in %s", part, round)
		}
	}
	if strings.Contains(round, "job log") {
		t.Errorf("round = %s", round)
	}
}

func TestAFailedCheckRunOnTheSameHeadStartsOneFixRound(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixesNothing, noChange)
	sha := readyForReview(t, server, fake)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
	endedImplementers(t, server, 2)
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	if got := head(t, fake, "mobius/41"); got != sha {
		t.Errorf("head = %s", got)
	}
	waitForPolls(t, fake)

	if count := implementers(t, server); count != 2 {
		t.Errorf("Implementers = %d", count)
	}
	if state := taskState(t, server); state != "ready_for_review" {
		t.Errorf("state = %s", state)
	}
}

func TestACancelledOrTimedOutCheckRunOnTheHeadStartsAFixRound(t *testing.T) {
	for _, conclusion := range []string{"cancelled", "timed_out"} {
		t.Run(conclusion, func(t *testing.T) {
			fake := testkit.NewFakeGitHub(t)
			server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
			sha := readyForReview(t, server, fake)

			fake.AddCheckRun(shop, checkRun("build", sha, "completed", conclusion))

			testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
		})
	}
}

func TestAFailedCheckRunMobiusHasNoEffect(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := readyForReview(t, server, fake)

	fake.AddCheckRun(shop, checkRun("Mobius", sha, "completed", "failure"))
	round := sentinelRound(t, server, fake, sha)

	if !strings.Contains(round, "Check run \"sentinel\"") || strings.Contains(round, "Check run \"Mobius\"") {
		t.Errorf("round = %s", round)
	}
}

func TestARunningCheckRunAndAPassedCheckRunHaveNoEffect(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := readyForReview(t, server, fake)

	fake.AddCheckRun(shop, checkRun("running", sha, "in_progress", ""))
	fake.AddCheckRun(shop, checkRun("passed", sha, "completed", "success"))
	round := sentinelRound(t, server, fake, sha)

	if !strings.Contains(round, "Check run \"sentinel\"") || strings.Contains(round, "Check run \"running\"") || strings.Contains(round, "Check run \"passed\"") {
		t.Errorf("round = %s", round)
	}
}

func TestAFailedCheckRunAtMaxFixRoundsHandsTheTaskToAHuman(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, func(cfg *config.Config) { cfg.MaxFixRounds = 1 })
	sha := readyForReview(t, server, fake)
	if _, err := server.DB.Exec("UPDATE tasks SET fix_rounds = 1 WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") })
	if hasLabel(fake, "mobius:working") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if count := implementers(t, server); count != 1 {
		t.Errorf("Implementers = %d", count)
	}
	runs := testkit.WaitForValue(t, func() ([]testkit.CheckRun, bool) {
		runs := fake.CheckRuns(shop)
		last := runs[len(runs)-1]
		return runs, last.Output != nil && last.Output.Title == "Round limit"
	})
	if last := runs[len(runs)-1]; last.Conclusion != "failure" || last.Output.Summary != "The pull request has open items after 1 fix rounds." {
		t.Errorf("check runs = %+v", runs)
	}
	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": the pull request has open items after 1 fix rounds.")
}
