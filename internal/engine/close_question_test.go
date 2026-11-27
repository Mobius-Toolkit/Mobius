package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func TestCloseQuestionPostsTheAnswerAndClosesTheQuestionOfAnAskedTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "answer of #41"
reply = ["Seen"]

[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(apiData[[]needsHuman](t, server, "/api/needs-human"), func(row needsHuman) bool { return row.Number == 41 && row.Question })
	})

	sendChat(t, server, leadChat, "Dollars were chosen")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["result"] == "Closed the question on #41."
	})
	want := []testkit.Comment{
		{Author: testkit.AppSlug + "[bot]", Body: marked("Cents or dollars?", leadMark)},
		{Author: testkit.AppSlug + "[bot]", Body: marked("The Owner answered in the Lead chat:\n\n> Dollars.", leadMark)},
	}
	if got := fake.Comments(shop, 41); !slices.Equal(got, want) {
		t.Errorf("comments = %v", got)
	}
	if slices.Contains(fake.Labels(shop, 41), "mobius:question") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	if items := inbox(t, server); len(items) != 0 {
		t.Errorf("inbox = %+v", items)
	}
	if rows := apiData[[]needsHuman](t, server, "/api/needs-human"); len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
	prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model"`)
	if got := strings.Count(prompt, closingAnswer); got != 1 {
		t.Errorf("answers in the dispatch event = %d: %q", got, prompt)
	}
	if strings.Contains(prompt, "by @:") {
		t.Errorf("dispatch event has no actor: %q", prompt)
	}
}

func TestCloseQuestionRunsAgainWhenTheTaskDoesNotContinue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "answer of #41"
reply = ["Seen"]

[[prompts]]
when = "Dollars again"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(apiData[[]needsHuman](t, server, "/api/needs-human"), func(row needsHuman) bool { return row.Number == 41 && row.Question })
	})
	// A poll that reads the new App comment of #41 also lists its comments, so one refused read can go to the poll.
	fake.RefuseCommentLists(shop, 41, http.StatusInternalServerError)

	sendChat(t, server, leadChat, "Dollars were chosen")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["error"] != nil
	})
	fake.RefuseCommentLists(shop, 41, 0)
	if !slices.Contains(fake.Labels(shop, 41), "mobius:question") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}

	sendChat(t, server, leadChat, "Dollars again")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 3 && calls[2]["result"] == "Closed the question on #41."
	})
	if slices.Contains(fake.Labels(shop, 41), "mobius:question") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	want := []testkit.Comment{
		{Author: testkit.AppSlug + "[bot]", Body: marked("Cents or dollars?", leadMark)},
		{Author: testkit.AppSlug + "[bot]", Body: marked("The Owner answered in the Lead chat:\n\n> Dollars.", leadMark)},
	}
	if got := fake.Comments(shop, 41); !slices.Equal(got, want) {
		t.Errorf("comments = %v", got)
	}
	prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model"`)
	if got := strings.Count(prompt, "The Owner answered in the Lead chat:"); got != 1 {
		t.Errorf("answers in the dispatch event = %d", got)
	}
}

func TestCloseQuestionRefusesAnIssueWithNoQuestionAndWritesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })

	sendChat(t, server, leadChat, "Dollars were chosen")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 1 && calls[0]["error"] == "#41 has no open question. An answer of the Owner on GitHub closes the question, so the Lead calls no close_question for it."
	})
	if got := fake.Comments(shop, 41); len(got) != 0 {
		t.Errorf("comments = %v", got)
	}
}

