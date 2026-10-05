package engine_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func TestARestartStartsTheImplementerAgainAndTheHousekeeperRemovesTheDirectoriesOfTheEarlierRun(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	const prompt = "You are the Implementer of one task. Store plans in cents."
	var scratch string
	server, dataDir := connectWith(t, fake, "", func(cfg *config.Config) {
		fake.AddIssue(shop, 41, "Add plan model")
		fake.AddSubIssue(shop, 12, 41)
		fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
		testkit.InstallFakeHarness(t, cfg.DataDir, "devin", options+commits)
		// The store of a server that stopped during a turn of the Implementer.
		seed(t, cfg.DataDir,
			`INSERT INTO tasks (id, repository, issue, workstream, state, dispatched_at, queued_at, worker, worker_input)
			 VALUES (1, 'owner/shop', 41, 12, 'working', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z', 'implementer', '`+prompt+`')`,
			`INSERT INTO sessions (id, role, harness, model, organization, repository, workstream, issue, parent, started_at)
			 VALUES (1, 'lead_event', 'claude-code', 'sonnet', 'owner', 'owner/shop', 12, NULL, NULL, '2026-10-04T10:00:00Z'),
			        (2, 'implementer', 'devin', 'swe-1.5', 'owner', 'owner/shop', 12, 41, 1, '2026-10-04T10:00:00Z')`,
			`INSERT INTO transcript (session, time, kind, json) VALUES (2, '2026-10-04T10:00:00Z', 'prompt', '{"text":"`+prompt+`"}')`)
		scratch = filepath.Join(cfg.DataDir, "scratch", "2")
		if err := os.MkdirAll(scratch, 0o750); err != nil {
			t.Fatal(err)
		}
	})

	testkit.WaitFor(t, func() bool { return len(fake.PullRequests(shop)) == 1 })
	implementers := roleSessions(t, server, engine.ImplementerRole)
	if len(implementers) != 2 || implementers[0].ID != 2 || implementers[0].EndReason.String != "restart" || implementers[1].Parent.Int64 != 1 {
		t.Errorf("Implementers = %+v", implementers)
	}
	if prompts := promptTexts(t, server, implementers[1].ID); !reflect.DeepEqual(prompts, []string{prompt}) {
		t.Errorf("prompts = %q", prompts)
	}
	if head := testkit.Git(t, filepath.Join(dataDir, "worktrees", "owner", "shop", "task-41"), "branch", "--show-current"); head != "mobius/41" {
		t.Errorf("branch = %s", head)
	}
	testkit.WaitFor(t, func() bool {
		_, err := os.Stat(scratch)
		return os.IsNotExist(err)
	})
}

func TestAStartWithAnEmptyStoreHandsAWorkingIssueToAHuman(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)

	server, _ := connectWith(t, fake, "", func(*config.Config) {
		fake.AddIssue(shop, 41, "Add plan model")
		fake.AddSubIssue(shop, 12, 41)
		fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
	})

	var item struct {
		kind, text, link  string
		issue, workstream int64
	}
	if err := server.DB.QueryRow("SELECT kind, text, link, issue, workstream FROM inbox_items").Scan(&item.kind, &item.text, &item.link, &item.issue, &item.workstream); err != nil {
		t.Fatal(err)
	}
	want := struct {
		kind, text, link  string
		issue, workstream int64
	}{"stopped", "Mobius lost the state of this task. Add mobius:ready to start again.", "https://github.com/owner/shop/issues/41", 41, 12}
	if item != want {
		t.Errorf("item = %+v", item)
	}
	if labels := fake.Labels(shop, 41); !reflect.DeepEqual(labels, []string{"mobius:needs-human"}) {
		t.Errorf("labels = %q", labels)
	}
}

func TestARestartGivesTheWaitingEventToTheLead(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	seed(t, dataDir, `INSERT INTO lead_events (repository, workstream, issue, kind, payload, time) VALUES ('owner/shop', 12, 41, 'comment', 'A comment before the restart.', '2026-10-04T10:00:00Z')`)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nreply = [\"Seen\"]\n")

	server := startServer(t, fake, dataDir, "")

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool { return strings.Contains(prompt, "A comment before the restart.") })
	})
}

func TestARestartStartsTheJudgeAgainBelowTheParentOfTheEndedJudge(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddLabel(shop, 41, "mobius:working", testkit.AppSlug+"[bot]")
	testkit.InstallFakeAgent(t, dataDir, options)
	// The store of a server that stopped during a turn of the Judge on the pull request #42.
	seed(t, dataDir,
		`INSERT INTO tasks (id, repository, issue, workstream, state, dispatched_at, queued_at, branch, pull_request, worker, worker_input)
		 VALUES (1, 'owner/shop', 41, 12, 'working', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z', 'mobius/41', 42, 'judge', 'reviewed')`,
		`INSERT INTO sessions (id, role, harness, model, organization, repository, workstream, issue, parent, started_at)
		 VALUES (1, 'lead_event', 'claude-code', 'sonnet', 'owner', 'owner/shop', 12, NULL, NULL, '2026-10-04T10:00:00Z'),
		        (2, 'implementer', 'devin', 'swe-1.5', 'owner', 'owner/shop', 12, 41, 1, '2026-10-04T10:00:00Z'),
		        (3, 'judge', 'claude-code', 'haiku', 'owner', 'owner/shop', 12, 41, 2, '2026-10-04T10:00:00Z')`)
	cfg := testserver.Config(t, dataDir)
	cfg.ReviewQuietPeriod = 200 * time.Millisecond

	server := startServerWith(t, fake, cfg, "")
	fake.PushCommit(shop, "mobius/41", "Add plan model")
	if number := fake.OpenPullRequest(shop, "Add plan model", "mobius/41"); number != 42 {
		t.Fatalf("pull request = %d", number)
	}
	fake.AddReviewComment(shop, 42, 0, "owner", "Use price_cents.")

	judges := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		judges := roleSessions(t, server, engine.JudgeRole)
		return judges, len(judges) == 2
	})
	if judges[0].ID != 3 || judges[0].EndReason.String != "restart" || judges[1].Parent.Int64 != 2 {
		t.Errorf("Judges = %+v", judges)
	}
}
