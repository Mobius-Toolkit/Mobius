// Package store holds the SQLite database of Mobius.
package store

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	_ "modernc.org/sqlite" // registers the database/sql driver "sqlite"
)

// The migration files up to 20261003000000_workstream_copy.sql are copies of the migration
// files of the Rust version (crates/mobius-store/migrations in Mobius-Toolkit/Mobius-rust,
// which is archived). The files have no goose annotations, so each file runs as one Go migration.
//
//go:embed migrations/*.sql
var migrations embed.FS

// Open opens the database at path and applies the pending migrations.
// A write waits up to 5 s (busy_timeout is in milliseconds) while another connection writes.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	if err := adoptSqlxVersions(ctx, db); err != nil {
		return err
	}
	ms, err := goMigrations()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, nil,
		goose.WithGoMigrations(ms...), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

// adoptSqlxVersions copies the applied versions of the sqlx table of the Rust
// version into a new goose version table, so that goose does not run them again.
func adoptSqlxVersions(ctx context.Context, db *sql.DB) error {
	var adopt bool
	err := db.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = '_sqlx_migrations')
		AND NOT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version')`,
	).Scan(&adopt)
	if err != nil || !adopt {
		return err
	}
	versions, err := database.NewStore(database.DialectSQLite3, "goose_db_version")
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := versions.CreateVersionTable(ctx, tx); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO goose_db_version (version_id, is_applied)
		SELECT 0, 1 UNION ALL SELECT version, 1 FROM _sqlx_migrations WHERE success ORDER BY 1`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func goMigrations() ([]*goose.Migration, error) {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	ms := make([]*goose.Migration, 0, len(names))
	for _, name := range names {
		version, err := goose.NumericComponent(name)
		if err != nil {
			return nil, err
		}
		query, err := migrations.ReadFile(name)
		if err != nil {
			return nil, err
		}
		up := &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, string(query))
			return err
		}}
		ms = append(ms, goose.NewGoMigration(version, up, nil))
	}
	return ms, nil
}
