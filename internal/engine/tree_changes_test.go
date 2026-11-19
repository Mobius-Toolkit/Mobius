package engine_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestATrustedUserAddsASubIssueBelowATaskAndPollAddsItsRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	fake.CreateIssue(shop, 60, "Invoice model", "Store invoices.", "alice")
	fake.AddLabel(shop, 60, "bug", "owner")

	fake.AddSubIssueBy(shop, 41, 60, "owner")

	testkit.WaitFor(t, func() bool {
		return slices.Contains(issueNumbers(t, server), 60)
	})
	want := []copiedIssue{
		{Workstream: 12, Number: 41, Parent: 12, State: "open", Author: "owner"},
		{Workstream: 12, Number: 50, Parent: 41, State: "open", Author: "owner"},
		{Workstream: 12, Number: 60, Parent: 41, State: "open", Author: "alice"},
	}
	if got := copiedIssues(t, server); !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %+v, want %+v", got, want)
	}
	var body string
	if err := server.DB.QueryRow("SELECT body FROM issues WHERE repository = ? AND number = 60", shop).Scan(&body); err != nil || body != "Store invoices." {
		t.Errorf("body = %q, %v", body, err)
	}
	if got := copiedLabels(t, server, 60); !slices.Equal(got, []string{"bug"}) {
		t.Errorf("labels = %v", got)
	}
	testkit.WaitFor(t, func() bool {
		return doneChangesOfPersons(t, server) == 1 && incomingChanges(t, server, "pending") == 0 && ownWrites(t, server) == 0
	})
}

func TestATrustedUserMovesASubIssueAndItsSubtreeToAnotherWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddIssue(shop, 13, "Billing")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 60, "Invoice totals")
	fake.AddSubIssue(shop, 13, 60)
	server := startCopied(t, fake)

	fake.AddSubIssueBy(shop, 60, 41, "owner")

	want := []copiedIssue{
		{Workstream: 13, Number: 60, Parent: 13, State: "open", Author: "owner"},
		{Workstream: 13, Number: 41, Parent: 60, State: "open", Author: "owner"},
		{Workstream: 13, Number: 50, Parent: 41, State: "open", Author: "owner"},
	}
	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(copiedIssues(t, server), want)
	})
	testkit.WaitFor(t, func() bool {
		return doneChangesOfPersons(t, server) == 1 && incomingChanges(t, server, "pending") == 0
	})
}

func TestATrustedUserRemovesASubIssueAndItsSubtreeLeavesTheTree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)

	fake.RemoveSubIssueBy(shop, 12, 41, "owner")

	testkit.WaitFor(t, func() bool { return len(issueNumbers(t, server)) == 0 })
	testkit.WaitFor(t, func() bool {
		return doneChangesOfPersons(t, server) == 1 && incomingChanges(t, server, "pending") == 0
	})
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number IN (41, 50)", shop); got != 0 {
		t.Errorf("remaining detached rows = %d", got)
	}
}

func TestATrustedUserMovesASubIssueOutsideAllTreesAndItLeavesItsTree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 90, "Outside")
	server := startWithTasks(t, fake)

	fake.AddSubIssueBy(shop, 90, 41, "owner")

	testkit.WaitFor(t, func() bool { return len(issueNumbers(t, server)) == 0 })
	testkit.WaitFor(t, func() bool {
		return doneChangesOfPersons(t, server) == 1 && incomingChanges(t, server, "pending") == 0
	})
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number IN (41, 50)", shop); got != 0 {
		t.Errorf("remaining detached rows = %d", got)
	}
}

