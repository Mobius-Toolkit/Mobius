package engine_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// readyToMerge starts a server whose Owner authorized the Mobius App, and approves #41 with the Lead. It gives the
// open ready for review item of #41, when the task is in ready_for_review.
func readyToMerge(t *testing.T, fake *testkit.FakeGitHub, lead string) (*testserver.Server, inboxItem) {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, leadApproves+lead+leadStarts, commits, noChange)
	authorize(t, server)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	item := waitForItem(t, server, "ready for review")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	return server, item
}

func merge(t *testing.T, server *testserver.Server, id int64) (int, string) {
	t.Helper()
	return send(t, server, http.MethodPost, "/api/inbox/"+itoa(id)+"/merge", "")
}

func approvals(fake *testkit.FakeGitHub) int {
	count := 0
	for _, review := range fake.SubmittedReviews(shop, 42) {
		if review.Event == "APPROVE" {
			count++
		}
	}
	return count
}

func refusedReview(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var id string
	if err := server.DB.QueryRow("SELECT coalesce(max(refused_review), '') FROM tasks WHERE issue = 41").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func countLeadEvents(t *testing.T, server *testserver.Server, kind string) int {
	t.Helper()
	count := 0
	for _, event := range leadEvents(t, server) {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

func TestTheMergeRouteApprovesOnceAndMobiusMergesEndsTheTaskAndDismissesTheItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := readyToMerge(t, fake, "")
	sha := head(t, fake, "mobius/41")

	status, body := merge(t, server, item.ID)

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var result struct {
		Data inboxItem `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Data.Merging || result.Data.ID != item.ID || result.Data.MergeRefusal != "" {
		t.Errorf("item = %+v", result.Data)
	}
	if status, body := merge(t, server, item.ID); status != http.StatusConflict {
		t.Errorf("second status = %d: %s", status, body)
	}

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	testkit.WaitFor(t, func() bool { return len(inbox(t, server)) == 0 })
	if state, _ := fake.State(shop, 41); state != "closed" {
		t.Errorf("state of #41 = %s", state)
	}
	if calls := fake.MergeCalls(); calls != 1 {
		t.Errorf("merge calls = %d", calls)
	}
	if count := approvals(fake); count != 1 {
		t.Errorf("approvals = %d", count)
	}
	for _, review := range fake.SubmittedReviews(shop, 42) {
		if review.Event == "APPROVE" && review.CommitID != sha {
			t.Errorf("commit of the approval = %s, want %s", review.CommitID, sha)
		}
	}
}

func TestTheMergeRouteRefusesATaskThatLeftReadyForReviewAndSendsNoReview(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := readyToMerge(t, fake, leadFindings)
	sendChat(t, server, leadChat, "Send the findings to #41")
	testkit.WaitFor(t, func() bool { return taskState(t, server) != "ready_for_review" })

	status, body := merge(t, server, item.ID)

	if status != http.StatusConflict || !strings.Contains(body, "The task of #41 is not ready for review.") {
		t.Errorf("status = %d: %s", status, body)
	}
	if count := approvals(fake); count != 0 {
		t.Errorf("approvals = %d", count)
	}
}

func TestTheMergeRouteRefusesAPullRequestThatAPersonMergedBeforeThePollEndedTheTask(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := readyToMerge(t, fake, "")
	fake.MergePullRequest(shop, 42)

	status, body := merge(t, server, item.ID)

	if status != http.StatusConflict {
		t.Errorf("status = %d: %s", status, body)
	}
	if count := approvals(fake); count != 0 {
		t.Errorf("approvals = %d", count)
	}
}

func TestTheMergeRouteGives409WithTheStepsWhenTheOwnerDidNotAuthorizeTheApp(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadApproves+leadStarts, commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	item := waitForItem(t, server, "ready for review")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })

	status, body := merge(t, server, item.ID)

	if status != http.StatusConflict || !strings.Contains(body, "tell the Owner to open") {
		t.Errorf("status = %d: %s", status, body)
	}
	if count := approvals(fake); count != 0 {
		t.Errorf("approvals = %d", count)
	}
}

func TestTheMergeRouteGives404ForAnUnknownItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, leadStarts)

	if status, body := merge(t, server, 99); status != http.StatusNotFound {
		t.Errorf("status = %d: %s", status, body)
	}
}

func TestARefusedMergeOfTheMergeRouteGivesTheItemTheReasonAndTheLeadTheEvent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := readyToMerge(t, fake, "")
	fake.RefuseMerge(shop, 42, "Required status check is expected.")

	if status, body := merge(t, server, item.ID); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForLeadPrompt(t, server, " merge refused for #41 \"Add plan model\"")
	testkit.WaitFor(t, func() bool {
		for _, found := range inbox(t, server) {
			if found.ID == item.ID {
				return !found.Merging && found.MergeRefusal == "Required status check is expected."
			}
		}
		return false
	})
	if count := countLeadEvents(t, server, "merge_refused"); count != 1 {
		t.Errorf("merge_refused events = %d", count)
	}
}

func TestAPersonWhoMergesDuringTheMergeOfMobiusCausesNoRefusalAndTheTaskEnds(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, item := readyToMerge(t, fake, "")
	reached, release := fake.HoldMerge(shop, 42)

	if status, body := merge(t, server, item.ID); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	<-reached
	fake.MergePullRequest(shop, 42)
	release()

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	testkit.WaitFor(t, func() bool { return len(inbox(t, server)) == 0 })
	if state, _ := fake.State(shop, 41); state != "closed" {
		t.Errorf("state of #41 = %s", state)
	}
	var refusal string
	if err := server.DB.QueryRow("SELECT merge_refusal FROM inbox_items WHERE id = ?", item.ID).Scan(&refusal); err != nil {
		t.Fatal(err)
	}
	if refusal != "" || refusedReview(t, server) != "" || countLeadEvents(t, server, "merge_refused") != 0 {
		t.Errorf("refusal = %q, refused review = %q, events = %d", refusal, refusedReview(t, server), countLeadEvents(t, server, "merge_refused"))
	}
}
