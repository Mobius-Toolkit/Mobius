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
