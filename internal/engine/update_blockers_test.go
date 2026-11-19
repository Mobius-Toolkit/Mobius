package engine_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// blockerWrites gives the number of requests that add or remove a blocker of the issue number in owner/shop.
func blockerWrites(fake *testkit.FakeGitHub, number string) int {
	count := 0
	for _, request := range fake.Requests() {
		if request.Method != "GET" && strings.HasPrefix(request.Path, "/repos/owner/shop/issues/"+number+"/dependencies/blocked_by") {
			count++
		}
	}
	return count
}

// successfulBlockerWrites gives the number of 2xx requests that add or remove a blocker of the issue number in
// owner/shop.
func successfulBlockerWrites(fake *testkit.FakeGitHub, number string) int {
	count := 0
	for _, request := range fake.Requests() {
		if request.Method != "GET" &&
			request.Status >= 200 && request.Status < 300 &&
			strings.HasPrefix(request.Path, "/repos/owner/shop/issues/"+number+"/dependencies/blocked_by") {
			count++
		}
	}
	return count
}

func waitForCopiedBlocker(t *testing.T, server *testserver.Server, issue, blocker int64) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		var count int
		_ = server.DB.QueryRow(
			`SELECT count(*) FROM blockers b
			 JOIN issues i ON i.id = b.issue
			 JOIN issues bi ON bi.id = b.blocker
			 WHERE i.repository = ? AND i.number = ? AND bi.repository = ? AND bi.number = ?`,
			shop, issue, shop, blocker,
		).Scan(&count)
		return count == 1
	})
}

func withBlockers(l taskLine, blockers ...taskBlocker) taskLine {
	l.BlockedBy = blockers
	return l
}

func TestUpdateBlockersAddsABlockerInTheRepositoryAndABlockerInAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40", "owner/garden#7"] }`)+
		"[[prompts]]\ncall = { tool = \"list_tasks\" }\n")
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	waitForTasks(t, server, []taskLine{line(41, "Plan API", "open", 0)})

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	want := "Added owner/shop#40 and owner/garden#7 as blockers of owner/shop#41." +
		"owner/shop#41 Plan API: open, blocked by owner/garden#7, owner/shop#40\n"
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := fake.BlockerNumbers(shop, 41); !reflect.DeepEqual(got, []int64{7, 40}) {
		t.Errorf("blockers = %v", got)
	}
}

func TestUpdateBlockersRemovesTheOpenBlockerThatIsNotInTheListAndWritesNothingTheSecondTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`))
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 40)
	fake.AddForeignBlockedBy(shop, 41, garden, 7)
	waitForTasks(t, server, []taskLine{
		withBlockers(line(41, "Plan API", "open", 0), taskBlocker{Number: 7, Repository: new(garden)}, taskBlocker{Number: 40}),
	})

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	want := "Removed owner/garden#7 as blockers of owner/shop#41.The blockers of owner/shop#41 already match."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := fake.BlockerNumbers(shop, 41); !reflect.DeepEqual(got, []int64{40}) {
		t.Errorf("blockers = %v", got)
	}
	if got := blockerWrites(fake, "41"); got != 1 {
		t.Errorf("writes = %d", got)
	}
}

func TestUpdateBlockersAddsAndRemovesAndKeepsAClosedBlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = [] }`))
	fake.AddIssue(shop, 39, "Closed blocker")
	fake.CloseIssue(shop, 39)
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 39)
	fake.AddForeignBlockedBy(shop, 41, garden, 7)
	waitForCopiedBlocker(t, server, 41, 39)
	waitForTasks(t, server, []taskLine{
		withBlockers(line(41, "Plan API", "open", 0), taskBlocker{Number: 7, Repository: new(garden)}),
	})

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	want := "Added owner/shop#40 and removed owner/garden#7 as blockers of owner/shop#41." +
		"Removed owner/shop#40 as blockers of owner/shop#41."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := fake.BlockerNumbers(shop, 41); !reflect.DeepEqual(got, []int64{39}) {
		t.Errorf("blockers = %v", got)
	}
}

