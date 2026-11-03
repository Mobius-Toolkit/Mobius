package engine_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

type antigravityEntry struct {
	idx  int64
	data []byte
}

func makeAntigravityData(input, output, cacheRead int64, model string) []byte {
	var b4 []byte
	if input >= 0 {
		b4 = protowire.AppendTag(b4, 2, protowire.VarintType)
		b4 = protowire.AppendVarint(b4, uint64(input))
	}

	if output >= 0 {
		b4 = protowire.AppendTag(b4, 3, protowire.VarintType)
		b4 = protowire.AppendVarint(b4, uint64(output))
	}

	if cacheRead > 0 {
		b4 = protowire.AppendTag(b4, 5, protowire.VarintType)
		b4 = protowire.AppendVarint(b4, uint64(cacheRead))
	}

	var b1 []byte
	b1 = protowire.AppendTag(b1, 4, protowire.BytesType)
	b1 = protowire.AppendBytes(b1, b4)

	if model != "" {
		b1 = protowire.AppendTag(b1, 21, protowire.BytesType)
		b1 = protowire.AppendString(b1, model)
	}

	var top []byte
	top = protowire.AppendTag(top, 1, protowire.BytesType)
	top = protowire.AppendBytes(top, b1)

	return top
}

func writeAntigravityDB(t *testing.T, path string, entries ...antigravityEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS gen_metadata (idx integer, data blob, size integer NOT NULL DEFAULT 0, PRIMARY KEY (idx))`); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := db.Exec(`INSERT OR REPLACE INTO gen_metadata (idx, data, size) VALUES (?, ?, ?)`, e.idx, e.data, len(e.data)); err != nil {
			t.Fatal(err)
		}
	}
}

// Serial: it sets HOME, which os.UserHomeDir and the reader of the Antigravity conversation database read.
func TestAnAntigravityTurnReadsTheTokensOfItsOwnDatabaseRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := testkit.NewFakeGitHub(t)
	model := "gemini-3-pro"
	script := options + `
[[prompts]]
reply = ["Done."]

[[prompts]]
reply = ["Done again."]
`
	server, _ := connectScript(t, fake, script, func(cfg *config.Config) {
		cfg.Roles.Researcher = config.RoleBinding{Harness: config.Antigravity, Model: model, Max: 2}
	})
	spec := leadSpec(t)
	spec.Role = engine.ResearcherRole
	agent := start(t, server, spec)

	var acpSession sql.NullString
	if err := server.DB.QueryRowContext(t.Context(), `SELECT acp_session_id FROM sessions WHERE id = ?`, agent.ID()).Scan(&acpSession); err != nil {
		t.Fatal(err)
	}
	if !acpSession.Valid {
		t.Fatal("the session has no ACP session id")
	}
	dbPath := engine.AntigravityDBPath(engine.AntigravityDir(), acpSession.String)
	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 0, data: makeAntigravityData(100, 10, 1000, "gemini-3.8-flash-high")},
		antigravityEntry{idx: 1, data: makeAntigravityData(20, 10, 0, "gemini-3.8-flash-high")},
	)
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 2, data: makeAntigravityData(30, 3, 200, "gemini-3.8-flash-pro")},
	)
	if err := agent.Prompt(t.Context(), "Two", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}

	rows := usageRows(t, server, agent.ID())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	first, second := rows[0], rows[1]
	if first.Harness != "antigravity" || first.Model != model || first.ReportedModel.String != "gemini-3.8-flash-high" {
		t.Errorf("first = %+v", first)
	}
	if first.Input != nullInt(120) || first.Output != nullInt(20) || first.CacheRead != nullInt(1000) || first.CacheWrite.Valid {
		t.Errorf("first tokens = %+v", first)
	}
	if first.Cost.Valid {
		t.Errorf("first cost = %+v", first.Cost)
	}
	if second.ReportedModel.String != "gemini-3.8-flash-pro" {
		t.Errorf("second = %+v", second)
	}
	if second.Input != nullInt(30) || second.Output != nullInt(3) || second.CacheRead != nullInt(200) || second.CacheWrite.Valid {
		t.Errorf("second tokens = %+v", second)
	}
	if second.Cost.Valid {
		t.Errorf("second cost = %+v", second.Cost)
	}
}

// Serial: it sets HOME, which os.UserHomeDir and the reader of the Antigravity conversation database read.
func TestAnAntigravityTurnWithNoDatabaseGivesNoTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := testkit.NewFakeGitHub(t)
	model := "gemini-3-pro"
	script := options + `
[[prompts]]
reply = ["Done."]
`
	server, _ := connectScript(t, fake, script, func(cfg *config.Config) {
		cfg.Roles.Researcher = config.RoleBinding{Harness: config.Antigravity, Model: model, Max: 2}
	})
	spec := leadSpec(t)
	spec.Role = engine.ResearcherRole
	agent := start(t, server, spec)
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}

	rows := usageRows(t, server, agent.ID())
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Input.Valid || rows[0].Output.Valid || rows[0].CacheRead.Valid || rows[0].ReportedModel.Valid {
		t.Errorf("row = %+v, want no tokens and no model", rows[0])
	}
}

// Serial: it sets HOME, which os.UserHomeDir and the reader of the Antigravity conversation database read.
func TestAnAntigravityTurnAfterInvalidBlobGetsTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := testkit.NewFakeGitHub(t)
	model := "gemini-3-pro"
	script := options + `
[[prompts]]
reply = ["Done."]

[[prompts]]
reply = ["Done again."]
`
	server, _ := connectScript(t, fake, script, func(cfg *config.Config) {
		cfg.Roles.Researcher = config.RoleBinding{Harness: config.Antigravity, Model: model, Max: 2}
	})
	spec := leadSpec(t)
	spec.Role = engine.ResearcherRole
	agent := start(t, server, spec)

	var acpSession sql.NullString
	if err := server.DB.QueryRowContext(t.Context(), `SELECT acp_session_id FROM sessions WHERE id = ?`, agent.ID()).Scan(&acpSession); err != nil {
		t.Fatal(err)
	}
	dbPath := engine.AntigravityDBPath(engine.AntigravityDir(), acpSession.String)
	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 0, data: []byte("broken protobuf not valid")},
	)
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}

	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 1, data: makeAntigravityData(50, 5, 200, "gemini-3.8-flash-pro")},
	)
	if err := agent.Prompt(t.Context(), "Two", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}

	rows := usageRows(t, server, agent.ID())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Input.Valid || rows[0].Output.Valid || rows[0].CacheRead.Valid || rows[0].ReportedModel.Valid {
		t.Errorf("first turn = %+v, want no tokens and no model", rows[0])
	}
	if rows[1].Input != nullInt(50) || rows[1].Output != nullInt(5) || rows[1].CacheRead != nullInt(200) || rows[1].ReportedModel.String != "gemini-3.8-flash-pro" {
		t.Errorf("second turn = %+v, want tokens and model from row 1", rows[1])
	}
}

// Serial: it sets HOME, which os.UserHomeDir and the reader of the Antigravity conversation database read.
func TestAnAntigravityTurnWithNoNewRowGivesNoTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := testkit.NewFakeGitHub(t)
	model := "gemini-3-pro"
	script := options + `
[[prompts]]
reply = ["Done."]

[[prompts]]
reply = ["Done again."]
`
	server, _ := connectScript(t, fake, script, func(cfg *config.Config) {
		cfg.Roles.Researcher = config.RoleBinding{Harness: config.Antigravity, Model: model, Max: 2}
	})
	spec := leadSpec(t)
	spec.Role = engine.ResearcherRole
	agent := start(t, server, spec)

	var acpSession sql.NullString
	if err := server.DB.QueryRowContext(t.Context(), `SELECT acp_session_id FROM sessions WHERE id = ?`, agent.ID()).Scan(&acpSession); err != nil {
		t.Fatal(err)
	}
	dbPath := engine.AntigravityDBPath(engine.AntigravityDir(), acpSession.String)
	writeAntigravityDB(t, dbPath,
		antigravityEntry{idx: 0, data: makeAntigravityData(100, 10, 500, "gemini-3.8-flash-high")},
	)
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.Prompt(t.Context(), "Two", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}

	rows := usageRows(t, server, agent.ID())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Input != nullInt(100) || !rows[0].ReportedModel.Valid {
		t.Errorf("first = %+v", rows[0])
	}
	if rows[1].Input.Valid || rows[1].Output.Valid || rows[1].CacheRead.Valid || rows[1].ReportedModel.Valid {
		t.Errorf("second = %+v, want no tokens and no model", rows[1])
	}
}

// Serial: it sets HOME, which os.UserHomeDir and the reader of the Antigravity conversation database read.
func TestAContinuedAntigravityRoundCountsOnlyTheNewRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	// The linger of the first turn gives the test the time to write the conversation database of that turn before the
	// engine reads the database at the end of the turn.
	script := counting + options +
		"[[prompts]]\nlinger = \"3s\"\n" + commitCents +
		"\n[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo cents >> plan.txt && git commit -q -am 'Store plans in cents'\"\n"
	server, _ := connectTaskIn(t, fake, dataDir, keptLead, "", func(cfg *config.Config) {
		cfg.Roles.Implementer = config.RoleBinding{Harness: config.Antigravity, Model: "gemini-3-pro", Max: 2}
		testkit.InstallFakeHarness(t, cfg.DataDir, "agy_acp_server", script)
	})
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	first := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		if len(sessions) != 1 || !sessions[0].AcpSessionID.Valid {
			return store.Session{}, false
		}
		return sessions[0], true
	})
	dbPath := engine.AntigravityDBPath(engine.AntigravityDir(), first.AcpSessionID.String)
	writeAntigravityDB(t, dbPath, antigravityEntry{idx: 0, data: makeAntigravityData(100, 10, 1000, "gemini-3.8-flash-high")})

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	waitForKept(t, server, 41)
	writeAntigravityDB(t, dbPath, antigravityEntry{idx: 1, data: makeAntigravityData(30, 3, 200, "gemini-3.8-flash-pro")})

	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 2)
	if rows := usageRows(t, server, sessions[0].ID); len(rows) != 1 || rows[0].Input != nullInt(100) {
		t.Errorf("tokens of the first row = %+v", rows)
	}
	if sessions[1].Continued != 1 || sessions[1].AcpSessionID.String != sessions[0].AcpSessionID.String {
		t.Errorf("second row = %+v", sessions[1])
	}
	if got := harnessStarts(t, dataDir); got != 2 {
		t.Errorf("starts = %d, want the access check and one Implementer process", got)
	}
	rows := usageRows(t, server, sessions[1].ID)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Input != nullInt(30) || rows[0].Output != nullInt(3) || rows[0].CacheRead != nullInt(200) {
		t.Errorf("tokens of the continued row = %+v, want only the records after the earlier row", rows[0])
	}
}
