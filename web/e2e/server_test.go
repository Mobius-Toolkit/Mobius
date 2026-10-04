// Package e2e holds the Playwright tests of the UI and the server that they test.
package e2e

import (
	"database/sql"
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

func TestMain(m *testing.M) {
	testkit.Main(m)
}

// script is the fake agent of each Harness. The options have the models and the efforts of the Role bindings of
// testserver.Config. The Implementer of #41 works until the server stops.
const script = `
[options]
model = ["sonnet", "opus", "haiku", "swe-1.5", "gemini-3-pro"]
thought_level = ["low", "medium", "high"]
mode = ["default", "bypassPermissions", "yolo"]

[[prompts]]
updates = ['{"sessionUpdate": "tool_call", "toolCallId": "read", "title": "Read internal/plans/plan.go", "kind": "read", "status": "completed"}']
reply = ["The plan prices are in cents now."]
hang = true
`

// TestServer serves the built UI and the API at the address in MOBIUS_E2E_ADDR until
// SIGINT or SIGTERM. The server has a new database and a fake GitHub with the
// accounts "owner" and "plants". The Playwright tests create the Apps on the GitHub page.
//
// After the first poll of owner/shop, an Implementer and a Lead run, a drain waits for them,
// and the drain holds a second Implementer. Release v0.1.4 of Mobius is newer than this server.
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
	for _, workstream := range []struct {
		repository string
		number     int64
		title      string
	}{
		{"owner/shop", 12, "Integrate loyalty plans"},
		{"owner/shop", 13, "Seasonal prices"},
		{"plants/garden", 12, "Plant roses"},
	} {
		github.AddIssue(workstream.repository, workstream.number, workstream.title)
		github.AddLabel(workstream.repository, workstream.number, "mobius:workstream", "owner")
	}
	github.AddSubIssueOf("owner/shop", 12, 41, "Add plan model")
	github.AddSubIssueOf("owner/shop", 12, 42, "Let customers change plans")
	github.AddSubIssueOf("owner/shop", 13, 43, "Add season table")
	github.CloseIssue("owner/shop", 43)
	engine.Release = "v0.1.0"
	github.SetLatestRelease("v0.1.4")
	github.SetComparedCommits(
		"Send all events and Owner messages to one Lead session (#317)",
		"Check for a new release each hour (#316)\n\nThe check runs once each hour.",
		"Keep a Workstream in the list when its sub-issues cannot be read (#314)",
		"Show the release changes in a modal before the upgrade (#320)",
	)
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, script)
	server := testserver.Start(t, dataDir, github.URL)
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

	spec := implementerSpec(t, server, 41)
	if _, err := server.DB.Exec("UPDATE tasks SET pull_request = 44 WHERE id = ?", spec.Task); err != nil {
		t.Fatal(err)
	}
	implementer, err := server.Engine.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = implementer.Prompt(ctx, "Store the plan prices in cents.") }()
	lead := engine.Spec{Role: engine.LeadRole, Organization: "owner", Repository: "owner/shop", Workstream: 12, Dir: t.TempDir()}
	if _, err := server.Engine.Start(ctx, lead); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = server.Engine.Drain(ctx) }()
	testkit.WaitFor(t, func() bool { return server.Engine.Draining().On })
	held := implementerSpec(t, server, 42)
	go func() { _, _ = server.Engine.Start(ctx, held) }()
	<-ctx.Done()
	_ = srv.Close()
}

// implementerSpec adds a queued task of the issue number of the Workstream owner/shop#12, and gives the spec of its
// Implementer.
func implementerSpec(t *testing.T, server *testserver.Server, number int64) engine.Spec {
	t.Helper()
	queuedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := server.DB.Exec(`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, queued_at)
		VALUES ('owner/shop', ?, 12, 'queued', ?, ?)`, number, queuedAt, queuedAt)
	if err != nil {
		t.Fatal(err)
	}
	task, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return engine.Spec{
		Role:         engine.ImplementerRole,
		Organization: "owner",
		Repository:   "owner/shop",
		Workstream:   12,
		Issue:        sql.NullInt64{Int64: number, Valid: true},
		Task:         task,
		Dir:          t.TempDir(),
	}
}
