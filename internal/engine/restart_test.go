package engine_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
)

func TestARestartEndsTheSessionsOfTheEarlierRunAndTheHousekeeperRemovesTheirDirectories(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	var scratch string
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) {
		// The store of a server that stopped during a turn of the Implementer.
		seed(t, cfg.DataDir,
			`INSERT INTO tasks (id, repository, issue, workstream, state, dispatched_at, queued_at, worker, worker_input)
			 VALUES (1, 'owner/shop', 41, 12, 'working', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z', 'implementer', 'You are the Implementer of one task.')`,
			`INSERT INTO sessions (id, role, harness, model, organization, repository, workstream, issue, parent, started_at)
			 VALUES (1, 'lead_event', 'claude-code', 'sonnet', 'owner', 'owner/shop', 12, NULL, NULL, '2026-10-04T10:00:00Z'),
			        (2, 'implementer', 'devin', 'swe-1.5', 'owner', 'owner/shop', 12, 41, 1, '2026-10-04T10:00:00Z')`,
			`INSERT INTO transcript (session, time, kind, json) VALUES (2, '2026-10-04T10:00:00Z', 'prompt', '{"text":"You are the Implementer of one task."}')`)
		scratch = filepath.Join(cfg.DataDir, "scratch", "2")
		if err := os.MkdirAll(scratch, 0o750); err != nil {
			t.Fatal(err)
		}
	})

	var reasons []string
	for _, node := range tree(t, server) {
		reasons = append(reasons, node.Session.EndReason.String)
	}
	if !reflect.DeepEqual(reasons, []string{"restart", "restart"}) {
		t.Errorf("end reasons = %q", reasons)
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
