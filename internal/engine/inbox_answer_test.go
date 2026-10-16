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
