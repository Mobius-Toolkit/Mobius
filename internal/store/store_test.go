package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func migrationVersions(t *testing.T) []int64 {
	t.Helper()
	ms, err := goMigrations()
	if err != nil {
		t.Fatal(err)
	}
	versions := []int64{0}
	for _, m := range ms {
		versions = append(versions, m.Version)
	}
	return versions
}

func gooseVersions(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.Query("SELECT version_id FROM goose_db_version WHERE is_applied ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var versions []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return versions
}

func TestOpenNewDatabase(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if got, want := gooseVersions(t, db), migrationVersions(t); !slices.Equal(got, want) {
		t.Errorf("goose versions = %v, want %v", got, want)
	}
}

// sqlxDatabase makes a database like the Rust version: all migrations but the
// last one, recorded in _sqlx_migrations.
func sqlxDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`CREATE TABLE _sqlx_migrations (
		version BIGINT PRIMARY KEY,
		description TEXT NOT NULL,
		installed_on TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		success BOOLEAN NOT NULL,
		checksum BLOB NOT NULL,
		execution_time BIGINT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names[:len(names)-1] {
		query, err := migrations.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(query)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		version, err := goose.NumericComponent(name)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`INSERT INTO _sqlx_migrations (version, description, success, checksum, execution_time)
			VALUES (?, ?, 1, x'00', 0)`, version, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec(`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at)
		VALUES ('o/r', 2, 1, 'working', '2026-10-01T10:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenAdoptsSqlxVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobius.db")
	sqlxDatabase(t, path)

	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if got, want := gooseVersions(t, db), migrationVersions(t); !slices.Equal(got, want) {
		t.Errorf("goose versions = %v, want %v", got, want)
	}
	var state string
	if err := db.QueryRow("SELECT state FROM tasks WHERE repository = 'o/r' AND issue = 2").Scan(&state); err != nil {
		t.Fatalf("read the task: %v", err)
	}
	if state != "working" {
		t.Errorf("state = %q, want %q", state, "working")
	}
	if _, err := db.Exec(`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at)
		VALUES ('o/r', 3, 1, 'unknown', '2026-10-01T10:00:00Z')`); err == nil {
		t.Error("the last migration did not run: the database accepts an unknown task state")
	}
}

