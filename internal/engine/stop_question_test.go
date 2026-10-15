package engine_test

import (
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	leadSeesAnswer     = "[[prompts]]\nwhen = \"Cents.\"\nreply = [\"Seen\"]\n\n"
	askCall            = "call = { tool = \"ask\", arguments = { n = 41, text = \"Cents or dollars?\" } }\n\n"
	leadAsksOnStop     = leadSeesAnswer + "[[prompts]]\nwhen = \" stop of #41\"\n" + askCall
	leadAsksInChat     = leadSeesAnswer + "[[prompts]]\nwhen = \" stop of #41\"\nreply = [\"Seen\"]\n\n[[prompts]]\nwhen = \"Ask about #41\"\n" + askCall
	leadAsksAfterStops = leadSeesAnswer + "[[prompts]]\nwhen = \"Ask about #41\"\n" + askCall + "[[prompts]]\nwhen = \" stop of #41\"\nreply = [\"Seen\"]\n\n"
)

// openKinds gives the kinds of the open Inbox items of issue #41.
func openKinds(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var kinds []string
	for _, item := range inbox(t, server) {
		if item.Issue == 41 {
			kinds = append(kinds, item.Kind)
		}
	}
	return kinds
}

func wantOpenKinds(t *testing.T, server *testserver.Server, want ...string) {
	t.Helper()
	if got := openKinds(t, server); !slices.Equal(got, want) {
		t.Errorf("open kinds = %v, want %v", got, want)
	}
}

func TestTheAnswerToAQuestionAboutAStoppedTaskContinuesTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadAsksOnStop+leadStarts, fixesNothing, noChange)
	sha := approvalHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))
	waitForItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	waitForPolls(t, fake)

	wantOpenKinds(t, server, "question")
	rounds := implementerRounds(t, server)

	fake.AddComment(shop, 41, "owner", "Cents.")

	waitForNoItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return implementerRounds(t, server) > rounds })
}

func TestAStopDuringAnOpenQuestionKeepsOnlyTheQuestionItemAndTheAnswerContinuesTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadAsksInChat+leadStarts, fixesNothing, noChange)
	sha := approvalHead(t, server, fake)
	sendChat(t, server, leadChat, "Ask about #41")
	waitForItem(t, server, "question")

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && hasLabel(fake, "mobius:needs-human") })
	waitForLeadPrompt(t, server, " stop of #41")
	waitForPolls(t, fake)
	wantOpenKinds(t, server, "question")
	rounds := implementerRounds(t, server)

	fake.AddComment(shop, 41, "owner", "Cents.")

	waitForNoItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return implementerRounds(t, server) > rounds })
}

func TestTheEndOfAQuestionAfterAResumeOpensNoStoppedItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	staleAsk := leadSeesAnswer + "[[prompts]]\nwhen = \"stale pull request #42\"\n" + askCall
	server, _ := connectTask(t, fake, staleAsk+leadStarts, mergesCents, keepSessionOpen)
	authorize(t, server)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	fake.SetCreatedAt(shop, pullRequestNumber, 0)
	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")
	waitForItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" && pullRequestHasNeedsHuman(fake) })
	wantOpenKinds(t, server, "question")

	resume(t, server)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" && !hasLabel(fake, "mobius:needs-human") })
	fake.AddComment(shop, 41, "owner", "Cents.")
	waitForNoItem(t, server, "question")
	waitForPolls(t, fake)
	wantOpenKinds(t, server)
}

func TestTheAnswerOfAQuestionAfterAFailedJudgeOfAStoppedTaskContinuesTheTaskAndOpensNoStoppedItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	first := leadAsksAfterStops + "[[prompts]]\nwhen = \"Judge again.\"\nerror = { code = -32000, message = \"The Judge crashed.\" }\n\n"
	server := judgeToHumanAfter(t, fake, first, "shell = \"true\"\n")
	waitForItem(t, server, "stopped")
	stops := func() (count int) {
		if err := server.DB.QueryRow("SELECT count(*) FROM lead_events WHERE kind = 'stop' AND issue = 41").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := stops()

	fake.AddComment(shop, 42, "owner", "Judge again.")

	testkit.WaitFor(t, func() bool { return stops() > before })
	waitForPolls(t, fake)
	sendChat(t, server, leadChat, "Ask about #41")
	waitForItem(t, server, "question")
	wantOpenKinds(t, server, "question")

	fake.AddComment(shop, 41, "owner", "Cents.")

	waitForNoItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return taskState(t, server) != "needs_human" && !hasLabel(fake, "mobius:needs-human") })
	waitForPolls(t, fake)
	wantOpenKinds(t, server)
}
