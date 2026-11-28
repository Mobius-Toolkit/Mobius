package engine_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestARoleChangeOfTheOwnerAddsOneRecordToTheCoordinatorChatAndARefusalAddsNone(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), usageTurn)

	if status, text := sendRole(t, server, "lead", `{"harness": "claude-code", "model": "opus", "effort": "high", "max": 0}`); status != http.StatusUnprocessableEntity {
		t.Fatalf("refused PUT: status %d: %s", status, text)
	}
	if got := records(t, server, coordinatorChat); len(got) != 0 {
		t.Errorf("records after a refusal = %q", got)
	}

	sendRole(t, server, "lead", leadOpus)
	want := []string{"You set the Role Lead to claude-code, opus, effort high."}
	if got := records(t, server, coordinatorChat); !reflect.DeepEqual(got, want) {
		t.Errorf("records after the PUT = %q, want %q", got, want)
	}

	if status, text := sendRole(t, server, "lead", ""); status != http.StatusOK {
		t.Fatalf("DELETE: status %d: %s", status, text)
	}
	got := records(t, server, coordinatorChat)
	if len(got) != 2 || !strings.HasPrefix(got[1], "You reset the Role Lead to ") {
		t.Errorf("records after the DELETE = %q", got)
	}
}

func TestTheDrainOfTheOwnerAddsOneRecordToTheCoordinatorChatOfEachOrganization(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(other)
	server := startWithTasks(t, fake)
	otherChat := engine.ChatKey{Organization: "other"}
	t.Cleanup(func() { server.Engine.CancelDrain() })

	for range 2 {
		if status, text := send(t, server, http.MethodPost, "/api/drain", ""); status != http.StatusOK {
			t.Fatalf("POST: status %d: %s", status, text)
		}
	}
	for _, key := range []engine.ChatKey{coordinatorChat, otherChat} {
		if got, want := records(t, server, key), []string{"You turned on the drain."}; !reflect.DeepEqual(got, want) {
			t.Errorf("records of %s after the start = %q, want %q", key.Organization, got, want)
		}
	}

	for range 2 {
		if status, text := send(t, server, http.MethodDelete, "/api/drain", ""); status != http.StatusNoContent {
			t.Fatalf("DELETE: status %d: %s", status, text)
		}
	}
	for _, key := range []engine.ChatKey{coordinatorChat, otherChat} {
		want := []string{"You turned on the drain.", "You turned off the drain."}
		if got := records(t, server, key); !reflect.DeepEqual(got, want) {
			t.Errorf("records of %s after the cancel = %q, want %q", key.Organization, got, want)
		}
	}
}

func TestTheResumeOfAHarnessWithNoPauseAddsNoRecord(t *testing.T) {
	t.Parallel()
	server := startWithTasks(t, testkit.NewFakeGitHub(t))

	if status, text := send(t, server, http.MethodPost, "/api/pauses/devin/resume", ""); status != http.StatusNoContent {
		t.Fatalf("status %d: %s", status, text)
	}

	if got := records(t, server, coordinatorChat); len(got) != 0 {
		t.Errorf("records = %q", got)
	}
}

func TestASaveOfAMemoryFileAddsOneRecordAndTheSameTextAddsNone(t *testing.T) {
	t.Parallel()
	server := startWithTasks(t, testkit.NewFakeGitHub(t))
	body := `{"text": "One.\n", "base_version": 0}`

	if status, text := send(t, server, http.MethodPut, "/api/repositories/owner/shop/memory/shared", body); status != http.StatusNoContent {
		t.Fatalf("first PUT: status %d: %s", status, text)
	}
	if status, text := send(t, server, http.MethodPut, "/api/repositories/owner/missing/memory/shared", body); status != http.StatusNotFound {
		t.Fatalf("PUT to an unknown repository: status %d: %s", status, text)
	}
	want := []string{"You saved the memory file shared of owner/shop."}
	if got := records(t, server, coordinatorChat); !reflect.DeepEqual(got, want) {
		t.Fatalf("records after the first PUT = %q, want %q", got, want)
	}

	if status, text := send(t, server, http.MethodPut, "/api/repositories/owner/shop/memory/shared", `{"text": "One.\n", "base_version": 1}`); status != http.StatusNoContent {
		t.Fatalf("second PUT: status %d: %s", status, text)
	}

	if got := records(t, server, coordinatorChat); !reflect.DeepEqual(got, want) {
		t.Errorf("records after the second PUT = %q, want %q", got, want)
	}
}
