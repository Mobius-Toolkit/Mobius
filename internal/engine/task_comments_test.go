package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// commentRows gives the number of outgoing comment changes of the issue number of owner/shop with text, whatever their
// status.
func commentRows(t *testing.T, server *testserver.Server, number int64, text string) int {
	t.Helper()
	var count int
	err := server.DB.QueryRow("SELECT count(*) FROM outgoing_changes WHERE repository = ? AND number = ? AND field = 'comment' AND text = ?", shop, number, text).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

const (
	askScript = `
[[prompts]]
when = "answer of #41"
reply = ["Seen"]

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`
	askedText     = "Cents or dollars?"
	closingAnswer = "The Owner answered in the Lead chat:\n\n> Dollars."
)

func TestTheCloseOfAWorkstreamAddsOneClosedRowForEachOpenTaskIssueAndAPollThatRunsItAgainAddsNone(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 43, "Add plan price")
	fake.AddSubIssue(shop, 12, 43)
	server := startCopied(t, fake)
	fake.FailClose(shop, 41)

	fake.CloseIssue(shop, 12)

	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 41), closedComment) })
	testkit.WaitFor(t, func() bool {
		refused := 0
		for _, request := range fake.Requests() {
			if request.Method == "PATCH" && strings.HasSuffix(request.Path, "/issues/41") && request.Status == http.StatusInternalServerError {
				refused++
			}
		}
		return refused >= 3
	})
	fake.AllowClose(shop, 41)
	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 41); return state == "closed" })
	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 43); return state == "closed" })
	waitForPolls(t, fake)
	for _, number := range []int64{41, 43} {
		if got := commentRows(t, server, number, closed); got != 1 {
			t.Errorf("rows of #%d = %d", number, got)
		}
		if got := fake.Comments(shop, number); !slices.Equal(got, []testkit.Comment{closedComment}) {
			t.Errorf("comments of #%d = %v", number, got)
		}
	}
}

func TestAnAskCommentThatGitHubRefusesAtOnceIsPostedOnceByALaterPollAndAnAnswerAfterItReachesTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, askScript)
	fake.FailAddComment(shop, 41, true)

	dispatchTask(fake, 41, "Add plan model")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) > 0 && calls[0]["result"] == "Asked on #41."
	})
	if got := appComments(fake, 41); len(got) != 0 {
		t.Fatalf("comments = %q", got)
	}
	if got := commentWrites(t, server); got != 1 {
		t.Fatalf("pending outgoing changes = %d", got)
	}
	fake.FailAddComment(shop, 41, false)

	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 1 })
	waitForPolls(t, fake)
	if got := fake.Comments(shop, 41); !slices.Equal(got, []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: marked(askedText, leadMark)}}) {
		t.Errorf("comments = %v", got)
	}
	if !slices.Contains(fake.Labels(shop, 41), "mobius:question") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}

	fake.AddComment(shop, 41, "owner", "Cents.")

	prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model" by @owner:`)
	if !strings.Contains(prompt, "> Cents.") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestACloseQuestionCommentThatGitHubRefusesAtOnceIsInTheDispatchEventAndPostedOnceAfterARunAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Dollars again"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }
`+askScript)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 1 })
	fake.FailAddComment(shop, 41, true)
	fake.FailAddLabels(shop, 41, true)

	sendChat(t, server, leadChat, "Dollars were chosen")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["error"] != nil
	})
	text := marked(closingAnswer, leadMark)
	if got := commentRows(t, server, 41, text); got != 1 {
		t.Fatalf("rows after the first call = %d", got)
	}
	fake.FailAddLabels(shop, 41, false)

	sendChat(t, server, leadChat, "Dollars again")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 3 && calls[2]["result"] == "Closed the question on #41."
	})
	prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model"`)
	if got := strings.Count(prompt, closingAnswer); got != 1 {
		t.Errorf("answers in the dispatch event = %d", got)
	}
	if got := appComments(fake, 41); len(got) != 1 {
		t.Fatalf("comments = %q", got)
	}
	fake.FailAddComment(shop, 41, false)

	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 2 })
	waitForPolls(t, fake)
	if got := commentRows(t, server, 41, text); got != 1 {
		t.Errorf("rows = %d", got)
	}
	want := []testkit.Comment{
		{Author: testkit.AppSlug + "[bot]", Body: marked(askedText, leadMark)},
		{Author: testkit.AppSlug + "[bot]", Body: text},
	}
	if got := fake.Comments(shop, 41); !slices.Equal(got, want) {
		t.Errorf("comments = %v", got)
	}
}

func TestACommentOfAPersonBeforeAnAskIsNoAnswerWhileTheQuestionCommentWaitsForItsSend(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, askScript)
	fake.FailAddComment(shop, 41, true)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) > 0 && calls[0]["result"] == "Asked on #41."
	})

	fake.AddCommentCreatedAt(shop, 41, "owner", "Maybe dollars.", time.Now().Add(-time.Minute))

	waitForPolls(t, fake)
	if !slices.Contains(fake.Labels(shop, 41), "mobius:question") || taskStateOf(t, server, 41) != "asked" {
		t.Errorf("labels = %v, state = %s", fake.Labels(shop, 41), taskStateOf(t, server, 41))
	}
	fake.FailAddComment(shop, 41, false)
	testkit.WaitFor(t, func() bool { return outgoingChanges(t, server, "sent") == 1 })
	waitForPolls(t, fake)
	if !slices.Contains(fake.Labels(shop, 41), "mobius:question") || taskStateOf(t, server, 41) != "asked" {
		t.Errorf("labels = %v, state = %s", fake.Labels(shop, 41), taskStateOf(t, server, 41))
	}

	fake.AddComment(shop, 41, "owner", "Cents.")

	prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model" by @owner:`)
	_, event, _ := strings.Cut(prompt, " answer of #41")
	if !strings.Contains(event, "> Cents.") || strings.Contains(event, "Maybe dollars.") {
		t.Errorf("dispatch event = %s", event)
	}
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:question") })
}
