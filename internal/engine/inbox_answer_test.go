package engine_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func answer(t *testing.T, server *testserver.Server, id int64, text string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	status, response := send(t, server, http.MethodPost, "/api/inbox/"+itoa(id)+"/answer", string(body))
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, response)
	}
	var result struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		t.Fatal(err)
	}
	return result.Data.URL
}

// answerCommentIDs gives $.github.answers of each Inbox item, in the order of the items and of the answers.
func answerCommentIDs(t *testing.T, server *testserver.Server) []int64 {
	t.Helper()
	rows, err := server.DB.Query(`SELECT answer.value FROM inbox_items, json_each(inbox_items.metadata, '$.github.answers') AS answer ORDER BY inbox_items.id, answer.key`)
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

func TestAnAnswerFromTheInboxRemovesTheQuestionLabelAndGivesTheLeadTheCommentEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	url := answer(t, server, item.ID, "Cents.")

	if !strings.HasPrefix(url, "https://github.com/owner/shop/issues/41#issuecomment-") {
		t.Errorf("url = %s", url)
	}
	if got := fake.Comments(shop, 41); !slices.Contains(got, testkit.Comment{Author: "owner", Body: "Cents."}) {
		t.Errorf("comments = %v", got)
	}
	prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model" by @owner:`)
	if !strings.Contains(prompt, ` reply on #41 "Add plan model" by @owner:`+"\n\n> Cents.") {
		t.Errorf("prompt = %s", prompt)
	}
	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:question") })
	waitForNoItem(t, server, "question")
	ids := fake.CommentIDs(shop, 41)
	if got := answerCommentIDs(t, server); len(ids) != 2 || !slices.Equal(got, []int64{ids[1]}) {
		t.Errorf("answer ids = %v, comment ids = %v", got, ids)
	}
}

const answerEvents = "SELECT count(*) FROM lead_events WHERE kind = 'dispatch' AND payload LIKE '%answer of #41%'"

func TestAnAnswerFromTheInboxClosesTheQuestionItemAndGivesTheLeadOneEventInTheRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	answer(t, server, item.ID, "Cents.")

	assertDismissed(t, server, item.ID)
	if got := countRows(t, server, answerEvents+" AND payload LIKE '%> Cents.%'"); got != 1 {
		t.Errorf("Lead events with the answer = %d", got)
	}
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if got := countRows(t, server, answerEvents); got != 1 {
		t.Errorf("Lead events with the answer after the polls = %d", got)
	}
}

func TestADispatchedTaskThatWaitsForTheAnswerFromTheInboxContinuesOneTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	answer(t, server, item.ID, "Cents.")

	if state := taskState(t, server); state != "dispatched" {
		t.Errorf("state = %s", state)
	}
	waitForLeadPrompt(t, server, " answer of #41 ")
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if got := countRows(t, server, answerEvents); got != 1 {
		t.Errorf("Lead events with the answer = %d", got)
	}
	prompts := 0
	for _, prompt := range leadPrompts(t, server) {
		prompts += strings.Count(prompt, " answer of #41 ")
	}
	if prompts != 1 {
		t.Errorf("prompts with the answer = %d", prompts)
	}
}

func TestASecondAnswerFromTheInboxToTheSameItemIsRefusedWithNoCommentAndNoLeadEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")
	answer(t, server, item.ID, "Cents.")
	comments := len(fake.Comments(shop, 41))

	status, response := send(t, server, http.MethodPost, "/api/inbox/"+itoa(item.ID)+"/answer", `{"text": "Dollars."}`)

	if status != http.StatusConflict || !strings.Contains(response, "already answered or closed") {
		t.Errorf("status = %d: %s", status, response)
	}
	if got := len(fake.Comments(shop, 41)); got != comments {
		t.Errorf("comments = %d, want %d", got, comments)
	}
	if got := countRows(t, server, answerEvents); got != 1 {
		t.Errorf("Lead events with the answer = %d", got)
	}
	if got := answerCommentIDs(t, server); len(got) != 1 {
		t.Errorf("answer ids = %v", got)
	}
}

func TestACommentOfTheLeadWithGhOnTheIssueOfAQuestionIsNoAnswerAndAnAnswerFromTheInboxIs(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	fake.AddAppComment(shop, 41, "owner", "Cents.")
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	if !slices.Contains(fake.Labels(shop, 41), "mobius:question") || taskState(t, server) != "asked" {
		t.Errorf("labels = %v, state = %s", fake.Labels(shop, 41), taskState(t, server))
	}

	answer(t, server, item.ID, "Dollars.")

	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 41), "mobius:question") })
}

func TestAnAnswerFromTheInboxOnTheIssueOfAStoppedTaskContinuesTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := stoppedOnFailedCI(t, fake, noChange)
	authorize(t, server)
	item := waitForItem(t, server, "stopped")
	before := implementers(t, server)

	answer(t, server, item.ID, "Continue now.")

	testkit.WaitFor(t, func() bool { return implementers(t, server) == before+1 })
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool { return strings.Contains(prompt, "> Continue now.") })
	})
}

func TestAnAnswerFromTheInboxThatFailsAfterTheCloseLeavesTheCommentToThePoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")
	fake.FailReactions("eyes", true)

	body := `{"text": "Cents."}`
	if status, _ := send(t, server, http.MethodPost, "/api/inbox/"+itoa(item.ID)+"/answer", body); status == http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	fake.FailReactions("eyes", false)

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if got := countRows(t, server, answerEvents); got != 1 {
		t.Errorf("Lead events with the answer = %d", got)
	}
}
