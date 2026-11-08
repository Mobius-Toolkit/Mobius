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
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// sendVoiceMessage posts a message of the Owner with the text "Add a plan price" to the Lead chat of the Workstream #12.
// The form has the field voice with the value voice, or no field when voice is empty.
func sendVoiceMessage(t *testing.T, server *testserver.Server, voice string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	fields := [][2]string{{"id", "browser-1"}, {"organization", "owner"}, {"repository", shop}, {"workstream", "12"}, {"text", "Add a plan price"}}
	if voice != "" {
		fields = append(fields, [2]string{"voice", voice})
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
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.StatusCode, answer)
	}
}

func ownerMessageVoiceMarks(t *testing.T, server *testserver.Server) []bool {
	t.Helper()
	chat := apiData[struct {
		Messages []struct {
			Author string `json:"author"`
			Voice  bool   `json:"voice"`
		} `json:"messages"`
	}](t, server, "/api/chat?organization=owner&repository=owner/shop&workstream=12")
	var marks []bool
	for _, message := range chat.Messages {
		if message.Author == "Owner" {
			marks = append(marks, message.Voice)
		}
	}
	return marks
}

func TestAMessageWithVoiceTrueKeepsTheVoiceMarkAndGetChatGivesIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	sendVoiceMessage(t, server, "true")

	waitForChat(t, server, leadChat, "Lead", "Seen")
	if got := ownerMessageVoiceMarks(t, server); !slices.Equal(got, []bool{true}) {
		t.Errorf("voice marks = %v", got)
	}
}

func TestAMessageWithNoVoiceFieldHasNoVoiceMark(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")

	sendVoiceMessage(t, server, "")

	waitForChat(t, server, leadChat, "Lead", "Seen")
	if got := ownerMessageVoiceMarks(t, server); !slices.Equal(got, []bool{false}) {
		t.Errorf("voice marks = %v", got)
	}
}

func TestSendAgainGivesTheCopyTheVoiceMarkOfTheCancelledMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	flag := filepath.Join(t.TempDir(), "taken")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"if [ -e '"+flag+"' ]; then true; else kill -9 $PPID; sleep 5; fi\"\n")
	sendVoiceMessage(t, server, "true")
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

	if got := ownerMessageVoiceMarks(t, server); !slices.Equal(got, []bool{true, true}) {
		t.Errorf("voice marks = %v", got)
	}
}
