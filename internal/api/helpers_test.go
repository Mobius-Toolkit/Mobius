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
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/voice"
)

const password = "correct horse"

// newWhisper gives a final converter that listens on a free address, not on the address of the running Mobius.
func newWhisper(t *testing.T) *voice.Whisper {
	t.Helper()
	w := voice.New(t.TempDir(), "")
	w.Address = testkit.FreeAddress(t)
	return w
}

// newDraftWhisper gives a draft converter that listens on a free address, not on the address of the running Mobius.
func newDraftWhisper(t *testing.T) *voice.Whisper {
	t.Helper()
	w := voice.NewDraft(t.TempDir(), "")
	w.Address = testkit.FreeAddress(t)
	return w
}

// newRecordings gives the voice recordings with a final and a draft converter on free addresses.
func newRecordings(t *testing.T) *voice.Recordings {
	t.Helper()
	return voice.NewRecordings(newWhisper(t), newDraftWhisper(t))
}

// newMux gives the API with a new database and the access password "correct horse".
func newMux(t *testing.T) (*http.ServeMux, *sql.DB, *auth.Auth) {
	t.Helper()
	return newMuxWith(t, newRecordings(t))
}

// newMuxWith gives the API with the voice recordings, a new database and the access password "correct horse".
func newMuxWith(t *testing.T, recordings *voice.Recordings) (*http.ServeMux, *sql.DB, *auth.Auth) {
	t.Helper()
	return newMuxConfig(t, recordings, &config.Config{})
}

// newMuxConfig gives newMuxWith with the config.
func newMuxConfig(t *testing.T, recordings *voice.Recordings, cfg *config.Config) (*http.ServeMux, *sql.DB, *auth.Auth) {
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
	Routes(mux, queries, a, nil, engine.New(db, nil, cfg, engine.Agents{}), recordings)
	return mux, db, a
}

// testMux gives the API with a handler that adds the cookie of a device login to each request.
func testMux(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	return testMuxWith(t, newRecordings(t))
}

// testMuxWith gives testMux with the voice recordings.
func testMuxWith(t *testing.T, recordings *voice.Recordings) (http.Handler, *sql.DB) {
	t.Helper()
	return testMuxConfig(t, recordings, &config.Config{})
}

// testMuxConfig gives testMuxWith with the config.
func testMuxConfig(t *testing.T, recordings *voice.Recordings, cfg *config.Config) (http.Handler, *sql.DB) {
	t.Helper()
	mux, db, a := newMuxConfig(t, recordings, cfg)
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
