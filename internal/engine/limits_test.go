package engine_test

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// The first prompt hits the usage limit, and the same prompt again does the work.
const usageLimit = `
[[prompts]]
error = { code = -32011, message = "Rate limited", data = { retryAfterSeconds = 3600 } }

[[prompts]]
reply = ["Done."]
`

// The first prompt hits the usage limit of Claude Code, and the same prompt again and the next prompt do the work.
const claudeCodeLimit = `
[[prompts]]
error = { code = -32603, message = "Rate limited", data = { errorKind = "rate_limit" } }

[[prompts]]
reply = ["Noted."]

[[prompts]]
reply = ["Noted."]
`

// seed runs the SQL statements on the database in dataDir, as a server of an earlier run.
func seed(t *testing.T, dataDir string, statements ...string) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(dataDir, "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

type pause struct {
	until     string
	inboxItem int64
}

// devinPause gives the pause of Devin, and false when Devin has no pause.
func devinPause(t *testing.T, server *testserver.Server) (pause, bool) {
	t.Helper()
	var found pause
	err := server.DB.QueryRow("SELECT paused_until, inbox_item FROM harness_pauses WHERE harness = 'devin'").Scan(&found.until, &found.inboxItem)
	if errors.Is(err, sql.ErrNoRows) {
		return pause{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return found, true
}

func dismissed(t *testing.T, server *testserver.Server, item int64) bool {
	t.Helper()
	var dismissedAt sql.NullString
	if err := server.DB.QueryRow("SELECT dismissed_at FROM inbox_items WHERE id = ?", item).Scan(&dismissedAt); err != nil {
		t.Fatal(err)
	}
	return dismissedAt.Valid
}

func TestAUsageLimitPausesTheHarnessUntilResumeNowSendsThePromptAgain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageLimit)
	changes := listen(t, server)
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	before := time.Now()
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Store plans in cents.") }()

	var item struct {
		id               int64
		kind, text, link string
		workstream       int64
	}
	testkit.WaitFor(t, func() bool {
		err := server.DB.QueryRow("SELECT id, kind, text, link, workstream FROM inbox_items").Scan(&item.id, &item.kind, &item.text, &item.link, &item.workstream)
		return err == nil
	})
	if item.kind != "usage limit" || !strings.HasPrefix(item.text, "devin reached a usage limit. Mobius sends the prompt again at ") || item.workstream != 12 {
		t.Errorf("item = %+v", item)
	}
	// Mobius adds the Inbox item before the pause.
	found := testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })
	until := parseTime(t, found.until)
	if found.inboxItem != item.id || until.Before(before.Add(59*time.Minute)) || until.After(time.Now().Add(61*time.Minute)) {
		t.Errorf("pause = %+v", found)
	}
	var message string
	if err := server.DB.QueryRow("SELECT text FROM chat_messages WHERE author = 'Mobius' AND workstream = 12").Scan(&message); err != nil || message != item.text {
		t.Errorf("chat message = %q: %v", message, err)
	}
	if got := inbox(t, server); len(got) != 1 || got[0].ID != item.id {
		t.Errorf("inbox = %+v", got)
	}
	waitForChange(t, changes, func(change engine.Change) bool { return change.Inbox != nil && change.Inbox.ID == item.id })
	waitForChange(t, changes, func(change engine.Change) bool { return change.Message != nil && change.Message.Author == "Mobius" })
	reason := "paused until " + until.UTC().Format("2006-01-02 15:04 UTC")
	testkit.WaitFor(t, func() bool { return session(t, server, agent.ID()).QueueReason.String == reason })
	// The pause also holds a Worker of the paused Harness in the queue.
	second := startLater(t.Context(), server, implementerSpec(t, server, fake, 43))
	testkit.WaitFor(t, func() bool {
		return len(tree(t, server)) == 2 && tree(t, server)[1].Session.QueueReason.String == reason
	})

	response, err := server.Client.Post(server.URL+"/api/inbox/"+strconv.FormatInt(item.id, 10)+"/resume", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("resume: status %d", response.StatusCode)
	}

	if err := <-prompted; err != nil {
		t.Fatal(err)
	}
	end(t, await(t, second), "done")
	if _, ok := devinPause(t, server); ok || !dismissed(t, server, item.id) {
		t.Error("the pause did not end")
	}
	if got := session(t, server, agent.ID()); got.QueueReason.Valid {
		t.Errorf("session = %+v", got)
	}
	var prompts []string
	for _, row := range rows(t, server, agent.ID(), "prompt") {
		prompts = append(prompts, row["text"].(string))
	}
	if !reflect.DeepEqual(prompts, []string{"Store plans in cents.", "Store plans in cents."}) {
		t.Errorf("prompts = %q", prompts)
	}
	if text := reply(t, server, agent.ID()); text != "Done." {
		t.Errorf("reply = %q", text)
	}
}

