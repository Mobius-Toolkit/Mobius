package engine_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const maxCountedRequests = 10

func TestAPollWithNoChangeSendsAtMostTenRequestsThatCountAgainstTheLimit(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	const workstreams = 12
	for number := int64(1); number <= workstreams; number++ {
		fake.AddIssue(shop, number, fmt.Sprintf("Workstream %d", number))
		fake.AddLabel(shop, number, "mobius:workstream", "owner")
		fake.AddLabel(shop, number, "mobius:autopilot", "owner")
	}
	for i := int64(0); i < 100; i++ {
		number := 101 + i
		parent := 1 + i%workstreams
		if i%5 == 4 {
			parent = number - 1
		}
		fake.AddIssue(shop, number, fmt.Sprintf("Closed node %d", number))
		fake.AddSubIssue(shop, parent, number)
		fake.CloseIssue(shop, number)
	}
	server := startAutopilot(t, fake, func(*config.Config) {}, "")
	server.WaitForFirstPoll(t, shop)
	waitForPolls(t, fake)
	waitForPolls(t, fake)

	requests := fake.Requests()
	var starts []int
	for i, request := range requests {
		if request.Method == http.MethodGet && request.Path == "/installation/repositories" {
			starts = append(starts, i)
		}
	}
	last := len(starts) - 1
	var counted []string
	for _, request := range requests[starts[last-1]:starts[last]] {
		if request.Status == http.StatusNotModified || request.Path == "/app/installations" || strings.HasSuffix(request.Path, "/access_tokens") {
			continue
		}
		counted = append(counted, request.Method+" "+request.Path)
	}

	if len(counted) > maxCountedRequests {
		t.Errorf("%d requests count against the limit, want at most %d:\n%s", len(counted), maxCountedRequests, strings.Join(counted, "\n"))
	}
}
