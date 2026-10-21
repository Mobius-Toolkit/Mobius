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
	"strconv"
	"strings"
	"sync/atomic"
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

// script is the fake agent of each Harness. It has the parts scriptHead, scriptBody and scriptTail, and the rules of
// ownerCalls between them. The options have the models and the efforts of the Role bindings of
// testserver.Config. The first rule whose text is in the prompt answers the prompt, so a rule of a chat message comes
// before the rule of each earlier event or message that the first prompt of a new session also holds. The Implementer
// of #41 works until the server stops. The Lead turn of each move_task does not end, so its session runs until the
// close of its Workstream.
const scriptHead = `
[options]
model = ["sonnet", "opus", "haiku", "swe-1.5", "gemini-3-pro"]
thought_level = ["low", "medium", "high"]
mode = ["default", "bypassPermissions", "yolo"]

[[prompts]]
when = "# Approval message"

[[prompts]]
when = "Message of the Coordinator, approved by the Owner"
reply = ["The Lead has the message."]

[[prompts]]
when = "# Owner message\n\nWhich store?"
reply = ["The answer is noted."]

[[prompts]]
when = "# Owner message\n\nKeep the old data."
reply = ["The answer is noted."]

[[prompts]]
when = "# Owner message\n\nYes, draft it now."
reply = ["The answer is noted."]

`

const scriptBody = `[[prompts]]
when = "Which lilies sell best?"
reply = ["Pink lilies sell best."]
busy = "3s"

[[prompts]]
when = "Which poppies sell best?"
reply = ["Orange poppies sell best."]
busy = "3s"

[[prompts]]
when = "Which daisies sell best?"
reply = ["White daisies sell best."]
busy = "2s"

[[prompts]]
when = "Which tulips sell best?"
reply = ["Yellow tulips sell best."]
busy = "2s"

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
later = { after = "100ms", call = { tool = "ask", arguments = { n = 12, text = "#45 needs a decision: one seat limit for each plan, or one for all plans?" } } }

[[prompts]]
when = "Save in the Workstream memory"

`

const scriptTail = `[[prompts]]
updates = ['{"sessionUpdate": "tool_call", "toolCallId": "read", "title": "Read internal/plans/plan.go", "kind": "read", "status": "completed"}']
reply = ["The plan prices are in cents now."]
hang = true
`

// ownerCalls are the Owner messages that the fake Coordinator answers with a tool call. The first prompt of a session
// has the chat history, which holds the earlier messages, and then the heading of the Owner message and the text. A
// later prompt of the session is only the text. Thus the rules with the heading come first, and the rules with the
// text only come after the other rules of the chat messages.
var ownerCalls = []struct{ message, tool, arguments string }{
	{"Draft the Workstream for gift cards.", "create_workstream", `{ title = "Gift cards", brief = "## Goal\n\nLet customers buy a gift card in the shop and pay with it at the checkout. A gift card has a code, a balance and an end date.\n\n## Today\n\n- The shop has only the payment by card.\n- The checkout has no field for a code.\n- The balance of a customer is not stored.\n\n## Scope\n\n- Add the table gift_cards with the code, the balance in cents and the end date.\n- Add the page where a customer buys a gift card and gets the code by e-mail.\n- Add the field for a code at the checkout. The balance pays the order before the card does.\n\n## Limits\n\n- Do not change the payment by card.\n- Do not add a refund of a gift card.\n- Follow AGENTS.md: write the migration as a new file.\n\n## Done\n\n- A Go test: a customer buys a gift card and the table has the balance.\n- A Go test: an order that is larger than the balance charges the card for the rest.\n- A Playwright test buys a gift card and pays an order with its code." }`},
	{"Tell the Lead of #14 to keep the prices in cents.", "message_lead", `{ workstream = 14, text = "The Owner decided that the prices stay in cents.\n\nChange the price column of the plan table to an integer. Write the migration as a new file, and keep the old column until the next release.\n\nTell the Owner when the pull request is ready." }`},
	{"Tell the Lead of #14 to drop the old price column.", "message_lead", `{ workstream = 14, text = "Drop the old price column of the plan table after the migration of #12.\n\nWait until that migration is in the main branch." }`},
	{"Tell the Lead of #14 to add a price history.", "message_lead", `{ workstream = 14, text = "Add a history of the plan prices." }`},
	{"Tell the Lead of #14 to add a price history table.", "message_lead", `{ workstream = 14, text = "Add a table with the history of the plan prices." }`},
	{"Tell the Lead of #9999 to stop.", "message_lead", `{ workstream = 9999, text = "Stop the work." }`},
	{"Ask which store and which checks.", "ask", `{ text = "The shop needs a store for the plans.", questions = [ { text = "Which store?", multiple = false, options = [ { title = "SQLite", details = "One file, no server." }, { title = "Postgres", details = "A server that many writers share." } ] }, { text = "Which checks?", multiple = true, options = [ { title = "Lint", details = "Finds faults of the style." }, { title = "Tests", details = "Runs the Go tests." }, { title = "Types", details = "Checks the TypeScript types." } ] } ] }`},
	{"Ask whether to draft the Workstream.", "ask", `{ text = "Should I draft the Workstream now?" }`},
}