func TestATrustedUserAddsAndRemovesABlockerAndBlockersChanges(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 60, "Invoice model")
	server := startWithTasks(t, fake)

	fake.AddBlockedByBy(shop, 50, 60, "owner")

	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(copiedBlockers(t, server), []copiedBlocker{{Issue: 50, Repository: shop, Number: 60}})
	})
	testkit.WaitFor(t, func() bool { return doneChangesOfPersons(t, server) == 1 })

	fake.RemoveBlockedByBy(shop, 50, 60, "owner")

	testkit.WaitFor(t, func() bool { return len(copiedBlockers(t, server)) == 0 })
	testkit.WaitFor(t, func() bool {
		return doneChangesOfPersons(t, server) == 2 && incomingChanges(t, server, "pending") == 0
	})
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 60", shop); got != 0 {
		t.Errorf("remaining detached blocker rows = %d", got)
	}
}

func TestAnUntrustedPersonCannotChangeTreeOrBlockersAndGitHubIsWrittenBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 60, "Untrusted child")
	fake.AddIssue(shop, 61, "Untrusted blocker")
	server := startWithTasks(t, fake)

	fake.AddSubIssueBy(shop, 41, 60, "mallory")
	fake.AddBlockedByBy(shop, 50, 61, "mallory")

	testkit.WaitFor(t, func() bool {
		return !slices.Contains(fake.SubIssueNumbers(shop, 41), 60) && len(fake.BlockerNumbers(shop, 50)) == 0
	})
	testkit.WaitFor(t, func() bool {
		return ownWrites(t, server) == 0 && doneChangesOfPersons(t, server) == 2 && incomingChanges(t, server, "pending") == 0
	})
	waitForPolls(t, fake)
	if got := issueNumbers(t, server); !slices.Equal(got, []int64{41, 50}) {
		t.Errorf("issues = %v, want [41 50]", got)
	}
	if got := copiedBlockers(t, server); len(got) != 0 {
		t.Errorf("blockers = %+v", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number IN (60, 61)", shop); got != 0 {
		t.Errorf("untrusted issue rows = %d", got)
	}
}

func TestAnUntrustedPersonCannotMoveOrRemoveATreeIssueOrBlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddIssue(shop, 60, "Invoice model")
	fake.AddSubIssue(shop, 12, 60)
	fake.AddBlockedBy(shop, 50, 60)
	server := startCopied(t, fake)

	fake.AddSubIssueBy(shop, 12, 50, "mallory")
	fake.RemoveBlockedByBy(shop, 50, 60, "mallory")

	testkit.WaitFor(t, func() bool {
		return slices.Contains(fake.SubIssueNumbers(shop, 41), 50) &&
			!slices.Contains(fake.SubIssueNumbers(shop, 12), 50) &&
			slices.Equal(fake.BlockerNumbers(shop, 50), []int64{60})
	})
	testkit.WaitFor(t, func() bool {
		return ownWrites(t, server) == 0 && doneChangesOfPersons(t, server) == 2 && incomingChanges(t, server, "pending") == 0
	})
	waitForPolls(t, fake)
	wantIssues := []copiedIssue{
		{Workstream: 12, Number: 41, Parent: 12, State: "open", Author: "owner"},
		{Workstream: 12, Number: 50, Parent: 41, State: "open", Author: "owner"},
		{Workstream: 12, Number: 60, Parent: 12, State: "open", Author: "owner"},
	}
	if got := copiedIssues(t, server); !reflect.DeepEqual(got, wantIssues) {
		t.Errorf("issues = %+v, want %+v", got, wantIssues)
	}
	if got := copiedBlockers(t, server); len(got) != 1 || got[0].Issue != 50 || got[0].Number != 60 {
		t.Errorf("blockers = %+v", got)
	}
}

