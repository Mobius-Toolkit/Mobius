package github

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type etagServer struct {
	*httptest.Server
	mu   sync.Mutex
	sent []string
}

var bodies = map[string]string{"one": "body of one", "two": "body of two"}

// newETagServer answers 304 to the ETag of the body and 200 with the body of the Accept header otherwise.
func newETagServer(t *testing.T) *etagServer {
	t.Helper()
	s := &etagServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.sent = append(s.sent, r.Header.Get("If-None-Match"))
		s.mu.Unlock()
		etag := `"` + r.Header.Get("Accept") + `"`
		w.Header().Set("X-Ratelimit-Remaining", "4000")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Link", "<next>; rel=\"next\"")
		_, _ = io.WriteString(w, bodies[r.Header.Get("Accept")])
	}))
	t.Cleanup(s.Close)
	return s
}

func get(t *testing.T, client *http.Client, url, accept, ifNoneMatch string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", accept)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

func TestTheSecondGetOfAURLSendsTheETagAndGivesTheKeptResponse(t *testing.T) {
	server := newETagServer(t)
	client := &http.Client{Transport: newETagTransport(http.DefaultTransport)}

	_, firstHeader, firstBody := get(t, client, server.URL+"/a", "one", "")
	secondStatus, secondHeader, secondBody := get(t, client, server.URL+"/a", "one", "")

	if want := []string{"", `"one"`}; server.sent[0] != want[0] || server.sent[1] != want[1] {
		t.Fatalf("If-None-Match of the requests = %q, want %q", server.sent, want)
	}
	if secondStatus != http.StatusOK || secondBody != firstBody || firstBody != "body of one" {
		t.Fatalf("second response = %d %q, first body = %q", secondStatus, secondBody, firstBody)
	}
	if secondHeader.Get("Link") != firstHeader.Get("Link") || secondHeader.Get("Link") == "" {
		t.Fatalf("Link of the second response = %q", secondHeader.Get("Link"))
	}
}

func TestAGetWithAnotherAcceptHeaderDoesNotUseTheEntry(t *testing.T) {
	server := newETagServer(t)
	client := &http.Client{Transport: newETagTransport(http.DefaultTransport)}

	get(t, client, server.URL+"/a", "one", "")
	_, _, body := get(t, client, server.URL+"/a", "two", "")

	if body != "body of two" || server.sent[1] != "" {
		t.Fatalf("body = %q, If-None-Match = %q", body, server.sent[1])
	}
}

func TestARequestWithIfNoneMatchGetsTheAnswerOfTheServer(t *testing.T) {
	server := newETagServer(t)
	client := &http.Client{Transport: newETagTransport(http.DefaultTransport)}

	get(t, client, server.URL+"/a", "one", "")
	status, _, body := get(t, client, server.URL+"/a", "one", `"one"`)

	if status != http.StatusNotModified || body != "" {
		t.Fatalf("response = %d %q, want 304 with no body", status, body)
	}
}

// Serial: the test changes etagClock.
func TestAnEntryThatNoRequestUsedForAnHourIsRemoved(t *testing.T) {
	now := time.Now()
	etagClock = func() time.Time { return now }
	t.Cleanup(func() { etagClock = time.Now })
	server := newETagServer(t)
	client := &http.Client{Transport: newETagTransport(http.DefaultTransport)}

	get(t, client, server.URL+"/a", "one", "")
	now = now.Add(etagLife - time.Minute)
	get(t, client, server.URL+"/a", "one", "")
	now = now.Add(etagLife)
	get(t, client, server.URL+"/a", "one", "")

	if want := []string{"", `"one"`, ""}; server.sent[0] != want[0] || server.sent[1] != want[1] || server.sent[2] != want[2] {
		t.Fatalf("If-None-Match of the requests = %q, want %q", server.sent, want)
	}
}
