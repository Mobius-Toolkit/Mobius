package engine_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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

// The first prompt of Claude Code sends the reset time in a usage update and then hits the usage limit with another
// reset time in the text. 4102444800 is 2100-01-01 00:00:00 UTC.
const claudeCodeLimitWithResetTime = `
[[prompts]]
updates = ['{"sessionUpdate": "usage_update", "used": 78345, "size": 1000000, "_meta": {"_claude/rateLimit": {"status": "rejected", "resetsAt": 4102444800}}}']
error = { code = -32603, message = "You've hit your session limit · resets 5:10pm (Europe/Warsaw)", data = { errorKind = "rate_limit" } }
`

// The first prompt of Claude Code sends the raw text of the usage limit after the usage update with the status
// "rejected", and then hits the usage limit.
const claudeCodeLimitWithRawText = `
[[prompts]]
updates = [
  '{"sessionUpdate": "usage_update", "used": 78345, "size": 1000000, "_meta": {"_claude/rateLimit": {"status": "rejected", "resetsAt": 4102444800}}}',
  '''{"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "You've hit your session limit · resets 5:10pm (Europe/Warsaw)"}}''',
]
error = { code = -32603, message = "You've hit your session limit · resets 5:10pm (Europe/Warsaw)", data = { errorKind = "rate_limit" } }
`

// The first prompt of Claude Code hits the usage limit, the same prompt again works, and the third prompt hits the
// usage limit again.
const claudeCodeLimitTwice = `
[[prompts]]
error = { code = -32603, message = "Rate limited", data = { errorKind = "rate_limit" } }

[[prompts]]
reply = ["Noted."]

[[prompts]]
error = { code = -32603, message = "Rate limited", data = { errorKind = "rate_limit" } }

[[prompts]]
reply = ["Noted."]
`

// The first prompt of Claude Code sends a usage update with the status "rejected" and overage in use, and then the
// reply of the turn.
const claudeCodeOverage = `
[[prompts]]
updates = ['{"sessionUpdate": "usage_update", "used": 78345, "size": 1000000, "_meta": {"_claude/rateLimit": {"status": "rejected", "isUsingOverage": true}}}']
reply = ["Planned on overage."]
`

// The first prompt of Claude Code sends a usage update with the status "rejected" and ends with a reply. Then an
// autonomous turn hits the usage limit and sends the raw text, with no prompt error.
const claudeCodeAutonomousLimit = `
[[prompts]]
updates = ['{"sessionUpdate": "usage_update", "used": 78345, "size": 1000000, "_meta": {"_claude/rateLimit": {"status": "rejected"}}}']
reply = ["Planned."]
later = { after = "500ms", updates = [
  '''{"sessionUpdate": "usage_update", "used": 78345, "size": 1000000, "_meta": {"_claude/rateLimit": {"status": "rejected"}}}''',
  '''{"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "You've hit your session limit"}}''',
] }
`

const rawLimitText = "You've hit your session limit"

// chatTexts gives the texts of the chat messages of the Workstream #12 with the author.
func chatTexts(t *testing.T, server *testserver.Server, author string) []string {
	t.Helper()
	rows, err := server.DB.Query("SELECT text FROM chat_messages WHERE workstream = 12 AND author = ? ORDER BY id", author)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var texts []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, text)
	}
	return texts
}

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

type apiPause struct {
	Harness     string    `json:"harness"`
	PausedUntil time.Time `json:"pausedUntil"`
}

