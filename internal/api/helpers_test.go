package api

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

func testMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mux := http.NewServeMux()
	Routes(mux, store.New(db))
	return mux, db
}

func exec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
