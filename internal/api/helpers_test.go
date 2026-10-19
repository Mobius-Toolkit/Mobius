package api

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/auth"
	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

const password = "correct horse"

// newMux gives the API with a new database and the access password "correct horse".
func newMux(t *testing.T) (*http.ServeMux, *sql.DB, *auth.Auth) {
	t.Helper()
	return newMuxWith(t, voice.NewRecordings(voice.New(t.TempDir(), ""), voice.NewDraft(t.TempDir(), "")))
}

// newMuxWith gives the API with the voice recordings, a new database and the access password "correct horse".
func newMuxWith(t *testing.T, recordings *voice.Recordings) (*http.ServeMux, *sql.DB, *auth.Auth) {
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
	Routes(mux, queries, a, nil, engine.New(db, nil, &config.Config{}, engine.Agents{}), recordings)
	return mux, db, a
}

// testMux gives the API with a handler that adds the cookie of a device login to each request.
func testMux(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	return testMuxWith(t, voice.NewRecordings(voice.New(t.TempDir(), ""), voice.NewDraft(t.TempDir(), "")))
}

// testMuxWith gives testMux with the voice recordings.
func testMuxWith(t *testing.T, recordings *voice.Recordings) (http.Handler, *sql.DB) {
	t.Helper()
	mux, db, a := newMuxWith(t, recordings)
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
