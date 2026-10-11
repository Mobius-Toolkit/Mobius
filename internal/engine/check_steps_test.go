package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	hangsInFixRound  = "[[prompts]]\nwhen = \"Action: fix\"\nhang = true\n\n" + commits
	hangsOnLeadRound = "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nhang = true\n\n" + commits
	fixVerdict       = "call = { tool = \"submit_verdicts\", arguments = { items = [{ item = 2, actions = [{ verdict = \"fix\", text = \"Rename the field.\" }] }] } }\n"
)

var approvedOutput = &testkit.CheckRunOutput{Title: "Approved", Summary: "The Lead approved the pull request."}
var approvalOutput = &testkit.CheckRunOutput{Title: "Wait for the Lead approval", Summary: "The CI passed. Mobius waits for the Lead to approve the pull request."}

// mobiusTitles gives the titles of the Mobius check runs of the shop, in the order of their creation.
func mobiusTitles(fake *testkit.FakeGitHub) []string {
	var titles []string
	for _, run := range mobiusCheckRuns(fake) {
		title := ""
		if run.Output != nil {
			title = run.Output.Title
		}
		titles = append(titles, title)
	}
	return titles
}

// waitForTitle waits until the only Mobius check run of the shop has the title.
func waitForTitle(t *testing.T, fake *testkit.FakeGitHub, title string) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return slices.Equal(mobiusTitles(fake), []string{title}) })
}

func TestAPushShowsTheReviewRoundOnTheCheckRunOfTheNewHead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	connectTask(t, fake, "[[prompts]]\nwhen = \"You are the Reviewer\"\nhang = true\n\n"+leadStarts, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForTitle(t, fake, "Review, round 1 of 10")
}

func TestAReviewFindingShowsTheFixRoundWithItsCauseOnTheCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	connectTask(t, fake, finding+leadStarts, hangsInFixRound, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForTitle(t, fake, "Fix round 1 of 10, review")
}

func TestAFailedCheckRunShowsTheFixRoundWithItsCauseOnTheCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, hangsInFixRound, noChange)
	sha := approvalHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	waitForTitle(t, fake, "Fix round 1 of 10, CI")
}

func TestTheFindingsOfTheLeadShowTheFixRoundWithItsCauseOnTheCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, hangsOnLeadRound, noChange)
	approvalHead(t, server, fake)

	sendChat(t, server, leadChat, "Send the findings to #41")

	waitForTitle(t, fake, "Fix round 1 of 10, Lead")
}

func TestAConflictRoundShowsItsCauseOnTheCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	startReady(t, fake, "", commits+"\n"+conflictWhen+"hang = true\n")

	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")

	waitForTitle(t, fake, "Fix round, conflict")
}

func TestTheJudgeShowsItsStepOnTheCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, "hang = true\n", "", noChange)

	fake.AddReviewComment(shop, 42, 0, "owner", "Use price_cents.")

	waitForTitle(t, fake, "Judge")
}

func TestAFixRoundOfTheJudgeShowsItsCauseOnTheCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	connectJudge(t, fake, fixVerdict, hangsInFixRound, noChange)

	fake.AddReviewComment(shop, 42, 0, "owner", "Use price_cents.")

	waitForTitle(t, fake, "Fix round 1 of 10, Judge")
}

func TestAFixRoundAfterTheApprovalKeepsOneCompleteCheckRunOnTheHead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, noFinding+leadApproves+leadFindings+leadStarts, hangsOnLeadRound, noChange)
	approvalHead(t, server, fake)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got it for review.")

	sendChat(t, server, leadChat, "Send the findings to #41")

	roundPrompt(t, server)
	runs := mobiusCheckRuns(fake)
	if len(runs) != 1 || runs[0].Status != "completed" || runs[0].Conclusion != "success" {
		t.Errorf("check runs = %+v", runs)
	}
}

func TestAPushAfterTheLastReviewRoundShowsNoReviewRoundOnTheCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	implementer := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo more >> plan.txt && git commit -q -am 'Remove the lines'\"\n\n" + commits
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, implementer, func(cfg *config.Config) { cfg.MaxFixRounds = 1 })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	readyForReview(t, server, fake)

	sendChat(t, server, leadChat, "Send the findings to #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	testkit.WaitFor(t, func() bool { return slices.Contains(mobiusTitles(fake), "Round limit") })
	if titles := mobiusTitles(fake); slices.Contains(titles, "Review, round 2 of 1") {
		t.Errorf("titles = %q", titles)
	}
}

func TestAFixRoundAfterTheResumeOfAStoppedTaskShowsTheCountOfTheResetCounter(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dir := t.TempDir()
	go1, running, release := filepath.Join(dir, "go"), filepath.Join(dir, "running"), filepath.Join(dir, "release")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	implementer := fmt.Sprintf("[[prompts]]\nwhen = \"Action: fix\"\nshell = \"if [ -e '%s' ]; then touch '%s'; while [ ! -e '%s' ]; do sleep 0.05; done; else kill -9 $PPID; sleep 5; fi\"\n\n%s", go1, running, release, commits)
	server, _ := connectTask(t, fake, finding+leadStarts, implementer, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 1 })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	if _, err := server.DB.Exec("UPDATE tasks SET fix_rounds = 4 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { runs := mobiusCheckRuns(fake); return len(runs) == 1 && runs[0].Status == "completed" })
	// A stop completes the check run, and a fix round changes only an open check run.
	fake.SetCheckRunStatus(1, "in_progress", "")
	if err := os.WriteFile(go1, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	fake.RemoveLabel(shop, 41, "mobius:needs-human", "owner")

	testkit.WaitFor(t, func() bool { _, err := os.Stat(running); return err == nil })
	waitForTitle(t, fake, "Fix round 1 of 10, review")
}
