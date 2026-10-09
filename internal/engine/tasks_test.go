package engine_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type taskLine struct {
	Number          int64         `json:"number"`
	Title           string        `json:"title"`
	State           string        `json:"state"`
	URL             string        `json:"url"`
	Depth           int64         `json:"depth"`
	OtherRepository bool          `json:"otherRepository"`
	BlockedBy       []taskBlocker `json:"blockedBy"`
}

type taskBlocker struct {
	Number          int64   `json:"number"`
	WorkstreamTitle *string `json:"workstreamTitle"`
}

type needsHuman struct {
	Repository     string  `json:"repository"`
	Workstream     int64   `json:"workstream"`
	Number         int64   `json:"number"`
	Title          string  `json:"title"`
	URL            string  `json:"url"`
	PullRequest    *int64  `json:"pullRequest"`
	PullRequestURL *string `json:"pullRequestUrl"`
	Reason         string  `json:"reason"`
}

// apiData gives the data of the API path as T.
func apiData[T any](t *testing.T, server *testserver.Server, path string) T {
	t.Helper()
	status, text := send(t, server, http.MethodGet, path, "")
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil {
		t.Fatalf("%s: status %d, %v: %s", path, status, err, text)
	}
	return body.Data
}

func line(number int64, title, state string, depth int64) taskLine {
	return taskLine{Number: number, Title: title, State: state, URL: "https://github.com/owner/shop/issues/" + itoa(number), Depth: depth, BlockedBy: []taskBlocker{}}
}

// waitForTasks waits until the Tasks tab of the Workstream #12 has want.
func waitForTasks(t *testing.T, server *testserver.Server, want []taskLine) {
	t.Helper()
	var got []taskLine
	testkit.WaitFor(t, func() bool {
		got = apiData[[]taskLine](t, server, "/api/workstreams/owner/shop/12/tasks")
		return reflect.DeepEqual(got, want)
	})
}

func TestTheTasksTabShowsTheSubIssuesOfTheWorkstream(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddIssue(shop, 42, "Let customers change plans")
	fake.AddSubIssue(shop, 12, 42)

	server := startCopied(t, fake)

	// The nested task follows its parent, and each level adds one to the depth.
	waitForTasks(t, server, []taskLine{
		line(41, "Add plan model", "open", 0),
		line(50, "Store the price in cents", "open", 1),
		line(42, "Let customers change plans", "open", 0),
	})
}

// A sub-issue in another repository is a task of the Workstream, but its number names a different issue here, so this
// repository cannot give its blockers, its task state or its sub-issues.
func TestTheTasksTabShowsASubIssueOfAnotherRepository(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue("other/repo", 70, "A blocker in another repository")
	fake.AddIssue("other/repo", 77, "A task in another repository")
	fake.AddBlockedBy("other/repo", 77, 70)
	fake.AddLabel("other/repo", 77, "mobius:working", "owner")
	fake.AddForeignSubIssue(shop, 12, "other/repo", 77)
	// The different issue 77 of this repository has a sub-issue, a blocker and a queued task.
	fake.AddIssue(shop, 77, "A different issue")
	fake.AddIssue(shop, 90, "Task of the different issue")
	fake.AddSubIssue(shop, 77, 90)
	fake.AddBlockedBy(shop, 77, 41)
	fake.AddIssue(shop, 42, "Let customers change plans")
	fake.AddSubIssue(shop, 12, 42)

	server := startServer(t, fake, t.TempDir(), `INSERT INTO tasks (repository, issue, workstream, state, dispatched_at)
		VALUES ('owner/shop', 77, 12, 'queued', '2026-09-30T00:00:00Z')`)
	server.WaitForFirstPoll(t, shop)

	foreign := taskLine{Number: 77, Title: "A task in another repository", State: "working", URL: "https://github.com/other/repo/issues/77", OtherRepository: true, BlockedBy: []taskBlocker{}}
	waitForTasks(t, server, []taskLine{line(41, "Add plan model", "open", 0), foreign, line(42, "Let customers change plans", "open", 0)})
}

