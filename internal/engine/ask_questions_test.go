package engine_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type inboxQuestion struct {
	Text     string        `json:"text"`
	Multiple bool          `json:"multiple"`
	Options  []inboxOption `json:"options"`
}

type inboxOption struct {
	Title   string `json:"title"`
	Details string `json:"details"`
}

// questionsOfItems gives the questions of each open Inbox item through the API, with a null list as nil.
func questionsOfItems(t *testing.T, server *testserver.Server) [][]inboxQuestion {
	t.Helper()
	status, text := send(t, server, http.MethodGet, "/api/inbox", "")
	var body struct {
		Data []struct {
			Questions []inboxQuestion `json:"questions"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil {
		t.Fatalf("inbox: status %d, %v: %s", status, err, text)
	}
	var questions [][]inboxQuestion
	for _, item := range body.Data {
		questions = append(questions, item.Questions)
	}
	return questions
}

func TestAskWithQuestionsStoresThemAndListsTheOptionsInTheComment(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Ask choices"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 12, text = "Two choices.", questions = [
  { text = "Which unit?", options = [{ title = "Cents", details = "Whole numbers." }, { title = "Dollars", details = "Decimals." }] },
  { text = "Which plans?", multiple = true, options = [{ title = "Free" }, { title = "Pro", details = "Paid." }, { title = "Team" }] },
] } }

[[prompts]]
reply = ["Seen"]
`)

	sendChat(t, server, leadChat, "Ask choices")

	item := waitForItem(t, server, "question")
	want := []inboxQuestion{
		{Text: "Which unit?", Options: []inboxOption{{Title: "Cents", Details: "Whole numbers."}, {Title: "Dollars", Details: "Decimals."}}},
		{Text: "Which plans?", Multiple: true, Options: []inboxOption{{Title: "Free"}, {Title: "Pro", Details: "Paid."}, {Title: "Team"}}},
	}
	got := questionsOfItems(t, server)
	if len(got) != 1 || !slices.EqualFunc(got[0], want, func(a, b inboxQuestion) bool {
		return a.Text == b.Text && a.Multiple == b.Multiple && slices.Equal(a.Options, b.Options)
	}) {
		t.Errorf("questions = %+v", got)
	}
	if item.Text != "Two choices." {
		t.Errorf("text = %q", item.Text)
	}
	body := "Two choices.\n\n" +
		"**1. Which unit?** Select one.\n\n" +
		"1. **Cents**: Whole numbers.\n" +
		"2. **Dollars**: Decimals.\n\n" +
		"**2. Which plans?** Select one or more.\n\n" +
		"1. **Free**\n" +
		"2. **Pro**: Paid.\n" +
		"3. **Team**"
	wantComments := []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: body}}
	if got := fake.Comments(shop, 12); !slices.Equal(got, wantComments) {
		t.Errorf("comments = %q", got)
	}
}

func TestAskWithNoQuestionsPostsTheTextAndStoresNoQuestions(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Ask plain"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 12, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)

	sendChat(t, server, leadChat, "Ask plain")

	waitForItem(t, server, "question")
	if got := questionsOfItems(t, server); len(got) != 1 || got[0] == nil || len(got[0]) != 0 {
		t.Errorf("questions = %#v", got)
	}
	want := []testkit.Comment{{Author: testkit.AppSlug + "[bot]", Body: "Cents or dollars?"}}
	if got := fake.Comments(shop, 12); !slices.Equal(got, want) {
		t.Errorf("comments = %v", got)
	}
	if !slices.Contains(fake.Labels(shop, 12), "mobius:question") {
		t.Errorf("labels = %v", fake.Labels(shop, 12))
	}
}

func TestAskRefusesAQuestionWithOneOption(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Ask one option"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 12, text = "Choose.", questions = [{ text = "Which?", options = [{ title = "Only" }] }] } }

[[prompts]]
reply = ["Seen"]
`)

	sendChat(t, server, leadChat, "Ask one option")

	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 1 && calls[0]["error"] != nil
	})
	if got := fake.Comments(shop, 12); len(got) != 0 {
		t.Errorf("comments = %v", got)
	}
	if items := inbox(t, server); len(items) != 0 {
		t.Errorf("inbox = %+v", items)
	}
}
