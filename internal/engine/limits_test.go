package engine_test

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/testserver"
)

// The first prompt hits the usage limit, and the same prompt again does the work.
const usageLimit = `
[[prompts]]
error = { code = -32011, message = "Rate limited", data = { retryAfterSeconds = 3600 } }

[[prompts]]
reply = ["Done."]
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
	agent := start(t, server, implementerSpec(t, server, 41))
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
	reason := "paused until " + until.UTC().Format("2006-01-02 15:04 UTC")
	testkit.WaitFor(t, func() bool { return session(t, server, agent.ID()).QueueReason.String == reason })
	// The pause also holds a Worker of the paused Harness in the queue.
	second := startLater(t.Context(), server, implementerSpec(t, server, 43))
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
