// Package e2e holds the Playwright tests of the UI and the server that they test.
package e2e

import (
	"context"
	"database/sql"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/testserver"
	"github.com/Mobius-Toolkit/mobius-go/web"
)

func TestMain(m *testing.M) {
	testkit.Main(m)
}

// script is the fake agent of each Harness. The options have the models and the efforts of the Role bindings of
// testserver.Config. The first rule whose text is in the prompt answers the prompt, so a rule of a chat message comes
// before the rule of an earlier event that the first prompt of a new Lead session also holds. The Implementer of #41
// works until the server stops.
const script = `
[options]
model = ["sonnet", "opus", "haiku", "swe-1.5", "gemini-3-pro"]
thought_level = ["low", "medium", "high"]
mode = ["default", "bypassPermissions", "yolo"]

[[prompts]]
when = "What is the state of the plans?"
reply = ["The Implementer works on #41. #42 and #45 wait for your decision."]

[[prompts]]
when = "Which roses sell best?"
reply = ["Red roses sell best."]

[[prompts]]
when = "Start a Workstream for gift cards."
reply = ["Title: Gift cards\n\nBrief: Sell gift cards in the shop."]

[[prompts]]
when = "dispatch of #45"
call = { tool = "tell_owner", arguments = { text = "#45 needs a decision: one seat limit for each plan, or one for all plans?" } }

[[prompts]]
when = "Save in the Workstream memory"

[[prompts]]
updates = ['{"sessionUpdate": "tool_call", "toolCallId": "read", "title": "Read internal/plans/plan.go", "kind": "read", "status": "completed"}']
reply = ["The plan prices are in cents now."]
hang = true
`

// question is the first Owner message of the Lead chat of owner/shop#12. The wide code block and the wide table
// scroll inside the message.
const question = "What is the state of the plans? The full report is at " +
	"https://example.com/reports/loyalty/plans/every-customer-segment-and-billing-period.\n\n" +
	"```text\n" +
	`summary = [{ plan: "standard", seats: 10, price_per_seat: 100, discount_code: "SPRING-SALE-EXTRA-LONG-2026", renewal: "monthly" }]` + "\n" +
	"```\n\n" +
	"| Plan | Seats | Price per seat | Discount code | Region | Renewal |\n" +
	"| --- | --- | --- | --- | --- | --- |\n" +
	"| Standard | 10 | $100 | SPRING-SALE-EXTRA-LONG-2026 | Worldwide | Monthly |\n" +
	"| Extended | 40 | $80 | AUTUMN-SALE-EXTRA-LONG-2026 | Europe | Yearly |"

// TestServer serves the built UI and the API at the address in MOBIUS_E2E_ADDR until
// SIGINT or SIGTERM. The server has a new database and a fake GitHub with the
// accounts "owner" and "plants". The Playwright tests create the Apps on the GitHub page.
//
// After the first poll of both repositories, a dispatch of #45 gives an Inbox item, and the Owner writes to the
// Lead chats of owner/shop#12 and plants/garden#12 and to the Triager chat of owner. Each chat session ends before
// the next step. Then an Implementer and a Lead run, a drain waits for them, and the drain holds a second
// Implementer. The parent of the first Implementer is a Lead session that ended. Release v0.1.4 of Mobius is newer
// than this server.
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
	github.AddSubIssueOf("owner/shop", 12, 45, "Pick the plan limits")
	github.AddSubIssueOf("owner/shop", 13, 43, "Add season table")
	github.CloseIssue("owner/shop", 43)
	github.SetBody("owner/shop", 12, "Reward repeat customers.\n\n- Points on every order\n- One **free** plan for staff")
	github.SetBody("owner/shop", 13, "Change the prices for each season.")
	github.SetBody("owner/shop", 45, "Each plan has a limit of seats.")
	github.AddLabel("owner/shop", 41, "mobius:needs-human", "owner")
	github.AddLabel("owner/shop", 42, "mobius:needs-human", "owner")
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
	server.WaitForFirstPoll(t, "plants/garden")

	shop := engine.ChatKey{Organization: "owner", Repository: "owner/shop", Workstream: 12}
	github.AddLabel("owner/shop", 45, "mobius:ready", "owner")
	waitForChat(t, server, shop, "tell_owner")
	chat(ctx, t, server, shop, question, "Lead")
	chat(ctx, t, server, engine.ChatKey{Organization: "plants", Repository: "plants/garden", Workstream: 12}, "Which roses sell best?", "Lead")
	triager := engine.ChatKey{Organization: "owner"}
	chat(ctx, t, server, triager, "Start a Workstream for gift cards.", "Triager")
	// The plants chat keeps its unread reply for the organization switch.
	for _, key := range []engine.ChatKey{shop, triager} {
		view, err := server.Engine.ChatView(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := server.Engine.SeeChat(ctx, key, view.Messages[len(view.Messages)-1].ID); err != nil {
			t.Fatal(err)
		}
	}

	spec := implementerSpec(t, server, 41)
	if err := server.DB.QueryRow("SELECT MIN(id) FROM sessions WHERE role = ?", engine.LeadRole).Scan(&spec.Parent); err != nil {
		t.Fatal(err)
	}
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

// chat sends the Owner message text to the chat of key, and waits for the reply of author and the end of the session.
func chat(ctx context.Context, t *testing.T, server *testserver.Server, key engine.ChatKey, text, author string) {
	t.Helper()
	if err := server.Engine.SendChat(ctx, key, text); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, server, key, author)
}

// waitForChat waits for a message of author in the chat of key, and then for the end of each session.
func waitForChat(t *testing.T, server *testserver.Server, key engine.ChatKey, author string) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		view, err := server.Engine.ChatView(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(view.Messages, func(message store.ChatMessage) bool { return message.Author == author })
	})
	testkit.WaitFor(t, func() bool {
		var open int
		if err := server.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL").Scan(&open); err != nil {
			t.Fatal(err)
		}
		return open == 0
	})
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
