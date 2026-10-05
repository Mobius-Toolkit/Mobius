package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
)

// The Rust version writes each time column in RFC 3339 of the time crate in UTC, with the fraction of a second only
// when it is not zero, and with no trailing zero.

func TestARestartReadsTheQueueTimeAndThePauseThatTheRustVersionWrote(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	const prompt = "You are the Implementer of one task. Store plans in cents."
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) {
		fake.AddIssue(shop, 41, "Add plan model")
		fake.AddSubIssue(shop, 12, 41)
		fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
		seed(t, cfg.DataDir,
			`INSERT INTO inbox_items (id, kind, organization, repository, workstream, issue, text, link, time)
			 VALUES (5, 'usage limit', 'owner', '', 0, 0, 'Devin reached its usage limit.', '', '2026-10-04T10:00:00.5Z')`,
			`INSERT INTO harness_pauses (harness, paused_until, inbox_item) VALUES ('devin', '2099-01-01T00:00:00.123456789Z', 5)`,
			`INSERT INTO tasks (id, repository, issue, workstream, state, dispatched_at, queued_at, worker, worker_input)
			 VALUES (1, 'owner/shop', 41, 12, 'queued', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00.123456789Z', 'implementer', '`+prompt+`')`)
	})

	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) == 0 {
			return store.Session{}, false
		}
		return sessions[0], sessions[0].QueueReason.Valid
	})
	if session.QueueReason.String != "paused until 2099-01-01 00:00 UTC" || session.EndedAt.Valid {
		t.Errorf("session = %+v", session)
	}
}

func TestTheJudgeReadsTheTimeOfTheLastItemThatTheRustVersionWrote(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := connectJudge(t, fake, "shell = \"true\"\n", "", func(cfg *config.Config) { cfg.ReviewQuietPeriod = 500 * time.Millisecond })
	fake.AddComment(shop, 42, "owner", "Old comment.")
	judged := fake.Now().UTC().Format("2006-01-02T15:04:05") + ".5Z"
	if _, err := server.DB.Exec("UPDATE tasks SET judged_at = ? WHERE issue = 41", judged); err != nil {
		t.Fatal(err)
	}

	fake.AddComment(shop, 42, "owner", "New comment.")

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		prompts := judgePrompts(t, server)
		return strings.Join(prompts, ""), len(prompts) > 0
	})
	if !strings.Contains(prompt, "New comment.") || strings.Contains(prompt, "Old comment.") {
		t.Errorf("prompt = %s", prompt)
	}
}