// The Start button of the Tasks tab adds mobius:ready to the number in the repository of the Workstream, so an open
// task of another repository must show that it is not in that repository.
func TestTheTasksTabMarksAnOpenSubIssueOfAnotherRepository(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue("other/repo", 5, "An open task in another repository")
	fake.AddForeignSubIssue(shop, 12, "other/repo", 5)

	server := startCopied(t, fake)

	foreign := taskLine{Number: 5, Title: "An open task in another repository", State: "open", URL: "https://github.com/other/repo/issues/5", OtherRepository: true, BlockedBy: []taskBlocker{}}
	waitForTasks(t, server, []taskLine{foreign})
}

// A closed task has the state closed and keeps its place in the tree, so its sub-issue follows it one level deeper.
func TestTheTasksTabShowsAClosedTaskWithItsSubIssues(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 40, "First task")
	fake.AddSubIssue(shop, 12, 40)
	fake.AddIssue(shop, 41, "Closed task")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Task of the closed task")
	fake.AddSubIssue(shop, 41, 50)
	fake.CloseIssue(shop, 41)

	server := startCopied(t, fake)

	waitForTasks(t, server, []taskLine{line(40, "First task", "open", 0), line(41, "Closed task", "closed", 0), line(50, "Task of the closed task", "open", 1)})
}

func TestTheTasksTabHidesAClosedTaskOfAnUntrustedAuthor(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 40, "First task")
	fake.AddSubIssue(shop, 12, 40)
	fake.AddIssue(shop, 41, "Closed task of a stranger")
	fake.SetAuthor(shop, 41, "mallory")
	fake.AddSubIssue(shop, 12, 41)
	fake.CloseIssue(shop, 41)

	server := startCopied(t, fake)

	waitForTasks(t, server, []taskLine{line(40, "First task", "open", 0)})
}

