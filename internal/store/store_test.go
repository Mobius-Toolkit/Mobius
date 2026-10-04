package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"slices"
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
	if err := db.QueryRow("SELECT count(*) FROM copied_workstreams").Scan(new(int)); err != nil {
		t.Errorf("the last migration did not run: %v", err)
	}
}

func TestListWorkstreams(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`
		INSERT INTO tasks (repository, issue, workstream, state, dispatched_at) VALUES
			('o/a', 11, 1, 'working', '2026-10-01T10:00:00Z'),
			('o/a', 12, 1, 'ended', '2026-10-01T11:00:00Z'),
			('o/a', 13, 1, 'stopped', '2026-10-01T12:00:00Z');
		INSERT INTO sessions (role, harness, model, repository, workstream, started_at, organization) VALUES
			('lead', 'claude', 'opus', 'o/b', 5, '2026-10-02T10:00:00Z', 'o'),
			('triager', 'claude', 'opus', '', 0, '2026-10-03T10:00:00Z', 'o');
		INSERT INTO events (time, repository, workstream, issue, actor, text, link) VALUES
			('2026-10-01T13:00:00.5Z', 'o/a', 1, 11, 'bot', 'Dispatched', 'https://example.com');
	`)
	if err != nil {
		t.Fatal(err)
	}

	got, err := New(db).ListWorkstreams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ListWorkstreamsRow{
		{Repository: "o/b", Workstream: 5, LastActivity: "2026-10-02T10:00:00Z", Tasks: 0, OpenTasks: 0},
		{Repository: "o/a", Workstream: 1, LastActivity: "2026-10-01T13:00:00.5Z", Tasks: 3, OpenTasks: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("workstreams = %+v, want %+v", got, want)
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
