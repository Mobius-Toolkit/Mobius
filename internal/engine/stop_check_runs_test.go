package engine_test

import (
	"errors"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// wantStopCheck tests that the head has one Mobius check run, and that it is complete as failure with title.
func wantStopCheck(t *testing.T, fake *testkit.FakeGitHub, sha, title string) {
	t.Helper()
	runs := mobiusCheckRuns(fake)
	if len(runs) != 1 || runs[0].HeadSHA != sha || runs[0].Status != "completed" || runs[0].Conclusion != "failure" || runs[0].Output == nil || runs[0].Output.Title != title {
		t.Errorf("check runs = %+v", runs)
	}
}

func TestAHungSessionCompletesTheMobiusCheckRunAsFailure(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\nwhen = \"Action: fix\"\nhang = true\n\n[[prompts]]\nwhen = \"You are probably stuck\"\nhang = true\n\n"+commits, noChange)
	sha := approvalHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("sentinel", sha, "completed", "failure"))

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": the implementer session ")
	wantStopCheck(t, fake, sha, "Session hung")
}

func TestTheWorkerRestartLimitCompletesTheMobiusCheckRunAsFailure(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	sha := approvalHead(t, server, fake)
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'working', worker = NULL, worker_restarts = 3 WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	restart, err := server.Engine.RestartWorker(t.Context(), liveTask(t, server, 41), "Add plan model", errors.New("the Worker died"))

	if err != nil || restart {
		t.Fatalf("restart = %v: %v", restart, err)
	}
	wantStopCheck(t, fake, sha, "Restart limit")
}

func TestAPushThatGitHubRejectsCompletesTheMobiusCheckRunAsFailure(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, noChange)
	sha := approvalHead(t, server, fake)
	// GitHub refuses each push to a hidden ref.
	testkit.Git(t, fake.Remote(shop), "config", "receive.hideRefs", "refs/heads/mobius")

	fake.AddCheckRun(shop, checkRun("sentinel", sha, "completed", "failure"))

	waitForLeadPrompt(t, server, " stop of #41 \"Add plan model\": GitHub rejected the push.")
	wantStopCheck(t, fake, sha, "Push rejected")
}

func TestAStalePullRequestCompletesTheMobiusCheckRunAsFailure(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	sha := approvalHead(t, server, fake)
	fake.SetCreatedAt(shop, 42, 0)
	fake.SetBehind(shop, 42)

	fake.CommitFile(shop, "price.txt", "dollars\n", "Add price")

	waitForLeadPrompt(t, server, " stale pull request #42 of #41 \"Add plan model\": it is behind its base branch")
	wantStopCheck(t, fake, sha, "Stale pull request")
}

func TestCannotDoInAFixRoundCompletesTheMobiusCheckRunAsFailure(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\nwhen = \"Action: fix\"\n"+cannotDoCall+"\n"+commits, noChange)
	sha := approvalHead(t, server, fake)

	fake.AddCheckRun(shop, checkRun("sentinel", sha, "completed", "failure"))

	waitForLeadPrompt(t, server, " cannot_do on #41 \"Add plan model\" by the Implementer:")
	wantStopCheck(t, fake, sha, "Cannot do")
}

// completeMobiusCheck completes the Mobius check run as success, as the approval does.
func completeMobiusCheck(t *testing.T, fake *testkit.FakeGitHub) {
	t.Helper()
	for i, run := range fake.CheckRuns(shop) {
		if run.Name == "Mobius" {
			fake.SetCheckRunStatus(int64(i+1), "completed", "success")
			return
		}
	}
	t.Fatal("no Mobius check run")
}

// wantApprovedCheck tests that the head has one Mobius check run, and that it is still complete as success.
func wantApprovedCheck(t *testing.T, fake *testkit.FakeGitHub, sha string) {
	t.Helper()
	runs := mobiusCheckRuns(fake)
	if len(runs) != 1 || runs[0].HeadSHA != sha || runs[0].Status != "completed" || runs[0].Conclusion != "success" {
		t.Errorf("check runs = %+v", runs)
	}
}

func TestAStalePullRequestKeepsTheCompleteMobiusCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	sha := approvalHead(t, server, fake)
	completeMobiusCheck(t, fake)
	fake.SetCreatedAt(shop, 42, 0)
	fake.SetBehind(shop, 42)

	fake.CommitFile(shop, "price.txt", "dollars\n", "Add price")

	waitForLeadPrompt(t, server, " stale pull request #42 of #41 \"Add plan model\": it is behind its base branch")
	wantApprovedCheck(t, fake, sha)
}

func TestCannotDoInAFixRoundKeepsTheCompleteMobiusCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, "[[prompts]]\nwhen = \"Action: fix\"\n"+cannotDoCall+"\n"+commits, noChange)
	sha := approvalHead(t, server, fake)
	completeMobiusCheck(t, fake)

	fake.AddCheckRun(shop, checkRun("sentinel", sha, "completed", "failure"))

	waitForLeadPrompt(t, server, " cannot_do on #41 \"Add plan model\" by the Implementer:")
	wantApprovedCheck(t, fake, sha)
}

func TestTheWorkerRestartLimitKeepsTheCompleteMobiusCheckRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	sha := approvalHead(t, server, fake)
	completeMobiusCheck(t, fake)
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'working', worker = NULL, worker_restarts = 3 WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	restart, err := server.Engine.RestartWorker(t.Context(), liveTask(t, server, 41), "Add plan model", errors.New("the Worker died"))

	if err != nil || restart {
		t.Fatalf("restart = %v: %v", restart, err)
	}
	wantApprovedCheck(t, fake, sha)
}

func TestTheWorkerRestartLimitAddsNoMobiusCheckRunToAHeadThatHasNone(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	sha := approvalHead(t, server, fake)
	remote := fake.Remote(shop)
	newHead := testkit.Git(t, remote, "commit-tree", sha+"^{tree}", "-p", sha, "-m", "Add price")
	testkit.Git(t, remote, "update-ref", "refs/heads/mobius/41", newHead)
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'working', worker = NULL, worker_restarts = 3 WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	restart, err := server.Engine.RestartWorker(t.Context(), liveTask(t, server, 41), "Add plan model", errors.New("the Worker died"))

	if err != nil || restart {
		t.Fatalf("restart = %v: %v", restart, err)
	}
	for _, run := range mobiusCheckRuns(fake) {
		if run.HeadSHA == newHead {
			t.Errorf("check run on the new head = %+v", run)
		}
	}
}

func TestTheRoundLimitAddsAFailedMobiusCheckRunToAHeadWithAnApprovedOne(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, fixes, func(cfg *config.Config) { cfg.MaxFixRounds = 1 })
	sha := approvalHead(t, server, fake)
	completeMobiusCheck(t, fake)
	if _, err := server.DB.Exec("UPDATE tasks SET fix_rounds = 1 WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	runs := testkit.WaitForValue(t, func() ([]testkit.CheckRun, bool) {
		runs := mobiusCheckRuns(fake)
		return runs, len(runs) == 2
	})
	if runs[0].Conclusion != "success" || runs[1].Status != "completed" || runs[1].Conclusion != "failure" || runs[1].Output == nil || runs[1].Output.Title != "Round limit" {
		t.Errorf("check runs = %+v", runs)
	}
}
