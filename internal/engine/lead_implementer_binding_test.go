package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func requireNoBindingRows(t *testing.T, server *testserver.Server) {
	t.Helper()
	rows, err := store.New(server.DB).ListIssueBindings(t.Context())
	if err != nil || len(rows) != 0 {
		t.Errorf("rows = %+v, err = %v", rows, err)
	}
}

func TestTheLeadSetsTheImplementerSettingOfATaskAndTheNextImplementerSessionUsesIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("set_implementer_binding", `{ repository = "owner/shop", n = 41, harness = "devin", model = "opus", effort = "low" }`))
	addCopiedTask(t, server, fake, 41)

	session := run(t, server, leadSpec(t), "Set it")

	if got := reply(t, server, session); got != "Set the Implementer setting of owner/shop#41 to devin opus low." {
		t.Errorf("reply = %q", got)
	}
	requireNext(t, server, "implementer", shop, 41, engine.LevelTask, "devin", "opus", "low")
	requireBinding(t, taskSession(t, server, shop, 41), "devin", "opus", "low")
}

func TestTheLeadClearsTheImplementerSettingOfATaskAndTheNextImplementerSessionUsesTheBindingOfTheRole(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("set_implementer_binding", `{ repository = "owner/shop", n = 41, harness = "", model = "", effort = "" }`))
	addCopiedTask(t, server, fake, 41)
	setBinding(t, server, "implementer", 41, config.Devin, "opus", "low")

	session := run(t, server, leadSpec(t), "Clear it")

	if got := reply(t, server, session); got != "Cleared the Implementer setting of owner/shop#41. The next Implementer session uses devin swe-1.5 high (level: role)." {
		t.Errorf("reply = %q", got)
	}
	if _, found, err := server.Engine.IssueBinding(t.Context(), "implementer", shop, 41); err != nil || found {
		t.Errorf("found = %v, err = %v after the clear", found, err)
	}
	requireBinding(t, taskSession(t, server, shop, 41), "devin", "swe-1.5", "high")
}

func TestTheLeadSetsTheImplementerSettingOfTheWorkstreamAndATaskWithItsOwnSettingKeepsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("set_implementer_binding", `{ repository = "owner/shop", n = 12, harness = "devin", model = "opus", effort = "medium" }`))
	addCopiedTask(t, server, fake, 41)
	addCopiedTask(t, server, fake, 43)
	setBinding(t, server, "implementer", 43, config.Devin, "sonnet", "low")

	session := run(t, server, leadSpec(t), "Set it")

	if got := reply(t, server, session); got != "Set the Implementer setting of the Workstream owner/shop#12 to devin opus medium." {
		t.Errorf("reply = %q", got)
	}
	requireNext(t, server, "implementer", shop, 12, engine.LevelWorkstream, "devin", "opus", "medium")
	requireBinding(t, taskSession(t, server, shop, 41), "devin", "opus", "medium")
	requireBinding(t, taskSession(t, server, shop, 43), "devin", "sonnet", "low")
}

func TestTheLeadCannotSetTheImplementerSettingOfAnIssueOutsideItsWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository("other/thing")
	fake.AddIssue("other/thing", 7, "Foreign issue")
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddIssue(shop, 50, "Loose issue")
	server, _ := connect(t, fake, call("set_implementer_binding", `{ repository = "owner/shop", n = 50, harness = "devin", model = "opus", effort = "low" }`)+
		call("set_implementer_binding", `{ repository = "owner/shop", n = 20, harness = "devin", model = "opus", effort = "low" }`)+
		call("set_implementer_binding", `{ repository = "other/thing", n = 7, harness = "devin", model = "opus", effort = "low" }`))
	addCopiedTask(t, server, fake, 41)

	session := run(t, server, leadSpec(t), "1. ", "2. ", "3. ")

	want := "error: owner/shop#50 is not a task of this Workstream and not the Workstream issue." +
		"error: owner/shop#20 is not a task of this Workstream and not the Workstream issue." +
		"error: "
	if got := reply(t, server, session); !strings.HasPrefix(got, want) {
		t.Errorf("reply = %q", got)
	}
	requireNoBindingRows(t, server)
}