func TestTheTaskStateMigrationKeepsTheRowsAndRefusesAnUnknownState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobius.db")
	sqlxDatabase(t, path)
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, branch, queued_at, fix_rounds,
			pull_request, judged_at, worker_restarts, worker, worker_input, review_rounds, review_comment, check_head)
		VALUES ('o/r', 5, 4, 'reviewed', '2026-10-01T11:00:00Z', 'mobius/5', '2026-10-01T11:01:00Z', 2,
			9, '2026-10-01T11:02:00Z', 1, 'judge', 'needs_human', 3, 77, 'abc')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	tasks, err := New(db).ListLiveTasks(t.Context(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	want := Task{
		ID:             tasks[1].ID,
		Repository:     "o/r",
		Issue:          5,
		Workstream:     4,
		State:          "reviewed",
		DispatchedAt:   "2026-10-01T11:00:00Z",
		Branch:         sql.NullString{String: "mobius/5", Valid: true},
		QueuedAt:       sql.NullString{String: "2026-10-01T11:01:00Z", Valid: true},
		FixRounds:      2,
		PullRequest:    sql.NullInt64{Int64: 9, Valid: true},
		JudgedAt:       sql.NullString{String: "2026-10-01T11:02:00Z", Valid: true},
		WorkerRestarts: 1,
		Worker:         sql.NullString{String: "judge", Valid: true},
		WorkerInput:    sql.NullString{String: "needs_human", Valid: true},
		ReviewRounds:   3,
		ReviewComment:  sql.NullInt64{Int64: 77, Valid: true},
		CheckHead:      sql.NullString{String: "abc", Valid: true},
		StateAt:        tasks[1].StateAt,
	}
	if tasks[1] != want {
		t.Errorf("task = %+v, want %+v", tasks[1], want)
	}
	for _, state := range []string{"dispatched", "queued", "working", "reviewed", "needs_human", "ready_for_review", "stopped", "ended", "checks", "approval"} {
		if _, err := db.Exec("UPDATE tasks SET state = ? WHERE issue = 5", state); err != nil {
			t.Errorf("state %q: %v", state, err)
		}
	}
	if _, err := db.Exec("UPDATE tasks SET state = 'unknown' WHERE issue = 5"); err == nil {
		t.Error("the database accepts an unknown task state")
	}
}

// A Workstream has all tasks closed when its issue has sub-issues and each one is closed. An issue deeper in the tree does not count.
func TestListCopiedWorkstreams(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`
		INSERT INTO copied_workstreams (repository, number, title, body, autopilot) VALUES
			('o/a', 1, 'No task', '', 0),
			('o/a', 2, 'Open task', 'Brief', 1),
			('o/a', 3, 'Closed tasks', '', 0),
			('o/b', 1, 'Other repository', '', 0);
		INSERT INTO copied_issues (repository, workstream, position, number, parent, title, body, state, author, html_url, repository_url) VALUES
			('o/a', 2, 0, 20, 2, 'Task', '', 'closed', 'owner', '', ''),
			('o/a', 2, 1, 21, 2, 'Task', '', 'open', 'owner', '', ''),
			('o/a', 3, 0, 30, 3, 'Task', '', 'closed', 'owner', '', ''),
			('o/a', 3, 1, 31, 30, 'Nested task', '', 'open', 'owner', '', '');
	`)
	if err != nil {
		t.Fatal(err)
	}

	got, err := New(db).ListCopiedWorkstreams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ListCopiedWorkstreamsRow{
		{Repository: "o/a", Number: 3, Title: "Closed tasks", AllTasksClosed: true},
		{Repository: "o/a", Number: 2, Title: "Open task", Body: "Brief", Autopilot: true},
		{Repository: "o/a", Number: 1, Title: "No task"},
		{Repository: "o/b", Number: 1, Title: "Other repository"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("workstreams = %+v, want %+v", got, want)
	}
}

func TestACuratorThatIsOpenOrEndedDoneFailedOrHungResetsTheCountOfEndedSessions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`
		INSERT INTO sessions (role, harness, model, repository, workstream, started_at, ended_at, end_reason) VALUES
			('lead', 'h', 'm', 'o/a', 1, '2026-10-01T00:00:00Z', '2026-10-01T01:00:00Z', 'done'),
			('lead', 'h', 'm', 'o/a', 1, '2026-10-01T00:00:00Z', '2026-10-01T02:00:00Z', 'done'),
			('curator', 'h', 'm', 'o/a', 0, '2026-10-02T00:00:00Z', '2026-10-02T01:00:00Z', 'declined'),
			('curator', 'h', 'm', 'o/a', 0, '2026-10-02T02:00:00Z', '2026-10-02T03:00:00Z', 'stopped'),
			('curator', 'h', 'm', 'o/a', 0, '2026-10-02T04:00:00Z', '2026-10-02T05:00:00Z', 'restart');
	`)
	if err != nil {
		t.Fatal(err)
	}
	count := func() int64 {
		t.Helper()
		n, err := New(db).CountSessionsEndedSinceCurator(ctx, "o/a")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 2 {
		t.Fatalf("count after Curators that did no work = %d, want 2", n)
	}

	for i, reason := range []string{"failed", "hung", "done"} {
		_, err = db.Exec(`INSERT INTO sessions (role, harness, model, repository, workstream, started_at, ended_at, end_reason) VALUES
			('lead', 'h', 'm', 'o/a', 1, ?, ?, 'done'),
			('curator', 'h', 'm', 'o/a', 0, ?, ?, ?)`,
			fmt.Sprintf("2026-10-%02dT00:00:00Z", 3+i), fmt.Sprintf("2026-10-%02dT01:00:00Z", 3+i),
			fmt.Sprintf("2026-10-%02dT02:00:00Z", 3+i), fmt.Sprintf("2026-10-%02dT03:00:00Z", 3+i), reason)
		if err != nil {
			t.Fatal(err)
		}
		if n := count(); n != 0 {
			t.Errorf("count after a Curator that ended %s = %d, want 0", reason, n)
		}
	}

	_, err = db.Exec(`INSERT INTO sessions (role, harness, model, repository, workstream, started_at, ended_at, end_reason) VALUES
		('lead', 'h', 'm', 'o/a', 1, '2026-10-06T00:00:00Z', '2026-10-06T01:00:00Z', 'done')`)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("count after a Lead session = %d, want 1", n)
	}
	_, err = db.Exec(`INSERT INTO sessions (role, harness, model, repository, workstream, started_at) VALUES
		('curator', 'h', 'm', 'o/a', 0, '2026-10-07T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Errorf("count with an open Curator = %d, want 0", n)
	}
}

func TestOnlyACuratorThatEndedDoneGivesTheStartOfTheLastRun(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	queries := New(db)
	if _, err := queries.GetLastDoneCuratorStart(ctx, "o/a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("error with no Curator = %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO sessions (role, harness, model, repository, workstream, started_at, ended_at, end_reason) VALUES
			('curator', 'h', 'm', 'o/a', 0, '2026-10-01T00:00:00Z', '2026-10-01T01:00:00Z', 'done'),
			('curator', 'h', 'm', 'o/a', 0, '2026-10-02T00:00:00Z', '2026-10-02T01:00:00Z', 'failed'),
			('curator', 'h', 'm', 'o/a', 0, '2026-10-03T00:00:00Z', '2026-10-03T01:00:00Z', 'hung'),
			('curator', 'h', 'm', 'o/a', 0, '2026-10-04T00:00:00Z', NULL, NULL),
			('curator', 'h', 'm', 'o/b', 0, '2026-10-05T00:00:00Z', '2026-10-05T01:00:00Z', 'done');
	`)
	if err != nil {
		t.Fatal(err)
	}
	started, err := queries.GetLastDoneCuratorStart(ctx, "o/a")
	if err != nil || started != "2026-10-01T00:00:00Z" {
		t.Errorf("start = %q, %v", started, err)
	}
}

