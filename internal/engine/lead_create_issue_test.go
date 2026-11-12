package engine_test

import (
	"net/http"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const createPlanModel = `{ repository = "owner/shop", title = "Add plan model", body = "", parent = "owner/shop#12", blocked_by = [] }`

// Serial: the test sets the hook beforeRecopyTrees.
func TestCreateIssueGivesTheNumberWhileTheCopyOfTheTreeIsHeld(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_issue", createPlanModel))
	copied := func(number int64) bool {
		var count int
		_ = server.DB.QueryRow("SELECT count(*) FROM copied_issues WHERE repository = ? AND workstream = 12 AND number = ?", shop, number).Scan(&count)
		return count == 1
	}
	testkit.WaitFor(t, func() bool {
		var count int
		_ = server.DB.QueryRow("SELECT count(*) FROM copied_workstreams WHERE repository = ? AND number = 12", shop).Scan(&count)
		return count == 1
	})
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	*engine.BeforeRecopyTrees = func() { <-hold }
	t.Cleanup(func() {
		release()
		*engine.BeforeRecopyTrees = nil
	})

	agent := start(t, server, leadSpec(t))
	prompted := make(chan error, 1)
	go func() { prompted <- agent.Prompt(t.Context(), "Plan the work.", nil) }()

	testkit.WaitFor(t, func() bool { return reply(t, server, agent.ID()) == "Created owner/shop#13." })
	if copied(13) {
		t.Error("the copy has the issue before the release")
	}
	release()
	if err := <-prompted; err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return copied(13) })
}

func TestTwoCreateIssueCallsOfOneSessionWithTheSameParentAndTitleCreateOneIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	create := call("create_issue", createPlanModel)
	server, _ := connect(t, fake, create+create)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	if got := reply(t, server, session); got != "Created owner/shop#13.Created owner/shop#13." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); len(got) != 1 {
		t.Errorf("sub-issues = %v", got)
	}
}

func TestACreateIssueCallWaitsForTheCallWithTheSameParentAndTitleThatRuns(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n")
	start(t, server, leadSpec(t))
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0.1.0"}, nil)
	connection, err := client.Connect(t.Context(), &sdk.StreamableClientTransport{Endpoint: mcpURL(t, dataDir)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	createdPlanModels := func() int {
		count := 0
		for _, request := range fake.Requests() {
			if request.Method == http.MethodPost && request.Path == "/repos/owner/shop/issues" {
				count++
			}
		}
		return count
	}
	release := sync.OnceFunc(fake.HoldNext("POST /repos/{owner}/{repo}/issues"))
	t.Cleanup(release)
	arguments := map[string]any{"repository": "owner/shop", "title": "Add plan model", "body": "", "parent": "owner/shop#12", "blocked_by": []string{}}
	results := make(chan string, 2)
	createIssue := func() {
		result, err := connection.CallTool(t.Context(), &sdk.CallToolParams{Name: "create_issue", Arguments: arguments})
		if err != nil {
			t.Error(err)
			results <- ""
			return
		}
		results <- result.Content[0].(*sdk.TextContent).Text
	}

	go createIssue()
	testkit.WaitFor(t, func() bool { return createdPlanModels() == 1 })
	go createIssue()
	release()

	if first, second := <-results, <-results; first != "Created owner/shop#13." || second != first {
		t.Errorf("results = %q, %q", first, second)
	}
	if got := fake.SubIssueNumbers(shop, 12); len(got) != 1 {
		t.Errorf("sub-issues = %v", got)
	}
}
