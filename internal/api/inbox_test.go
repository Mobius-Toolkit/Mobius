package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

// formPart is a part of a multipart request. A part with a content type is an image.
type formPart struct {
	name, value, contentType string
}

func formRequest(t *testing.T, path string, parts ...formPart) *http.Request {
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
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	return request
}

const inboxSelection = `{"source":"inbox","text":"Cents.","reference":"#41","link":"https://github.com/owner/shop/issues/41","part":"question"}`

func TestAnAnswerOfAnUnknownItemGives404AnEmptyTextGives400AndAnItemWithNoIssueGives400(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO inbox_items (id, kind, organization, repository, workstream_repository, workstream, issue, text, link, time) VALUES
		(1, 'Lead failed', 'owner', '', '', 0, 0, 'The Lead did not take the message.', '', '2026-10-04T10:00:00Z'),
		(2, 'question', 'owner', 'owner/shop', 'owner/shop', 12, 41, 'Cents or dollars?', '', '2026-10-04T10:00:00Z')`)

	for _, test := range []struct {
		path   string
		parts  []formPart
		status int
	}{
		{"/api/inbox/9/answer", []formPart{{name: "text", value: "Cents."}}, http.StatusNotFound},
		{"/api/inbox/2/answer", []formPart{{name: "text"}}, http.StatusBadRequest},
		{"/api/inbox/2/answer", []formPart{{name: "text", value: " \n"}}, http.StatusBadRequest},
		{"/api/inbox/1/answer", []formPart{{name: "text", value: "Cents."}}, http.StatusBadRequest},
		{"/api/inbox/9/answer", []formPart{{name: "selections", value: inboxSelection}}, http.StatusNotFound},
		{"/api/inbox/9/answer", []formPart{{name: "text"}, {name: "images", value: "png", contentType: "image/png"}}, http.StatusNotFound},
		{"/api/inbox/9/answer", []formPart{{name: "inboxItems", value: `{"reference":"#41","title":"Add plan model","reason":"","link":""}`}}, http.StatusNotFound},
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, formRequest(t, test.path, test.parts...))
		if recorder.Code != test.status {
			t.Errorf("%s %v: status %d: %s", test.path, test.parts, recorder.Code, recorder.Body)
		}
	}
}

func TestEachDecisionEndpointRefusesAFifthImageAndAnImageOfAnotherType(t *testing.T) {
	mux, _ := testMux(t)
	png := formPart{name: "images", value: "png", contentType: "image/png"}

	for _, path := range []string{"/api/inbox/2/answer", "/api/inbox/2/done", "/api/actions/3/approve", "/api/actions/3/reject"} {
		for name, parts := range map[string][]formPart{
			"a fifth image":    {png, png, png, png, png},
			"a text file":      {{name: "images", value: "text", contentType: "text/plain"}},
			"a mixed set":      {png, {name: "images", value: "svg", contentType: "image/svg+xml"}},
			"a text and a bad": {{name: "text", value: "Cents."}, {name: "images", value: "pdf", contentType: "application/pdf"}},
		} {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, formRequest(t, path, parts...))
			if recorder.Code != http.StatusBadRequest {
				t.Errorf("%s with %s: status %d: %s", path, name, recorder.Code, recorder.Body)
			}
		}
	}
}

func TestAnAnswerWithNoTextAndNoAttachmentGivesTheEmptyTextError(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO inbox_items (id, kind, organization, repository, workstream_repository, workstream, issue, text, link, time) VALUES
		(2, 'question', 'owner', 'owner/shop', 'owner/shop', 12, 41, 'Cents or dollars?', '', '2026-10-04T10:00:00Z')`)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, formRequest(t, "/api/inbox/2/answer", formPart{name: "voice", value: "true"}))

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "The text must not be empty.") {
		t.Errorf("status %d: %s", recorder.Code, recorder.Body)
	}
}
