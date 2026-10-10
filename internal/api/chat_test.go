package api_test

import (
	"bufio"
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type testImage struct {
	contentType string
	data        []byte
}

var chatIDs atomic.Int64

// sendChat posts a message of the Owner with a new browser id to the Triager chat of the organization owner.
func sendChat(t *testing.T, server *testserver.Server, text string, images ...testImage) (int, string) {
	t.Helper()
	return sendChatID(t, server, "message-"+strconv.FormatInt(chatIDs.Add(1), 10), text, images...)
}

// sendChatID posts a message of the Owner with the browser id id to the Triager chat of the organization owner as
// multipart/form-data.
func sendChatID(t *testing.T, server *testserver.Server, id, text string, images ...testImage) (int, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("id", id); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("organization", "owner"); err != nil {
		t.Fatal(err)
	}
	if text != "" {
		if err := form.WriteField("text", text); err != nil {
			t.Fatal(err)
		}
	}
	for i, image := range images {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="images"; filename="image`+strconv.Itoa(i)+`"`)
		header.Set("Content-Type", image.contentType)
		part, err := form.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(image.data); err != nil {
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
	reply, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reply.Body.Close() }()
	answer, err := io.ReadAll(reply.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply.StatusCode, string(answer)
}

type chatMessage struct {
	ID        int64  `json:"id"`
	Text      string `json:"text"`
	Images    int64  `json:"images"`
	BrowserID string `json:"browserId"`
}

func getMessages(t *testing.T, server *testserver.Server) []chatMessage {
	t.Helper()
	var body struct {
		Data struct {
			Messages []chatMessage `json:"messages"`
		} `json:"data"`
	}
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/chat?organization=owner", "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("chat: status %d", reply.StatusCode)
	}
	return body.Data.Messages
}

func startChat(t *testing.T) *testserver.Server {
	t.Helper()
	return startWithApp(t, testkit.NewFakeGitHub(t), "User")
}

func bytesOf(n int) []byte {
	return bytes.Repeat([]byte{'x'}, n)
}

func TestSendChatKeepsTheTextAndTheImagesOfAMessage(t *testing.T) {
	server := startChat(t)
	png := testImage{"image/png", []byte("png data")}
	jpeg := testImage{"image/jpeg", []byte("jpeg data")}

	if status, text := sendChat(t, server, "Look", png, jpeg); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, text)
	}

	messages := getMessages(t, server)
	if len(messages) != 1 || messages[0].Text != "Look" || messages[0].Images != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	for position, want := range []testImage{png, jpeg} {
		reply, err := server.Client.Get(server.URL + "/api/chat/messages/" + strconv.FormatInt(messages[0].ID, 10) + "/images/" + strconv.Itoa(position))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reply.Body)
		_ = reply.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if reply.StatusCode != http.StatusOK || reply.Header.Get("Content-Type") != want.contentType || !bytes.Equal(data, want.data) {
			t.Errorf("image %d: status %d, type %q, data %q", position, reply.StatusCode, reply.Header.Get("Content-Type"), data)
		}
	}
}

func TestSendChatStoresAMessageWithTheSameIDOnlyOnce(t *testing.T) {
	server := startChat(t)
	png := testImage{"image/png", []byte("png data")}

	for range 2 {
		if status, text := sendChatID(t, server, "browser-1", "Look", png); status != http.StatusNoContent {
			t.Fatalf("status = %d: %s", status, text)
		}
	}

	messages := getMessages(t, server)
	if len(messages) != 1 || messages[0].BrowserID != "browser-1" {
		t.Errorf("messages = %+v", messages)
	}
}

func TestSendChatRefusesAMessageWithNoID(t *testing.T) {
	server := startChat(t)

	if status, text := sendChatID(t, server, "", "Look"); status != http.StatusBadRequest {
		t.Errorf("status = %d, body = %s", status, text)
	}
}

func TestSendChatChangesTheNewLinesOfTheTextToLF(t *testing.T) {
	server := startChat(t)

	if status, text := sendChat(t, server, "one\r\ntwo"); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, text)
	}

	messages := getMessages(t, server)
	if len(messages) != 1 || messages[0].Text != "one\ntwo" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestSendChatAcceptsAMessageWithOnlyImages(t *testing.T) {
	server := startChat(t)

	if status, text := sendChat(t, server, "", testImage{"image/webp", []byte("webp data")}); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, text)
	}

	messages := getMessages(t, server)
	if len(messages) != 1 || messages[0].Text != "" || messages[0].Images != 1 {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestSendChatRefusesAMessageWithNoTextAndNoImage(t *testing.T) {
	server := startChat(t)

	if status, _ := sendChat(t, server, ""); status != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", status, http.StatusBadRequest)
	}

	if messages := getMessages(t, server); len(messages) != 0 {
		t.Errorf("messages = %+v", messages)
	}
}

func TestSendChatRefusesAnImageThatIsNotValid(t *testing.T) {
	png := testImage{"image/png", []byte("png data")}
	tests := map[string][]testImage{
		"more than 4 images":  {png, png, png, png, png},
		"an image over 5 MB":  {{"image/png", bytesOf(5*1024*1024 + 1)}},
		"another type":        {{"image/svg+xml", []byte("<svg/>")}},
		"a file with no type": {{"", []byte("data")}},
	}
	for name, images := range tests {
		t.Run(name, func(t *testing.T) {
			server := startChat(t)

			if status, _ := sendChat(t, server, "Look", images...); status != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", status, http.StatusBadRequest)
			}

			if messages := getMessages(t, server); len(messages) != 0 {
				t.Errorf("messages = %+v", messages)
			}
		})
	}
}

func TestSendChatAcceptsFourImagesOfFiveMegabytes(t *testing.T) {
	server := startChat(t)
	image := testImage{"image/png", bytesOf(5 * 1024 * 1024)}

	if status, text := sendChat(t, server, "Look", image, image, image, image); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, text)
	}

	if messages := getMessages(t, server); len(messages) != 1 || messages[0].Images != 4 {
		t.Errorf("messages = %+v", messages)
	}
}

func TestSendChatRefusesARequestThatIsTooLarge(t *testing.T) {
	server := startChat(t)

	status, text := sendChat(t, server, "Look", testImage{"image/png", bytesOf(22 * 1024 * 1024)})

	if status != http.StatusBadRequest || !strings.Contains(text, "request body too large") {
		t.Errorf("status = %d, body = %s", status, text)
	}
	if messages := getMessages(t, server); len(messages) != 0 {
		t.Errorf("messages = %+v", messages)
	}
}

func TestTheMessageEventHasTheImageCount(t *testing.T) {
	server := startChat(t)
	reply, err := server.Client.Get(server.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reply.Body.Close() }()
	events := bufio.NewReader(reply.Body)

	if status, text := sendChat(t, server, "Look", testImage{"image/gif", []byte("gif data")}); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, text)
	}

	for {
		var event strings.Builder
		for {
			line, err := events.ReadString('\n')
			if err != nil {
				t.Fatalf("read event: %v", err)
			}
			if line == "\n" {
				break
			}
			event.WriteString(line)
		}
		if strings.Contains(event.String(), "event: message\n") {
			if !strings.Contains(event.String(), `"images":1`) {
				t.Errorf("event = %q", event.String())
			}
			return
		}
	}
}

func TestGetChatImageReturns404WhenTheMessageHasNoSuchImage(t *testing.T) {
	server := startChat(t)
	if status, text := sendChat(t, server, "Look", testImage{"image/png", []byte("png data")}); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, text)
	}
	id := strconv.FormatInt(getMessages(t, server)[0].ID, 10)

	for _, path := range []string{"/api/chat/messages/" + id + "/images/1", "/api/chat/messages/999/images/0"} {
		if reply := call(t, server.Client, http.MethodGet, server.URL+path, "", nil); reply.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want %d", path, reply.StatusCode, http.StatusNotFound)
		}
	}
}