func TestTheSendOfCreateIssueAndUpdateBlockersMakesNoIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 42, "Plan schema")
	fake.AddSubIssue(shop, 12, 42)
	script := "[[prompts]]\nwhen = \"Create billing\"\ncall = { tool = \"create_issue\", arguments = { repository = \"owner/shop\", title = \"Add billing\", body = \"\", parent = \"owner/shop#12\", blocked_by = [\"owner/shop#42\"] } }\n\n" +
		"[[prompts]]\nwhen = \"Block #41\"\ncall = { tool = \"update_blockers\", arguments = { repository = \"owner/shop\", n = 41, blocked_by = [\"owner/shop#42\"] } }\n\n" + seen
	server, _ := connect(t, fake, script)
	testkit.WaitFor(t, func() bool {
		return slices.Equal(issueNumbers(t, server), []int64{41, 42})
	})

	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "Create billing.", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.Prompt(t.Context(), "Block #41.", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool {
		return slices.Contains(fake.SubIssueNumbers(shop, 12), 43) &&
			slices.Equal(fake.BlockerNumbers(shop, 43), []int64{42}) &&
			slices.Equal(fake.BlockerNumbers(shop, 41), []int64{42})
	})
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 })
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field IN ('parent', 'blockers') AND actor <> ?", appActor); got != 0 {
		t.Errorf("person tree incoming changes = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field IN ('parent', 'blockers') AND actor = ? AND status = 'done'", appActor); got != 3 {
		t.Errorf("consumed app tree incoming changes = %d, want 3", got)
	}
}

func TestASubIssueBelowABucketMakesNoIncomingChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Bucket 1 of #12")
	fake.AddLabel(shop, 41, "mobius:bucket", "owner")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Task in bucket")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddIssue(shop, 51, "Direct task")
	fake.AddSubIssue(shop, 12, 51)
	server := startCopied(t, fake)

	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if got := doneChangesOfPersons(t, server); got != 0 {
		t.Errorf("done incoming changes of persons = %d", got)
	}
	if got := ownWrites(t, server); got != 0 {
		t.Errorf("own writes = %d", got)
	}

	// Move #51 into the bucket #41 on GitHub: the Workstream parent stays #12, with no incoming change.
	fake.AddSubIssue(shop, 41, 51)

	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 51 AND json_extract(metadata, '$.github.bucket') = 41", shop) == 1
	})
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes after bucket move = %d", got)
	}
	if got := doneChangesOfPersons(t, server); got != 0 {
		t.Errorf("done incoming changes of persons after bucket move = %d", got)
	}
	want := []copiedIssue{
		{Workstream: 12, Number: 50, Parent: 12, State: "open", Author: "owner"},
		{Workstream: 12, Number: 51, Parent: 12, State: "open", Author: "owner"},
	}
	if got := copiedIssues(t, server); !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %+v, want %+v", got, want)
	}
}

func TestAnAppSubIssueWriteWithNoOwnWriteMakesNoWriteBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	fake.CreateIssue(shop, 60, "Check timed out", "The check timed out.", appActor)

	fake.AddSubIssueBy(shop, 12, 60, appActor)

	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'parent' AND actor = ? AND status = 'done'", appActor) == 1
	})
	waitForPolls(t, fake)
	if got := incomingChanges(t, server, "pending"); got != 0 {
		t.Errorf("pending incoming changes = %d", got)
	}
	if !slices.Contains(fake.SubIssueNumbers(shop, 12), 60) {
		t.Errorf("sub-issues of #12 = %v, want #60 kept", fake.SubIssueNumbers(shop, 12))
	}
	want := []copiedIssue{
		{Workstream: 12, Number: 41, Parent: 12, State: "open", Author: "owner"},
		{Workstream: 12, Number: 50, Parent: 41, State: "open", Author: "owner"},
		{Workstream: 12, Number: 60, Parent: 12, State: "open", Author: appActor},
	}
	if got := copiedIssues(t, server); !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %+v, want %+v", got, want)
	}
}

