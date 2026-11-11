package api_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const barn = "owner/barn"

func TestTheTasksEndpointGivesTheRepositoryOfEachTask(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddRepository(barn)
	github.AddIssue(shop, 12, "Integrate loyalty plans")
	github.AddLabel(shop, 12, "mobius:workstream", "owner")
	github.AddSubIssueOf(shop, 12, 41, "Add plan model")
	github.AddIssue(barn, 77, "Paint the barn")
	github.AddForeignSubIssue(shop, 12, barn, 77)
	server := startWithApp(t, github, "User")

	type line struct {
		Number          int64  `json:"number"`
		Repository      string `json:"repository"`
		OtherRepository bool   `json:"otherRepository"`
	}
	var got []line
	testkit.WaitFor(t, func() bool {
		var body struct {
			Data []line `json:"data"`
		}
		if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/tasks", "", &body); reply.StatusCode != http.StatusOK {
			return false
		}
		got = body.Data
		return len(got) == 2
	})

	want := []line{{41, shop, false}, {77, barn, true}}
	if !slices.Equal(got, want) {
		t.Errorf("tasks = %+v, want %+v", got, want)
	}
}

func TestTheBoardEndpointGivesTheTasksOfAllWorkstreamsWithTheirTitlesAndRepositories(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddRepository(barn)
	github.AddIssue(shop, 12, "Integrate loyalty plans")
	github.AddLabel(shop, 12, "mobius:workstream", "owner")
	github.AddIssue(barn, 77, "Paint the barn")
	github.AddForeignSubIssue(shop, 12, barn, 77)
	server := startWithApp(t, github, "User")
	github.AddLabel(barn, 77, "mobius:ready", "owner")

	type task struct {
		Repository           string    `json:"repository"`
		Number               int64     `json:"number"`
		Title                string    `json:"title"`
		URL                  string    `json:"url"`
		WorkstreamRepository string    `json:"workstreamRepository"`
		Workstream           int64     `json:"workstream"`
		WorkstreamTitle      string    `json:"workstreamTitle"`
		State                string    `json:"state"`
		StateAt              time.Time `json:"stateAt"`
		PullRequest          *int64    `json:"pullRequest"`
		PullRequestURL       *string   `json:"pullRequestUrl"`
		FixRounds            int64     `json:"fixRounds"`
		ReviewRounds         int64     `json:"reviewRounds"`
	}
	var got []task
	testkit.WaitFor(t, func() bool {
		var body struct {
			Data []task `json:"data"`
		}
		if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/tasks", "", &body); reply.StatusCode != http.StatusOK {
			return false
		}
		got = body.Data
		return len(got) == 1
	})

	want := task{
		Repository: barn, Number: 77, Title: "Paint the barn", URL: "https://github.com/owner/barn/issues/77",
		WorkstreamRepository: shop, Workstream: 12, WorkstreamTitle: "Integrate loyalty plans", State: "dispatched", StateAt: got[0].StateAt,
	}
	if got[0] != want || got[0].StateAt.IsZero() {
		t.Errorf("task = %+v, want %+v", got[0], want)
	}
}

func TestTheTaskEndpointGivesTheDetailOfATask(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddRepository(barn)
	github.AddIssue(shop, 12, "Integrate loyalty plans")
	github.AddLabel(shop, 12, "mobius:workstream", "owner")
	github.CreateIssue(shop, 41, "Add plan model", "The model fields:\n- id\n- name", "owner")
	github.AddSubIssue(shop, 12, 41)
	github.CreateIssue(shop, 42, "Let customers change plans", "Change plan body", "owner")
	github.AddSubIssue(shop, 12, 42)
	github.CreateIssue(barn, 77, "Paint the barn", "Paint barn body", "owner")
	github.AddForeignSubIssue(shop, 12, barn, 77)
	github.CreateIssue(shop, 99, "Unrelated issue", "Not in tree", "owner")

	server := startWithApp(t, github, "User")

	// Wait for the tree to be copied.
	testkit.WaitFor(t, func() bool {
		var body struct {
			Data []struct {
				Number int64 `json:"number"`
			} `json:"data"`
		}
		if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/tasks", "", &body); reply.StatusCode != http.StatusOK {
			return false
		}
		return len(body.Data) == 3
	})

	// Add live task with pull request 44 for shop#41.
	if _, err := server.DB.Exec(`INSERT INTO tasks (repository, workstream_repository, issue, workstream, state, pull_request, dispatched_at)
		VALUES ('owner/shop', 'owner/shop', 41, 12, 'working', 44, ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	type detail struct {
		Body           string  `json:"body"`
		PullRequest    *int64  `json:"pullRequest"`
		PullRequestURL *string `json:"pullRequestUrl"`
	}

	// 1. Task with pull request (shop#41).
	var task41 detail
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/tasks/owner/shop/41", "", &struct {
		Data *detail `json:"data"`
	}{Data: &task41}); reply.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", reply.StatusCode, http.StatusOK)
	}
	pr44 := int64(44)
	prURL44 := "https://github.com/owner/shop/pull/44"
	if task41.Body != "The model fields:\n- id\n- name" || task41.PullRequest == nil || *task41.PullRequest != pr44 || task41.PullRequestURL == nil || *task41.PullRequestURL != prURL44 {
		t.Errorf("task41 = %+v, want body and PR #44", task41)
	}

	// 2. Task with no pull request (shop#42).
	var task42 detail
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/tasks/owner/shop/42", "", &struct {
		Data *detail `json:"data"`
	}{Data: &task42}); reply.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", reply.StatusCode, http.StatusOK)
	}
	if task42.Body != "Change plan body" || task42.PullRequest != nil || task42.PullRequestURL != nil {
		t.Errorf("task42 = %+v, want body and no PR", task42)
	}

	// 3. Task in another repository (barn#77).
	var task77 detail
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/tasks/owner/barn/77", "", &struct {
		Data *detail `json:"data"`
	}{Data: &task77}); reply.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", reply.StatusCode, http.StatusOK)
	}
	if task77.Body != "Paint barn body" || task77.PullRequest != nil || task77.PullRequestURL != nil {
		t.Errorf("task77 = %+v, want body of barn#77", task77)
	}

	// 4. Issue not in the tree (shop#99).
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/tasks/owner/shop/99", "", nil); reply.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d for issue not in tree", reply.StatusCode, http.StatusNotFound)
	}
}