func TestAWriteWaitsForTheWriteOfAnotherConnection(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO sync_cursors (repository, endpoint) VALUES ('owner/shop', 'issues')"); err != nil {
		t.Fatal(err)
	}
	written := make(chan error)
	go func() {
		_, err := db.Exec("INSERT INTO sync_cursors (repository, endpoint) VALUES ('owner/shop', 'pulls')")
		written <- err
	}()

	time.Sleep(100 * time.Millisecond)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := <-written; err != nil {
		t.Errorf("write = %v", err)
	}
}

func TestATransactionThatReadsAndThenWritesWaitsForTheWriteLockAtItsStart(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	readThenWrite := func(tx *sql.Tx, endpoint string) error {
		var n int
		if err := tx.QueryRow("SELECT count(*) FROM sync_cursors").Scan(&n); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO sync_cursors (repository, endpoint) VALUES ('owner/shop', ?)", endpoint)
		return err
	}
	txA, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := txA.QueryRow("SELECT count(*) FROM sync_cursors").Scan(&n); err != nil {
		t.Fatal(err)
	}
	doneB := make(chan error, 1)
	go func() {
		txB, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			doneB <- err
			return
		}
		if err := readThenWrite(txB, "pulls"); err != nil {
			_ = txB.Rollback()
			doneB <- err
			return
		}
		doneB <- txB.Commit()
	}()

	// With a deferred transaction, B finishes in this time and the write of A fails.
	var errB error
	finishedB := false
	select {
	case errB = <-doneB:
		finishedB = true
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := txA.Exec("INSERT INTO sync_cursors (repository, endpoint) VALUES ('owner/shop', 'issues')"); err != nil {
		t.Fatalf("write of transaction A = %v", err)
	}
	if err := txA.Commit(); err != nil {
		t.Fatal(err)
	}
	if !finishedB {
		errB = <-doneB
	}
	if errB != nil {
		t.Errorf("transaction B = %v", errB)
	}
}

