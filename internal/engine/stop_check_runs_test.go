package engine_test

import (
	"errors"
	"testing"

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