func TestAnIssueWaitsForItsBlockerUntilUpdateBlockersRemovesTheBlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Remove the blocker\"\ncall = { tool = \"update_blockers\", arguments = { repository = \"owner/shop\", n = 41, blocked_by = [] } }\n\n"+seen,
		func(cfg *config.Config) { cfg.LeadIdleTimeout = 5 * time.Second })
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddSubIssue(shop, 12, 40)
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 40)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	dispatchTask(fake, 43, "Price rounding")
	liveTaskOf(t, server, 43)
	waitForCopiedBlocker(t, server, 41, 40)
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 41) {
		t.Fatal("the issue with an open blocker has a task")
	}

	session := run(t, server, leadSpec(t), "Remove the blocker")

	if got := reply(t, server, session); got != "Removed owner/shop#40 as blockers of owner/shop#41." {
		t.Errorf("reply = %q", got)
	}
	if task := liveTaskOf(t, server, 41); task.Workstream != 12 {
		t.Errorf("task = %+v", task)
	}
}

func TestUpdateBlockersRefusesBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 50, blocked_by = ["owner/shop#40"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40", "owner/shop#41"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#45"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40", "owner/shop#40"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 45, blocked_by = [] }`))
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Outside")
	fake.AddPullRequest(shop, 45, "A pull request")
	fake.AddBlockedBy(shop, 41, 40)
	waitForTasks(t, server, []taskLine{
		withBlockers(line(41, "Plan API", "open", 0), taskBlocker{Number: 40}),
	})

	session := run(t, server, leadSpec(t), "1. ", "2. ", "3. ", "4. ", "5. ")

	want := "error: owner/shop#50 is not in this Workstream." +
		"error: owner/shop#41 cannot block itself." +
		"error: owner/shop#45 is not an issue." +
		"error: owner/shop#40 is two times in blocked_by." +
		"error: owner/shop#45 is not an issue."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := blockerWrites(fake, "41") + blockerWrites(fake, "50") + blockerWrites(fake, "45"); got != 0 {
		t.Errorf("writes = %d", got)
	}
	if got := fake.BlockerNumbers(shop, 41); !slices.Equal(got, []int64{40}) {
		t.Errorf("blockers = %v", got)
	}
}

func TestUpdateBlockersRefusesABlockerThatClosesACycleAndWritesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 44, blocked_by = ["owner/shop#42"] }`))
	for number, title := range map[int64]string{40: "A", 41: "B", 42: "C", 43: "D", 44: "E"} {
		fake.AddIssue(shop, number, title)
		fake.AddSubIssue(shop, 12, number)
	}
	fake.AddBlockedBy(shop, 40, 41)
	fake.AddBlockedBy(shop, 42, 43)
	fake.AddBlockedBy(shop, 43, 44)
	waitForTasks(t, server, []taskLine{
		withBlockers(line(40, "A", "open", 0), taskBlocker{Number: 41}),
		line(41, "B", "open", 0),
		withBlockers(line(42, "C", "open", 0), taskBlocker{Number: 43}),
		withBlockers(line(43, "D", "open", 0), taskBlocker{Number: 44}),
		line(44, "E", "open", 0),
	})

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	want := "error: owner/shop#40 cannot block owner/shop#41: owner/shop#40 is already blocked by owner/shop#41." +
		"error: owner/shop#42 cannot block owner/shop#44: owner/shop#42 is already blocked by owner/shop#44 through owner/shop#43."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := fake.BlockerNumbers(shop, 41); len(got) != 0 {
		t.Errorf("blockers of 41 = %v", got)
	}
	if got := fake.BlockerNumbers(shop, 44); len(got) != 0 {
		t.Errorf("blockers of 44 = %v", got)
	}
}

func TestUpdateBlockersAddsABlockerWhoseBlockersDoNotLeadBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 42, blocked_by = ["owner/shop#40"] }`))
	for number, title := range map[int64]string{40: "A", 41: "B", 42: "C"} {
		fake.AddIssue(shop, number, title)
		fake.AddSubIssue(shop, 12, number)
	}
	fake.AddBlockedBy(shop, 40, 41)
	waitForTasks(t, server, []taskLine{
		withBlockers(line(40, "A", "open", 0), taskBlocker{Number: 41}),
		line(41, "B", "open", 0),
		line(42, "C", "open", 0),
	})

	session := run(t, server, leadSpec(t), "1. ")

	if got := reply(t, server, session); got != "Added owner/shop#40 as blockers of owner/shop#42." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.BlockerNumbers(shop, 42); !slices.Equal(got, []int64{40}) {
		t.Errorf("blockers = %v", got)
	}
}

