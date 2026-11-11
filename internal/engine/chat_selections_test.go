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

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type apiSelection struct {
	Source        string `json:"source"`
	Text          string `json:"text"`
	Context       string `json:"context"`
	Reference     string `json:"reference"`
	Link          string `json:"link"`
	Part          string `json:"part"`
	MessageID     int64  `json:"messageId"`
	MessageAuthor string `json:"messageAuthor"`
}

var (
	taskSelectionItem    = `{"source":"task","text":"the selected text","context":"the paragraph of the selection","reference":"#12","link":"https://github.com/owner/name/issues/12","part":"issue body"}`
	messageSelectionItem = `{"source":"message","text":"…","context":"…","messageId":345,"messageAuthor":"Lead"}`
	briefSelectionItem   = `{"source":"brief","text":"…","context":"…","reference":"#1651","link":"https://github.com/owner/name/issues/1651","part":"section Scope"}`

	taskSelectionBlock    = "Selection from task #12 (issue body)\nLink: https://github.com/owner/name/issues/12\nSelected text:\n> the selected text\nContext:\n> the paragraph of the selection"
	messageSelectionBlock = "Selection from chat message 345 of Lead\nSelected text:\n> …\nContext:\n> …"
	briefSelectionBlock   = "Selection from the Brief of #1651 (section Scope)\nLink: https://github.com/owner/name/issues/1651\nSelected text:\n> …\nContext:\n> …"
)

