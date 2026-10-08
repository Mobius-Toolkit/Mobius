package engine_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// approved dispatches #41, lets the Lead approve its pull request, and gives the server. The Lead plays lead, and the
// Implementer plays implementer.
func approved(t *testing.T, fake *testkit.FakeGitHub, lead, implementer string, adjust func(*config.Config)) *testserver.Server {
	t.Helper()
	server, _ := connectTask(t, fake, lead+leadApproves+leadStarts, implementer+commits, adjust)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got it for review.")
	return server
}

func inReview(fake *testkit.FakeGitHub) bool {
	return hasLabel(fake, "mobius:review") && !hasLabel(fake, "mobius:working")
}

func TestApprovePullRequestReplacesTheWorkingLabelWithTheReviewLabel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)

	server := approved(t, fake, "", "", noChange)

	if labels := fake.Labels(shop, 41); !reflect.DeepEqual(labels, []string{"mobius:review"}) {
		t.Errorf("labels = %q", labels)
	}
	testkit.WaitFor(t, func() bool {
		lines := taskTab(t, server)
		return len(lines) == 1 && lines[0].State == "review"
	})
}

func TestATaskInReadyForReviewWithNoWorkingLabelStaysInReadyForReview(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := approved(t, fake, "", "", noChange)

	waitForPolls(t, fake)

	if state := taskState(t, server); state != "ready_for_review" {
		t.Errorf("state = %s", state)
	}
	if !inReview(fake) {
		t.Errorf("labels = %q", fake.Labels(shop, 41))
	}
}

func TestARemovalOfTheReviewLabelStopsATaskInReadyForReview(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := approved(t, fake, "", "", noChange)

	fake.RemoveLabel(shop, 41, "mobius:review", "mallory")

	testkit.WaitFor(t, func() bool {
		feed := activities(t, server)
		return feed[len(feed)-1].Text == "Stopped \"Add plan model\" after a removal of mobius:review"
	})
	if state := taskState(t, server); state != "stopped" {
		t.Errorf("state = %s", state)
	}
	noTaskLabels(t, fake)
}

func TestAJudgeThatRunsFromReadyForReviewNeedsTheReviewLabel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, "[[prompts]]\nwhen = \"You are the Judge\"\nhang = true\n\n"+noFinding+leadApproves+leadStarts, commits, func(cfg *config.Config) {
		cfg.ReviewQuietPeriod = 200 * time.Millisecond
	})
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got it for review.")
	fake.AddComment(shop, 42, "owner", "Why cents?")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "working" })

	waitForPolls(t, fake)

	if state := taskState(t, server); state != "working" || !inReview(fake) {
		t.Errorf("state = %s, labels = %q", state, fake.Labels(shop, 41))
	}

	fake.RemoveLabel(shop, 41, "mobius:review", "mallory")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "stopped" })
	testkit.WaitFor(t, func() bool {
		judges := roleSessions(t, server, engine.JudgeRole)
		return len(judges) == 1 && judges[0].EndReason.String == "stopped"
	})
}

func TestAFixRoundOfTheJudgeFromReadyForReviewPutsTheWorkingLabelBack(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, "shell = \"true\"\n", fixesEach, noChange)
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool { return inReview(fake) })

	fake.AddComment(shop, 42, "owner", "Why cents?")

	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") && !hasLabel(fake, "mobius:review") })
	testkit.WaitFor(t, func() bool { return implementers(t, server) == 2 })
}

func TestAFixRoundForAFailedCheckRunFromReadyForReviewPutsTheWorkingLabelBack(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := approved(t, fake, "", fixes, noChange)
	sha := head(t, fake, "mobius/41")

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "failure"))

	roundPrompt(t, server)
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") && !hasLabel(fake, "mobius:review") })
}

func TestAFixRoundOfTheLeadFromReadyForReviewPutsTheWorkingLabelBack(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := approved(t, fake, leadFindings, "", noChange)

	sendChat(t, server, leadChat, "Send the findings to #41")

	waitForChat(t, server, leadChat, "Lead", "Sent the findings to a fix round of #41. At max_fix_rounds, Mobius stops the task instead.")
	testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:working") && !hasLabel(fake, "mobius:review") })
}

func TestAConflictRoundFromReadyForReviewPutsTheWorkingLabelBack(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	// A new Lead session has the chat history in its first prompt, and the history has "Approve #41". Thus the session
	// stays open, or the Lead approves the pull request again at the second event.
	server := approved(t, fake, "", mergesCents, keepSessionOpen)

	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")

	waitForReadyEvents(t, server, 2)
	if !hasLabel(fake, "mobius:working") || hasLabel(fake, "mobius:review") {
		t.Errorf("labels = %q", fake.Labels(shop, 41))
	}
}

func TestAMergeRemovesTheReviewLabel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := approved(t, fake, "", "", noChange)

	fake.MergePullRequest(shop, 42)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	noTaskLabels(t, fake)
}

func TestAHandOverToAHumanFromReadyForReviewRemovesTheReviewLabel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := approved(t, fake, leadFindings, "", func(cfg *config.Config) { cfg.MaxFixRounds = 1 })
	if _, err := server.DB.Exec("UPDATE tasks SET fix_rounds = 1 WHERE issue = 41"); err != nil {
		t.Fatal(err)
	}

	sendChat(t, server, leadChat, "Send the findings to #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	testkit.WaitFor(t, func() bool {
		return hasLabel(fake, "mobius:needs-human") && !hasLabel(fake, "mobius:review") && !hasLabel(fake, "mobius:working")
	})
}

func TestAStalePullRequestFromReadyForReviewGoesToAHumanAndLosesTheReviewLabel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := approved(t, fake, "", "", noChange)
	fake.SetCreatedAt(shop, 42, 0)

	fake.CommitFile(shop, "plan.txt", "dollars\n", "Use dollars")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "needs_human" })
	testkit.WaitFor(t, func() bool {
		return hasLabel(fake, "mobius:needs-human") && !hasLabel(fake, "mobius:review") && !hasLabel(fake, "mobius:working")
	})
}

func TestAStartWithAnEmptyStoreHandsAnIssueWithTheReviewLabelToAHuman(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)

	server, _ := connectWith(t, fake, "", func(*config.Config) {
		fake.AddIssue(shop, 41, "Add plan model")
		fake.AddSubIssue(shop, 12, 41)
		fake.AddLabel(shop, 41, "mobius:review", testkit.AppSlug+"[bot]")
	})

	var kind, text string
	if err := server.DB.QueryRow("SELECT kind, text FROM inbox_items").Scan(&kind, &text); err != nil {
		t.Fatal(err)
	}
	if kind != "stopped" || !strings.HasPrefix(text, "Mobius lost the state of this task.") {
		t.Errorf("item = %s, %s", kind, text)
	}
	if labels := fake.Labels(shop, 41); !reflect.DeepEqual(labels, []string{"mobius:needs-human"}) {
		t.Errorf("labels = %q", labels)
	}
}
