package engine_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestCreateProjectAddsARowWithKindProjectBelowTheWorkstreamAndDispatchDoesNotStartIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	server, _ := connectWith(t, fake, call("create_project", `{ title = "Stats display", body = "# Goal\nShow the stats." }`), withUIURL)
	fake.RefuseIssueCreates(http.StatusInternalServerError)

	session := run(t, server, leadSpec(t), "Plan the work.")

	if got, want := reply(t, server, session), "Mobius creates the issue \"Stats display\" on GitHub later. A Lead event gives its number."; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND title = 'Stats display' AND number IS NULL AND kind = 'project' AND parent IS NULL", shop); got != 1 {
		t.Fatalf("project rows with no number = %d", got)
	}

	fake.RefuseIssueCreates(0)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 13 AND kind = 'project'", shop) == 1 &&
			countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE status = 'pending'") == 0
	})
	if got := fake.Labels(shop, 13); !slices.Equal(got, []string{"mobius:project"}) {
		t.Errorf("labels of #13 = %v", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); !slices.Equal(got, []int64{13}) {
		t.Errorf("sub-issues of #12 = %v", got)
	}
	if title, body := fake.Issue(shop, 13); title != "Stats display" {
		t.Errorf("title = %q", title)
	} else if got, _ := withoutWriteMarkerText(body); got != "# Goal\nShow the stats.\n\n"+chatLink+"\n\n"+leadLinkMark {
		t.Errorf("body = %q", body)
	}
	waitForPolls(t, fake)
	if hasLiveTask(t, server, 13) {
		t.Error("the Project started as a task")
	}
}

func TestTwoCreateProjectCallsOfOneSessionWithTheSameTitleCreateOneProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	create := call("create_project", `{ title = "Stats display", body = "" }`)
	server, _ := connect(t, fake, create+create)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	if got := reply(t, server, session); got != "Created owner/shop#13.Created owner/shop#13." {
		t.Errorf("reply = %q", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); len(got) != 1 {
		t.Errorf("sub-issues = %v", got)
	}
}

func TestCreateProjectRefusesAnEmptyTitle(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_project", `{ title = " ", body = "" }`))

	session := run(t, server, leadSpec(t), "Plan the work.")

	calls := mcpCalls(t, server, session)
	if len(calls) != 1 || calls[0]["error"] != "title must not be empty." {
		t.Errorf("calls = %+v", calls)
	}
	if countCreates(fake.Requests()) != 0 {
		t.Error("the call created an issue")
	}
}

func closeProject(n int, report string) string {
	return "[[prompts]]\nwhen = \"Close " + itoa(int64(n)) + "\"\ncall = { tool = \"close_project\", arguments = { n = " + itoa(int64(n)) + ", report = \"" + report + "\" } }\n\n"
}

func TestCloseProjectRefusesATaskAnIssueOfAnotherWorkstreamAndAProjectWithAnOpenSubIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, closeProject(43, "Done.")+closeProject(51, "Done.")+closeProject(41, "Done.")+closeProject(12, "Done."))
	fake.AddIssue(shop, 41, "Stats display")
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 42, "Show the counters")
	fake.AddSubIssue(shop, 41, 42)
	fake.AddIssue(shop, 43, "Add plan model")
	fake.AddSubIssue(shop, 12, 43)
	fake.AddIssue(shop, 13, "Other Workstream")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 51, "Other project")
	fake.AddLabel(shop, 51, "mobius:project", "owner")
	fake.AddSubIssue(shop, 13, 51)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number IN (41, 42, 43, 51) AND workstream IS NOT NULL", shop) == 4 &&
			countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 41 AND kind = 'project'", shop) == 1
	})

	session := run(t, server, leadSpec(t), "Close 43", "Close 51", "Close 41", "Close 12")

	calls := mcpCalls(t, server, session)
	want := []string{
		"owner/shop#43 is not a Project of this Workstream.",
		"owner/shop#51 is not a Project of this Workstream.",
		"owner/shop#41 has open sub-issues: owner/shop#42 \"Show the counters\".",
		"owner/shop#12 is not a Project of this Workstream.",
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v", calls)
	}
	for i, message := range want {
		if calls[i]["error"] != message {
			t.Errorf("call %d error = %v, want %q", i, calls[i]["error"], message)
		}
	}
	for _, number := range []int64{12, 41, 43, 51} {
		if state, _ := fake.State(shop, number); state != "open" || len(fake.Comments(shop, number)) != 0 {
			t.Errorf("#%d: state = %s, comments = %+v", number, state, fake.Comments(shop, number))
		}
	}
}

func TestCloseProjectPostsTheReportAndClosesAProjectWithNoOpenSubIssueAsCompleted(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, closeProject(41, "Each item is on main.")+closeProject(41, "Each item is on main."))
	fake.AddIssue(shop, 41, "Stats display")
	fake.AddLabel(shop, 41, "mobius:project", "owner")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 42, "Show the counters")
	fake.AddSubIssue(shop, 41, 42)
	fake.CloseIssue(shop, 42)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 42 AND state = 'closed' AND parent IS NOT NULL", shop) == 1 &&
			countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 41 AND kind = 'project'", shop) == 1
	})

	session := run(t, server, leadSpec(t), "Close 41", "Close 41")

	calls := mcpCalls(t, server, session)
	if len(calls) != 2 || calls[0]["result"] != "Closed owner/shop#41." || calls[1]["error"] != "owner/shop#41 is already closed." {
		t.Fatalf("calls = %+v", calls)
	}
	if state, reason := fake.State(shop, 41); state != "closed" || reason != "completed" {
		t.Errorf("state = %s, reason = %s", state, reason)
	}
	if got, want := fake.Comments(shop, 41), []testkit.Comment{{Author: "mobius-test[bot]", Body: marked("Each item is on main.", leadMark)}}; !slices.Equal(got, want) {
		t.Errorf("comments = %+v", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 41 AND state = 'closed'", shop); got != 1 {
		t.Errorf("closed rows of #41 = %d", got)
	}
}
