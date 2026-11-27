package engine_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// formPart is a part of a multipart request. A part with a content type is an image.
type formPart struct {
	name, value, contentType string
}

func textPart(name, value string) formPart {
	return formPart{name: name, value: value}
}

func imagePart(contentType, data string) formPart {
	return formPart{name: "images", value: data, contentType: contentType}
}

// postForm posts the parts as multipart/form-data to the API path, and gives the status and the body of the response.
func postForm(t *testing.T, server *testserver.Server, path string, parts ...formPart) (int, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, part := range parts {
		if part.contentType == "" {
			if err := form.WriteField(part.name, part.value); err != nil {
				t.Fatal(err)
			}
			continue
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename="image"`, part.name))
		header.Set("Content-Type", part.contentType)
		file, err := form.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(part.value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	text, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(text)
}

const (
	itemCardPart       = `{"reference":"#41","title":"Add plan model","reason":"It waits for an answer.","link":"https://github.com/owner/shop/issues/41"}`
	inboxSelectionPart = `{"source":"inbox","text":"Cents.","context":"","reference":"#41","link":"https://github.com/owner/shop/issues/41","part":"question"}`

	itemCardBlock       = "Inbox item: #41 Add plan model\nReason: It waits for an answer.\nLink: https://github.com/owner/shop/issues/41"
	inboxSelectionBlock = "Selection from Inbox item #41 (question)\nLink: https://github.com/owner/shop/issues/41\nSelected text:\n> Cents."

	itemCardMarkdown       = "Inbox item: #41 Add plan model (https://github.com/owner/shop/issues/41)"
	inboxSelectionMarkdown = "_Inbox item #41 · question_ (https://github.com/owner/shop/issues/41)\n\n> Cents."
)

var pngPart = imagePart(pngImage.MIMEType, string(pngImage.Data))

type storedApproval struct {
	ID         int64
	InboxItems string
	Selections string
}

func storedApprovals(t *testing.T, server *testserver.Server) []storedApproval {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, a *storedApproval) error {
		return rows.Scan(&a.ID, &a.InboxItems, &a.Selections)
	}, "SELECT id, inbox_items, selections FROM chat_messages WHERE author = 'Approval' ORDER BY id")
}

func TestTheDecisionOnAnActionGivesTheAgentTheImageTheInboxItemAndTheSelectionOfTheOwner(t *testing.T) {
	t.Parallel()
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			t.Parallel()
			server, _ := connectScript(t, testkit.NewFakeGitHub(t), imageReading+options+approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript, func(*config.Config) {})
			run(t, server, coordinatorSpec(t), "Send.")
			action := waitForActions(t, server, 1)[0]

			status, body := postForm(t, server, "/api/actions/"+itoa(action.ID)+"/"+decision,
				textPart("message", "Look."), pngPart, textPart("inboxItems", itemCardPart), textPart("selections", inboxSelectionPart))

			if status != http.StatusOK {
				t.Fatalf("status = %d: %s", status, body)
			}
			testkit.WaitFor(t, func() bool { return len(approvalPrompts(t, server)) == 1 })
			if got := approvalPrompts(t, server)[0]; !strings.HasSuffix(got, "The message of the Owner: Look.\n\n"+itemCardBlock+"\n\n"+inboxSelectionBlock) {
				t.Errorf("approval prompt = %q", got)
			}
			waitForChat(t, server, coordinatorChat, "Coordinator", "Seen"+imageReply(pngImage))
			messages := storedApprovals(t, server)
			if len(messages) != 1 {
				t.Fatalf("approval messages = %+v", messages)
			}
			if count, err := server.Engine.ImageCount(messages[0].ID); err != nil || count != 1 {
				t.Errorf("images = %d, %v", count, err)
			}
			if want := "[" + itemCardPart + "]"; messages[0].InboxItems != want {
				t.Errorf("inbox items = %s, want %s", messages[0].InboxItems, want)
			}
			var selections []apiSelection
			if err := json.Unmarshal([]byte(messages[0].Selections), &selections); err != nil {
				t.Fatal(err)
			}
			want := apiSelection{Source: "inbox", Text: "Cents.", Reference: "#41", Link: "https://github.com/owner/shop/issues/41", Part: "question"}
			if len(selections) != 1 || selections[0] != want {
				t.Errorf("selections = %+v, want %+v", selections, want)
			}
		})
	}
}

func TestTheRejectionOfAnActionOfTheTriagerGivesTheCommentTheInboxItemAndTheSelectionAsMarkdownAndNoImage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 71, "Add refunds", "Refund each order.", "owner")
	proposal := triagerComment(71, "Proposal: a message to the Lead of #12.")
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 71), proposal) })
	fake.AddComment(shop, 71, "owner", "Send it to the Lead.")
	action := waitForActions(t, server, 1)[0]

	status, body := postForm(t, server, "/api/actions/"+itoa(action.ID)+"/reject",
		textPart("message", "Ask me again tomorrow."), pngPart, textPart("inboxItems", itemCardPart), textPart("selections", inboxSelectionPart))

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	notice := testkit.Comment{
		Author: testkit.AppSlug + "[bot]",
		Body:   "The Owner rejected `message_lead` 1 (Add refunds to the loyalty plans.).\n\nThe message of the Owner: Ask me again tomorrow.\n\n" + itemCardMarkdown + "\n\n" + inboxSelectionMarkdown,
	}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 71), notice) })
	if messages := storedApprovals(t, server); len(messages) != 0 {
		t.Errorf("approval messages = %+v", messages)
	}
}

func TestAnAnswerOfAGitHubQuestionWithAttachmentsPostsMarkdownAndAddsOneMessageWithTheImagesToTheChatOfTheItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	status, body := postForm(t, server, "/api/inbox/"+itoa(item.ID)+"/answer",
		textPart("text", "Cents, please."), pngPart, textPart("inboxItems", itemCardPart), textPart("selections", inboxSelectionPart))

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var result struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	wantComment := testkit.Comment{Author: "owner", Body: itemCardMarkdown + "\n\n" + inboxSelectionMarkdown + "\n\nCents, please."}
	if got := fake.Comments(shop, 41); !slices.Contains(got, wantComment) {
		t.Errorf("comments = %v", got)
	}
	card := apiInboxItem{Reference: "#41", Title: item.Title, Reason: item.Reason, Link: "https://github.com/owner/shop/issues/41"}
	messages := ownerChatMessages(t, server, leadChat)
	var withImages, shown []ownerChatMessage
	for _, message := range messages {
		switch {
		case message.Author == "Owner" && message.Images > 0:
			withImages = append(withImages, message)
		case message.Author == "Owner" && message.Text == "Cents, please.":
			shown = append(shown, message)
		}
	}
	if len(withImages) != 1 || withImages[0].DisplayOnly || withImages[0].Images != 1 ||
		withImages[0].Text != "Images of my answer on #41: "+result.Data.URL || !slices.Equal(withImages[0].InboxItems, []apiInboxItem{card}) {
		t.Errorf("messages with images = %+v", withImages)
	}
	if len(shown) != 1 || !shown[0].DisplayOnly || len(shown[0].Selections) != 1 || shown[0].Selections[0].Source != "inbox" ||
		!slices.Equal(shown[0].InboxItems, []apiInboxItem{card, {Reference: "#41", Title: "Add plan model", Reason: "It waits for an answer.", Link: "https://github.com/owner/shop/issues/41"}}) {
		t.Errorf("displayed messages = %+v", shown)
	}
	waitForLeadPrompt(t, server, "Images of my answer on #41: "+result.Data.URL)
	testkit.WaitFor(t, func() bool {
		for _, message := range ownerChatMessages(t, server, leadChat) {
			if message.Images > 0 && message.DeliveredAt != nil {
				return true
			}
		}
		return false
	})
}

func TestAnAnswerOfAGitHubQuestionWithOnlyTextAddsNoMessageWithImages(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	answer(t, server, item.ID, "Cents.")

	waitForLeadPrompt(t, server, ` answer of #41 "Add plan model" by @owner:`)
	testkit.WaitFor(t, func() bool { return !chatView(t, server, leadChat).Writing })
	waitForPolls(t, fake)
	var owner int
	for _, message := range ownerChatMessages(t, server, leadChat) {
		if message.Author == "Owner" {
			owner++
			if !message.DisplayOnly || message.Images != 0 {
				t.Errorf("message = %+v", message)
			}
		}
	}
	if owner != 1 {
		t.Errorf("Owner messages = %d", owner)
	}
	for _, prompt := range leadPrompts(t, server) {
		if strings.Contains(prompt, "Images of my answer") {
			t.Errorf("prompt = %s", prompt)
		}
	}
}

func TestAnAnswerOfACoordinatorQuestionWithAnImageGivesTheCoordinatorChatMessageTheImage(t *testing.T) {
	t.Parallel()
	server, _ := connectScript(t, testkit.NewFakeGitHub(t), imageReading+options+`
[[prompts]]
when = "Cents, please."
reply = ["Got the answer"]

[[prompts]]
when = "Ask the Owner"
call = { tool = "ask", arguments = { text = "Two choices.", questions = [{ text = "Which unit?", options = [{ title = "Cents" }, { title = "Dollars" }] }] } }

[[prompts]]
reply = ["Asked"]
`, func(*config.Config) {})
	sendChat(t, server, coordinatorChat, "Ask the Owner")
	item := testkit.WaitForValue(t, func() (coordinatorItem, bool) {
		items := coordinatorItems(t, server)
		if len(items) != 1 {
			return coordinatorItem{}, false
		}
		return items[0], true
	})

	status, body := postForm(t, server, "/api/inbox/"+itoa(item.ID)+"/answer", textPart("text", "Cents, please."), pngPart)

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	waitForChat(t, server, coordinatorChat, "Coordinator", "Got the answer"+imageReply(pngImage))
	var found []ownerChatMessage
	for _, message := range ownerChatMessages(t, server, coordinatorChat) {
		if message.Author == "Owner" && message.Text == "Cents, please." {
			found = append(found, message)
		}
	}
	wantCards := []apiInboxItem{{Title: "Which unit?", Reason: "The Coordinator asks a question"}}
	if len(found) != 1 || found[0].DisplayOnly || found[0].Images != 1 || found[0].DeliveredAt == nil || !slices.Equal(found[0].InboxItems, wantCards) {
		t.Errorf("messages = %+v", found)
	}
}

func TestDoneOfATodoWithAnAttachmentAddsOneMessageThatTheAgentGetsAndDoneWithOnlyANoteStaysDisplayOnly(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		parts []formPart
		agent bool
	}{
		{"an image", []formPart{pngPart}, true},
		{"a selection", []formPart{textPart("selections", inboxSelectionPart)}, true},
		{"an Inbox item", []formPart{textPart("inboxItems", itemCardPart)}, true},
		{"only a note", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := approvableWith(t, fake, "off", true, mandatoryNeed)
			sendChat(t, server, leadChat, "Approve #41")
			waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY. The Owner got it for review. The pull request stays a draft until the Owner closes the open todos.")
			todo := waitForItem(t, server, "todo")

			status, body := postForm(t, server, "/api/inbox/"+itoa(todo.ID)+"/done", append([]formPart{textPart("note", "Set in prod.")}, test.parts...)...)

			if status != http.StatusOK {
				t.Fatalf("status = %d: %s", status, body)
			}
			var found []ownerChatMessage
			for _, message := range ownerChatMessages(t, server, leadChat) {
				if message.Author == "Owner" && message.Text == "Set in prod." {
					found = append(found, message)
				}
			}
			if len(found) != 1 || found[0].DisplayOnly == test.agent {
				t.Fatalf("messages = %+v", found)
			}
			card := apiInboxItem{Reference: "PR #42", Title: "PLAN_API_KEY", Reason: todo.Reason, Link: "https://github.com/owner/shop/pull/42"}
			if found[0].InboxItems[0] != card {
				t.Errorf("first card = %+v, want %+v", found[0].InboxItems[0], card)
			}
			if !test.agent {
				return
			}
			if len(test.parts) == 1 && test.parts[0].contentType != "" && found[0].Images != 1 {
				t.Errorf("images = %d", found[0].Images)
			}
			testkit.WaitFor(t, func() bool {
				for _, message := range ownerChatMessages(t, server, leadChat) {
					if message.Author == "Owner" && message.Text == "Set in prod." && message.DeliveredAt != nil {
						return true
					}
				}
				return false
			})
		})
	}
}

func TestAnAnswerOfAGitHubQuestionWithOnlyAnImagePostsACommentThatNamesTheChatAndAddsTheImageMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := askedServer(t, fake)
	authorize(t, server)
	item := waitForItem(t, server, "question")

	status, body := postForm(t, server, "/api/inbox/"+itoa(item.ID)+"/answer", pngPart)

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	want := testkit.Comment{Author: "owner", Body: "The images of my answer are in the Lead chat."}
	if got := fake.Comments(shop, 41); !slices.Contains(got, want) {
		t.Errorf("comments = %v", got)
	}
	testkit.WaitFor(t, func() bool {
		for _, message := range ownerChatMessages(t, server, leadChat) {
			if message.Author == "Owner" && message.Images == 1 && !message.DisplayOnly && message.DeliveredAt != nil {
				return true
			}
		}
		return false
	})
}