func TestTheTasksTabShowsTheBlockersAndAQueuedTask(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddSubIssue(shop, 20, 88)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddSubIssue(shop, 12, 40)
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 40)
	fake.AddBlockedBy(shop, 41, 88)

	server := startCopied(t, fake)
	if _, err := server.DB.Exec(`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at) VALUES ('owner/shop', 40, 12, 'queued', '2026-09-30T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	fake.AddLabel(shop, 40, "mobius:working", "owner")

	billing := "Billing"
	api := line(41, "Plan API", "open", 0)
	api.BlockedBy = []taskBlocker{{Number: 40}, {Number: 88, WorkstreamTitle: &billing}}
	waitForTasks(t, server, []taskLine{line(40, "Add plan model", "queued", 0), api})
}

func TestTheNeedsHumanListHasTheOpenIssuesWithTheLabelInTheTrees(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 13, "Add a storefront")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	// The issue of a task exists, so the poll keeps the task.
	fake.AddIssue(shop, 41, "Add plan model")
	server := startServer(t, fake, t.TempDir(), `INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, pull_request)
		VALUES ('owner/shop', 41, 12, 'needs_human', '2026-09-30T00:00:00Z', 45)`)
	server.WaitForFirstPoll(t, shop)
	fake.AddSubIssue(shop, 12, 41)
	fake.AddLabel(shop, 41, "mobius:needs-human", "owner")
	fake.AddPullRequest(shop, 45, "Add plan model")
	// The task keeps working while it asks, so mobius:working comes first.
	fake.AddIssue(shop, 50, "Store the price in cents")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddLabel(shop, 50, "mobius:working", "owner")
	fake.AddLabel(shop, 50, "mobius:needs-human", "owner")
	fake.AddIssue(shop, 42, "Let customers change plans")
	fake.AddSubIssue(shop, 12, 42)
	fake.AddLabel(shop, 42, "mobius:working", "owner")
	fake.AddIssue(shop, 43, "Add season table")
	fake.AddSubIssue(shop, 13, 43)
	fake.AddLabel(shop, 43, "mobius:needs-human", "owner")
	fake.CloseIssue(shop, 43)
	fake.AddIssue(shop, 44, "Ask a stranger")
	fake.AddSubIssue(shop, 13, 44)
	fake.SetAuthor(shop, 44, "mallory")
	fake.AddLabel(shop, 44, "mobius:needs-human", "owner")

	pullRequest := int64(45)
	pullRequestURL := "https://github.com/owner/shop/pull/45"
	want := []needsHuman{
		{Repository: shop, Workstream: 12, Number: 41, Title: "Add plan model", URL: "https://github.com/owner/shop/issues/41", PullRequest: &pullRequest, PullRequestURL: &pullRequestURL},
		{Repository: shop, Workstream: 12, Number: 50, Title: "Store the price in cents", URL: "https://github.com/owner/shop/issues/50"},
	}
	var got []needsHuman
	testkit.WaitFor(t, func() bool {
		got = apiData[[]needsHuman](t, server, "/api/needs-human")
		return reflect.DeepEqual(got, want)
	})
}

func TestTheNeedsHumanListHasTheReasonOfTheNewestStopOfTheIssue(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddIssue(shop, 42, "Let customers change plans")
	server := startServer(t, fake, t.TempDir(), "")
	server.WaitForFirstPoll(t, shop)
	fake.AddSubIssue(shop, 12, 41)
	fake.AddSubIssue(shop, 12, 42)
	fake.AddLabel(shop, 41, "mobius:needs-human", "owner")
	fake.AddLabel(shop, 42, "mobius:needs-human", "owner")
	stop := func(issue int64, text string) {
		t.Helper()
		if _, err := server.DB.Exec(`INSERT INTO lead_events (repository, workstream, issue, kind, payload, time) VALUES ('owner/shop', 12, ?, 'stop', ?, '2026-10-04T10:00:00Z')`, issue, text); err != nil {
			t.Fatal(err)
		}
	}
	reasons := func() map[int64]string {
		reasons := map[int64]string{}
		for _, issue := range apiData[[]needsHuman](t, server, "/api/needs-human") {
			reasons[issue.Number] = issue.Reason
		}
		return reasons
	}
	stop(41, "the CI of the head commit failed")
	stop(42, "the agent did not push")
	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(reasons(), map[int64]string{41: "the CI of the head commit failed", 42: "the agent did not push"})
	})

	stop(41, "the pull request has open items after 3 fix rounds")
	testkit.WaitFor(t, func() bool {
		return reflect.DeepEqual(reasons(), map[int64]string{41: "the pull request has open items after 3 fix rounds", 42: "the agent did not push"})
	})
}

func TestResumeReplacesMobiusNeedsHumanWithMobiusReadyAsTheOwner(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddLabel(shop, 41, "mobius:needs-human", testkit.AppSlug+"[bot]")
	server := startCopied(t, fake)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/resume", ""); status != http.StatusConflict {
		t.Errorf("status = %d: %s", status, body)
	}
	authorize(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/resume", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if got := fake.Labels(shop, 41); !slices.Equal(got, []string{"mobius:ready"}) {
		t.Errorf("labels = %v", got)
	}
	for _, label := range []string{"mobius:needs-human", "mobius:ready"} {
		if got := fake.LabelActor(shop, 41, label); got != "owner" {
			t.Errorf("actor of %s = %s", label, got)
		}
	}
}

func TestStartAddsMobiusReadyAsTheOwner(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	server := startCopied(t, fake)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/start", ""); status != http.StatusConflict {
		t.Errorf("status = %d: %s", status, body)
	}
	authorize(t, server)

	if status, body := send(t, server, http.MethodPost, "/api/repositories/owner/shop/issues/41/start", ""); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if got := fake.Labels(shop, 41); !slices.Equal(got, []string{"mobius:ready"}) {
		t.Errorf("labels = %v", got)
	}
	if got := fake.LabelActor(shop, 41, "mobius:ready"); got != "owner" {
		t.Errorf("actor of mobius:ready = %s", got)
	}
}
