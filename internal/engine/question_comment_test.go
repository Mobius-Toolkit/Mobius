package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const leadAsksOnTheWorkstream = `
[[prompts]]
when = "Ask two"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 12, text = "Euros or dollars?" } }

[[prompts]]
when = "Ask one"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 12, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`

// questionIDs gives $.github.question of each question Inbox item, the oldest first, with 0 for an item with no id.
func questionIDs(t *testing.T, server *testserver.Server) []int64 {
	t.Helper()
	rows, err := server.DB.Query(`SELECT COALESCE(json_extract(metadata, '$.github.question'), 0) FROM inbox_items WHERE kind = 'question' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestTheSendOfTheQuestionStoresTheIdOfTheCommentInTheInboxItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, leadAsksOnTheWorkstream)

	sendChat(t, server, leadChat, "Ask one")

	ids := testkit.WaitForValue(t, func() ([]int64, bool) {
		ids := questionIDs(t, server)
		return ids, len(ids) == 1 && ids[0] != 0
	})
	if want := fake.CommentIDs(shop, 12); len(want) != 1 || ids[0] != want[0] {
		t.Errorf("question ids = %v, comment ids = %v", ids, want)
	}
}

func TestTwoQuestionsBeforeTheFirstSendEachGetTheIdOfTheirOwnComment(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, leadAsksOnTheWorkstream)
	fake.FailAddComment(shop, 12, true)

	sendChat(t, server, leadChat, "Ask one")
	testkit.WaitFor(t, func() bool { return len(questionIDs(t, server)) == 1 })
	sendChat(t, server, leadChat, "Ask two")
	testkit.WaitFor(t, func() bool { return len(questionIDs(t, server)) == 2 })
	fake.FailAddComment(shop, 12, false)

	ids := testkit.WaitForValue(t, func() ([]int64, bool) {
		ids := questionIDs(t, server)
		return ids, len(ids) == 2 && ids[0] != 0 && ids[1] != 0
	})
	comments := fake.Comments(shop, 12)
	want := fake.CommentIDs(shop, 12)
	if len(comments) != 2 || !strings.Contains(comments[0].Body, "Cents or dollars?") || !strings.Contains(comments[1].Body, "Euros or dollars?") {
		t.Fatalf("comments = %+v", comments)
	}
	if ids[0] != want[0] || ids[1] != want[1] {
		t.Errorf("question ids = %v, comment ids = %v", ids, want)
	}
}

func TestAReplyAfterTheGitHubTimeOfTheQuestionButBeforeTheInboxTimeAnswersTheQuestion(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, leadAsksOnTheWorkstream)
	sendChat(t, server, leadChat, "Ask one")
	ids := testkit.WaitForValue(t, func() ([]int64, bool) {
		ids := questionIDs(t, server)
		return ids, len(ids) == 1 && ids[0] != 0
	})
	waitForPolls(t, fake)
	var asked string
	if err := server.DB.QueryRow(`SELECT time FROM inbox_items WHERE kind = 'question'`).Scan(&asked); err != nil {
		t.Fatal(err)
	}
	inboxTime, err := time.Parse(time.RFC3339Nano, asked)
	if err != nil {
		t.Fatal(err)
	}
	fake.SetCommentCreatedAt(shop, ids[0], inboxTime.Add(-20*time.Second))

	fake.AddCommentCreatedAt(shop, 12, "owner", "Cents.", inboxTime.Add(-10*time.Second))

	waitForNoItem(t, server, "question")
}
