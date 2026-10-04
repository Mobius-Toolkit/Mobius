// Package testserver starts the Mobius server for a test.
package testserver

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/api"
	"github.com/Mobius-Toolkit/mobius-go/internal/auth"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
)

// Password is the access password of each server under test.
const Password = "correct horse"

// Server is a Mobius server under test.
type Server struct {
	URL string
	DB  *sql.DB
	// Client has the cookie of a device login.
	Client *http.Client
}

// Start starts a Mobius server with the database in dataDir, and logs in. The server
// stops at the end of the test.
func Start(t testing.TB, dataDir string) *Server {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(dataDir, "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	queries := store.New(db)
	a, err := auth.Start(t.Context(), queries, Password)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.Routes(mux, queries, a)
	// The session cookie is Secure, and a cookie jar sends a Secure cookie only over HTTPS.
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Jar, err = cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(server.URL+"/api/login", "application/json", strings.NewReader(`{"password":"`+Password+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("login: status %d", response.StatusCode)
	}
	return &Server{URL: server.URL, DB: db, Client: client}
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
