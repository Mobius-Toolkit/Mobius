package engine_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	wontDo   = "mobius:wont-do"
	otherRep = "owner/other"
)

var wontDoComment = testkit.Comment{Author: "mobius-test[bot]", Body: `The Workstream #12 closed as "won't do".`}

type closureItem struct {
	Kind   string `json:"kind"`
	Number int64  `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

func closure(t *testing.T, server *testserver.Server) []closureItem {
	t.Helper()
	status, text := send(t, server, http.MethodGet, "/api/workstreams/owner/shop/12/closure", "")
	var body struct {
		Data []closureItem `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil {
		t.Fatalf("closure: status %d, %v: %s", status, err, text)
	}
	return body.Data
}

func closeWontDo(t *testing.T, server *testserver.Server, number int64) (int, string) {
	t.Helper()
	return send(t, server, http.MethodPost, "/api/workstreams/owner/shop/"+itoa(number)+"/close", "")
}

func unchanged(t *testing.T, fake *testkit.FakeGitHub, repository string, number int64, state string) {
	t.Helper()
	if got, _ := fake.State(repository, number); got != state {
		t.Errorf("state of %s#%d = %s", repository, number, got)
	}
	if comments := fake.Comments(repository, number); len(comments) != 0 {
		t.Errorf("comments of %s#%d = %v", repository, number, comments)
	}
	if slices.Contains(fake.Labels(repository, number), wontDo) {
		t.Errorf("labels of %s#%d = %v", repository, number, fake.Labels(repository, number))
	}
}

func TestACloseAsWontDoClosesTheOpenItemsAndKeepsTheOthers(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addLiveTask(fake)
	fake.AddSubIssueOf(shop, 41, 42, "Store the price in cents")
	fake.AddSubIssueOf(shop, 12, 43, "Old idea")
	fake.CloseIssue(shop, 43)
	fake.AddSubIssueOf(shop, 12, 50, "Nested Workstream")
	fake.AddLabel(shop, 50, "mobius:workstream", "owner")
	fake.AddSubIssueOf(shop, 50, 51, "Nested task")
	fake.AddIssue(otherRep, 7, "Issue of another repository")
	fake.AddForeignSubIssue(shop, 12, otherRep, 7)
	server := startWithLiveTask(t, fake)

	items := closure(t, server)

	want := []closureItem{
		{Kind: "pullRequest", Number: 45, Title: "Add plan model"},
		{Kind: "issue", Number: 41, Title: "Add plan model"},
		{Kind: "issue", Number: 42, Title: "Store the price in cents"},
		{Kind: "issue", Number: 12, Title: "Integrate loyalty plans"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v", items)
	}
	for i, item := range items {
		if item.URL == "" {
			t.Errorf("item %d has no URL", i)
		}
		item.URL = ""
		if item != want[i] {
			t.Errorf("item %d = %+v", i, item)
		}
	}
	if status, body := closeWontDo(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	for _, item := range items {
		number := item.Number
		state, reason := fake.State(shop, number)
		if state != "closed" || (item.Kind == "issue" && reason != "not_planned") {
			t.Errorf("state of #%d = %s, %s", number, state, reason)
		}
		if comments := fake.Comments(shop, number); !slices.Equal(comments, []testkit.Comment{wontDoComment}) {
			t.Errorf("comments of #%d = %v", number, comments)
		}
		if !slices.Contains(fake.Labels(shop, number), wontDo) {
			t.Errorf("labels of #%d = %v", number, fake.Labels(shop, number))
		}
	}
	if labels := fake.Labels(shop, 12); !slices.Contains(labels, "mobius:workstream") {
		t.Errorf("labels of #12 = %v", labels)
	}
	unchanged(t, fake, shop, 43, "closed")
	unchanged(t, fake, shop, 50, "open")
	unchanged(t, fake, shop, 51, "open")
	unchanged(t, fake, otherRep, 7, "open")
	if live(t, server) {
		t.Error("the task of #41 is live")
	}

	waitForPoll(t, server, fake)
	for _, number := range []int64{12, 41, 42, 45} {
		if slices.Contains(fake.Comments(shop, number), closedComment) {
			t.Errorf("comments of #%d = %v", number, fake.Comments(shop, number))
		}
	}
}

func TestACloseAsWontDoOfAClosedWorkstreamIsRefused(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startCopied(t, fake)
	fake.CloseIssue(shop, 12)

	if status, body := closeWontDo(t, server, 12); status != http.StatusConflict {
		t.Errorf("status = %d: %s", status, body)
	}
	if status, body := closeWontDo(t, server, 41); status != http.StatusConflict {
		t.Errorf("status of a task = %d: %s", status, body)
	}
	if status, body := send(t, server, http.MethodGet, "/api/workstreams/owner/shop/12/closure", ""); status != http.StatusConflict {
		t.Errorf("closure status = %d: %s", status, body)
	}
}

func TestACloseAsWontDoStopsTheRunningImplementer(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, hangs, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) > 0 && sessions[0].AcpSessionID.Valid
	})

	if status, body := closeWontDo(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if session := endedImplementers(t, server, 1)[0]; session.EndReason.String != "stopped" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	testkit.WaitFor(t, func() bool { return !live(t, server) && !hasLabel(fake, "mobius:working") })
	for _, number := range []int64{12, 41} {
		if state, reason := fake.State(shop, number); state != "closed" || reason != "not_planned" {
			t.Errorf("state of #%d = %s, %s", number, state, reason)
		}
	}
}

func TestACloseAsWontDoClosesTheOpenPullRequestOfAClosedTaskIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddSubIssueOf(shop, 12, 42, "Store the price in cents")
	fake.AddPullRequest(shop, 45, "Add plan model")
	fake.AddPullRequest(shop, 46, "Old plan model")
	fake.CloseIssue(shop, 41)
	fake.CloseIssue(shop, 46)
	server := startServer(t, fake, t.TempDir(), `INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, pull_request)
		VALUES ('owner/shop', 41, 12, 'ended', '2026-10-04T10:00:00Z', 46), ('owner/shop', 41, 12, 'ended', '2026-10-04T11:00:00Z', 45)`)
	server.WaitForFirstPoll(t, shop)

	items := closure(t, server)

	if len(items) != 3 || items[0].Kind != "pullRequest" || items[0].Number != 45 || items[1].Number != 42 || items[2].Number != 12 {
		t.Fatalf("items = %+v", items)
	}
	if status, body := closeWontDo(t, server, 12); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if state, _ := fake.State(shop, 45); state != "closed" {
		t.Errorf("state of #45 = %s", state)
	}
	if comments := fake.Comments(shop, 45); !slices.Equal(comments, []testkit.Comment{wontDoComment}) {
		t.Errorf("comments of #45 = %v", comments)
	}
	if !slices.Contains(fake.Labels(shop, 45), wontDo) {
		t.Errorf("labels of #45 = %v", fake.Labels(shop, 45))
	}
	unchanged(t, fake, shop, 41, "closed")
	unchanged(t, fake, shop, 46, "closed")

	waitForPoll(t, server, fake)
	if slices.Contains(fake.Comments(shop, 45), closedComment) {
		t.Errorf("comments of #45 = %v", fake.Comments(shop, 45))
	}
}