func TestTheLeadGetsTheRefusalWhenTheImplementerSettingIsRefused(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("set_implementer_binding", `{ repository = "owner/shop", n = 41, harness = "devin", model = "opus", effort = "extreme" }`))
	addCopiedTask(t, server, fake, 41)

	session := run(t, server, leadSpec(t), "Set it")

	got := reply(t, server, session)
	if !strings.HasPrefix(got, "error: access check failed: devin refuses thought_level \"extreme\"") {
		t.Errorf("reply = %q", got)
	}
	requireNoBindingRows(t, server)
	requireNext(t, server, "implementer", shop, 41, engine.LevelRole, "devin", "swe-1.5", "high")
}

func TestTheLeadCreatesAnIssueWithAnImplementerSetting(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_issue", `{ repository = "owner/shop", title = "Add plan model", body = "", parent = "owner/shop#12", blocked_by = [], implementer = { harness = "devin", model = "opus", effort = "low" } }`))

	session := run(t, server, leadSpec(t), "Plan it")

	if got := reply(t, server, session); got != "Created owner/shop#13. Its Implementer setting is devin opus low." {
		t.Errorf("reply = %q", got)
	}
	row, found, err := server.Engine.IssueBinding(t.Context(), "implementer", shop, 13)
	if err != nil || !found || row.Harness != "devin" || row.Model != "opus" || row.Effort != "low" {
		t.Errorf("row = %+v, found = %v, err = %v", row, found, err)
	}
	waitForCopy(t, server, shop, 13)
	requireBinding(t, taskSession(t, server, shop, 13), "devin", "opus", "low")
}

func TestTheLeadCreatesAnIssueWithAnEmptyImplementerSettingAsNoSetting(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_issue", `{ repository = "owner/shop", title = "Add plan model", body = "", parent = "owner/shop#12", blocked_by = [], implementer = { harness = "", model = "", effort = "" } }`))

	session := run(t, server, leadSpec(t), "Plan it")

	if got := reply(t, server, session); got != "Created owner/shop#13." {
		t.Errorf("reply = %q", got)
	}
	if !fake.HasIssue(shop, 13) {
		t.Error("owner/shop#13 does not exist")
	}
	requireNoBindingRows(t, server)
}

func TestTheLeadCreatesNoIssueWhenItsImplementerSettingIsRefused(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_issue", `{ repository = "owner/shop", title = "Add plan model", body = "", parent = "owner/shop#12", blocked_by = [], implementer = { harness = "devin", model = "opus", effort = "extreme" } }`))

	session := run(t, server, leadSpec(t), "Plan it")

	got := reply(t, server, session)
	if !strings.HasPrefix(got, "error: access check failed: devin refuses thought_level \"extreme\"") {
		t.Errorf("reply = %q", got)
	}
	if fake.HasIssue(shop, 13) {
		t.Error("owner/shop#13 exists")
	}
	requireNoBindingRows(t, server)
}

func TestTheLeadSetsTheImplementerSettingOfATaskInAnotherRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := issueRepositoryShop(t, fake, call("set_implementer_binding", `{ repository = "owner/garden", n = 7, harness = "devin", model = "opus", effort = "low" }`))
	fake.AddForeignSubIssue(shop, 12, garden, 7)
	waitForCopy(t, server, garden, 7)

	session := run(t, server, leadSpec(t), "Set it")

	if got := reply(t, server, session); got != "Set the Implementer setting of owner/garden#7 to devin opus low." {
		t.Errorf("reply = %q", got)
	}
	requireBinding(t, taskSession(t, server, garden, 7), "devin", "opus", "low")
}
