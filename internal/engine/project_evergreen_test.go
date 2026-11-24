package engine_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestTheLabelEvergreenOfAProjectSetsTheColumnAndTheLabelOfATaskDoesNot(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 42, "Show the counters")
	fake.AddSubIssue(shop, 12, 42)
	fake.AddIssue(shop, 43, "Stats display")
	fake.AddLabel(shop, 43, "mobius:project", "owner")
	fake.AddLabel(shop, 43, "mobius:evergreen", "owner")
	fake.AddSubIssue(shop, 12, 43)
	fake.AddIssue(shop, 44, "Maintenance")
	fake.AddLabel(shop, 44, "mobius:project", "owner")
	fake.AddSubIssue(shop, 12, 44)
	server := startCopied(t, fake)
	evergreen := func(number int64) int {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = ? AND evergreen = 1", shop, number)
	}
	if evergreen(43) != 1 || evergreen(44) != 0 || evergreen(42) != 0 {
		t.Fatalf("evergreen of #43 = %d, #44 = %d, #42 = %d", evergreen(43), evergreen(44), evergreen(42))
	}

	fake.AddLabel(shop, 44, "mobius:evergreen", "owner")
	testkit.WaitFor(t, func() bool { return evergreen(44) == 1 })
	fake.RemoveLabel(shop, 44, "mobius:evergreen", "owner")
	testkit.WaitFor(t, func() bool { return evergreen(44) == 0 })
	fake.AddLabel(shop, 42, "mobius:evergreen", "owner")
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'evergreen' AND status = 'done' AND number = 42") == 1
	})
	if got := evergreen(42); got != 0 {
		t.Errorf("evergreen rows of the task #42 = %d", got)
	}
	if got := evergreen(43); got != 1 {
		t.Errorf("evergreen rows of #43 = %d", got)
	}
}

func TestAnUntrustedPersonCannotSetTheLabelEvergreenOfAProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	fake.AddIssue(shop, 44, "Maintenance")
	fake.AddLabel(shop, 44, "mobius:project", "owner")
	fake.AddSubIssue(shop, 12, 44)
	server := startWithChatLink(t, fake)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'kind' AND status = 'done' AND number = 44") == 1
	})

	fake.AddLabel(shop, 44, "mobius:evergreen", "mallory")

	testkit.WaitFor(t, func() bool { return !slices.Contains(fake.Labels(shop, 44), "mobius:evergreen") })
	testkit.WaitFor(t, func() bool { return ownWrites(t, server) == 0 && incomingChanges(t, server, "pending") == 0 })
	waitForPolls(t, fake)
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 44 AND evergreen = 1", shop); got != 0 {
		t.Errorf("evergreen rows of #44 = %d", got)
	}
}

func TestCreateProjectWithEvergreenAddsTheRowAndTheTwoLabels(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_project", `{ title = "Maintenance", body = "", evergreen = true }`))
	fake.RefuseIssueCreates(http.StatusInternalServerError)

	run(t, server, leadSpec(t), "Plan the work.")

	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND title = 'Maintenance' AND number IS NULL AND kind = 'project' AND evergreen = 1", shop); got != 1 {
		t.Fatalf("evergreen project rows with no number = %d", got)
	}
	fake.RefuseIssueCreates(0)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 13 AND kind = 'project' AND evergreen = 1", shop) == 1 &&
			countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE status = 'pending'") == 0
	})
	if got := slices.Sorted(slices.Values(fake.Labels(shop, 13))); !slices.Equal(got, []string{"mobius:evergreen", "mobius:project"}) {
		t.Errorf("labels of #13 = %v", got)
	}
}

func TestCreateProjectWithNoEvergreenAddsAProjectThatIsNotEvergreen(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_project", `{ title = "Stats display", body = "" }`))

	run(t, server, leadSpec(t), "Plan the work.")

	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 13 AND kind = 'project' AND evergreen = 0", shop) == 1 &&
			countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE status = 'pending'") == 0
	})
	if got := fake.Labels(shop, 13); !slices.Equal(got, []string{"mobius:project"}) {
		t.Errorf("labels of #13 = %v", got)
	}
}

func TestCloseProjectRefusesAnEvergreenProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, closeProject(41, "Done."))
	fake.AddIssue(shop, 41, "Maintenance")
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	fake.AddLabel(shop, 41, "mobius:evergreen", "owner")
	fake.AddSubIssue(shop, 12, 41)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 41 AND kind = 'project' AND evergreen = 1", shop) == 1
	})

	session := run(t, server, leadSpec(t), "Close 41")

	calls := mcpCalls(t, server, session)
	if len(calls) != 1 || calls[0]["error"] != "owner/shop#41 is an evergreen Project. It has no end." {
		t.Fatalf("calls = %+v", calls)
	}
	if state, _ := fake.State(shop, 41); state != "open" || len(fake.Comments(shop, 41)) != 0 {
		t.Errorf("state = %s, comments = %+v", state, fake.Comments(shop, 41))
	}
}

func TestTheTaskListOfTheLeadMarksAnEvergreenProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\ncall = { tool = \"list_tasks\" }\n")
	fake.AddIssue(shop, 41, "Maintenance")
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	fake.AddLabel(shop, 41, "mobius:evergreen", "owner")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 42, "Stats display")
	fake.AddLabel(shop, 42, "mobius:project", "owner")
	fake.AddSubIssue(shop, 12, 42)
	waitForTasks(t, server, []taskLine{line(41, "Maintenance", "open", 0), line(42, "Stats display", "open", 0)})

	_, text := leadReply(t, server)

	if want := "owner/shop#41 Maintenance (evergreen Project): open\nowner/shop#42 Stats display: open\n"; text != want {
		t.Errorf("reply = %q, want %q", text, want)
	}
}

func TestALabelProjectAfterTheLabelEvergreenAndAnAddAfterARemovalGiveAnEvergreenProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addWorkstream(fake)
	server := startWithChatLink(t, fake)
	evergreen := func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 41 AND kind = 'project' AND evergreen = 1", shop) == 1
	}

	fake.AddLabel(shop, 41, "mobius:evergreen", "owner")
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'evergreen' AND status = 'done' AND number = 41") == 1
	})
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	testkit.WaitFor(t, evergreen)

	fake.RemoveLabel(shop, 41, "mobius:project", "owner")
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 41 AND kind = 'task' AND evergreen = 0", shop) == 1
	})
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	testkit.WaitFor(t, evergreen)
}

func TestCreateProjectMakesEvergreenOptionalInTheSchema(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nlist_tools = true\n")

	_, text := leadReply(t, server)

	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	for _, tool := range list.Tools {
		if tool.Name == "create_project" {
			slices.Sort(tool.InputSchema.Required)
			if want := []string{"body", "title"}; !slices.Equal(tool.InputSchema.Required, want) {
				t.Fatalf("required = %v, want %v", tool.InputSchema.Required, want)
			}
			return
		}
	}
	t.Fatal("create_project is not in the tool list")
}
