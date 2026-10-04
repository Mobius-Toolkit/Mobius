package api

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/auth"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

const password = "correct horse"

// newMux gives the API with a new database and the access password "correct horse".
func newMux(t *testing.T) (*http.ServeMux, *sql.DB, *auth.Auth) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	queries := store.New(db)
	a, err := auth.Start(t.Context(), queries, password)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Routes(mux, queries, a, nil, nil)
	return mux, db, a
}

// testMux gives the API with a handler that adds the cookie of a device login to each request.
func testMux(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	mux, db, a := newMux(t)
	token, _, err := a.Login(t.Context(), password, "Go test")
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Cookie", sessionCookie+"="+token)
		mux.ServeHTTP(w, r)
	}), db
}

func exec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