func TestAFailedUpdateCopyRefreshesLabelsAndTextsOnSyncCopyWithoutChangingTreeOrBlockers(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 42, "Document the API")
	fake.AddSubIssue(shop, 12, 42)
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddLabel(shop, 50, "bug", "owner")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddIssue(shop, 60, "Untrusted blocker")
	server := startCopied(t, fake)

	testkit.WaitFor(t, func() bool {
		return slices.Contains(copiedLabels(t, server, 50), "bug") && slices.Equal(issueNumbers(t, server), []int64{41, 50, 42})
	})
	waitForPolls(t, fake)

	issueListReads := func() int {
		reads := 0
		for _, request := range fake.Requests() {
			if request.Method == "GET" && request.Path == "/repos/"+shop+"/issues" {
				reads++
			}
		}
		return reads
	}
	if _, err := server.DB.Exec(`CREATE TRIGGER fail_issue_update BEFORE UPDATE OF metadata ON issues WHEN NEW.number = 50 BEGIN SELECT RAISE(ABORT, 'busy'); END`); err != nil {
		t.Fatal(err)
	}
	beforeReads := issueListReads()
	release := fake.HoldNext("GET /repos/{owner}/{repo}/issues")
	testkit.WaitFor(t, func() bool { return issueListReads() > beforeReads })

	fake.RemoveLabel(shop, 50, "bug", "owner")
	fake.AddLabel(shop, 50, "enhancement", "owner")
	fake.SetTitle(shop, 50, "Store integer cents")
	fake.SetBody(shop, 50, "Store prices in integer cents.")
	fake.CloseIssue(shop, 42)
	fake.AddSubIssueBy(shop, 12, 50, "mallory")
	fake.AddBlockedByBy(shop, 50, 60, "mallory")
	release()

	waitForPoll(t, server, fake)
	if _, err := server.DB.Exec(`DROP TRIGGER fail_issue_update`); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool {
		return copiedTitle(t, server, 50) == "Store integer cents" &&
			slices.Equal(copiedLabels(t, server, 50), []string{"enhancement"}) &&
			doneChangesOfPersons(t, server) == 5 &&
			incomingChanges(t, server, "pending") == 0
	})
	waitForPolls(t, fake)

	var body string
	if err := server.DB.QueryRow("SELECT body FROM issues WHERE repository = ? AND number = 50", shop).Scan(&body); err != nil || body != "Store prices in integer cents." {
		t.Errorf("body = %q, %v", body, err)
	}
	wantIssues := []copiedIssue{
		{Workstream: 12, Number: 41, Parent: 12, State: "open", Author: "owner"},
		{Workstream: 12, Number: 50, Parent: 41, State: "open", Author: "owner"},
		{Workstream: 12, Number: 42, Parent: 12, State: "closed", Author: "owner"},
	}
	if got := copiedIssues(t, server); !reflect.DeepEqual(got, wantIssues) {
		t.Errorf("issues = %+v, want %+v", got, wantIssues)
	}
	if got := copiedBlockers(t, server); len(got) != 0 {
		t.Errorf("blockers = %+v", got)
	}
	if !slices.Contains(fake.SubIssueNumbers(shop, 41), 50) || slices.Contains(fake.SubIssueNumbers(shop, 12), 50) {
		t.Errorf("sub-issues on GitHub: #41=%v, #12=%v", fake.SubIssueNumbers(shop, 41), fake.SubIssueNumbers(shop, 12))
	}
	if got := fake.BlockerNumbers(shop, 50); len(got) != 0 {
		t.Errorf("blockers of #50 on GitHub = %v", got)
	}
}

