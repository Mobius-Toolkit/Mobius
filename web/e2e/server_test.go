// Package e2e holds the Playwright tests of the UI and the server that they test.
package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
	"github.com/Mobius-Toolkit/Mobius/web"
)

func TestMain(m *testing.M) {
	testkit.Main(m)
}

// script is the fake agent of each Harness. The options have the models and the efforts of the Role bindings of
// testserver.Config. The first rule whose text is in the prompt answers the prompt, so a rule of a chat message comes
// before the rule of each earlier event or message that the first prompt of a new session also holds. The Implementer
// of #41 works until the server stops. The Lead turn of each move_task does not end, so its session runs until the
// close of its Workstream.
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
when = "Move #52 to the Workstream #20."
call = { tool = "move_task", arguments = { n = 52, workstream = 20 } }
hang = true

[[prompts]]
when = "Move #57 to the Workstream #20."
call = { tool = "move_task", arguments = { n = 57, workstream = 20 } }
hang = true

[[prompts]]
when = "Move #8 to the Workstream."
call = { tool = "move_issue", arguments = { n = 8, workstream = 12 } }

[[prompts]]
when = "Create the phone Workstream."
call = { tool = "create_workstream", arguments = { title = "Phone plans", brief = "Plans for the phone." } }

[[prompts]]
when = "Move #7 to the Workstream."
call = { tool = "move_issue", arguments = { n = 7, workstream = 12 } }

