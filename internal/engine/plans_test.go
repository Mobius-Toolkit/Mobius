package engine_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// startWithBilling starts a server with the Workstream #20 and its task #88.
func startWithBilling(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddSubIssue(shop, 20, 88)
	return startCopied(t, fake)
}

// authorizeOwner starts a server with startWithBilling, where the Owner authorized the Mobius App.
func authorizeOwner(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server := startWithBilling(t, fake)
	authorize(t, server)
	return server
}

func setAutopilot(t *testing.T, server *testserver.Server, on bool) (int, string) {
	t.Helper()
	return send(t, server, http.MethodPut, "/api/workstreams/owner/shop/20/autopilot", `{"on":`+strconv.FormatBool(on)+`}`)
}

func autopilotOfBilling(t *testing.T, server *testserver.Server) bool {
	t.Helper()
	return workstreams(t, server)[0].Autopilot
}

func TestSetAutopilotOnAddsTheLabelAsTheOwner(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := authorizeOwner(t, fake)
	changes := listen(t, server)

	if status, body := setAutopilot(t, server, true); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if !slices.Contains(fake.Labels(shop, 20), "mobius:autopilot") {
		t.Errorf("labels = %v", fake.Labels(shop, 20))
	}
	if got := fake.LabelActor(shop, 20, "mobius:autopilot"); got != "owner" {
		t.Errorf("actor = %s", got)
	}
	testkit.WaitFor(t, func() bool { return autopilotOfBilling(t, server) })
	waitForWorkstreams(t, changes)
}

// GitHub records no labeled event when the issue has the label, so an add alone keeps the App bot as the last actor.
// The switch removes first.
func TestSetAutopilotOnReplacesALabelOfTheApp(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := authorizeOwner(t, fake)
	fake.AddLabel(shop, 20, "mobius:autopilot", "mobius-test[bot]")

	if status, body := setAutopilot(t, server, true); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if got := fake.LabelActor(shop, 20, "mobius:autopilot"); got != "owner" {
		t.Errorf("actor = %s", got)
	}
	testkit.WaitFor(t, func() bool { return autopilotOfBilling(t, server) })
}

func TestSetAutopilotOffRemovesTheLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddLabel(shop, 20, "mobius:autopilot", "owner")
	server := startCopied(t, fake)
	authorize(t, server)
	if !autopilotOfBilling(t, server) {
		t.Fatal("autopilot is off")
	}

	if status, body := setAutopilot(t, server, false); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if slices.Contains(fake.Labels(shop, 20), "mobius:autopilot") {
		t.Errorf("labels = %v", fake.Labels(shop, 20))
	}
	testkit.WaitFor(t, func() bool { return !autopilotOfBilling(t, server) })
}

func TestSetAutopilotNeedsAnAuthorizedOwner(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithBilling(t, fake)

	status, body := setAutopilot(t, server, true)

	if status != http.StatusConflict || !strings.Contains(body, "authorize the Mobius App") {
		t.Errorf("status = %d: %s", status, body)
	}
	if slices.Contains(fake.Labels(shop, 20), "mobius:autopilot") {
		t.Errorf("labels = %v", fake.Labels(shop, 20))
	}
}

// leadReplies gives the reply text of the sessions of the Workstream number.
func leadReplies(t *testing.T, server *testserver.Server, number int64) string {
	t.Helper()
	var replies strings.Builder
	for _, session := range chatSessions(t, server, engine.ChatKey{Organization: "owner", Repository: shop, Workstream: number}, engine.LeadRole) {
		replies.WriteString(reply(t, server, session.ID))
	}
	return replies.String()
}

// startWithLeadPlans starts a server with the Workstream #20 and its task #88, and a Lead that plans the new
// Workstreams #12, #13 and #14 at their creation.
func startWithLeadPlans(t *testing.T, fake *testkit.FakeGitHub) *testserver.Server {
	t.Helper()
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 88, "Invoice totals")
	fake.AddSubIssue(shop, 20, 88)
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+`
[[prompts]]
when = "creation of Workstream #12"
call = { tool = "create_issue", arguments = { title = "Add plan model", body = "Plans have a price.", parent = 12, blocked_by = [88] } }

[[prompts]]
when = "creation of Workstream #13"
call = { tool = "mark_ready", arguments = { n = 30 } }

[[prompts]]
when = "creation of Workstream #14"
call = { tool = "mark_ready", arguments = { n = 31 } }
`)
	server := startServer(t, fake, dataDir, "")
	server.WaitForFirstPoll(t, shop)
	return server
}

func TestTheLeadCreatesASubIssueWithABlockerInAnotherWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithLeadPlans(t, fake)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")

	fake.AddLabel(shop, 12, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool { return leadReplies(t, server, 12) == "Created #89." })
	if got := fake.SubIssueNumbers(shop, 12); !slices.Equal(got, []int64{89}) {
		t.Errorf("sub-issues = %v", got)
	}
	if title, body := fake.Issue(shop, 89); title != "Add plan model" || body != "Plans have a price." {
		t.Errorf("issue = %s, %s", title, body)
	}
	if got := fake.BlockerNumbers(shop, 89); !slices.Equal(got, []int64{88}) {
		t.Errorf("blockers = %v", got)
	}
	if got := fake.Labels(shop, 89); len(got) != 0 {
		t.Errorf("labels = %v", got)
	}
	billing := "Billing"
	task := line(89, "Add plan model", "open", 0)
	task.BlockedBy = []taskBlocker{{Number: 88, WorkstreamTitle: &billing}}
	waitForTasks(t, server, []taskLine{task})
}

func TestMarkReadyStartsTheTaskWhenTheWorkstreamHasNoAutopilot(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithLeadPlans(t, fake)
	fake.AddIssue(shop, 13, "Plan prices")
	fake.AddIssue(shop, 30, "Add plan price")
	fake.AddSubIssue(shop, 13, 30)

	fake.AddLabel(shop, 13, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool { return leadReplies(t, server, 13) == "Marked #30 ready." })
	testkit.WaitFor(t, func() bool { return hasLiveTask(t, server, 30) })
	if slices.Contains(fake.Labels(shop, 13), "mobius:autopilot") {
		t.Errorf("labels = %v", fake.Labels(shop, 13))
	}
}

func TestMarkReadyOfAnIssueWithAnOpenBlockerWaitsWhenTheWorkstreamHasNoAutopilot(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithLeadPlans(t, fake)
	fake.AddIssue(shop, 14, "Plan limits")
	fake.AddIssue(shop, 31, "Add plan limit")
	fake.AddSubIssue(shop, 14, 31)
	fake.AddBlockedBy(shop, 31, 88)

	fake.AddLabel(shop, 14, "mobius:workstream", "owner")

	testkit.WaitFor(t, func() bool { return leadReplies(t, server, 14) == "Marked #31 ready." })
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 31) || !slices.Contains(fake.Labels(shop, 31), "mobius:ready") {
		t.Errorf("labels = %v", fake.Labels(shop, 31))
	}
}
