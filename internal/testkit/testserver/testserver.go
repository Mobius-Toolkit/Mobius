// Package testserver starts the Mobius server for a test.
package testserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/api"
	"github.com/Mobius-Toolkit/mobius-go/internal/auth"
	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/mcp"
	"github.com/Mobius-Toolkit/mobius-go/internal/runner"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
)

// Password is the access password of each server under test.
const Password = "correct horse"

// TrustedUser is the one trusted user of each server under test.
const TrustedUser = "owner"

// Server is a Mobius server under test.
type Server struct {
	URL string
	DB  *sql.DB
	// Client has the cookie of a device login.
	Client *http.Client
	Engine *engine.Engine
	// Mux has the API routes. The server serves it.
	Mux *http.ServeMux
}

// Config gives the config of a server under test with the data in dataDir: the access password Password, the
// trusted user TrustedUser, a poll each 50 ms, and a binding for each Role.
func Config(t testing.TB, dataDir string) *config.Config {
	t.Helper()
	cfg, err := config.Parse(fmt.Appendf(nil, `
access_password = %q
trusted_users = [%q]
data_dir = %q
poll_interval = "50ms"

[roles]
lead        = { harness = "claude-code", model = "opus",    effort = "high" }
triager     = { harness = "claude-code", model = "sonnet",  effort = "medium" }
implementer = { harness = "devin",       model = "swe-1.5", effort = "high" }
researcher  = { harness = "antigravity", model = "gemini-3-pro" }
reviewer    = { harness = "claude-code", model = "opus",    effort = "high" }
judge       = { harness = "claude-code", model = "haiku",   effort = "low" }
`, Password, TrustedUser, dataDir))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Start starts a Mobius server with the config of Config, the GitHub at githubURL (the API and the web pages), and logs in.
// The server stops at the end of the test.
func Start(t testing.TB, dataDir, githubURL string) *Server {
	t.Helper()
	return StartWith(t, Config(t, dataDir), githubURL)
}

// StartWith starts a Mobius server with cfg and the GitHub at githubURL (the API and the web pages), and logs in.
// The server stops at the end of the test.
//
// The agents run with the Harness commands in the directory harnesses of the data directory and then on the PATH
// of the test, and reach the Mobius MCP server over plain HTTP.
func StartWith(t testing.TB, cfg *config.Config, githubURL string) *Server {
	t.Helper()
	dataDir := cfg.DataDir
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
	gh, err := github.New(queries, githubURL, githubURL, []string{TrustedUser})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Prepare(dataDir); err != nil {
		t.Fatal(err)
	}
	mcpServer := mcp.New()
	mcpMux := http.NewServeMux()
	mcpServer.Register(mcpMux)
	agents := httptest.NewServer(mcpMux)
	t.Cleanup(agents.Close)
	e := engine.New(db, gh, cfg, engine.Agents{
		MCP:  mcpServer,
		Addr: strings.TrimPrefix(agents.URL, "http://"),
		Path: filepath.Join(dataDir, "harnesses") + string(filepath.ListSeparator) + os.Getenv("PATH"),
	})
	if err := e.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		e.Run(ctx)
		close(stopped)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	mux := http.NewServeMux()
	api.Routes(mux, queries, a, gh, e)
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
	return &Server{URL: server.URL, DB: db, Client: client, Engine: e, Mux: mux}
}

// WaitForFirstPoll waits for the end of the first poll of repository. The first poll
// hands the lost tasks to a human, and stores the `since` cursor of the issues endpoint at its end.
// A test that changes an issue before this end races with the first poll.
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
