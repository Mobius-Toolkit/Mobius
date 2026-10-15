package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
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
call = { tool = "close_question", arguments = { n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { n = 41, text = "Cents or dollars?" } }

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
		{Author: testkit.AppSlug + "[bot]", Body: "Cents or dollars?"},
		{Author: testkit.AppSlug + "[bot]", Body: "The Owner answered in the Lead chat:\n\n> Dollars."},
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
	if !strings.Contains(prompt, "The Owner answered in the Lead chat:\n\n> Dollars.") {
		t.Errorf("dispatch event = %q", prompt)
	}
}

func TestCloseQuestionRefusesAnIssueWithNoQuestionAndWritesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })

	sendChat(t, server, leadChat, "Dollars were chosen")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 1 && calls[0]["error"] == "#41 has no open question."
	})
	if got := fake.Comments(shop, 41); len(got) != 0 {
		t.Errorf("comments = %v", got)
	}
}

func TestApprovePullRequestClosesTheOpenQuestionOfTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	ask := "[[prompts]]\nwhen = \"Ask #41\"\ncall = { tool = \"ask\", arguments = { n = 41, text = \"Cents or dollars?\" } }\n\n"
	server, _ := connectTask(t, fake, ask+leadApproves+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
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
	if got, want := fake.Comments(shop, 41)[1:], []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: "The Lead approved PR #42, so the question is closed."}}; !slices.Equal(got, want) {
		t.Errorf("comments = %v", got)
	}
	items := inbox(t, server)
	if len(items) != 1 || items[0].Kind != "ready for review" {
		t.Errorf("inbox = %+v", items)
	}
	if rows := apiData[[]needsHuman](t, server, "/api/needs-human"); len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
}