// apiPauses gives the pauses through the API.
func apiPauses(t *testing.T, server *testserver.Server) []apiPause {
	t.Helper()
	status, text := send(t, server, http.MethodGet, "/api/pauses", "")
	var body struct {
		Data []apiPause `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil {
		t.Fatalf("pauses: status %d, %v: %s", status, err, text)
	}
	return body.Data
}

type pause struct {
	until string
}

// devinPause gives the pause of Devin, and false when Devin has no pause.
func devinPause(t *testing.T, server *testserver.Server) (pause, bool) {
	t.Helper()
	var found pause
	err := server.DB.QueryRow("SELECT paused_until FROM harness_pauses WHERE harness = 'devin'").Scan(&found.until)
	if errors.Is(err, sql.ErrNoRows) {
		return pause{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return found, true
}

func TestAUsageLimitPausesTheHarnessUntilResumeNowSendsThePromptAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageLimit)
	events := liveEvents(t, server, "pauses")
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	before := time.Now()
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Store plans in cents.", nil) }()

	found := testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })
	until := parseTime(t, found.until)
	if until.Before(before.Add(59*time.Minute)) || until.After(time.Now().Add(61*time.Minute)) {
		t.Errorf("pause = %+v", found)
	}
	want := "devin reached a usage limit. Mobius sends the prompt again at " + until.UTC().Format("2006-01-02 15:04 UTC") + "."
	testkit.WaitFor(t, func() bool { return reflect.DeepEqual(chatTexts(t, server, "Mobius"), []string{want}) })
	started := nextEventData(t, events, func(pauses []apiPause) bool { return len(pauses) == 1 })
	if started[0].Harness != "devin" || !started[0].PausedUntil.Equal(until) {
		t.Errorf("pauses event = %+v, want devin until %v", started, until)
	}
	if got := apiPauses(t, server); !reflect.DeepEqual(got, started) {
		t.Errorf("pauses = %+v, want %+v", got, started)
	}
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
	reason := "paused until " + until.UTC().Format("2006-01-02 15:04 UTC")
	testkit.WaitFor(t, func() bool { return session(t, server, agent.ID()).QueueReason.String == reason })
	// The pause also holds a Worker of the paused Harness in the queue.
	second := startLater(t.Context(), server, implementerSpec(t, server, fake, 43))
	testkit.WaitFor(t, func() bool {
		return len(tree(t, server)) == 2 && tree(t, server)[1].Session.QueueReason.String == reason
	})

	if status, text := send(t, server, http.MethodPost, "/api/pauses/devin/resume", ""); status != http.StatusNoContent {
		t.Fatalf("resume: status %d: %s", status, text)
	}

	nextEventData(t, events, func(pauses []apiPause) bool { return len(pauses) == 0 })
	if err := <-prompted; err != nil {
		t.Fatal(err)
	}
	end(t, await(t, second), "done")
	if _, ok := devinPause(t, server); ok {
		t.Error("the pause did not end")
	}
	if got := apiPauses(t, server); len(got) != 0 {
		t.Errorf("pauses = %+v", got)
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
	if status, text := send(t, server, http.MethodPost, "/api/pauses/devin/resume", ""); status != http.StatusNoContent {
		t.Errorf("resume of a Harness with no pause: status %d: %s", status, text)
	}
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
}

// The first two prompts hit the usage limit, the second one with a later reset, and the same prompt again does the work.
const usageLimitTwice = `
[[prompts]]
error = { code = -32011, message = "Rate limited", data = { retryAfterSeconds = 3600 } }

[[prompts]]
error = { code = -32011, message = "Rate limited", data = { retryAfterSeconds = 7200 } }

[[prompts]]
reply = ["Done."]
`

// The first prompt hits the usage limit of Devin and of Claude Code.
const usageLimitOfTwoHarnesses = `
[[prompts]]
error = { code = -32011, message = "Rate limited", data = { errorKind = "rate_limit", retryAfterSeconds = 3600 } }

[[prompts]]
reply = ["Done."]
`

func count(t *testing.T, server *testserver.Server, query string) int {
	t.Helper()
	var n int
	if err := server.DB.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestARetryThatHitsTheUsageLimitAgainPausesAgainWithNoSecondChatMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageLimitTwice)
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Store plans in cents.", nil) }()
	first := testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })

	if err := server.Engine.Resume(t.Context(), "devin"); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool {
		found, paused := devinPause(t, server)
		return paused && found.until != first.until
	})
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
	if n := count(t, server, "SELECT COUNT(*) FROM chat_messages WHERE author = 'Mobius'"); n != 1 {
		t.Errorf("%d chat messages", n)
	}

	if err := server.Engine.Resume(t.Context(), "devin"); err != nil {
		t.Fatal(err)
	}
	if err := <-prompted; err != nil {
		t.Fatal(err)
	}
}

func TestARetryThatHitsTheUsageLimitAfterARestartAddsNoChatMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	until := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	server, _ := connectWith(t, fake, usageLimit, func(cfg *config.Config) {
		seed(t, cfg.DataDir, `INSERT INTO harness_pauses (harness, paused_until) VALUES ('devin', '`+until+`')`)
	})
	testkit.WaitFor(t, func() bool { _, paused := devinPause(t, server); return !paused })
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Store plans in cents.", nil) }()

	testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })

	if n := count(t, server, "SELECT COUNT(*) FROM chat_messages WHERE author = 'Mobius'"); n != 0 {
		t.Errorf("%d chat messages", n)
	}

	if err := server.Engine.Resume(t.Context(), "devin"); err != nil {
		t.Fatal(err)
	}
	if err := <-prompted; err != nil {
		t.Fatal(err)
	}
}

func TestARetryThatHitsTheUsageLimitAfterARestartThatFollowsTheEndOfThePauseAddsNoChatMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	ended := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	server, _ := connectWith(t, fake, usageLimit, func(cfg *config.Config) {
		seed(t, cfg.DataDir, `INSERT INTO harness_pause_ends (harness, ended_at) VALUES ('devin', '`+ended+`')`)
	})
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Store plans in cents.", nil) }()

	testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })

	if n := count(t, server, "SELECT COUNT(*) FROM chat_messages WHERE author = 'Mobius'"); n != 0 {
		t.Errorf("%d chat messages", n)
	}

	if err := server.Engine.Resume(t.Context(), "devin"); err != nil {
		t.Fatal(err)
	}
	if err := <-prompted; err != nil {
		t.Fatal(err)
	}
}

func TestAUsageLimitOfAnotherHarnessGetsItsOwnPause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, usageLimitOfTwoHarnesses, keepSessionOpen)
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	go func() { _ = agent.Prompt(t.Context(), "Store plans in cents.", nil) }()
	testkit.WaitFor(t, func() bool { _, paused := devinPause(t, server); return paused })

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
	if got := apiPauses(t, server); len(got) != 2 || got[0].Harness != "claude-code" || got[1].Harness != "devin" {
		t.Errorf("pauses = %+v", got)
	}
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
}

func TestAPauseOfTheEarlierRunEndsAtItsTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	until := time.Now().Add(300 * time.Millisecond).UTC().Format(time.RFC3339Nano)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) {
		seed(t, cfg.DataDir, `INSERT INTO harness_pauses (harness, paused_until) VALUES ('devin', '`+until+`')`)
	})

	testkit.WaitFor(t, func() bool { _, paused := devinPause(t, server); return !paused })
}

func TestAUsageLimitOfTheImplementerHoldsThePullRequestUntilResume(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	limited := "[[prompts]]\nerror = { code = -32011, message = \"Rate limited\", data = { retryAfterSeconds = 3600 } }\n\n" + commits
	server, _ := connectTask(t, fake, leadStarts, limited, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool { _, paused := devinPause(t, server); return paused })
	if len(fake.PullRequests(shop)) != 0 {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}

	if err := server.Engine.Resume(t.Context(), "devin"); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	if _, paused := devinPause(t, server); paused {
		t.Error("the pause did not end")
	}
	prompts := promptTexts(t, server, roleSessions(t, server, engine.ImplementerRole)[0].ID)
	if len(prompts) != 2 || prompts[0] != prompts[1] {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestASuccessfulPromptEndsThePauseOfItsHarness(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Done.\"]\n")
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	for _, statement := range []string{
		`INSERT INTO harness_pauses (harness, paused_until) VALUES ('devin', '2099-01-01T00:00:00Z')`,
	} {
		if _, err := server.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := agent.Prompt(t.Context(), "Store plans in cents.", nil); err != nil {
		t.Fatal(err)
	}

	if _, paused := devinPause(t, server); paused {
		t.Error("the pause did not end")
	}
}

func TestASuccessfulPromptKeepsAPauseThatStartedDuringThePrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dir := t.TempDir()
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	server, _ := connect(t, fake, "[[prompts]]\nshell = \"touch "+started+"; until [ -f "+release+" ]; do sleep 0.05; done\"\n")
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, agent, "done")
	done := make(chan error, 1)
	go func() { done <- agent.Prompt(t.Context(), "Store plans in cents.", nil) }()
	testkit.WaitFor(t, func() bool { _, err := os.Stat(started); return err == nil })
	for _, statement := range []string{
		`INSERT INTO harness_pauses (harness, paused_until) VALUES ('devin', '2099-01-01T00:00:00Z')`,
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

	if _, paused := devinPause(t, server); !paused {
		t.Error("the pause ended")
	}
}

func TestAnEventForAPausedLeadGoesToThatLeadAfterThePause(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimit, keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the API")
	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == 1 })
	if got := chatSessions(t, server, leadChat, engine.LeadRole); len(got) != 1 || len(promptTexts(t, server, got[0].ID)) != 1 {
		t.Errorf("sessions = %+v", got)
	}

	if err := server.Engine.Resume(t.Context(), "claude-code"); err != nil {
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

func TestAClaudeCodePauseEndsAtTheResetTimeOfTheUsageUpdate(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimitWithResetTime, keepSessionOpen)

	sendChat(t, server, leadChat, "Plan the API")

	until := testkit.WaitForValue(t, func() (string, bool) {
		var until string
		err := server.DB.QueryRow("SELECT paused_until FROM harness_pauses WHERE harness = 'claude-code'").Scan(&until)
		return until, err == nil
	})
	if got := parseTime(t, until); !got.Equal(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("until = %v", got)
	}
}

func TestAClaudeCodeUsageLimitShowsTheMobiusMessageAndNotTheRawText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimitWithRawText, keepSessionOpen)

	sendChat(t, server, leadChat, "Plan the API")

	want := "claude-code reached a usage limit. Mobius sends the prompt again at 2100-01-01 00:00 UTC."
	testkit.WaitFor(t, func() bool { return len(chatTexts(t, server, "Mobius")) == 1 })
	if got := chatTexts(t, server, "Mobius"); got[0] != want {
		t.Errorf("chat = %q, want %q", got, want)
	}
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
	lead := chatSessions(t, server, leadChat, engine.LeadRole)[0].ID
	if got := chatTexts(t, server, "Lead"); len(got) != 0 {
		t.Errorf("Lead chat messages = %q", got)
	}
	if got := reply(t, server, lead); !strings.Contains(got, rawLimitText) {
		t.Errorf("the Transcript reply = %q", got)
	}
}

func TestTheChatOfALeadThatWaitsForAPauseGivesTheEndOfThePauseUntilThePauseEnds(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimitWithRawText, keepSessionOpen)
	chats := liveEvents(t, server, "chat")

	sendChat(t, server, leadChat, "Plan the API")

	want := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	paused := nextEventData(t, chats, func(state chatState) bool { return state.PausedUntil != nil })
	if !paused.Writing || !paused.PausedUntil.Equal(want) {
		t.Errorf("event = %+v", paused)
	}
	view := chatView(t, server, leadChat)
	if !view.Writing || view.PausedUntil == nil || !view.PausedUntil.Equal(want) {
		t.Errorf("view = %+v", view)
	}

	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
	if err := server.Engine.Resume(t.Context(), "claude-code"); err != nil {
		t.Fatal(err)
	}

	nextEventData(t, chats, func(state chatState) bool { return state.PausedUntil == nil })
	if view := chatView(t, server, leadChat); view.PausedUntil != nil {
		t.Errorf("view = %+v", view)
	}
}

func TestALeadThatHitsAPauseOfAnotherSessionGetsTheMobiusMessageInItsChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimitWithRawText, keepSessionOpen)
	for _, statement := range []string{
		`INSERT INTO harness_pauses (harness, paused_until) VALUES ('claude-code', '2099-01-01T00:00:00Z')`,
	} {
		if _, err := server.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	sendChat(t, server, leadChat, "Plan the API")

	want := []string{"claude-code reached a usage limit. Mobius sends the prompt again at 2099-01-01 00:00 UTC."}
	testkit.WaitFor(t, func() bool { return len(chatTexts(t, server, "Mobius")) == 1 })
	if got := chatTexts(t, server, "Mobius"); !reflect.DeepEqual(got, want) {
		t.Errorf("chat = %q, want %q", got, want)
	}
	if got := chatTexts(t, server, "Lead"); len(got) != 0 {
		t.Errorf("Lead chat messages = %q", got)
	}
}

func TestALeadThatHitsTheUsageLimitAgainAfterThePauseGetsTheMobiusMessageAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimitTwice, keepSessionOpen)
	sendChat(t, server, leadChat, "Plan the API")
	testkit.WaitFor(t, func() bool { return len(chatTexts(t, server, "Mobius")) == 1 })
	testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
	if err := server.Engine.Resume(t.Context(), "claude-code"); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return len(chatTexts(t, server, "Lead")) == 1 })
	testkit.WaitFor(t, func() bool { return !claudeCodePaused(t, server) })

	sendChat(t, server, leadChat, "Plan the UI")

	testkit.WaitFor(t, func() bool { return len(chatTexts(t, server, "Mobius")) == 2 })
	if got := inbox(t, server); len(got) != 0 {
		t.Errorf("inbox = %+v", got)
	}
}

func TestALeadThatHitsAPauseWithItsMessageAlreadyInTheChatGetsNoSecondMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeLimitWithRawText, keepSessionOpen)
	for _, statement := range []string{
		`INSERT INTO harness_pauses (harness, paused_until) VALUES ('claude-code', '2099-01-01T00:00:00Z')`,
		`INSERT INTO chat_messages (organization, repository, workstream, author, time, text)
		 VALUES ('owner', 'owner/shop', 12, 'Mobius', '2026-10-04T10:00:00Z', 'claude-code reached a usage limit. Mobius sends the prompt again at 2099-01-01 00:00 UTC.')`,
	} {
		if _, err := server.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return len(chatSessions(t, server, leadChat, engine.LeadRole)) == 1 })
	lead := chatSessions(t, server, leadChat, engine.LeadRole)[0].ID
	testkit.WaitFor(t, func() bool {
		var reason sql.NullString
		if err := server.DB.QueryRow("SELECT queue_reason FROM sessions WHERE id = ?", lead).Scan(&reason); err != nil {
			t.Fatal(err)
		}
		return strings.HasPrefix(reason.String, "paused until ")
	})
	if got := chatTexts(t, server, "Mobius"); len(got) != 1 {
		t.Errorf("chat = %q", got)
	}
}

func TestAClaudeCodeReplyWithOverageInUseShowsInTheChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeOverage, keepSessionOpen)

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return reflect.DeepEqual(chatTexts(t, server, "Lead"), []string{"Planned on overage."}) })
}

func TestTheRawUsageLimitTextOfAnAutonomousTurnShowsInTheChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeAutonomousLimit, keepSessionOpen)

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool {
		texts := chatTexts(t, server, "Lead")
		return len(texts) > 0 && strings.Contains(texts[len(texts)-1], rawLimitText)
	})
}

func claudeCodePaused(t *testing.T, server *testserver.Server) bool {
	t.Helper()
	var n int
	if err := server.DB.QueryRow("SELECT COUNT(*) FROM harness_pauses WHERE harness = 'claude-code'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// The first prompt of Claude Code sends the raw text of the usage limit with no usage update before it, and then hits
// the usage limit. The same prompt again does the work.
const claudeCodeLimitTextOnly = `
[[prompts]]
updates = ['''{"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "You've hit your session limit · resets 5:10pm (Europe/Warsaw)"}}''']
error = { code = -32603, message = "Rate limited", data = { errorKind = "rate_limit" } }

[[prompts]]
reply = ["Noted."]
`

// The same as claudeCodeLimitWithRawText, and the same prompt again does the work.
const claudeCodeLimitWithRawTextThenWork = claudeCodeLimitWithRawText + `
[[prompts]]
reply = ["Noted."]
`

// ownerMessageDelivered gives the delivery time of the first message of the Owner in the chat of the Workstream #12,
// and false when the message has no delivery time.
func ownerMessageDelivered(t *testing.T, server *testserver.Server) bool {
	t.Helper()
	var deliveredAt sql.NullString
	if err := server.DB.QueryRow("SELECT delivered_at FROM chat_messages WHERE workstream = 12 AND author = 'Owner' ORDER BY id LIMIT 1").Scan(&deliveredAt); err != nil {
		t.Fatal(err)
	}
	return deliveredAt.Valid
}

func TestAUsageLimitTurnShowsOnlyTheMobiusMessageAndDeliversTheOwnerMessageAfterThePause(t *testing.T) {
	t.Parallel()
	for name, script := range map[string]string{
		"a chunk after the usage update": claudeCodeLimitWithRawTextThenWork,
		"a chunk with no usage update":   claudeCodeLimitTextOnly,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := connectWith(t, fake, script, keepSessionOpen)

			sendChat(t, server, leadChat, "Plan the API")

			testkit.WaitFor(t, func() bool { return len(chatTexts(t, server, "Mobius")) == 1 })
			testkit.WaitFor(t, func() bool { return claudeCodePaused(t, server) })
			if got := chatTexts(t, server, "Lead"); len(got) != 0 {
				t.Errorf("Lead chat messages = %q", got)
			}
			if ownerMessageDelivered(t, server) {
				t.Error("the Owner message has a delivery time while the turn waits for the end of the pause")
			}
			lead := chatSessions(t, server, leadChat, engine.LeadRole)[0].ID
			if got := reply(t, server, lead); !strings.Contains(got, rawLimitText) {
				t.Errorf("the Transcript reply = %q", got)
			}

			if err := server.Engine.Resume(t.Context(), "claude-code"); err != nil {
				t.Fatal(err)
			}

			testkit.WaitFor(t, func() bool { return ownerMessageDelivered(t, server) })
			testkit.WaitFor(t, func() bool { return reflect.DeepEqual(chatTexts(t, server, "Lead"), []string{"Noted."}) })
			if got := chatTexts(t, server, "Mobius"); len(got) != 1 {
				t.Errorf("Mobius chat messages = %q", got)
			}
		})
	}
}

func TestATurnWithNoUsageLimitDeliversTheOwnerMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, claudeCodeOverage, keepSessionOpen)

	sendChat(t, server, leadChat, "Plan the API")

	testkit.WaitFor(t, func() bool { return ownerMessageDelivered(t, server) })
}