func TestCloseQuestionRefusesAQuestionThatTheOwnerAnsweredOnGitHub(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "answer of #41"
reply = ["Seen"]

[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(apiData[[]needsHuman](t, server, "/api/needs-human"), func(row needsHuman) bool { return row.Number == 41 && row.Question })
	})
	testkit.WaitFor(t, func() bool {
		return slices.Contains(fake.Labels(shop, 41), "mobius:question") && len(fake.Comments(shop, 41)) == 1
	})
	fake.AddComment(shop, 41, "owner", "Cents.")
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:question") })

	sendChat(t, server, leadChat, "Dollars were chosen")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["error"] == "#41 has no open question. An answer of the Owner on GitHub closes the question, so the Lead calls no close_question for it."
	})
	prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model" by @owner:`)
	if !strings.Contains(prompt, "> Cents.") || strings.Contains(prompt, "The Owner answered in the Lead chat:") {
		t.Errorf("dispatch event = %q", prompt)
	}
}

func TestCloseQuestionOfAnAskedTaskWithStartOffGivesTheChatAnswerOnceAtTheLaterDispatchAlsoWhenAPersonEditedItsComment(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "answer of #41"
reply = ["Seen"]

[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool {
		return slices.Contains(fake.Labels(shop, 41), "mobius:question") && len(fake.Comments(shop, 41)) == 1
	})
	turnStartOff(t, server)

	sendChat(t, server, leadChat, "Dollars were chosen")

	testkit.WaitFor(t, func() bool { return taskStateOf(t, server, 41) == "answered" })
	waitForSentChanges(t, server)
	ids := fake.CommentIDs(shop, 41)
	if !fake.EditComment(shop, ids[len(ids)-1], "Edited by a person.") {
		t.Fatalf("comment ids = %v", ids)
	}
	waitForPolls(t, fake)

	turnStartOn(t, server)

	prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model"`)
	if got := strings.Count(prompt, closingAnswer); got != 1 {
		t.Errorf("answers in the dispatch event = %d: %q", got, prompt)
	}
}

func TestApprovePullRequestClosesTheOpenQuestionOfTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	ask := "[[prompts]]\nwhen = \"Ask #41\"\ncall = { tool = \"ask\", arguments = { repository = \"owner/shop\", n = 41, text = \"Cents or dollars?\" } }\n\n"
	server, _ := connectTask(t, fake, ask+leadApproves+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	sendChat(t, server, leadChat, "Ask #41")
	waitForChat(t, server, leadChat, "Lead", "Asked on #41.")
	if !slices.Contains(fake.Labels(shop, 41), "mobius:question") {
		t.Fatalf("labels = %v", fake.Labels(shop, 41))
	}

	sendChat(t, server, leadChat, "Approve #41")

	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got it for review.")
	labels := fake.Labels(shop, 41)
	if !slices.Contains(labels, "mobius:review") || slices.Contains(labels, "mobius:question") {
		t.Errorf("labels = %v", labels)
	}
	if got, want := fake.Comments(shop, 41)[1:], []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: marked("The Lead approved PR #42, so the question is closed.", leadMark)}}; !slices.Equal(got, want) {
		t.Errorf("comments = %v", got)
	}
	items := inbox(t, server)
	if len(items) != 1 || items[0].Kind != "ready for review" {
		t.Errorf("inbox = %+v", items)
	}
	testkit.WaitFor(t, func() bool { return len(apiData[[]needsHuman](t, server, "/api/needs-human")) == 0 })
}

// askedWithNoPoll starts a server with a poll interval that is longer than the test, and lets the Lead ask a question
// on #41 after one manual poll. No later poll changes the copy of #41 or closes the question item.
func askedWithNoPoll(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	script := `
[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "answer of #41"
reply = ["Seen"]

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+script)
	server, _ := startOnePollWith(t, fake, "", dataDir)
	dispatchTask(fake, 41, "Add plan model")
	server.Engine.Poll(t.Context())
	return server
}

func TestCloseQuestionClosesAnOpenQuestionOfAnIssueWithNoQuestionLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := askedWithNoPoll(t, fake)
	waitForItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Labels(shop, 41), "mobius:question") })
	fake.RemoveLabel(shop, 41, "mobius:question", "owner")

	sendChat(t, server, leadChat, "Dollars were chosen")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["result"] == "Closed the question on #41."
	})
	if items := itemsOf(t, server, "question"); len(items) != 0 {
		t.Errorf("question items = %+v", items)
	}
}