func TestTheTranscriptMigrationRemovesTheDuplicatesAndAddsTheIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobius.db")
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	output := strings.Repeat("x", 100_000)
	commands := strings.Repeat(`{"name":"skill"},`, 5000) + `{"name":"last"}`
	updates := map[string]string{
		"tool_call_update": `{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"t","rawOutput":"` + output + `",` +
			`"content":[{"type":"content","content":{"type":"text","text":"` + output + `"}}],` +
			`"_meta":{"claudeCode":{"toolName":"Bash","toolResponse":{"stdout":"` + output + `"}}}}}`,
		"available_commands_update": `{"sessionId":"s","update":{"sessionUpdate":"available_commands_update","availableCommands":[` + commands + `]}}`,
		"tool_call":                 `{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"t","rawOutput":"keep"}}`,
	}
	if _, err := db.Exec("DROP INDEX transcript_session"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO sessions (id, role, harness, model, repository, workstream, started_at) VALUES (1, 'lead', 'h', 'm', 'o/a', 1, '2026-10-01T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	for _, update := range updates {
		for range 20 {
			if _, err := db.Exec("INSERT INTO transcript (session, time, kind, json) VALUES (1, '2026-10-01T10:00:00Z', 'update', ?)", update); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec("ALTER TABLE tasks DROP COLUMN recoveries"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE tasks DROP COLUMN lead_findings"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE events DROP COLUMN organization"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE harness_pauses ADD COLUMN inbox_item INTEGER NOT NULL DEFAULT 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE harness_pause_ends"); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"chat_message", "title", "reason", "pull_request", "report_link"} {
		if _, err := db.Exec("ALTER TABLE inbox_items DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("ALTER TABLE copied_workstreams DROP COLUMN evergreen"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM goose_db_version WHERE version_id >= 20261027000000"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before := fileSize(t, path)

	db, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if after := fileSize(t, path); after*2 > before {
		t.Errorf("the file has %d bytes after the migration and %d bytes before", after, before)
	}
	count := func(where string) int {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM transcript WHERE " + where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("json_extract(json, '$.update.sessionUpdate') = 'tool_call_update'"); n != 20 {
		t.Errorf("%d tool_call_update rows, want 20", n)
	}
	if n := count("json_extract(json, '$.update.sessionUpdate') = 'available_commands_update'"); n != 20 {
		t.Errorf("%d available_commands_update rows, want 20", n)
	}
	for _, where := range []string{
		"json_type(json, '$.update.rawOutput') IS NOT NULL AND json_extract(json, '$.update.sessionUpdate') = 'tool_call_update'",
		"json_type(json, '$.update._meta.claudeCode.toolResponse') IS NOT NULL",
		"json_type(json, '$.update.availableCommands') IS NOT NULL",
	} {
		if n := count(where); n != 0 {
			t.Errorf("%d rows keep a removed field: %s", n, where)
		}
	}
	if n := count("json_array_length(json, '$.update.content') = 1"); n != 20 {
		t.Errorf("%d rows keep the content, want 20", n)
	}
	if n := count("json_extract(json, '$.update.rawOutput') = 'keep'"); n != 20 {
		t.Errorf("%d tool_call rows keep rawOutput, want 20", n)
	}
	var plan string
	if err := db.QueryRow("EXPLAIN QUERY PLAN SELECT * FROM transcript WHERE session = 1 ORDER BY id").Scan(new(int), new(int), new(int), &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "USING INDEX transcript_session") && !strings.Contains(plan, "USING COVERING INDEX transcript_session") {
		t.Errorf("plan = %q", plan)
	}
}

func TestTheInboxMigrationsDismissTheOpenItemsAndKeepThePause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobius.db")
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ALTER TABLE tasks DROP COLUMN recoveries",
		"ALTER TABLE events DROP COLUMN organization",
		"ALTER TABLE harness_pauses ADD COLUMN inbox_item INTEGER NOT NULL DEFAULT 0",
		"DROP TABLE harness_pause_ends",
		"ALTER TABLE inbox_items DROP COLUMN chat_message",
		"ALTER TABLE inbox_items DROP COLUMN title",
		"ALTER TABLE inbox_items DROP COLUMN reason",
		"ALTER TABLE inbox_items DROP COLUMN pull_request",
		"ALTER TABLE inbox_items DROP COLUMN report_link",
		"ALTER TABLE copied_workstreams DROP COLUMN evergreen",
		"DELETE FROM goose_db_version WHERE version_id >= 20261029100000",
		`INSERT INTO inbox_items (id, kind, organization, repository, workstream, issue, text, link, time) VALUES
			(1, 'usage limit', 'owner', '', 0, 0, 'devin reached a usage limit.', '', '2026-10-04T10:00:00Z'),
			(2, 'question', 'owner', 'owner/shop', 12, 41, 'Why?', '', '2026-10-04T10:00:00Z')`,
		"INSERT INTO harness_pauses (harness, paused_until, inbox_item) VALUES ('devin', '2099-01-01T00:00:00Z', 1)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var open []int64
	rows, err := db.Query("SELECT id FROM inbox_items WHERE dismissed_at IS NULL")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		open = append(open, id)
	}
	if len(open) != 0 {
		t.Errorf("open items = %v, want none", open)
	}
	var until string
	if err := db.QueryRow("SELECT paused_until FROM harness_pauses WHERE harness = 'devin'").Scan(&until); err != nil || until != "2099-01-01T00:00:00Z" {
		t.Errorf("pause = %q: %v", until, err)
	}
}

func TestTheTasksTableAcceptsTheAskedStatesAndKeepsTheStateTrigger(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, statement := range []string{
		"INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, state_at) VALUES ('owner/shop', 41, 12, 'asked', '2026-10-04T10:00:00Z', '2026-10-04T10:00:00Z')",
		"UPDATE tasks SET state = 'answered', state_at = '2026-10-04T11:00:00Z'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	var state string
	if err := db.QueryRow("SELECT state FROM task_states WHERE task = 1").Scan(&state); err != nil || state != "asked" {
		t.Errorf("state = %q: %v", state, err)
	}
}

func TestTheCoordinatorMigrationMovesTheChatSessionsAndTheChatMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobius.db")
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ALTER TABLE tasks DROP COLUMN recoveries",
		"ALTER TABLE inbox_items DROP COLUMN chat_message",
		"ALTER TABLE copied_workstreams DROP COLUMN evergreen",
		"DELETE FROM goose_db_version WHERE version_id >= 20261105000000",
		`INSERT INTO sessions (id, role, harness, model, organization, repository, workstream, started_at) VALUES
			(1, 'triager', 'claude-code', 'sonnet', 'owner', '', 0, '2026-10-04T10:00:00Z'),
			(2, 'triager', 'claude-code', 'sonnet', 'owner', 'owner/shop', 0, '2026-10-04T10:00:00Z'),
			(3, 'lead', 'claude-code', 'sonnet', 'owner', 'owner/shop', 12, '2026-10-04T10:00:00Z')`,
		`INSERT INTO chat_messages (id, organization, repository, workstream, author, time, text) VALUES
			(1, 'owner', '', 0, 'Triager', '2026-10-04T10:00:00Z', 'Hello'),
			(2, 'owner', '', 0, 'Owner', '2026-10-04T10:00:00Z', 'Hi'),
			(3, 'owner', 'owner/shop', 12, 'Lead', '2026-10-04T10:00:00Z', 'Done')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	got := func(query string) string {
		var values []string
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		return strings.Join(values, " ")
	}
	if roles := got("SELECT role FROM sessions ORDER BY id"); roles != "coordinator triager lead" {
		t.Errorf("roles = %q", roles)
	}
	if authors := got("SELECT author FROM chat_messages ORDER BY id"); authors != "Coordinator Owner Lead" {
		t.Errorf("authors = %q", authors)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
