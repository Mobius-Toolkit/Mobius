package engine_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type coordinatorItem struct {
	ID           int64           `json:"id"`
	Kind         string          `json:"kind"`
	Organization string          `json:"organization"`
	Repository   string          `json:"repository"`
	Issue        int64           `json:"issue"`
	Title        string          `json:"title"`
	Reason       string          `json:"reason"`
	Text         string          `json:"text"`
	Questions    []inboxQuestion `json:"questions"`
}

func coordinatorItems(t *testing.T, server *testserver.Server) []coordinatorItem {
	t.Helper()
	var body struct {
		Data []coordinatorItem `json:"data"`
	}
	status, text := send(t, server, http.MethodGet, "/api/inbox", "")
	if status != http.StatusOK {
		t.Fatalf("inbox: status %d: %s", status, text)
	}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func TestTheOwnerAnswersAQuestionOfTheCoordinatorAndTheCoordinatorGetsTheAnswerAsAChatMessage(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Cents and Pro, please."
reply = ["Got the answer"]

[[prompts]]
when = "Ask the Owner"
call = { tool = "ask", arguments = { text = "Two choices.", questions = [
  { text = "Which unit?", options = [{ title = "Cents", details = "Whole numbers." }, { title = "Dollars" }] },
  { text = "Which plans?", multiple = true, options = [{ title = "Free" }, { title = "Pro" }] },
] } }

[[prompts]]
reply = ["Asked"]
`)

	sendChat(t, server, coordinatorChat, "Ask the Owner")

	testkit.WaitFor(t, func() bool { return len(coordinatorItems(t, server)) == 1 })
	item := coordinatorItems(t, server)[0]
	want := []inboxQuestion{
		{Text: "Which unit?", Options: []inboxOption{{Title: "Cents", Details: "Whole numbers."}, {Title: "Dollars"}}},
		{Text: "Which plans?", Multiple: true, Options: []inboxOption{{Title: "Free"}, {Title: "Pro"}}},
	}
	if item.Kind != "question" || item.Organization != "owner" || item.Repository != "" || item.Issue != 0 ||
		item.Title != "Which unit?" || item.Reason != "The Coordinator asks a question" || item.Text != "Two choices." ||
		!slices.EqualFunc(item.Questions, want, func(a, b inboxQuestion) bool {
			return a.Text == b.Text && a.Multiple == b.Multiple && slices.Equal(a.Options, b.Options)
		}) {
		t.Errorf("item = %+v", item)
	}

	if url := answer(t, server, item.ID, "Cents and Pro, please."); url != "" {
		t.Errorf("url = %q", url)
	}

	waitForChat(t, server, coordinatorChat, "Owner", "Cents and Pro, please.")
	waitForChat(t, server, coordinatorChat, "Coordinator", "Got the answer")
	if items := coordinatorItems(t, server); len(items) != 0 {
		t.Errorf("inbox = %+v", items)
	}
}

func TestTheResultOfAskNamesTheTrayRowAndTheIDOfTheQuestion(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Ask the Owner"
call = { tool = "ask", arguments = { text = "Which unit?", questions = [{ text = "Which unit?", options = [{ title = "Cents" }, { title = "Dollars" }] }] } }

[[prompts]]
reply = ["Asked"]
`)
	session := run(t, server, coordinatorSpec(t), "Ask the Owner")
	items := coordinatorItems(t, server)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	want := fmt.Sprintf(`The question waits for the Owner. Its tray row is "%s". In the chat, name the question by these words. Its ID is %d, only for withdraw. The answer arrives later as a chat message.`, items[0].Title, items[0].ID)
	calls := mcpCalls(t, server, session)
	if len(calls) != 1 || calls[0]["result"] != want {
		t.Errorf("calls = %v", calls)
	}
}

func TestASecondAskOfTheCoordinatorReplacesTheOpenQuestion(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Ask second"
call = { tool = "ask", arguments = { text = "Plain question.", questions = [{ text = "Second?", options = [{ title = "Yes" }, { title = "No" }] }] } }

[[prompts]]
when = "Ask first"
call = { tool = "ask", arguments = { text = "First line.\nMore text." } }

[[prompts]]
reply = ["Asked"]
`)

	sendChat(t, server, coordinatorChat, "Ask first")
	first := testkit.WaitForValue(t, func() (coordinatorItem, bool) {
		items := coordinatorItems(t, server)
		if len(items) != 1 {
			return coordinatorItem{}, false
		}
		return items[0], true
	})
	if first.Title != "First line." {
		t.Errorf("title = %q", first.Title)
	}

	sendChat(t, server, coordinatorChat, "Ask second")

	testkit.WaitFor(t, func() bool {
		items := coordinatorItems(t, server)
		return len(items) == 1 && items[0].Title == "Second?"
	})
	if items := coordinatorItems(t, server); items[0].ID == first.ID {
		t.Errorf("inbox = %+v", items)
	}
}
