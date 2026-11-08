package engine_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// keyAndAfterMergeNeed is a Reviewer that reports one mandatory env var and one config key that a person can add only
// after the merge.
const keyAndAfterMergeNeed = "[[prompts]]\nwhen = \"You are the Reviewer\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
	"{ kind = \"env_var\", name = \"PLAN_API_KEY\", path = \"plan.txt\", line = 1, reason = \"The code reads it at start.\", mandatory = true, after_merge = false }, " +
	"{ kind = \"config\", name = \"roles.lead.context\", path = \"plan.txt\", line = 3, reason = \"An older release refuses the key.\", mandatory = true, after_merge = true }] } }\n\n"

// blockedItem is the part of an Inbox item that the tests of the blocking todos read.
type blockedItem struct {
	Kind          string  `json:"kind"`
	Issue         int64   `json:"issue"`
	DismissedAt   *string `json:"dismissedAt"`
	BlockingTodos []int64 `json:"blockingTodos"`
}

// readyBlocking gives the blocking todos of each open ready for review item of the Inbox API.
func readyBlocking(t *testing.T, server *testserver.Server) [][]int64 {
	t.Helper()
	status, text := send(t, server, http.MethodGet, "/api/inbox", "")
	var body struct {
		Data []blockedItem `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); status != http.StatusOK || err != nil {
		t.Fatalf("inbox: status %d, %v: %s", status, err, text)
	}
	var blocking [][]int64
	for _, item := range body.Data {
		if item.Kind == "ready for review" {
			blocking = append(blocking, item.BlockingTodos)
		}
	}
	return blocking
}

// waitForBlocking waits until the Inbox API has one open ready for review item with the blocking todos want.
func waitForBlocking(t *testing.T, server *testserver.Server, want []int64) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		blocking := readyBlocking(t, server)
		return len(blocking) == 1 && slices.Equal(blocking[0], want)
	})
}

// readyItemStream gives the ready for review Inbox items that the live event stream sends after the call.
func readyItemStream(t *testing.T, server *testserver.Server) <-chan blockedItem {
	t.Helper()
	listeners := server.Engine.Listeners()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	testkit.WaitFor(t, func() bool { return server.Engine.Listeners() > listeners })
	items := make(chan blockedItem, 64)
	go func() {
		events := bufio.NewReader(response.Body)
		for {
			name, data, err := nextEvent(events)
			if err != nil {
				return
			}
			var item blockedItem
			if name == "inbox" && json.Unmarshal([]byte(data), &item) == nil && item.Kind == "ready for review" {
				select {
				case items <- item:
				default:
				}
			}
		}
	}()
	return items
}

// waitForReadyItem gives the first item of the stream that has the blocking todos want.
func waitForReadyItem(t *testing.T, items <-chan blockedItem, want []int64) blockedItem {
	t.Helper()
	timeout := time.After(time.Minute)
	for {
		select {
		case item := <-items:
			if item.BlockingTodos != nil && slices.Equal(item.BlockingTodos, want) {
				return item
			}
		case <-timeout:
			t.Fatalf("no ready for review event with the blocking todos %v", want)
		}
	}
}

func TestAnApprovalWithTwoBlockingTodosGivesTheReadyItemTheIDsOfBothTodoItemsOldestFirst(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, twoNeeds)
	items := readyItemStream(t, server)

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "todo")) == 2 })
	todos := itemsOf(t, server, "todo")
	want := []int64{todos[0].ID, todos[1].ID}
	if want[0] > want[1] {
		t.Fatalf("todo items = %v", want)
	}
	waitForReadyItem(t, items, want)
	waitForBlocking(t, server, want)
}

func TestAnAfterMergeTodoIsNotInTheBlockingTodosOfTheReadyItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, keyAndAfterMergeNeed)
	items := readyItemStream(t, server)

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "todo")) == 2 })
	var key inboxItem
	for _, todo := range itemsOf(t, server, "todo") {
		if todo.Title == "PLAN_API_KEY" {
			key = todo
		}
	}
	waitForReadyItem(t, items, []int64{key.ID})
	waitForBlocking(t, server, []int64{key.ID})
}

func TestATodoOfANeedThatALaterReviewRemovedIsNotInTheBlockingTodosOfTheReadyItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, first := todoBeforeAFixRound(t, fake, "off", onlyKeyInRoundTwo+twoNeeds)
	items := readyItemStream(t, server)

	sendChat(t, server, leadChat, "Approve #41")

	waitForReadyItem(t, items, []int64{first.ID})
	waitForBlocking(t, server, []int64{first.ID})
}

func TestCompleteOfTheLastMandatoryTodoPublishesTheReadyItemWithNoBlockingTodos(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, mandatoryNeed)
	sendChat(t, server, leadChat, "Approve #41")
	todo := waitForItem(t, server, "todo")
	waitForBlocking(t, server, []int64{todo.ID})
	items := readyItemStream(t, server)

	if status, body := doneTodo(t, server, todo.ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if item := waitForReadyItem(t, items, []int64{}); item.Issue != 41 || item.DismissedAt != nil {
		t.Errorf("item = %+v", item)
	}
}

func TestDismissOfAnOptionalTodoPublishesTheReadyItemWithOneIDLess(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, twoNeeds)
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "todo")) == 2 })
	todos := itemsOf(t, server, "todo")
	waitForBlocking(t, server, []int64{todos[0].ID, todos[1].ID})
	items := readyItemStream(t, server)

	if status, body := dismissItem(t, server, todos[1].ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	waitForReadyItem(t, items, []int64{todos[0].ID})
}
