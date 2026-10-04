// Package e2e holds the Playwright tests of the UI and the server that they test.
package e2e

import (
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/testserver"
	"github.com/Mobius-Toolkit/mobius-go/web"
)

// TestServer serves the built UI and the API at the address in MOBIUS_E2E_ADDR until
// SIGINT or SIGTERM. The server has a new database and a fake GitHub with the
// accounts "owner" and "plants". The Playwright tests create the Apps on the GitHub page.
func TestServer(t *testing.T) {
	addr := os.Getenv("MOBIUS_E2E_ADDR")
	if addr == "" {
		t.Skip("MOBIUS_E2E_ADDR is not set. The Playwright tests set it.")
	}
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	github := testkit.NewFakeGitHub(t)
	github.AddAccount("owner", "User")
	github.AddAccount("plants", "Organization")
	github.InstallSecondApp("plants")
	github.AddRepository("owner/shop")
	github.AddRepository("plants/garden")
	server := testserver.Start(t, t.TempDir(), github.URL)
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		t.Fatal(err)
	}
	server.Mux.Handle("/", web.Handler(dist))

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: server.Mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()

	// The first poll of owner/shop after the Playwright tests create the App creates the
	// Mobius labels. Then, for the Checkup screen, one label is missing again and one label
	// has a wrong color.
	testkit.WaitFor(t, func() bool { return len(github.RepositoryLabels("owner/shop")) == len(engine.Labels) })
	github.DeleteRepositoryLabel("owner/shop", "mobius:no-workstream")
	github.AddRepositoryLabel("owner/shop", "mobius:working", "ededed", "A Mobius agent works on this task")
	<-ctx.Done()
	_ = srv.Close()
}
