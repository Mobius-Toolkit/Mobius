package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const holdAfterWorkstreamQuestionScript = `
[[prompts]]
when = "dispatch of #42"
reply = ["Seen"]

[[prompts]]
call = { tool = "ask", arguments = { n = 12, text = "Cents or dollars?" } }

[[prompts]]
` + holdEvent + `

[[prompts]]
reply = ["Seen"]

[[prompts]]
reply = ["Seen"]
`

func holdComments(fake *testkit.FakeGitHub, number int64) []testkit.Comment {
	var found []testkit.Comment
	for _, comment := range fake.Comments(shop, number) {
		if strings.Contains(comment.Body, "This task waits for") {
			found = append(found, comment)
		}
	}
	return found
}

func TestAHeldEventTellsOnTheTaskIssueThatTheTaskWaitsForTheAnswerOnTheWorkstreamIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, holdAfterWorkstreamQuestionScript, keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	waitForItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	addLeadEvent(t, server, "follow_up", "Held follow-up.")
	dispatchTask(fake, 42, "Add plan route")

	testkit.WaitFor(t, func() bool { return len(holdComments(fake, 41)) > 0 })
	waitForPolls(t, fake)

	comments := holdComments(fake, 41)
	body := ""
	if len(comments) == 1 {
		body = comments[0].Body
	}
	if body == "" || !strings.Contains(body, "question on #12: https://github.com/owner/shop/issues/12") || !strings.Contains(body, "after the answer") || strings.Contains(body, "\n\nNext step: ") {
		t.Errorf("comments = %+v", comments)
	}
	if others := holdComments(fake, 42); len(others) != 0 {
		t.Errorf("comments of #42 = %+v", others)
	}
}

func TestAHeldEventAfterAQuestionOnTheTaskIssueAddsNoComment(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	heldAfterQuestion(t, fake)
	waitForPolls(t, fake)

	if comments := holdComments(fake, 41); len(comments) != 0 {
		t.Errorf("comments = %+v", comments)
	}
}

func TestATaskInApprovalWaitsForTheOwnerWhileItHasAHeldEventAndForTheLeadAfterTheAnswer(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, holdAfterWorkstreamQuestionScript, keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	waitForItem(t, server, "question")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	addLeadEvent(t, server, "follow_up", "Held follow-up.")
	dispatchTask(fake, 42, "Add plan route")
	testkit.WaitFor(t, func() bool { return held(t, server, 1) })
	fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
	if _, err := server.DB.Exec(`UPDATE tasks SET state = 'approval', state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE issue = 41`); err != nil {
		t.Fatal(err)
	}

	if state := stateOfTask(t, server, 41); state != "waits for Owner" {
		t.Errorf("state = %s", state)
	}

	fake.AddComment(shop, 12, "owner", "Cents.")

	testkit.WaitFor(t, func() bool { return stateOfTask(t, server, 41) == "waits for Lead" })
}

func stateOfTask(t *testing.T, server *testserver.Server, number int64) string {
	t.Helper()
	for _, line := range taskTab(t, server) {
		if line.Number == number {
			return line.State
		}
	}
	return ""
}
