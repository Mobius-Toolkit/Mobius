// Package testserver starts the Mobius server for a test.
package testserver

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/api"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
)

// Server is a Mobius server under test.
type Server struct {
	URL string
	DB  *sql.DB
}

// Start starts a Mobius server with the database in dataDir. The server stops at the end of the test.
func Start(t testing.TB, dataDir string) *Server {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(dataDir, "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mux := http.NewServeMux()
	api.Routes(mux, store.New(db))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &Server{URL: server.URL, DB: db}
}

// WaitForFirstPoll waits for the end of the first poll of repository. The first poll
// runs the lost-task recovery and treats each earlier issue event as old, so a test
// that changes an issue before this end races with the first poll. The first poll
// stores the `since` cursor of the issues endpoint at its end.
func (s *Server) WaitForFirstPoll(t testing.TB, repository string) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		var since sql.NullString
		err := s.DB.QueryRow("SELECT since FROM sync_cursors WHERE repository = ? AND endpoint = 'issues'", repository).Scan(&since)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		return since.Valid
	})
}