func TestRecopyTreesKeepsParentAndBlockersOfExistingRowsAndIgnoresUserIssues(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithTasks(t, fake)
	fake.AddIssue(shop, 60, "Untrusted blocker")
	fake.AddIssue(shop, 61, "Untrusted child")

	issueListReads := func() int {
		reads := 0
		for _, request := range fake.Requests() {
			if request.Method == "GET" && request.Path == "/repos/"+shop+"/issues" {
				reads++
			}
		}
		return reads
	}
	beforeReads := issueListReads()
	release := fake.HoldNext("GET /repos/{owner}/{repo}/issues")
	testkit.WaitFor(t, func() bool { return issueListReads() > beforeReads })
	fake.AddBlockedByBy(shop, 50, 60, "mallory")
	fake.AddSubIssueBy(shop, 12, 50, "mallory")
	fake.AddSubIssueBy(shop, 41, 61, "mallory")

	if err := server.Engine.RecopyTrees(t.Context(), shop, 12); err != nil {
		release()
		t.Fatal(err)
	}
	wantIssues := []copiedIssue{
		{Workstream: 12, Number: 41, Parent: 12, State: "open", Author: "owner"},
		{Workstream: 12, Number: 50, Parent: 41, State: "open", Author: "owner"},
	}
	if got := copiedIssues(t, server); !reflect.DeepEqual(got, wantIssues) {
		release()
		t.Fatalf("issues after recopyTrees = %+v, want %+v", got, wantIssues)
	}
	if got := copiedBlockers(t, server); len(got) != 0 {
		release()
		t.Fatalf("blockers after recopyTrees = %+v", got)
	}
	release()

	testkit.WaitFor(t, func() bool {
		return len(fake.BlockerNumbers(shop, 50)) == 0 &&
			slices.Contains(fake.SubIssueNumbers(shop, 41), 50) &&
			!slices.Contains(fake.SubIssueNumbers(shop, 12), 50) &&
			!slices.Contains(fake.SubIssueNumbers(shop, 41), 61)
	})
	testkit.WaitFor(t, func() bool {
		return doneChangesOfPersons(t, server) == 3 && incomingChanges(t, server, "pending") == 0
	})
	if got := copiedIssues(t, server); !reflect.DeepEqual(got, wantIssues) {
		t.Errorf("issues after polls = %+v, want %+v", got, wantIssues)
	}
	if got := copiedBlockers(t, server); len(got) != 0 {
		t.Errorf("blockers after polls = %+v", got)
	}
}

func TestAnOldPollIssueReadAfterLocalBodyEditDoesNotOverwriteIssuesBody(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake,
		editBodyCall("Edit #41.", `{ repository = "owner/shop", n = 41, body = "Updated goal." }`)+seen,
		withUIURL)
	fake.AddIssue(shop, 41, "Plan API")
	fake.SetBody(shop, 41, "Old body")
	fake.AddSubIssue(shop, 12, 41)
	waitForTasks(t, server, []taskLine{line(41, "Plan API", "open", 0)})
	waitForPolls(t, fake)

	releasePatch := fake.HoldNextOf("PATCH /repos/{owner}/{repo}/issues/{number}", "/repos/"+shop+"/issues/41")
	done := make(chan string, 1)
	go func() {
		session := run(t, server, leadSpec(t), "Edit #41.")
		done <- reply(t, server, session)
	}()

	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field = 'body' AND status = 'pending'") == 1
	})
	reached, releaseEvents := fake.HoldIssueEvents(shop, 12)
	fake.SetTitle(shop, 12, "Plan the shop")
	fake.SetTitle(shop, 41, "Plan API updated")
	<-reached

	releasePatch()
	if got := <-done; got != "Changed the body of owner/shop#41." {
		releaseEvents()
		t.Fatalf("reply = %q", got)
	}
	releaseEvents()

	testkit.WaitFor(t, func() bool { return copiedTitle(t, server, 41) == "Plan API updated" })
	var dbBody string
	if err := server.DB.QueryRow("SELECT body FROM issues WHERE repository = ? AND number = 41", shop).Scan(&dbBody); err != nil {
		t.Fatal(err)
	}
	if dbBody != "Updated goal." {
		t.Errorf("issues.body = %q, want %q", dbBody, "Updated goal.")
	}
	wantGH := "Updated goal.\n\n[Open the Lead chat in Mobius](https://mobius.example.com/workstreams/owner/shop/12)"
	if _, gotGH := fake.Issue(shop, 41); gotGH != wantGH {
		t.Errorf("github body = %q, want %q", gotGH, wantGH)
	}
}
