package engine_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const leadAsks = `
[[prompts]]
when = "answer of #41"
reply = ["Seen"]

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`

func itemsOf(t *testing.T, server *testserver.Server, kind string) []inboxItem {
	t.Helper()
	var found []inboxItem
	for _, item := range inbox(t, server) {
		if item.Kind == kind {
			found = append(found, item)
		}
	}
	return found
}

func waitForItem(t *testing.T, server *testserver.Server, kind string) inboxItem {
	t.Helper()
	return testkit.WaitForValue(t, func() (inboxItem, bool) {
		found := itemsOf(t, server, kind)
		if len(found) == 0 {
			return inboxItem{}, false
		}
		return found[0], true
	})
}

func waitForNoItem(t *testing.T, server *testserver.Server, kind string) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, kind)) == 0 })
}

func askedServer(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	server, _ := connect(t, fake, leadAsks)
	dispatchTask(fake, 41, "Add plan model")
	waitForItem(t, server, "question")
	return server
}

func TestAQuestionItemStaysOpenAfterThePollsThatFollowTheAsk(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := askedServer(t, fake)

	waitForPolls(t, fake)

	if got := itemsOf(t, server, "question"); len(got) != 1 {
		t.Errorf("question items = %+v", got)
	}
}

func TestAQuestionItemClosesAfterTheCommentOfATrustedUser(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := askedServer(t, fake)

	fake.AddComment(shop, 41, "owner", "Cents.")

	waitForNoItem(t, server, "question")
}

func TestAQuestionItemClosesAfterAPersonRemovesTheQuestionLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := askedServer(t, fake)

	fake.RemoveLabel(shop, 41, "mobius:question", "owner")

	waitForNoItem(t, server, "question")
}

func TestAQuestionItemClosesAfterTheCloseOfTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := askedServer(t, fake)

	fake.CloseIssue(shop, 41)

	waitForNoItem(t, server, "question")
}

func resume(t *testing.T, server *testserver.Server) {
	t.Helper()
	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/resume", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
}

func TestAStoppedItemOfAStalePullRequestClosesAfterResume(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := staleConflictInNeedsHuman(t, fake, commits)
	authorize(t, server)
	waitForItem(t, server, "stopped")

	resume(t, server)

	if got := itemsOf(t, server, "stopped"); len(got) != 0 {
		t.Errorf("stopped items = %+v", got)
	}
}

func TestAStoppedItemOfAStalePullRequestClosesAfterTheCloseOfTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := staleConflictInNeedsHuman(t, fake, commits)
	waitForItem(t, server, "stopped")
	waitForPolls(t, fake)
	if got := itemsOf(t, server, "stopped"); len(got) != 1 {
		t.Fatalf("stopped items = %+v", got)
	}

	fake.CloseIssue(shop, 41)

	waitForNoItem(t, server, "stopped")
}

func TestAStoppedItemOfAHungTaskStaysOpenAfterThePollsThatFollowTheStop(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, strings.Repeat(hang, 5), noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	item := waitForItem(t, server, "stopped")

	waitForPolls(t, fake)
	if got := itemsOf(t, server, "stopped"); len(got) != 1 || item.Reason != "The implementer session hung" || item.Title != "Add plan model" {
		t.Errorf("stopped items = %+v", got)
	}
}

func TestAStoppedItemOfAHungTaskClosesAfterResume(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadStarts, strings.Repeat(hang, 5), noChange)
	authorize(t, server)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForItem(t, server, "stopped")

	resume(t, server)

	if got := itemsOf(t, server, "stopped"); len(got) != 0 {
		t.Errorf("stopped items = %+v", got)
	}
}

func TestAStoppedItemOfAHungTaskClosesAfterTheCloseOfTheIssue(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, strings.Repeat(hang, 5), noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForItem(t, server, "stopped")

	fake.CloseIssue(shop, 41)

	waitForNoItem(t, server, "stopped")
}

func lostServer(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectWith(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n", func(*config.Config) {
		fake.AddIssue(shop, 41, "Add plan model")
		fake.AddSubIssue(shop, 12, 41)
		fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
	})
	authorize(t, server)
	item := waitForItem(t, server, "lost")
	if item.Reason != "Mobius lost the task" || item.Title != "Add plan model" {
		t.Errorf("lost item = %+v", item)
	}
	return server
}

func TestALostItemStaysOpenAfterThePollsThatFollowTheLoss(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := lostServer(t, fake)

	waitForPolls(t, fake)

	if got := itemsOf(t, server, "lost"); len(got) != 1 {
		t.Errorf("lost items = %+v", got)
	}
}

func TestALostItemClosesAfterStartAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := lostServer(t, fake)

	resume(t, server)

	if got := itemsOf(t, server, "lost"); len(got) != 0 {
		t.Errorf("lost items = %+v", got)
	}
	testkit.WaitFor(t, func() bool { return taskState(t, server) != "" })
}

func TestALostItemClosesWhenTheIssueGetsALiveTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := lostServer(t, fake)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForNoItem(t, server, "lost")
	if state := taskState(t, server); state == "" {
		t.Error("the issue has no live task")
	}
}

func TestALostItemClosesAfterTheCloseOfTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := lostServer(t, fake)

	fake.CloseIssue(shop, 41)

	waitForNoItem(t, server, "lost")
}

func readyForReviewServer(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	server, _ := connectTask(t, fake, leadApproves+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	waitForItem(t, server, "ready for review")
	return server
}

func TestAReadyForReviewItemStaysOpenAfterThePollsThatFollowTheApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := readyForReviewServer(t, fake)

	waitForPolls(t, fake)

	if got := itemsOf(t, server, "ready for review"); len(got) != 1 {
		t.Errorf("ready for review items = %+v", got)
	}
}

func TestAReadyForReviewItemClosesAfterTheMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := readyForReviewServer(t, fake)

	fake.MergePullRequest(shop, 42)

	waitForNoItem(t, server, "ready for review")
}

func TestAReadyForReviewItemClosesAfterTheCloseOfThePullRequestWithNoMerge(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := readyForReviewServer(t, fake)

	fake.CloseIssue(shop, 42)

	waitForNoItem(t, server, "ready for review")
	if state, _ := fake.State(shop, 41); state != "open" {
		t.Errorf("state of the issue = %s", state)
	}
}

func TestAReadyForReviewItemClosesAfterTheCloseOfTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := readyForReviewServer(t, fake)

	fake.CloseIssue(shop, 41)

	waitForNoItem(t, server, "ready for review")
}
