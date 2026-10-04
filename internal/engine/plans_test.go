package engine_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/testserver"
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