[[prompts]]
when = "Create the desktop Workstream."
call = { tool = "create_workstream", arguments = { title = "Desktop plans", brief = "Plans for the desktop." } }

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
// accounts "owner" and "plants". The Playwright tests create the Apps on the GitHub page. The repositories come after
// the second App, so the tests see the first App with no organization.
//
// After the first poll of both repositories, a dispatch of #45 gives an Inbox item, and the Owner writes to the
// Lead chats of owner/shop#12 and plants/garden#12 and to the Triager chat of owner. Each chat session ends before
// the next step. Then an Implementer and a Lead run, a drain waits for them, and the drain holds a second
// Implementer. The parent of the first Implementer is a Lead session that ended. Release v0.1.4 of Mobius is newer
// than this server. The Inbox has an item of a usage limit of claude-code, with no Workstream and no issue. The
// table of the pauses has no row, because a pause stops the fake agents.
//
// The chat tests use the issues owner/shop#7 and #8 with no Workstream, the Workstreams plants/garden#14 to #17 with
// unread Lead messages, the empty chats of plants/garden#18 and #19, the events in the chat of plants/garden#25, the
// Workstreams plants/garden#20 and #30 with tasks that need a human, the user code "user-code" of the second App, and
// the Workstreams plants/garden#50 and #55 with one closed task and one open task. GitHub does not close #55. The
// Workstream plants/garden#19 has an open task, an open task that the first blocks, and a task that needs a human.
// POST and DELETE /e2e/repositories/{owner}/{name} add and remove a repository of the fake GitHub.
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
	for _, workstream := range []struct {
		repository string
		number     int64
		title      string
	}{
		{"owner/shop", 12, "Integrate loyalty plans"},
		{"owner/shop", 13, "Seasonal prices"},
		{"plants/garden", 12, "Plant roses"},
		{"plants/garden", 14, "Water the roses"},
		{"plants/garden", 15, "Feed the roses"},
		{"plants/garden", 16, "Cut the roses"},
		{"plants/garden", 17, "Sell the roses"},
		{"plants/garden", 18, "Prune the roses"},
		{"plants/garden", 19, "Mulch the beds"},
		{"plants/garden", 20, "Plant tulips"},
		{"plants/garden", 25, "Read the comments"},
		{"plants/garden", 30, "Plant lilies"},
		{"plants/garden", 50, "Plant daisies"},
		{"plants/garden", 55, "Plant asters"},
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
	github.SetBody("plants/garden", 18, "Cut the **old** canes in March.")
	github.SetBody("plants/garden", 19, "Put **bark** on the beds.")
	github.AddLabel("owner/shop", 41, "mobius:needs-human", "owner")
	github.AddLabel("owner/shop", 42, "mobius:needs-human", "owner")
	github.AddSubIssueOf("owner/shop", 12, 38, "Show the plan prices")
	github.AddSubIssueOf("owner/shop", 12, 39, "Send the plan receipts")
	github.AddIssue("owner/shop", 7, "Add plan prices")
	github.AddIssue("owner/shop", 8, "Add plan names")
	github.AddSubIssueOf("plants/garden", 20, 21, "Dig the tulip beds")
	github.AddSubIssueOf("plants/garden", 20, 22, "Buy tulip bulbs")
	github.AddSubIssueOf("plants/garden", 30, 31, "Dig the lily beds along the north fence and along the south fence")
	github.AddSubIssueOf("plants/garden", 30, 32, "Buy lily bulbs")
	for _, number := range []int64{21, 22, 31, 32} {
		github.AddLabel("plants/garden", number, "mobius:needs-human", "owner")
	}
	github.AddUserCode(testkit.SecondAppID, "user-code", "owner")
	github.AddSubIssueOf("plants/garden", 50, 51, "Buy daisy seeds")
	github.CloseIssue("plants/garden", 51)
	github.AddSubIssueOf("plants/garden", 50, 52, "Sow the daisies")
	github.AddSubIssueOf("plants/garden", 55, 56, "Buy aster seeds")
	github.CloseIssue("plants/garden", 56)
	github.AddSubIssueOf("plants/garden", 55, 57, "Sow the asters")
	github.FailClose("plants/garden", 55)
	github.AddSubIssueOf("plants/garden", 19, 70, "Order the bark")
	github.AddSubIssueOf("plants/garden", 19, 71, "Spread the bark")
	github.AddBlockedBy("plants/garden", 71, 70)
	github.AddSubIssueOf("plants/garden", 19, 72, "Water the bark")
	github.AddLabel("plants/garden", 72, "mobius:needs-human", "owner")
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
	server.Mux.HandleFunc("POST /e2e/repositories/{owner}/{name}", func(_ http.ResponseWriter, r *http.Request) {
		github.AddRepository(r.PathValue("owner") + "/" + r.PathValue("name"))
	})
	server.Mux.HandleFunc("DELETE /e2e/repositories/{owner}/{name}", func(_ http.ResponseWriter, r *http.Request) {
		github.RemoveRepository(r.PathValue("owner") + "/" + r.PathValue("name"))
	})

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: server.Mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()

	testkit.WaitFor(t, func() bool {
		var apps int
		if err := server.DB.QueryRow("SELECT COUNT(*) FROM github_apps").Scan(&apps); err != nil {
			t.Fatal(err)
		}
		return apps == 2
	})
	github.AddRepository("owner/shop")
	github.AddRepository("plants/garden")

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
	// The chats of plants/garden#14 to #17 have 12 Lead messages each, and the Owner saw the first 5.
	queries := store.New(server.DB)
	for number := int64(14); number <= 17; number++ {
		key := engine.ChatKey{Organization: "plants", Repository: "plants/garden", Workstream: number}
		var seen int64
		for n := 1; n <= 12; n++ {
			message, err := queries.AddChatMessage(ctx, store.AddChatMessageParams{
				Organization: key.Organization,
				Repository:   key.Repository,
				Workstream:   key.Workstream,
				Author:       "Lead",
				Time:         time.Now().UTC().Format(time.RFC3339Nano),
				Text:         fmt.Sprintf("Note %d of #%d. %s", n, number, strings.Repeat("word ", 100)),
			})
			if err != nil {
				t.Fatal(err)
			}
			if n == 5 {
				seen = message.ID
			}
		}
		if err := server.Engine.SeeChat(ctx, key, seen); err != nil {
			t.Fatal(err)
		}
	}
	// The first event has a word that is wider than a phone.
	for _, text := range []string{
		"A comment arrived: " + strings.Repeat("word", 60) + "\n\nThe rest of the comment.",
		"A label changed.",
	} {
		_, err := queries.AddChatMessage(ctx, store.AddChatMessageParams{
			Organization: "plants",
			Repository:   "plants/garden",
			Workstream:   25,
			Author:       "Event",
			Time:         time.Now().UTC().Format(time.RFC3339Nano),
			Text:         text,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The first message has text and two images. The second message has only an image.
	for _, message := range []struct {
		text   string
		colors []color.RGBA
	}{
		{"This is the new plan page.", []color.RGBA{{37, 99, 235, 255}, {22, 163, 74, 255}}},
		{"", []color.RGBA{{220, 38, 38, 255}}},
	} {
		added, err := queries.AddChatMessage(ctx, store.AddChatMessageParams{
			Organization: "plants",
			Repository:   "plants/garden",
			Workstream:   25,
			Author:       "Owner",
			Time:         time.Now().UTC().Format(time.RFC3339Nano),
			Text:         message.text,
		})
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(dataDir, "images", fmt.Sprint(added.ID))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for position, fill := range message.colors {
			picture := image.NewRGBA(image.Rect(0, 0, 200, 150))
			for y := range 150 {
				for x := range 200 {
					picture.SetRGBA(x, y, fill)
				}
			}
			var data bytes.Buffer
			if err := png.Encode(&data, picture); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.png", position)), data.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
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
	go func() { _ = implementer.Prompt(ctx, "Store the plan prices in cents.", nil) }()
	lead := engine.Spec{Role: engine.LeadRole, Organization: "owner", Repository: "owner/shop", Workstream: 12, Dir: t.TempDir()}
	if _, err := server.Engine.Start(ctx, lead); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool {
		var lines int
		if err := server.DB.QueryRow("SELECT COUNT(*) FROM transcript WHERE json LIKE '%The plan prices are in cents now.%'").Scan(&lines); err != nil {
			t.Fatal(err)
		}
		return lines == 1
	})
	_, err = queries.AddInboxItem(ctx, store.AddInboxItemParams{
		Kind:         "usage limit",
		Organization: "owner",
		Repository:   "owner/shop",
		Text:         "claude-code reached a usage limit. Mobius sends the prompt again at 2026-09-28 12:00 UTC.",
		Time:         time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	for number, state := range map[int64]string{38: "checks", 39: "approval"} {
		if _, err := server.DB.Exec(`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at)
			VALUES ('owner/shop', ?, 12, ?, ?)`, number, state, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		github.AddLabel("owner/shop", number, "mobius:working", testkit.AppSlug+"[bot]")
	}
	fixTimes(t, server)
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
	if err := server.Engine.SendChat(ctx, key, text, nil); err != nil {
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

// fixTimes gives one fixed time to each time that the UI shows, so each run gives the same screenshots. An event text
// starts with its time in the format of the engine.
func fixTimes(t *testing.T, server *testserver.Server) {
	t.Helper()
	for _, query := range []string{
		"UPDATE device_logins SET created_at = ?1",
		"UPDATE events SET time = ?1",
		"UPDATE sessions SET started_at = ?1, ended_at = iif(ended_at IS NULL, NULL, ?1)",
		"UPDATE transcript SET time = ?1",
		"UPDATE chat_messages SET time = ?1",
		"UPDATE inbox_items SET time = ?1",
	} {
		if _, err := server.DB.Exec(query, "2026-09-28T09:30:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := server.DB.Exec("UPDATE chat_messages SET text = '2026-09-28 09:30 UTC' || substr(text, 21) WHERE author = 'Event' AND text LIKE '____-__-__ __:__ UTC %'"); err != nil {
		t.Fatal(err)
	}
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
