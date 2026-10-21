package api_test

import (
	"net/http"
	"slices"
	"testing"

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