// sendSelectionMessage posts a message of the Owner with one part selections for each selection.
func sendSelectionMessage(t *testing.T, server *testserver.Server, repo, workstream, text string, selections ...string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	fields := [][2]string{{"id", "browser-1"}, {"organization", "owner"}, {"repository", repo}, {"workstream", workstream}}
	if text != "" {
		fields = append(fields, [2]string{"text", text})
	}
	for _, selection := range selections {
		fields = append(fields, [2]string{"selections", selection})
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

func chatSelections(t *testing.T, server *testserver.Server) [][]apiSelection {
	t.Helper()
	chat := apiData[struct {
		Messages []struct {
			Author     string         `json:"author"`
			Selections []apiSelection `json:"selections"`
		} `json:"messages"`
	}](t, server, "/api/chat?organization=owner&repository=owner/shop&workstream=12")
	var items [][]apiSelection
	for _, message := range chat.Messages {
		if message.Author == "Owner" {
			items = append(items, message.Selections)
		}
	}
	return items
}

func TestAMessageWithSelectionsKeepsThemInOrderAndGivesThemToTheAgentBeforeTheText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	if status, body := sendSelectionMessage(t, server, shop, "12", "Look at these", taskSelectionItem, messageSelectionItem, briefSelectionItem); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForChat(t, server, leadChat, "Lead", "Seen")
	want := [][]apiSelection{{
		{"task", "the selected text", "the paragraph of the selection", "#12", "https://github.com/owner/name/issues/12", "issue body", 0, ""},
		{"message", "…", "…", "", "", "", 345, "Lead"},
		{"brief", "…", "…", "#1651", "https://github.com/owner/name/issues/1651", "section Scope", 0, ""},
	}}
	if got := chatSelections(t, server); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("selections = %+v", got)
	}
	prompts := leadPrompts(t, server)
	if len(prompts) != 1 || !strings.HasSuffix(prompts[0], "# Owner message\n\n"+taskSelectionBlock+"\n\n"+messageSelectionBlock+"\n\n"+briefSelectionBlock+"\n\nLook at these") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAMessageWithOnlySelectionsIsStoredAndTheAgentGetsOnlyTheBlocks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	if status, body := sendSelectionMessage(t, server, shop, "12", "", taskSelectionItem); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForChat(t, server, leadChat, "Lead", "Seen")
	if got := chatSelections(t, server); len(got) != 1 || len(got[0]) != 1 || got[0][0].Text != "the selected text" {
		t.Errorf("selections = %+v", got)
	}
	prompts := leadPrompts(t, server)
	if len(prompts) != 1 || !strings.HasSuffix(prompts[0], "# Owner message\n\n"+taskSelectionBlock) {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestCoordinatorChatPromptHoldsSelections(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	if status, body := sendSelectionMessage(t, server, "", "0", "Check this", taskSelectionItem, messageSelectionItem, briefSelectionItem); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForChat(t, server, coordinatorChat, "Coordinator", "Seen")
	prompts := coordinatorPrompts(t, server)
	if len(prompts) == 0 || !strings.HasSuffix(prompts[len(prompts)-1], "# Owner message\n\n"+taskSelectionBlock+"\n\n"+messageSelectionBlock+"\n\n"+briefSelectionBlock+"\n\nCheck this") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestSelectionBlockExactFormats(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	taskNoPart := `{"source":"task","text":"line 1\nline 2","context":"ctx 1\nctx 2","reference":"#42","link":"https://github.com/owner/name/issues/42"}`
	taskNoLink := `{"source":"task","text":"snippet","context":"surrounding","reference":"#42","part":"issue body"}`
	briefNoPart := `{"source":"brief","text":"intro text","context":"preamble","reference":"#1651","link":"https://github.com/owner/name/issues/1651"}`

	taskNoPartBlock := "Selection from task #42\nLink: https://github.com/owner/name/issues/42\nSelected text:\n> line 1\n> line 2\nContext:\n> ctx 1\n> ctx 2"
	taskNoLinkBlock := "Selection from task #42 (issue body)\nSelected text:\n> snippet\nContext:\n> surrounding"
	briefNoPartBlock := "Selection from the Brief of #1651\nLink: https://github.com/owner/name/issues/1651\nSelected text:\n> intro text\nContext:\n> preamble"

	if status, body := sendSelectionMessage(t, server, shop, "12", "All variants", taskNoPart, taskNoLink, briefNoPart); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForChat(t, server, leadChat, "Lead", "Seen")
	prompts := leadPrompts(t, server)
	if len(prompts) != 1 || !strings.HasSuffix(prompts[0], "# Owner message\n\n"+taskNoPartBlock+"\n\n"+taskNoLinkBlock+"\n\n"+briefNoPartBlock+"\n\nAll variants") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAMessageWithNoTextNoImageNoInboxItemAndNoSelectionGets400(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	if status, body := sendSelectionMessage(t, server, shop, "12", ""); status != http.StatusBadRequest {
		t.Errorf("status = %d: %s", status, body)
	}
}

func TestAMessageWithUnknownSelectionSourceGets400(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	invalidSourceItem := `{"source":"x","text":"the selected text"}`
	if status, body := sendSelectionMessage(t, server, shop, "12", "", invalidSourceItem); status != http.StatusBadRequest {
		t.Errorf("status = %d: %s, want %d", status, body, http.StatusBadRequest)
	}
}

func TestAMessageWithEmptySelectedTextGets400(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	emptyTextItem := `{"source":"task","text":""}`
	if status, body := sendSelectionMessage(t, server, shop, "12", "", emptyTextItem); status != http.StatusBadRequest {
		t.Errorf("status = %d: %s, want %d", status, body, http.StatusBadRequest)
	}
}

func TestSendChatRefusesMessageWithNoContent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	err := server.Engine.SendChat(t.Context(), leadChat, "", "", nil, nil, nil, false)
	if !engine.Refused(err) {
		t.Errorf("err = %v, want refusal", err)
	}
	if err.Error() != "A message must have text, an image or an Inbox item." {
		t.Errorf("err = %v", err)
	}
}

func TestSendAgainKeepsTheSelectionsOfTheMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	flag := filepath.Join(t.TempDir(), "taken")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"if [ -e '"+flag+"' ]; then true; else kill -9 $PPID; sleep 5; fi\"\n")
	if status, body := sendSelectionMessage(t, server, shop, "12", "", taskSelectionItem); status != http.StatusNoContent {
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

	want := [][]apiSelection{
		{{"task", "the selected text", "the paragraph of the selection", "#12", "https://github.com/owner/name/issues/12", "issue body", 0, ""}},
		{{"task", "the selected text", "the paragraph of the selection", "#12", "https://github.com/owner/name/issues/12", "issue body", 0, ""}},
	}
	if got := chatSelections(t, server); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("selections = %+v", got)
	}
	prompts := leadPrompts(t, server)
	if len(prompts) == 0 || !strings.HasSuffix(prompts[len(prompts)-1], taskSelectionBlock) {
		t.Errorf("prompts = %q", prompts)
	}
}
