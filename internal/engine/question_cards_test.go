package engine_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type questionItem struct {
	ID          int64   `json:"id"`
	Answer      string  `json:"answer"`
	Withdrawn   bool    `json:"withdrawn"`
	DismissedAt *string `json:"dismissedAt"`
}

type questionCard struct {
	Text     string        `json:"text"`
	Question *questionItem `json:"question"`
}

// questionCards gives the messages of the author Question in the chat of key, from the chat API.
func questionCards(t *testing.T, server *testserver.Server, key engine.ChatKey) []questionCard {
	t.Helper()
	query := url.Values{"organization": {key.Organization}, "repository": {key.Repository}, "workstream": {strconv.FormatInt(key.Workstream, 10)}}
	status, text := send(t, server, http.MethodGet, "/api/chat?"+query.Encode(), "")
	var body struct {
		Data struct {
			Messages []struct {
				Author string `json:"author"`
				questionCard
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil {
		t.Fatalf("chat: status %d, %v: %s", status, err, text)
	}
	var cards []questionCard
	for _, message := range body.Data.Messages {
		if message.Author == "Question" {
			cards = append(cards, message.questionCard)
		}
	}
	return cards
}

// waitForCard waits until the chat has the one card of a question that ended, when ended is true, or that is open.
func waitForCard(t *testing.T, server *testserver.Server, key engine.ChatKey, ended bool) questionCard {
	t.Helper()
	return testkit.WaitForValue(t, func() (questionCard, bool) {
		cards := questionCards(t, server, key)
		if len(cards) != 1 || cards[0].Question == nil || (cards[0].Question.DismissedAt != nil) != ended {
			return questionCard{}, false
		}
		return cards[0], true
	})
}

func TestALeadAskAddsOneQuestionMessageToTheLeadChatAndTheItemLinksIt(t *testing.T) {
	t.Parallel()
	server := askedServer(t, testkit.NewFakeGitHub(t))

	card := waitForCard(t, server, leadChat, false)

	item := itemsOf(t, server, "question")[0]
	if card.Text != "Cents or dollars?" || card.Question.ID != item.ID || card.Question.Answer != "" || card.Question.Withdrawn {
		t.Errorf("card = %+v, question = %+v", card, card.Question)
	}
}

func TestALeadAskOnTheIssueOfAnotherRepositoryAddsTheMessageToTheChatOfTheWorkstreamRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"dispatch of owner/garden#7\"\ncall = { tool = \"ask\", arguments = { repository = \"owner/garden\", n = 7, text = \"Which plants?\" } }\n"
	server := connectGarden(t, fake, lead, "")

	fake.AddLabel(garden, 7, "mobius:ready", "owner")

	card := waitForCard(t, server, leadChat, false)
	if card.Text != "Which plants?" || card.Question.ID != inbox(t, server)[0].ID {
		t.Errorf("card = %+v, question = %+v", card, card.Question)
	}
	if cards := questionCards(t, server, engine.ChatKey{Organization: "owner", Repository: garden, Workstream: 12}); len(cards) != 0 {
		t.Errorf("cards of owner/garden = %+v", cards)
	}
}

func TestACoordinatorAskAddsOneQuestionMessageToTheCoordinatorChatAndTheItemLinksIt(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Ask the Owner"
call = { tool = "ask", arguments = { text = "Which unit?" } }

[[prompts]]
reply = ["Asked"]
`)

	sendChat(t, server, coordinatorChat, "Ask the Owner")

	card := waitForCard(t, server, coordinatorChat, false)
	if card.Text != "Which unit?" || card.Question.ID != coordinatorItems(t, server)[0].ID {
		t.Errorf("card = %+v, question = %+v", card, card.Question)
	}
	if cards := questionCards(t, server, leadChat); len(cards) != 0 {
		t.Errorf("cards of the Lead chat = %+v", cards)
	}
}

func TestAnAnswerFromTheInboxIsTheAnswerOfTheQuestionAndTheLiveEventQuestionComesAtTheDismiss(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	events := liveEvents(t, server, "question")
	item := waitForItem(t, server, "question")

	answer(t, server, item.ID, "Cents.")

	ended := nextEventData(t, events, func(questionItem) bool { return true })
	if ended.ID != item.ID || ended.Answer != "Cents." || ended.Withdrawn || ended.DismissedAt == nil {
		t.Errorf("event = %+v", ended)
	}
	card := waitForCard(t, server, leadChat, true)
	if card.Question.Answer != "Cents." || card.Question.Withdrawn {
		t.Errorf("question = %+v", card.Question)
	}
}

func TestACommentOnGitHubIsTheAnswerOfTheQuestionWithNoMark(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := askedServer(t, fake)

	fake.AddComment(shop, 41, "owner", marked("Cents.", leadMark))

	card := waitForCard(t, server, leadChat, true)
	if card.Question.Answer != "Cents." || card.Question.Withdrawn {
		t.Errorf("question = %+v", card.Question)
	}
}

func TestACloseQuestionOfTheLeadIsTheAnswerOfTheQuestion(t *testing.T) {
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
	waitForCard(t, server, leadChat, false)

	sendChat(t, server, leadChat, "Dollars were chosen")

	card := waitForCard(t, server, leadChat, true)
	if card.Question.Answer != "Dollars." || card.Question.Withdrawn {
		t.Errorf("question = %+v", card.Question)
	}
}

func TestACoordinatorAnswerFromTheInboxIsTheAnswerOfTheQuestion(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Ask the Owner"
call = { tool = "ask", arguments = { text = "Which unit?" } }

[[prompts]]
reply = ["Seen"]
`)
	sendChat(t, server, coordinatorChat, "Ask the Owner")
	card := waitForCard(t, server, coordinatorChat, false)

	answer(t, server, card.Question.ID, "Cents.")

	card = waitForCard(t, server, coordinatorChat, true)
	if card.Question.Answer != "Cents." || card.Question.Withdrawn {
		t.Errorf("question = %+v", card.Question)
	}
}

func TestACoordinatorWithdrawMarksTheQuestionAsWithdrawnWithNoAnswer(t *testing.T) {
	t.Parallel()
	script := callWithdraw("Withdraw it.", 1) +
		"[[prompts]]\nwhen = \"Ask the Owner\"\ncall = { tool = \"ask\", arguments = { text = \"Which unit?\" } }\n\n" + seenScript
	server, _ := connect(t, testkit.NewFakeGitHub(t), script)
	run(t, server, coordinatorSpec(t), "Ask the Owner")
	waitForCard(t, server, coordinatorChat, false)

	run(t, server, coordinatorSpec(t), "Withdraw it.")

	card := waitForCard(t, server, coordinatorChat, true)
	if card.Question.Answer != "" || !card.Question.Withdrawn {
		t.Errorf("question = %+v", card.Question)
	}
}

func TestANewAskThatReplacesAQuestionLeavesTheOldQuestionWithNoAnswerAndNotWithdrawn(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Ask second"
call = { tool = "ask", arguments = { text = "Second?" } }

[[prompts]]
when = "Ask first"
call = { tool = "ask", arguments = { text = "First?" } }

[[prompts]]
reply = ["Asked"]
`)
	sendChat(t, server, coordinatorChat, "Ask first")
	waitForCard(t, server, coordinatorChat, false)

	sendChat(t, server, coordinatorChat, "Ask second")

	cards := testkit.WaitForValue(t, func() ([]questionCard, bool) {
		cards := questionCards(t, server, coordinatorChat)
		return cards, len(cards) == 2 && cards[0].Question != nil && cards[0].Question.DismissedAt != nil
	})
	if first := cards[0]; first.Text != "First?" || first.Question.Answer != "" || first.Question.Withdrawn {
		t.Errorf("first = %+v, question = %+v", first, first.Question)
	}
	if second := cards[1]; second.Text != "Second?" || second.Question.DismissedAt != nil {
		t.Errorf("second = %+v, question = %+v", second, second.Question)
	}
}
