package engine_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// blockerWrites gives the number of requests that add or remove a blocker of the issue number in owner/shop.
func blockerWrites(fake *testkit.FakeGitHub, number string) int {
	count := 0
	for _, request := range fake.Requests() {
		if request.Method != "GET" && strings.HasPrefix(request.Path, "/repos/owner/shop/issues/"+number+"/dependencies/blocked_by") {
			count++
		}
	}
	return count
}

func TestUpdateBlockersAddsABlockerInTheRepositoryAndABlockerInAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40", "owner/garden#7"] }`)+
		"[[prompts]]\ncall = { tool = \"list_tasks\" }\n")
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	want := "Added owner/shop#40 and owner/garden#7 as blockers of owner/shop#41." +
		"owner/shop#41 Plan API: open, blocked by owner/garden#7, owner/shop#40\n"
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := fake.BlockerNumbers(shop, 41); !reflect.DeepEqual(got, []int64{40, 7}) {
		t.Errorf("blockers = %v", got)
	}
}

func TestUpdateBlockersRemovesTheOpenBlockerThatIsNotInTheListAndWritesNothingTheSecondTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`))
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 40)
	fake.AddForeignBlockedBy(shop, 41, garden, 7)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	want := "Removed owner/garden#7 as blockers of owner/shop#41.The blockers of owner/shop#41 already match."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := fake.BlockerNumbers(shop, 41); !reflect.DeepEqual(got, []int64{40}) {
		t.Errorf("blockers = %v", got)
	}
	if got := blockerWrites(fake, "41"); got != 1 {
		t.Errorf("writes = %d", got)
	}
}

func TestUpdateBlockersAddsAndRemovesAndKeepsAClosedBlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = [] }`))
	fake.AddIssue(shop, 39, "Closed blocker")
	fake.CloseIssue(shop, 39)
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 39)
	fake.AddForeignBlockedBy(shop, 41, garden, 7)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	want := "Added owner/shop#40 and removed owner/garden#7 as blockers of owner/shop#41." +
		"Removed owner/shop#40 as blockers of owner/shop#41."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := fake.BlockerNumbers(shop, 41); !reflect.DeepEqual(got, []int64{39}) {
		t.Errorf("blockers = %v", got)
	}
}

func TestAnIssueWaitsForItsBlockerUntilUpdateBlockersRemovesTheBlocker(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nwhen = \"Remove the blocker\"\ncall = { tool = \"update_blockers\", arguments = { repository = \"owner/shop\", n = 41, blocked_by = [] } }\n\n"+seen,
		func(cfg *config.Config) { cfg.LeadIdleTimeout = 5 * time.Second })
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddSubIssue(shop, 12, 40)
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddBlockedBy(shop, 41, 40)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	dispatchTask(fake, 43, "Price rounding")
	liveTaskOf(t, server, 43)
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 41) {
		t.Fatal("the issue with an open blocker has a task")
	}

	session := run(t, server, leadSpec(t), "Remove the blocker")

	if got := reply(t, server, session); got != "Removed owner/shop#40 as blockers of owner/shop#41." {
		t.Errorf("reply = %q", got)
	}
	if task := liveTaskOf(t, server, 41); task.Workstream != 12 {
		t.Errorf("task = %+v", task)
	}
}

func TestUpdateBlockersRefusesBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("update_blockers", `{ repository = "owner/shop", n = 50, blocked_by = ["owner/shop#40"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40", "owner/shop#41"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#45"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 41, blocked_by = ["owner/shop#40", "owner/shop#40"] }`)+
		call("update_blockers", `{ repository = "owner/shop", n = 45, blocked_by = [] }`))
	fake.AddIssue(shop, 40, "Add plan model")
	fake.AddIssue(shop, 41, "Plan API")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Outside")
	fake.AddPullRequest(shop, 45, "A pull request")
	fake.AddBlockedBy(shop, 41, 40)

	session := run(t, server, leadSpec(t), "1. ", "2. ", "3. ", "4. ", "5. ")

	want := "error: owner/shop#50 is not in this Workstream." +
		"error: owner/shop#41 cannot block itself." +
		"error: owner/shop#45 is not an issue." +
		"error: owner/shop#40 is two times in blocked_by." +
		"error: owner/shop#45 is not an issue."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	if got := blockerWrites(fake, "41") + blockerWrites(fake, "50") + blockerWrites(fake, "45"); got != 0 {
		t.Errorf("writes = %d", got)
	}
	if got := fake.BlockerNumbers(shop, 41); !slices.Equal(got, []int64{40}) {
		t.Errorf("blockers = %v", got)
	}
}
