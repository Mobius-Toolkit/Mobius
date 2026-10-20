package engine_test

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type apiInboxItem struct {
	Reference string `json:"reference"`
	Title     string `json:"title"`
	Reason    string `json:"reason"`
	Link      string `json:"link"`
}

var (
	pullRequestItem = `{"reference":"PR #7","title":"Add loyalty points","reason":"The pull request waits for approval.","link":"https://github.com/owner/shop/pull/7"}`
	noIssueItem     = `{"reference":"","title":"Why is the queue full?","reason":"The Coordinator asks a question.","link":""}`

	pullRequestBlock = "Inbox item: PR #7 Add loyalty points\nReason: The pull request waits for approval.\nLink: https://github.com/owner/shop/pull/7"
	noIssueBlock     = "Inbox item: Why is the queue full?\nReason: The Coordinator asks a question."
)

// sendInboxMessage posts a message of the Owner to the Lead chat of the Workstream #12 with one part inboxItems for each item.
func sendInboxMessage(t *testing.T, server *testserver.Server, text string, items ...string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	fields := [][2]string{{"id", "browser-1"}, {"organization", "owner"}, {"repository", shop}, {"workstream", "12"}}
	if text != "" {
		fields = append(fields, [2]string{"text", text})
	}
	for _, item := range items {
		fields = append(fields, [2]string{"inboxItems", item})
	}
	for _, field := range fields {
		if err := form.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/chat/messages", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(answer)
}

func chatInboxItems(t *testing.T, server *testserver.Server) [][]apiInboxItem {
	t.Helper()
	chat := apiData[struct {
		Messages []struct {
			Author     string         `json:"author"`
			InboxItems []apiInboxItem `json:"inboxItems"`
		} `json:"messages"`
	}](t, server, "/api/chat?organization=owner&repository=owner/shop&workstream=12")
	var items [][]apiInboxItem
	for _, message := range chat.Messages {
		if message.Author == "Owner" {
			items = append(items, message.InboxItems)
		}
	}
	return items
}

func TestAMessageWithInboxItemsKeepsThemInOrderAndGivesThemToTheAgentBeforeTheText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	if status, body := sendInboxMessage(t, server, "Look at these", pullRequestItem, noIssueItem); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForChat(t, server, leadChat, "Lead", "Seen")
	want := [][]apiInboxItem{{
		{"PR #7", "Add loyalty points", "The pull request waits for approval.", "https://github.com/owner/shop/pull/7"},
		{"", "Why is the queue full?", "The Coordinator asks a question.", ""},
	}}
	if got := chatInboxItems(t, server); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("items = %+v", got)
	}
	prompts := leadPrompts(t, server)
	if len(prompts) != 1 || !strings.HasSuffix(prompts[0], "# Owner message\n\n"+pullRequestBlock+"\n\n"+noIssueBlock+"\n\nLook at these") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAMessageWithOnlyAnInboxItemIsStoredAndTheAgentGetsOnlyTheBlock(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	if status, body := sendInboxMessage(t, server, "", noIssueItem); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForChat(t, server, leadChat, "Lead", "Seen")
	if got := chatInboxItems(t, server); len(got) != 1 || len(got[0]) != 1 || got[0][0].Title != "Why is the queue full?" {
		t.Errorf("items = %+v", got)
	}
	prompts := leadPrompts(t, server)
	if len(prompts) != 1 || !strings.HasSuffix(prompts[0], "# Owner message\n\n"+noIssueBlock) {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTheHistoryOfANewSessionHasTheInboxItemsBeforeTheTextOfAnEarlierMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"First answer\"]\n")
	if status, body := sendInboxMessage(t, server, "Look at these", pullRequestItem); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	endedChatSession(t, server, 0)

	sendChat(t, server, leadChat, "Add a plan price")

	prompt := promptTexts(t, server, endedChatSession(t, server, 1).ID)[0]
	_, history, _ := strings.Cut(prompt, "# Chat history")
	if !strings.Contains(history, "):\n"+pullRequestBlock+"\n\nLook at these\n") {
		t.Errorf("history = %s", history)
	}
}

func TestAMessageWithNoTextNoImageAndNoInboxItemGets400(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	if status, body := sendInboxMessage(t, server, ""); status != http.StatusBadRequest {
		t.Errorf("status = %d: %s", status, body)
	}
}

func TestSendAgainKeepsTheInboxItemsOfTheMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	flag := filepath.Join(t.TempDir(), "taken")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"if [ -e '"+flag+"' ]; then true; else kill -9 $PPID; sleep 5; fi\"\n")
	if status, body := sendInboxMessage(t, server, "", noIssueItem); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}
	failed := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range inbox(t, server) {
			if item.Kind == "Lead failed" {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if status, body := send(t, server, http.MethodPost, fmt.Sprintf("/api/inbox/%d/send", failed.ID), ""); status != http.StatusNoContent {
		t.Fatalf("send: status %d: %s", status, body)
	}

	want := [][]apiInboxItem{
		{{"", "Why is the queue full?", "The Coordinator asks a question.", ""}},
		{{"", "Why is the queue full?", "The Coordinator asks a question.", ""}},
	}
	if got := chatInboxItems(t, server); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("items = %+v", got)
	}
	prompts := leadPrompts(t, server)
	if len(prompts) == 0 || !strings.HasSuffix(prompts[len(prompts)-1], noIssueBlock) {
		t.Errorf("prompts = %q", prompts)
	}
}