func TestUpdateBlockersChangesBlockersAndAddsOneOutgoingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40", "owner/garden#7"] }`))
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	waitForTasks(t, server, []taskLine{line(41, "Plan API", "open", 0)})

	session := run(t, server, leadSpec(t), "1. ")

	if got := reply(t, server, session); got != "Added owner/shop#40 and owner/garden#7 as blockers of owner/shop#41." {
		t.Errorf("reply = %q", got)
	}
	var count int
	if err := server.DB.QueryRow(`SELECT count(*) FROM blockers b
		JOIN issues i ON i.id = b.issue
		JOIN issues bi ON bi.id = b.blocker
		WHERE i.repository = ? AND i.number = 41
		  AND ((bi.repository = ? AND bi.number = 40) OR (bi.repository = ? AND bi.number = 7))`, shop, shop, garden).Scan(&count); err != nil || count != 2 {
		t.Errorf("blockers rows = %d, %v", count, err)
	}
	var fields []string
	rows, err := server.DB.Query(`SELECT c.field || ':' || c.status FROM outgoing_changes c
		JOIN issues i ON i.id = c.issue
		WHERE i.repository = ? AND i.number = 41`, shop)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			t.Fatal(err)
		}
		fields = append(fields, entry)
	}
	if !slices.Equal(fields, []string{"blockers:sent"}) {
		t.Errorf("outgoing changes = %v", fields)
	}
	if got := fake.BlockerNumbers(shop, 41); !slices.Equal(got, []int64{7, 40}) {
		t.Errorf("github blockers = %v", got)
	}
}

func TestUpdateBlockersTellsTheChangeOn500AndWritesOnceOnPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`)+
		"[[prompts]]\ncall = { tool = \"list_tasks\" }\n")
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	waitForTasks(t, server, []taskLine{line(41, "Plan API", "open", 0)})
	fake.FailBlockerWrites(true)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	want := "Added owner/shop#40 as blockers of owner/shop#41." +
		"owner/shop#41 Plan API: open, blocked by owner/shop#40\n"
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	var pending int
	if err := server.DB.QueryRow(`SELECT count(*) FROM outgoing_changes c
		JOIN issues i ON i.id = c.issue
		WHERE i.repository = ? AND i.number = 41 AND c.field = 'blockers' AND c.status = 'pending'`, shop).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending changes = %d, %v", pending, err)
	}
	if got := fake.BlockerNumbers(shop, 41); len(got) != 0 {
		t.Fatalf("github blockers before retry = %v", got)
	}
	waitForPolls(t, fake)
	waitForCopiedBlocker(t, server, 41, 40)

	fake.FailBlockerWrites(false)
	testkit.WaitFor(t, func() bool {
		var sent int
		_ = server.DB.QueryRow(`SELECT count(*) FROM outgoing_changes c
			JOIN issues i ON i.id = c.issue
			WHERE i.repository = ? AND i.number = 41 AND c.field = 'blockers' AND c.status = 'sent'`, shop).Scan(&sent)
		return sent == 1
	})
	waitForPolls(t, fake)
	if got := fake.BlockerNumbers(shop, 41); !slices.Equal(got, []int64{40}) {
		t.Errorf("github blockers after poll = %v", got)
	}
	if got := successfulBlockerWrites(fake, "41"); got != 1 {
		t.Errorf("successful writes = %d, want 1", got)
	}
}

func TestUpdateBlockersRefusesACycleFromTheBlockersTableAndChangesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 40, blocked_by = ["owner/shop#41"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`))
	fake.AddIssue(shop, 40, "A")
	fake.AddSubIssue(shop, 12, 40)
	fake.AddIssue(shop, 41, "B")
	fake.AddSubIssue(shop, 12, 41)
	waitForTasks(t, server, []taskLine{line(40, "A", "open", 0), line(41, "B", "open", 0)})
	// Hold GitHub writes so owner/shop#40 -> owner/shop#41 exists only in the blockers table.
	fake.FailBlockerWrites(true)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	want := "Added owner/shop#41 as blockers of owner/shop#40." +
		"error: owner/shop#40 cannot block owner/shop#41: owner/shop#40 is already blocked by owner/shop#41."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	var rows, changes int
	if err := server.DB.QueryRow(`SELECT count(*) FROM blockers b JOIN issues i ON i.id = b.issue WHERE i.repository = ? AND i.number = 41`, shop).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("blockers rows of 41 = %d, %v", rows, err)
	}
	if err := server.DB.QueryRow(`SELECT count(*) FROM outgoing_changes c JOIN issues i ON i.id = c.issue WHERE i.repository = ? AND i.number = 41`, shop).Scan(&changes); err != nil || changes != 0 {
		t.Errorf("outgoing changes of 41 = %d, %v", changes, err)
	}
	if got := blockerWrites(fake, "41"); got != 0 {
		t.Errorf("github writes of 41 = %d", got)
	}
}

