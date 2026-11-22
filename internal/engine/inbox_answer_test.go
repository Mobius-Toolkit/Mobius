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

// answerCommentIDs gives $.github.answer of each Inbox item that has one.
func answerCommentIDs(t *testing.T, server *testserver.Server) []int64 {
	t.Helper()
	rows, err := server.DB.Query(`SELECT json_extract(metadata, '$.github.answer') FROM inbox_items WHERE json_extract(metadata, '$.github.answer') IS NOT NULL ORDER BY id`)
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