func TestAPauseOfTheEarlierRunEndsAtItsTime(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	until := time.Now().Add(300 * time.Millisecond).UTC().Format(time.RFC3339Nano)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) {
		seed(t, cfg.DataDir,
			`INSERT INTO inbox_items (id, kind, organization, repository, workstream, issue, text, link, time)
			 VALUES (5, 'usage limit', 'owner', 'owner/shop', 12, 12, 'devin reached a usage limit.', '', '2026-10-04T10:00:00Z')`,
			`INSERT INTO harness_pauses (harness, paused_until, inbox_item) VALUES ('devin', '`+until+`', 5)`)
	})

	// The end of the pause also closes its Inbox item.
	testkit.WaitFor(t, func() bool {
		_, paused := devinPause(t, server)
		return !paused && dismissed(t, server, 5)
	})
}

func TestAUsageLimitOfTheImplementerHoldsThePullRequestUntilResume(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	limited := "[[prompts]]\nerror = { code = -32011, message = \"Rate limited\", data = { retryAfterSeconds = 3600 } }\n\n" + commits
	server, _ := connectTask(t, fake, leadStarts, limited, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	item := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range inbox(t, server) {
			if item.Kind == "usage limit" {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	testkit.WaitFor(t, func() bool { _, paused := devinPause(t, server); return paused })
	if len(fake.PullRequests(shop)) != 0 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}

	if err := server.Engine.Resume(t.Context(), item.ID); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	if _, paused := devinPause(t, server); paused || !dismissed(t, server, item.ID) {
		t.Errorf("paused = %v, dismissed = %v", paused, dismissed(t, server, item.ID))
	}
	prompts := promptTexts(t, server, roleSessions(t, server, engine.ImplementerRole)[0].ID)
	if len(prompts) != 2 || prompts[0] != prompts[1] {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestASuccessfulPromptEndsThePauseOfItsHarness(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Done.\"]\n")
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	for _, statement := range []string{
		`INSERT INTO inbox_items (id, kind, organization, repository, workstream, issue, text, link, time)
		 VALUES (5, 'usage limit', 'owner', 'owner/shop', 12, 12, 'devin reached a usage limit.', '', '2026-10-04T10:00:00Z')`,
		`INSERT INTO harness_pauses (harness, paused_until, inbox_item) VALUES ('devin', '2099-01-01T00:00:00Z', 5)`,
	} {
		if _, err := server.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := agent.Prompt(t.Context(), "Store plans in cents."); err != nil {
		t.Fatal(err)
	}

	if _, paused := devinPause(t, server); paused || !dismissed(t, server, 5) {
		t.Errorf("paused = %v, dismissed = %v", paused, dismissed(t, server, 5))
	}
}

func TestASuccessfulPromptKeepsAPauseThatStartedDuringThePrompt(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	dir := t.TempDir()
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"touch "+started+"; until [ -f "+release+" ]; do sleep 0.05; done\"\n")
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	done := make(chan error, 1)
	go func() { done <- agent.Prompt(t.Context(), "Store plans in cents.") }()
	testkit.WaitFor(t, func() bool { _, err := os.Stat(started); return err == nil })
	for _, statement := range []string{
		`INSERT INTO inbox_items (id, kind, organization, repository, workstream, issue, text, link, time)
		 VALUES (5, 'usage limit', 'owner', 'owner/shop', 12, 12, 'devin reached a usage limit.', '', '2026-10-04T10:00:00Z')`,
		`INSERT INTO harness_pauses (harness, paused_until, inbox_item) VALUES ('devin', '2099-01-01T00:00:00Z', 5)`,
	} {
		if _, err := server.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if _, paused := devinPause(t, server); !paused || dismissed(t, server, 5) {
		t.Errorf("paused = %v, dismissed = %v", paused, dismissed(t, server, 5))
	}
}

func TestAnEventForAPausedLeadGoesToThatLeadAfterThePause(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the API")
	item := testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range inbox(t, server) {
			if item.Kind == "usage limit" {
				return item, true
			}
		}
		return inboxItem{}, false
	})
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == 1 })
	if got := chatSessions(t, server, leadChat, engine.LeadRole); len(got) != 1 || len(promptTexts(t, server, got[0].ID)) != 1 {
		t.Errorf("sessions = %+v", got)
	}

	if err := server.Engine.Resume(t.Context(), item.ID); err != nil {
		t.Fatal(err)
	}

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := leadPrompts(t, server)
		return prompts, len(prompts) == 3 && eventDelivered(t, server)
	})
	if prompts[0] != prompts[1] || !strings.HasPrefix(prompts[2], "# Event\n\n") || !strings.Contains(prompts[2], " dispatch of #41 ") {
		t.Errorf("prompts = %q", prompts)
	}
	if got := chatSessions(t, server, leadChat, engine.LeadRole); len(got) != 1 {
		t.Errorf("sessions = %+v", got)
	}
}
