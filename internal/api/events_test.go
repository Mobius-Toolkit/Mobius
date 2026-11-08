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

func TestStreamEventsStartsWithAPing(t *testing.T) {
	mux, _ := testMux(t)
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

	if got, want := readEvent(t, r), "event: ping\ndata: {}\n"; got != want {
		t.Errorf("first event = %q, want %q", got, want)
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