func TestRemovingASubIssueWithPendingBlockersRefusesTheChangeAndDropsItsBlockers(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`))
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	waitForTasks(t, server, []taskLine{line(41, "Plan API", "open", 0)})
	fake.FailBlockerWrites(true)

	session := run(t, server, leadSpec(t), "1. ")

	if got := reply(t, server, session); got != "Added owner/shop#40 as blockers of owner/shop#41." {
		t.Errorf("reply = %q", got)
	}
	var id41 int64
	if err := server.DB.QueryRow(`SELECT id FROM issues WHERE repository = ? AND number = 41`, shop).Scan(&id41); err != nil {
		t.Fatal(err)
	}

	fake.RemoveSubIssue(shop, 12, 41)
	testkit.WaitFor(t, func() bool {
		var count int
		_ = server.DB.QueryRow(`SELECT count(*) FROM issues WHERE repository = ? AND number = 41`, shop).Scan(&count)
		return count == 0
	})
	var blockerRows, pendingRows, refusedRows, count40 int
	if err := server.DB.QueryRow(`SELECT count(*) FROM blockers`).Scan(&blockerRows); err != nil || blockerRows != 0 {
		t.Errorf("blockers rows after removal = %d, %v", blockerRows, err)
	}
	if err := server.DB.QueryRow(`SELECT count(*) FROM issues WHERE repository = ? AND number = 40`, shop).Scan(&count40); err != nil || count40 != 0 {
		t.Errorf("unused blocker issue 40 rows = %d, %v", count40, err)
	}
	if err := server.DB.QueryRow(`SELECT count(*) FROM outgoing_changes WHERE issue = ? AND status = 'pending'`, id41).Scan(&pendingRows); err != nil || pendingRows != 0 {
		t.Errorf("pending changes after removal = %d, %v", pendingRows, err)
	}
	if err := server.DB.QueryRow(`SELECT count(*) FROM outgoing_changes WHERE issue = ? AND field = 'blockers' AND status = 'refused'`, id41).Scan(&refusedRows); err != nil || refusedRows != 1 {
		t.Errorf("refused changes after removal = %d, %v", refusedRows, err)
	}

	// Add a new sub-issue with its own blocker so SQLite can reuse id41 without inheriting #41's old change.
	fake.AddIssue(shop, 42, "Cache")
	fake.AddIssue(shop, 50, "Billing API")
	fake.AddBlockedBy(shop, 50, 42)
	fake.AddSubIssue(shop, 12, 50)
	waitForTasks(t, server, []taskLine{withBlockers(line(50, "Billing API", "open", 0), taskBlocker{Number: 42})})

	fake.FailBlockerWrites(false)
	waitForPolls(t, fake)
	if got := fake.BlockerNumbers(shop, 50); !slices.Equal(got, []int64{42}) {
		t.Errorf("github blockers of 50 = %v, want [42]", got)
	}
}