func ownerCallRules(heading string) string {
	var rules strings.Builder
	for _, call := range ownerCalls {
		fmt.Fprintf(&rules, "[[prompts]]\nwhen = %q\ncall = { tool = %q, arguments = %s }\n\n", heading+call.message, call.tool, call.arguments)
	}
	return rules.String()
}

var script = scriptHead + ownerCallRules("# Owner message\n\n") + scriptBody + ownerCallRules("") + scriptTail

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
// Lead chats of owner/shop#12 and plants/garden#12 and to the Coordinator chats of owner and plants. Each chat session ends before
// the next step. Then an Implementer and a Lead run, a drain waits for them, and the drain holds a second
// Implementer. The parent of the first Implementer is a Lead session that ended. Release v0.1.4 of Mobius is newer
// than this server. The table of the pauses has a row of antigravity, with no timer. The Curator of a repository uses
// antigravity too, so it waits for the pause. A Curator starts after 10 ended sessions of a repository. Fewer
// sessions have ended when the screenshots test takes its screenshots, so no Curator session shows in them. The tools
// of the Checkup page have fixed paths and versions, and tar gives no
// version.
//
// The Workstream owner/shop#14 has Autopilot on and one task in ready_for_review.
//
// The chat tests use the issues owner/shop#7 and #8 with no Workstream, the Workstreams plants/garden#14 to #17 with
// unread Lead messages, the empty chats of plants/garden#18 and #19, the events in the chat of plants/garden#25, the
// Workstreams plants/garden#20 and #30 with tasks that need a human, the user code "user-code" of the second App, the
// user code "shop-code" of the first App, and
// the Workstreams plants/garden#50 and #55 with one closed task and one open task. GitHub does not close #55. The
// Workstream plants/garden#19 has an open task, an open task that the first blocks, and a task that needs a human.
// The Workstream owner/shop#12 has two closed tasks and a task with a question of the Lead. It also has four Implementer sessions with the queue reasons of a
// check that runs, a check that waits for a slot, a pause, and a full Role.
// The Workstream plants/garden#60 has a Lead chat of 50 messages, and the Owner saw the first 15. POST
// /e2e/seed-notes/{count} adds count Lead messages to that chat, with no event.
// POST /e2e/approvals/times/fix gives the fixed time to each chat message, Inbox item and action that has a real time.
// POST /e2e/inbox-items adds an Inbox item for owner/shop#40 and gives its id. With the query kind, repository,
// workstream, issue, title and reason, it adds that item instead. DELETE /e2e/inbox-items/{id}
// removes it. An answer to that item adds a comment to the issue and changes nothing else. POST and DELETE
// /e2e/repositories/{owner}/{name} add and remove a repository of the fake GitHub. PUT
// /e2e/agents/{issue}/queue-reason sets the queue reason of the live agent of an issue, with no event. DELETE
// /e2e/dispatches/{owner}/{name}/{issue} waits for the dispatch of an issue, removes its task, its feed events and
// its Lead events, and takes mobius:working off the issue. The query label, if present, is added to the issue.
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
		{"owner/shop", 14, "Early renewals"},
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
		{"plants/garden", 60, "Sort the seeds"},
	} {
		github.AddIssue(workstream.repository, workstream.number, workstream.title)
		github.AddLabel(workstream.repository, workstream.number, "mobius:workstream", "owner")
	}
	github.AddLabel("owner/shop", 14, "mobius:autopilot", "owner")
	github.AddLabel("owner/shop", 14, "mobius:maintenance", "owner")
	github.AddSubIssueOf("owner/shop", 12, 41, "Add plan model")
	github.AddSubIssueOf("owner/shop", 12, 42, "Let customers change plans")
	github.AddSubIssueOf("owner/shop", 12, 30, "Name the plan tiers")
	github.AddLabel("owner/shop", 30, "mobius:question", "owner")
	github.AddSubIssueOf("owner/shop", 13, 43, "Add season table")
	github.CloseIssue("owner/shop", 43)
	github.OpenPullRequest("owner/shop", "Add plan model", "mobius/41")
	github.AddSubIssueOf("owner/shop", 12, 45, "Pick the plan limits")
	github.SetBody("owner/shop", 45, "Each plan has a limit of seats.")
	github.AddSubIssueOf("owner/shop", 41, 36, "Rename the plan table")
	github.AddSubIssueOf("owner/shop", 12, 37, "Remove the old plan page")
	github.CloseIssue("owner/shop", 36)
	github.CloseIssue("owner/shop", 37)
	github.SetBody("owner/shop", 12, "Reward repeat customers.\n\n- Points on every order\n- One **free** plan for staff")
	github.SetBody("owner/shop", 13, "Change the prices for each season.")
	github.SetBody("plants/garden", 18, "Cut the **old** canes in March.")
	github.SetBody("plants/garden", 19, "Put **bark** on the beds.")
	github.AddIssue("owner/shop", 40, "Renew a month early")
	github.AddIssue("owner/shop", 35, "Hold the plan renewals")
	github.AddLabel("owner/shop", 35, "mobius:working", testkit.AppSlug+"[bot]")
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
	github.AddUserCode(testkit.AppID, "shop-code", "owner")
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
	// The Checkup page shows the path of each tool, so the data directory has the same path in each run.
	const dataDir = "/tmp/mobius-e2e"
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	testkit.InstallFakeAgent(t, dataDir, script)
	fixTools(t, dataDir)
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
	var lastStepTime, lastTaskState atomic.Int64
	server.Mux.HandleFunc("POST /e2e/times/fix", func(w http.ResponseWriter, _ *http.Request) {
		for query, last := range map[string]int64{
			"UPDATE step_times SET started_at = ?1, ended_at = ?1 WHERE id > ?2":  lastStepTime.Load(),
			"UPDATE task_states SET started_at = ?1, ended_at = ?1 WHERE id > ?2": lastTaskState.Load(),
		} {
			if _, err := server.DB.Exec(query, "2026-09-28T09:30:00Z", last); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	})
	server.Mux.HandleFunc("POST /e2e/approvals/times/fix", func(w http.ResponseWriter, _ *http.Request) {
		for _, query := range []string{
			"UPDATE chat_messages SET time = ?1, delivered_at = iif(delivered_at IS NULL, NULL, ?1), stopped_at = iif(stopped_at IS NULL, NULL, ?1) WHERE time > ?1",
			"UPDATE inbox_items SET time = ?1 WHERE time > ?1",
			"UPDATE pending_actions SET time = ?1, decided_at = iif(decided_at IS NULL, NULL, ?1) WHERE time > ?1",
		} {
			if _, err := server.DB.Exec(query, "2026-09-28T09:30:00Z"); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	})
	server.Mux.HandleFunc("POST /e2e/inbox-items", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		params := store.AddInboxItemParams{
			Kind:         "Lead failed",
			Organization: "owner",
			Repository:   "owner/shop",
			Workstream:   12,
			Issue:        40,
			Reason:       "The Lead did not get your message",
			Time:         "2026-09-28T09:30:00Z",
		}
		if kind := query.Get("kind"); kind != "" {
			params.Kind = kind
			params.Organization, _, _ = strings.Cut(query.Get("repository"), "/")
			params.Repository = query.Get("repository")
			params.Workstream, _ = strconv.ParseInt(query.Get("workstream"), 10, 64)
			params.Issue, _ = strconv.ParseInt(query.Get("issue"), 10, 64)
			params.Title = query.Get("title")
			params.Reason = query.Get("reason")
			params.Link = fmt.Sprintf("https://github.com/%s/issues/%d", params.Repository, params.Issue)
		}
		item, err := store.New(server.DB).AddInboxItem(r.Context(), params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, item.ID)
	})
	server.Mux.HandleFunc("DELETE /e2e/inbox-items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, err := server.DB.Exec("DELETE FROM inbox_items WHERE id = ?", r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	server.Mux.HandleFunc("PUT /e2e/agents/{issue}/queue-reason", func(w http.ResponseWriter, r *http.Request) {
		_, err := server.DB.Exec("UPDATE sessions SET queue_reason = ? WHERE issue = ? AND ended_at IS NULL", r.URL.Query().Get("reason"), r.PathValue("issue"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	server.Mux.HandleFunc("DELETE /e2e/dispatches/{owner}/{name}/{issue}", func(w http.ResponseWriter, r *http.Request) {
		repository := r.PathValue("owner") + "/" + r.PathValue("name")
		issue, err := strconv.ParseInt(r.PathValue("issue"), 10, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// The dispatch writes its Lead event last.
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			var events int
			if err := server.DB.QueryRow("SELECT COUNT(*) FROM lead_events WHERE repository = ? AND issue = ?", repository, issue).Scan(&events); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if events > 0 {
				break
			}
			if time.Now().After(deadline) {
				http.Error(w, "no dispatch", http.StatusNotFound)
				return
			}
		}
		for _, query := range []string{
			"DELETE FROM chat_messages WHERE id IN (SELECT chat_message FROM lead_events WHERE repository = ?1 AND issue = ?2)",
			"DELETE FROM lead_events WHERE repository = ?1 AND issue = ?2",
			"DELETE FROM events WHERE repository = ?1 AND issue = ?2",
			"DELETE FROM tasks WHERE repository = ?1 AND issue = ?2",
		} {
			if _, err := server.DB.Exec(query, repository, issue); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		github.RemoveLabel(repository, issue, "mobius:working", "owner")
		if label := r.URL.Query().Get("label"); label != "" {
			github.AddLabel(repository, issue, label, "owner")
		}
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
	github.PushCommit("owner/shop", "mobius/41", "Add the plan model")

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
	testkit.WaitFor(t, func() bool {
		var open int
		if err := server.DB.QueryRow("SELECT COUNT(*) FROM inbox_items WHERE kind = 'question'").Scan(&open); err != nil {
			t.Fatal(err)
		}
		return open == 1
	})
	chat(ctx, t, server, shop, question, "Lead")
	chat(ctx, t, server, engine.ChatKey{Organization: "plants", Repository: "plants/garden", Workstream: 12}, "Which roses sell best?", "Lead")
	coordinator := engine.ChatKey{Organization: "owner"}
	chat(ctx, t, server, coordinator, "Start a Workstream for gift cards.", "Coordinator")
	// The Lead chat and the Coordinator chat of plants keep their unread replies for the organization switch and for the
	// unread count of the Chat tab.
	chat(ctx, t, server, engine.ChatKey{Organization: "plants"}, "Start a Workstream for gift cards.", "Coordinator")
	for _, key := range []engine.ChatKey{shop, coordinator} {
		view, err := server.Engine.ChatView(ctx, key, 0)
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
				InboxItems:   "[]",
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
	seeds := engine.ChatKey{Organization: "plants", Repository: "plants/garden", Workstream: 60}
	addSeedNote := func(text string) store.ChatMessage {
		message, err := queries.AddChatMessage(ctx, store.AddChatMessageParams{
			Organization: seeds.Organization,
			Repository:   seeds.Repository,
			Workstream:   seeds.Workstream,
			Author:       "Lead",
			InboxItems:   "[]",
			Time:         time.Now().UTC().Format(time.RFC3339Nano),
			Text:         text + " " + strings.Repeat("word ", 100),
		})
		if err != nil {
			t.Fatal(err)
		}
		return message
	}
	var seen int64
	for n := 1; n <= 50; n++ {
		message := addSeedNote(fmt.Sprintf("Seed note %d.", n))
		if n == 15 {
			seen = message.ID
		}
	}
	if err := server.Engine.SeeChat(ctx, seeds, seen); err != nil {
		t.Fatal(err)
	}
	server.Mux.HandleFunc("POST /e2e/seed-notes/{count}", func(w http.ResponseWriter, r *http.Request) {
		count, err := strconv.Atoi(r.PathValue("count"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for n := 1; n <= count; n++ {
			addSeedNote(fmt.Sprintf("Late note %d.", n))
		}
	})
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
			InboxItems:   "[]",
			Time:         time.Now().UTC().Format(time.RFC3339Nano),
			Text:         text,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, message := range []struct {
		text       string
		inboxItems string
		colors     []color.RGBA
	}{
		{"This is the new plan page.", `[{"reference":"PR #44","title":"Integrate loyalty plans","reason":"PR #44 is ready to merge","link":"https://github.com/plants/garden/pull/44"}]`, []color.RGBA{{37, 99, 235, 255}, {22, 163, 74, 255}}},
		{"", "[]", []color.RGBA{{220, 38, 38, 255}}},
	} {
		added, err := queries.AddChatMessage(ctx, store.AddChatMessageParams{
			Organization: "plants",
			Repository:   "plants/garden",
			Workstream:   25,
			Author:       "Owner",
			InboxItems:   message.inboxItems,
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
	setNeedsHuman(t, server, github, spec.Task)
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
	testkit.WaitFor(t, func() bool {
		found, err := server.Engine.NeedsHuman(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(found, func(issue engine.NeedsHuman) bool {
			return issue.Number == 41 && issue.Stopped && issue.PullRequest == 44
		}) && slices.Contains(github.Labels("owner/shop", 41), "mobius:needs-human")
	})
	if err := queries.SetHarnessPause(ctx, store.SetHarnessPauseParams{Harness: "antigravity", PausedUntil: "2026-09-28T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for number, state := range map[int64]string{38: "checks", 39: "approval"} {
		if _, err := server.DB.Exec(`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at)
			VALUES ('owner/shop', 'owner/shop', ?, 12, ?, ?)`, number, state, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		github.AddLabel("owner/shop", number, "mobius:working", testkit.AppSlug+"[bot]")
	}
	if _, err := server.DB.Exec(`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at)
		VALUES ('owner/shop', 'owner/shop', 40, 14, 'ready_for_review', ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := server.DB.Exec(`INSERT INTO lead_events (repository, workstream, issue, kind, payload, time, delivered_at)
		VALUES ('owner/shop', 12, 41, 'stop', '2026-09-28 09:30 UTC stop of #41 "Add plan model": the CI of the head commit failed, and a fix round cannot change it.', '2026-09-28T09:30:00Z', '2026-09-28T09:30:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, version := range []struct{ author, reason, text string }{
		{"curator", "Add: two Implementer sessions used npm and not pnpm", "Run make fmt before each commit.\nUse pnpm for the frontend.\n"},
		{"curator", "Add: three fix rounds repeated the same wait with a fixed sleep in tests", "Run make fmt before each commit.\nUse pnpm for the frontend.\nWait for a condition with testkit.WaitFor.\n"},
		{"owner", "", "Run make fmt before each commit and each push.\nUse pnpm for the frontend.\nWait for a condition with testkit.WaitFor.\nWrite each message in Simplified Technical English.\n"},
		{"curator", "Add: fix rounds searched the same log files again", "Run make fmt before each commit and each push.\nUse pnpm for the frontend.\nWait for a condition with testkit.WaitFor.\nWrite each message in Simplified Technical English.\nCheck the logs of each failed job before you change a test, and name the test that failed, the line of the log that shows the failure, the cause that you found, and the change that fixes the cause, so the next fix round does not repeat the same search of the log files in the same pull request.\n"},
		{"curator", "Change: the long line names the edit of a test", "Run make fmt before each commit and each push.\nUse pnpm for the frontend.\nWait for a condition with testkit.WaitFor.\nWrite each message in Simplified Technical English.\nCheck the logs of each failed job before you edit a test, and name the test that failed, the line of the log that shows the failure, the cause that you found, and the change that fixes the cause, so the next Implementer does not repeat the same search of the log files in the same pull request.\n"},
	} {
		if err := server.Engine.SaveMemory(ctx, "owner/shop", version.author, version.reason, version.text); err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range []struct {
		number  int64
		harness string
		reason  string
	}{
		{38, "claude-code", "runs .mobius/check"},
		{39, "claude-code", "waits for a check slot"},
		{45, "antigravity", "paused until 2026-09-28 12:00 UTC"},
		{42, "claude-code", "no free Implementer slot (2/2)"},
	} {
		if _, err := server.DB.Exec(`INSERT INTO sessions (role, harness, model, organization, repository, workstream, issue, started_at, queue_reason)
			VALUES (?, ?, 'sonnet', 'owner', 'owner/shop', 12, ?, ?, ?)`,
			engine.ImplementerRole, session.harness, session.number, time.Now().UTC().Format(time.RFC3339Nano), session.reason); err != nil {
			t.Fatal(err)
		}
	}
	fixTimes(t, server, github)
	addTurnUsage(t, server)
	addStepTimes(t, server)
	for query, last := range map[string]*atomic.Int64{
		"SELECT MAX(id) FROM step_times":  &lastStepTime,
		"SELECT MAX(id) FROM task_states": &lastTaskState,
	} {
		var id int64
		if err := server.DB.QueryRow(query).Scan(&id); err != nil {
			t.Fatal(err)
		}
		last.Store(id)
	}
	go func() { _, _ = server.Engine.Drain(ctx) }()
	testkit.WaitFor(t, func() bool { return server.Engine.Draining().On })
	held := implementerSpec(t, server, 35)
	go func() { _, _ = server.Engine.Start(ctx, held) }()
	testkit.WaitFor(t, func() bool {
		result, err := server.DB.Exec("UPDATE sessions SET started_at = ? WHERE queue_reason = 'Mobius prepares an upgrade'", "2026-09-28T09:30:00Z")
		if err != nil {
			t.Fatal(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			t.Fatal(err)
		}
		return changed > 0
	})
	<-ctx.Done()
	_ = srv.Close()
}

// chat sends the Owner message text to the chat of key, and waits for the reply of author and the end of the session.
func chat(ctx context.Context, t *testing.T, server *testserver.Server, key engine.ChatKey, text, author string) {
	t.Helper()
	if err := server.Engine.SendChat(ctx, key, "", text, nil, nil); err != nil {
		t.Fatal(err)
	}
	waitForChat(t, server, key, author)
}

// waitForChat waits for a message of author in the chat of key, and then for the end of each session.
func waitForChat(t *testing.T, server *testserver.Server, key engine.ChatKey, author string) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		view, err := server.Engine.ChatView(t.Context(), key, 0)
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

// fixTools makes each tool of the Checkup page give the same path and version in each run. The directory of the fake
// Harness commands comes first in the PATH of the agents, so the programs there hide the programs of the machine.
// tar gives no version. The fake npm registry gives a fixed newest version of the critical tools.
func fixTools(t *testing.T, dataDir string) {
	t.Helper()
	harnesses := filepath.Join(dataDir, "harnesses")
	for program, output := range map[string]string{
		"git":    "git version 2.50.1\n",
		"curl":   "curl 8.7.1 (x86_64-apple-darwin23.0) libcurl/8.7.1 (SecureTransport) LibreSSL/3.3.6 zlib/1.2.12 nghttp2/1.61.0\n",
		"tar":    "",
		"claude": "2.1.284 (Claude Code)\n",
	} {
		testkit.InstallFakeProgram(t, harnesses, program, output)
	}
	t.Setenv("CLAUDE_CODE_EXECUTABLE", filepath.Join(harnesses, "claude"))
	registry := testkit.NewFakeNPM(t)
	registry.SetVersion("@agentclientprotocol/claude-agent-acp", "0.5.0")
	registry.SetVersion("@anthropic-ai/claude-code", "2.1.300")
	was := engine.NPMRegistry
	engine.NPMRegistry = registry.URL
	t.Cleanup(func() { engine.NPMRegistry = was })
}

// addTurnUsage adds the turns of the Usage screen on the 7 days that end on 2026-10-15, the date that the screenshots
// test sets. It runs after fixTimes, which moves the turns of the fake agents before these 7 days. The first turn is
// before the 7 days too. The turns of devin have no cost. The turns use the sessions that exist, because a new
// session changes the ids of the later sessions.
func addTurnUsage(t *testing.T, server *testserver.Server) {
	t.Helper()
	rows, err := server.DB.Query("SELECT id FROM sessions ORDER BY id LIMIT 4")
	if err != nil {
		t.Fatal(err)
	}
	var sessions []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	type turn struct {
		harness, model, reported, effort, role, repository, startedAt string
		input, output, cacheRead, cacheWrite                          int64
		cost                                                          sql.NullFloat64
	}
	price := func(cost float64) sql.NullFloat64 { return sql.NullFloat64{Float64: cost, Valid: true} }
	turns := []turn{
		{"claude-code", "sonnet", "claude-sonnet-5-5", "high", engine.ImplementerRole, "owner/shop", "2026-10-08T10:00:00Z", 11000, 2900, 190000, 16000, price(0.55)},
		{"claude-code", "sonnet", "claude-sonnet-5-5", "high", engine.ImplementerRole, "owner/shop", "2026-10-09T10:00:00Z", 12000, 3400, 210000, 18000, price(0.62)},
		{"claude-code", "sonnet", "claude-sonnet-5-5", "high", engine.ImplementerRole, "owner/shop", "2026-10-10T11:00:00Z", 20000, 5100, 340000, 25000, price(1.04)},
		{"claude-code", "opus", "claude-opus-5-5", "medium", engine.LeadRole, "owner/shop", "2026-10-11T12:00:00Z", 9000, 2500, 120000, 15000, price(2.10)},
		{"claude-code", "sonnet", "claude-sonnet-5-5", "high", engine.ImplementerRole, "owner/shop", "2026-10-12T09:30:00Z", 8000, 2100, 150000, 9000, price(0.41)},
		{"claude-code", "sonnet", "claude-sonnet-5-5", "low", engine.ReviewerRole, "plants/garden", "2026-10-12T15:00:00Z", 6000, 1500, 90000, 7000, price(0.28)},
		{"claude-code", "sonnet", "claude-sonnet-5-5", "high", engine.ImplementerRole, "owner/shop", "2026-10-13T14:00:00Z", 31000, 7800, 520000, 40000, price(1.58)},
		{"claude-code", "opus", "claude-opus-5-5", "medium", engine.LeadRole, "owner/shop", "2026-10-14T16:00:00Z", 14000, 3900, 210000, 22000, price(3.45)},
		{"claude-code", "sonnet", "claude-sonnet-5-5", "low", engine.ReviewerRole, "plants/garden", "2026-10-14T10:00:00Z", 7000, 1800, 110000, 8000, price(0.33)},
		{"claude-code", "sonnet", "claude-sonnet-5-5", "high", engine.ImplementerRole, "owner/shop", "2026-10-15T08:00:00Z", 15000, 4300, 260000, 21000, price(0.83)},
		{"devin", "swe-1.5", "", "", engine.ImplementerRole, "plants/garden", "2026-10-10T13:00:00Z", 18000, 4200, 52000, 6000, sql.NullFloat64{}},
		{"devin", "swe-1.5", "", "", engine.ImplementerRole, "plants/garden", "2026-10-13T11:00:00Z", 22000, 5600, 71000, 8000, sql.NullFloat64{}},
		{"devin", "swe-1.5", "", "", engine.ImplementerRole, "plants/garden", "2026-10-15T10:00:00Z", 9000, 2300, 33000, 3000, sql.NullFloat64{}},
	}
	for i, turn := range turns {
		organization, _, _ := strings.Cut(turn.repository, "/")
		if _, err := server.DB.Exec(`INSERT INTO turn_usage (session, workstream, organization, repository, role, harness, model,
			reported_model, effort, started_at, ended_at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
			VALUES (?, 12, ?, ?, ?, ?, ?, nullif(?, ''), nullif(?, ''), ?, ?, ?, ?, ?, ?, ?)`,
			sessions[i%len(sessions)], organization, turn.repository, turn.role, turn.harness, turn.model, turn.reported, turn.effort,
			turn.startedAt, turn.startedAt, turn.input, turn.output, turn.cacheRead, turn.cacheWrite, turn.cost); err != nil {
			t.Fatal(err)
		}
	}
}

// addStepTimes adds the steps and the task states of the tab "Times" on the 8 days that end on 2026-10-15. It runs after
// fixTimes, which moves the rows of the fake agents before these days. The rows use the sessions and the tasks that
// exist, because a new session or a new issue changes the ids of the later ones. A direct insert of a task state keeps
// the trigger of tasks out.
func addStepTimes(t *testing.T, server *testserver.Server) {
	t.Helper()
	ids := func(query string, count int) []int64 {
		rows, err := server.DB.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if len(ids) < count {
			t.Fatalf("%q gives %d rows, want %d", query, len(ids), count)
		}
		return ids
	}
	sessions := ids("SELECT id FROM sessions ORDER BY id LIMIT 4", 4)
	tasks := ids("SELECT id FROM tasks ORDER BY id LIMIT 3", 3)
	ended := func(startedAt string, seconds int64) time.Time {
		started, err := time.Parse(time.RFC3339, startedAt)
		if err != nil {
			t.Fatal(err)
		}
		return started.Add(time.Duration(seconds) * time.Second)
	}

	type step struct {
		kind, harness, model, effort, role, repository, startedAt string
		seconds, attempt                                          int64
		result                                                    string
	}
	steps := []step{
		{"queue", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-08T09:50:00Z", 90, 0, ""},
		{"prepare", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-08T10:00:00Z", 14, 0, ""},
		{"check", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-08T10:20:00Z", 312, 1, "fail"},
		{"check", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-08T10:30:00Z", 287, 2, ""},
		{"push", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-08T10:36:00Z", 4, 0, ""},
		{"ci", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-08T10:40:00Z", 540, 0, ""},
		{"queue", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-09T09:55:00Z", 45, 0, ""},
		{"check", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-09T10:20:00Z", 301, 1, ""},
		{"ci", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-09T10:40:00Z", 610, 0, "fail"},
		{"queue", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-10T10:40:00Z", 1260, 0, ""},
		{"check_slot", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-10T10:45:00Z", 75, 0, ""},
		{"low_load", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-10T10:46:00Z", 120, 0, ""},
		{"check", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-10T11:10:00Z", 905, 1, "timeout"},
		{"pull", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-10T11:30:00Z", 6, 0, ""},
		{"push", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-10T11:31:00Z", 5, 0, ""},
		{"queue", "devin", "swe-1.5", "", engine.ImplementerRole, "plants/garden", "2026-10-10T12:50:00Z", 33, 0, ""},
		{"check", "devin", "swe-1.5", "", engine.ImplementerRole, "plants/garden", "2026-10-10T13:20:00Z", 450, 1, ""},
		{"ci", "devin", "swe-1.5", "", engine.ImplementerRole, "plants/garden", "2026-10-10T13:40:00Z", 380, 0, ""},
		{"queue", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-12T09:00:00Z", 70, 0, ""},
		{"check", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-12T09:40:00Z", 295, 1, ""},
		{"ci", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-12T10:00:00Z", 8400, 0, ""},
		{"queue", "claude-code", "sonnet", "low", engine.ReviewerRole, "plants/garden", "2026-10-12T14:50:00Z", 22, 0, ""},
		{"queue", "devin", "swe-1.5", "", engine.ImplementerRole, "plants/garden", "2026-10-13T10:55:00Z", 51, 0, ""},
		{"check", "devin", "swe-1.5", "", engine.ImplementerRole, "plants/garden", "2026-10-13T11:20:00Z", 640, 1, "fail"},
		{"check", "devin", "swe-1.5", "", engine.ImplementerRole, "plants/garden", "2026-10-13T11:40:00Z", 700, 2, ""},
		{"ci", "devin", "swe-1.5", "", engine.ImplementerRole, "plants/garden", "2026-10-13T12:00:00Z", 1020, 0, "stopped"},
		{"queue", "claude-code", "sonnet", "low", engine.ReviewerRole, "plants/garden", "2026-10-14T09:55:00Z", 140, 0, ""},
		{"queue", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-15T07:50:00Z", 55, 0, ""},
		{"check", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-15T08:20:00Z", 320, 1, ""},
		{"push", "claude-code", "sonnet", "high", engine.ImplementerRole, "owner/shop", "2026-10-15T08:30:00Z", 3, 0, ""},
	}
	for i, step := range steps {
		organization, _, _ := strings.Cut(step.repository, "/")
		if _, err := server.DB.Exec(`INSERT INTO step_times (kind, session, task, issue, workstream, organization, repository, role,
			harness, model, effort, started_at, ended_at, attempt, result)
			VALUES (?, ?, ?, 41, 12, ?, ?, ?, ?, ?, nullif(?, ''), ?, ?, nullif(?, 0), nullif(?, ''))`,
			step.kind, sessions[i%len(sessions)], tasks[i%len(tasks)], organization, step.repository, step.role, step.harness,
			step.model, step.effort, step.startedAt, ended(step.startedAt, step.seconds).Format(time.RFC3339Nano), step.attempt,
			step.result); err != nil {
			t.Fatal(err)
		}
	}

	type state struct {
		state, startedAt, harness, model, effort string
		seconds                                  int64
	}
	states := []state{
		{"dispatched", "2026-10-08T09:40:00Z", "", "", "", 600},
		{"queued", "2026-10-08T09:50:00Z", "claude-code", "sonnet", "high", 90},
		{"working", "2026-10-08T10:00:00Z", "claude-code", "sonnet", "high", 2400},
		{"checks", "2026-10-08T10:40:00Z", "claude-code", "sonnet", "high", 540},
		{"approval", "2026-10-08T10:50:00Z", "claude-code", "sonnet", "high", 10800},
		{"dispatched", "2026-10-09T09:40:00Z", "", "", "", 900},
		{"working", "2026-10-09T10:00:00Z", "claude-code", "sonnet", "high", 3300},
		{"approval", "2026-10-09T11:00:00Z", "", "", "", 187200},
		{"queued", "2026-10-10T10:40:00Z", "claude-code", "sonnet", "high", 1260},
		{"working", "2026-10-10T11:00:00Z", "claude-code", "sonnet", "high", 5400},
		{"needs_human", "2026-10-10T12:30:00Z", "claude-code", "sonnet", "high", 14400},
		{"queued", "2026-10-10T12:50:00Z", "devin", "swe-1.5", "", 33},
		{"working", "2026-10-10T13:00:00Z", "devin", "swe-1.5", "", 2700},
		{"checks", "2026-10-10T13:40:00Z", "devin", "swe-1.5", "", 380},
		{"working", "2026-10-12T09:00:00Z", "claude-code", "sonnet", "high", 4200},
		{"checks", "2026-10-12T10:00:00Z", "claude-code", "sonnet", "high", 8400},
		{"ready_for_review", "2026-10-12T12:20:00Z", "claude-code", "sonnet", "high", 25200},
		{"queued", "2026-10-13T10:55:00Z", "devin", "swe-1.5", "", 51},
		{"working", "2026-10-13T11:00:00Z", "devin", "swe-1.5", "", 3600},
		{"approval", "2026-10-14T09:00:00Z", "", "", "", 7200},
		{"working", "2026-10-15T08:00:00Z", "claude-code", "sonnet", "high", 1800},
	}
	for i, state := range states {
		repository := "owner/shop"
		if state.harness == "devin" || i%5 == 4 {
			repository = "plants/garden"
		}
		organization, _, _ := strings.Cut(repository, "/")
		session := sql.NullInt64{Int64: sessions[i%len(sessions)], Valid: state.harness != ""}
		role := sql.NullString{String: engine.ImplementerRole, Valid: state.harness != ""}
		if _, err := server.DB.Exec(`INSERT INTO task_states (task, state, issue, workstream, organization, repository, session,
			role, harness, model, effort, started_at, ended_at)
			VALUES (?, ?, 41, 12, ?, ?, ?, ?, nullif(?, ''), nullif(?, ''), nullif(?, ''), ?, ?)`,
			tasks[i%len(tasks)], state.state, organization, repository, session, role, state.harness, state.model, state.effort,
			state.startedAt, ended(state.startedAt, state.seconds).Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
}

// fixTimes gives one fixed time to each time that the UI shows, so each run gives the same screenshots. An event text
// starts with its time in the format of the engine.
func fixTimes(t *testing.T, server *testserver.Server, github *testkit.FakeGitHub) {
	t.Helper()
	github.SetCreatedAt("owner/shop", 12, 1790587800)
	github.SetCreatedAt("plants/garden", 30, 1790587800)
	for _, query := range []string{
		"UPDATE device_logins SET created_at = ?1",
		"UPDATE events SET time = ?1",
		"UPDATE sessions SET started_at = ?1, ended_at = iif(ended_at IS NULL, NULL, ?1)",
		"UPDATE transcript SET time = ?1",
		"UPDATE turn_usage SET started_at = ?1, ended_at = ?1",
		"UPDATE step_times SET started_at = ?1, ended_at = ?1",
		"UPDATE task_states SET started_at = ?1, ended_at = ?1",
		"UPDATE chat_messages SET time = ?1, delivered_at = iif(delivered_at IS NULL, NULL, ?1), stopped_at = iif(stopped_at IS NULL, NULL, ?1)",
		"UPDATE inbox_items SET time = ?1",
		"UPDATE memory_versions SET time = ?1",
	} {
		if _, err := server.DB.Exec(query, "2026-09-28T09:30:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := server.DB.Exec("UPDATE chat_messages SET text = '2026-09-28 09:30 UTC' || substr(text, 21) WHERE author = 'Event' AND text LIKE '____-__-__ __:__ UTC %'"); err != nil {
		t.Fatal(err)
	}
}

// setNeedsHuman moves the task to needs_human, so that the poll keeps mobius:needs-human on its issue. The Worker of
// the task stays. continueByRemoval resumes the task for each removal of the label that is newer than needs_human_at.
// The fake GitHub dates its events from its own clock, which runs ahead of the wall clock, and a poll of the task
// before the move can still write a removal. Thus needs_human_at is one hour after the clock of the fake GitHub.
func setNeedsHuman(t *testing.T, server *testserver.Server, github *testkit.FakeGitHub, task int64) {
	t.Helper()
	since := github.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'needs_human', needs_human_at = ? WHERE id = ?", since, task); err != nil {
		t.Fatal(err)
	}
}

// implementerSpec adds a queued task of the issue number of the Workstream owner/shop#12, and gives the spec of its
// Implementer.
func implementerSpec(t *testing.T, server *testserver.Server, number int64) engine.Spec {
	t.Helper()
	queuedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := server.DB.Exec(`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, dispatched_at, queued_at)
		VALUES ('owner/shop', 'owner/shop', ?, 12, 'queued', ?, ?)`, number, queuedAt, queuedAt)
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
