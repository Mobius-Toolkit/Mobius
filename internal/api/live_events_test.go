package api_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const liveEventsLimit = 10 * time.Second

// openLiveEvents opens GET /api/events and reads its first event, the ping. The handler sends the ping after it listens
// to the engine, so an action after this call sends its event to the stream. A read of the stream fails at
// liveEventsLimit.
func openLiveEvents(t *testing.T, server *testserver.Server) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), liveEventsLimit)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reply.Body.Close() })
	events := bufio.NewReader(reply.Body)
	if event := readLiveEvent(t, events); !strings.HasPrefix(event, "event: ping\n") {
		t.Fatalf("first event = %q, want a ping", event)
	}
	return events
}

func readLiveEvent(t *testing.T, events *bufio.Reader) string {
	t.Helper()
	var event strings.Builder
	for {
		line, err := events.ReadString('\n')
		if err != nil {
			t.Fatalf("read event: %v", err)
		}
		if line == "\n" {
			return event.String()
		}
		event.WriteString(line)
	}
}
