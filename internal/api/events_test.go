package api

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func readEvent(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var event strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read event: %v", err)
		}
		if line == "\n" {
			return event.String()
		}
		event.WriteString(line)
	}
}

func TestStreamEvents(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO events (time, repository, workstream, issue, actor, text, link)
		VALUES ('2026-10-01T10:00:00Z', 'o/a', 1, 11, 'bot', 'Dispatched', 'https://example.com/11')`)
	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(server.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type = %q, want text/event-stream", ct)
	}
	r := bufio.NewReader(resp.Body)

	want := "id: 1\nevent: activity\n" +
		`data: {"actor":"bot","id":1,"issue":11,"link":"https://example.com/11","repository":"o/a","text":"Dispatched","time":"2026-10-01T10:00:00Z","workstream":1}` + "\n"
	if got := readEvent(t, r); got != want {
		t.Errorf("backlog event = %q, want %q", got, want)
	}

	exec(t, db, `INSERT INTO events (time, repository, workstream, issue, actor, text, link)
		VALUES ('2026-10-01T11:00:00Z', 'o/a', 1, 12, 'owner', 'Stopped', 'https://example.com/12')`)
	want = "id: 2\nevent: activity\n" +
		`data: {"actor":"owner","id":2,"issue":12,"link":"https://example.com/12","repository":"o/a","text":"Stopped","time":"2026-10-01T11:00:00Z","workstream":1}` + "\n"
	if got := readEvent(t, r); got != want {
		t.Errorf("new event = %q, want %q", got, want)
	}
}

func TestStreamEventsResume(t *testing.T) {
	mux, db := testMux(t)
	exec(t, db, `INSERT INTO events (time, repository, workstream, issue, actor, text, link) VALUES
		('2026-10-01T10:00:00Z', 'o/a', 1, 11, 'bot', 'Dispatched', 'https://example.com/11'),
		('2026-10-01T10:01:00Z', 'o/a', 1, 12, 'bot', 'Dispatched', 'https://example.com/12'),
		('2026-10-01T10:02:00Z', 'o/a', 1, 13, 'bot', 'Dispatched', 'https://example.com/13')`)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", "2")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	r := bufio.NewReader(resp.Body)

	want := "id: 3\nevent: activity\n" +
		`data: {"actor":"bot","id":3,"issue":13,"link":"https://example.com/13","repository":"o/a","text":"Dispatched","time":"2026-10-01T10:02:00Z","workstream":1}` + "\n"
	if got := readEvent(t, r); got != want {
		t.Errorf("first event after resume = %q, want %q", got, want)
	}

	exec(t, db, `INSERT INTO events (time, repository, workstream, issue, actor, text, link)
		VALUES ('2026-10-01T11:00:00Z', 'o/a', 1, 14, 'owner', 'Stopped', 'https://example.com/14')`)
	want = "id: 4\nevent: activity\n" +
		`data: {"actor":"owner","id":4,"issue":14,"link":"https://example.com/14","repository":"o/a","text":"Stopped","time":"2026-10-01T11:00:00Z","workstream":1}` + "\n"
	if got := readEvent(t, r); got != want {
		t.Errorf("new event after resume = %q, want %q", got, want)
	}
}

func TestStreamEventsPing(t *testing.T) {
	pingInterval = 10 * time.Millisecond
	t.Cleanup(func() { pingInterval = 15 * time.Second })
	mux, _ := testMux(t)
	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(server.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	r := bufio.NewReader(resp.Body)

	for range 2 {
		if got, want := readEvent(t, r), "event: ping\ndata: {}\n"; got != want {
			t.Errorf("idle event = %q, want %q", got, want)
		}
	}
}
