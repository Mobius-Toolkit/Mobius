package engine_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
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

func TestCreateIssueAndCreateProjectWithTheSameTitleCreateTwoIssues(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	task := call("create_issue", `{ repository = "owner/shop", title = "Stats display", body = "", parent = "owner/shop#12", blocked_by = [] }`)
	project := call("create_project", `{ title = "Stats display", body = "" }`)
	server, _ := connect(t, fake, task+project)

	session := run(t, server, leadSpec(t), "1. ", "2. ")

	if got := reply(t, server, session); got != "Created owner/shop#13.Created owner/shop#14." {
		t.Errorf("reply = %q", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 13 AND kind = 'task'", shop); got != 1 {
		t.Errorf("task rows of #13 = %d", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 14 AND kind = 'project'", shop); got != 1 {
		t.Errorf("project rows of #14 = %d", got)
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

func setProject(n, project int) string {
	return "[[prompts]]\nwhen = \"Move " + itoa(int64(n)) + " to " + itoa(int64(project)) + "\"\ncall = { tool = \"set_project\", arguments = { repository = \"owner/shop\", n = " + itoa(int64(n)) + ", project = " + itoa(int64(project)) + " } }\n\n"
}

// addProjectTree adds the Projects #41 and #42 below the Workstream #12, the task #43 below parent, and the issue #44
// below the Project #41.
func addProjectTree(fake *testkit.FakeGitHub, parent int64) {
	for _, number := range []int64{41, 42} {
		fake.AddIssue(shop, number, "Project "+itoa(number))
		fake.AddLabel(shop, number, "mobius:project", "owner")
		fake.AddSubIssue(shop, 12, number)
	}
	fake.AddIssue(shop, 43, "Add plan model")
	fake.AddSubIssue(shop, parent, 43)
	fake.AddIssue(shop, 44, "Show the counters")
	fake.AddSubIssue(shop, 41, 44)
}

func waitForProjectTree(t *testing.T, server *testserver.Server) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number IN (41, 42, 43, 44) AND workstream IS NOT NULL", shop) == 4 &&
			countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number IN (41, 42) AND kind = 'project'", shop) == 2
	})
}

func TestSetProjectMovesATaskIntoAProjectToAnotherProjectAndOutOfAProject(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		parent  int64
		project int
		want    int64
		result  string
	}{
		{"into a Project", 12, 41, 41, "Moved owner/shop#43 below the Project owner/shop#41. A running task continues. If an Implementer works on the task, send the Project body with send_message."},
		{"to another Project", 41, 42, 42, "Moved owner/shop#43 below the Project owner/shop#42. A running task continues. If an Implementer works on the task, send the Project body with send_message."},
		{"out of a Project", 41, 0, 12, "Moved owner/shop#43 directly below the Workstream issue. A running task continues. If an Implementer works on the task, tell it with send_message that the task is no longer in a Project."},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := connect(t, fake, setProject(43, test.project))
			addProjectTree(fake, test.parent)
			waitForProjectTree(t, server)

			session := run(t, server, leadSpec(t), "Move 43 to "+itoa(int64(test.project)))

			if got := reply(t, server, session); got != test.result {
				t.Errorf("reply = %q", got)
			}
			// A task directly below the Workstream issue has no parent row.
			if got := countRows(t, server, "SELECT count(*) FROM issues i WHERE i.repository = ? AND i.number = 43 AND coalesce((SELECT p.number FROM issues p WHERE p.id = i.parent), 12) = ?", shop, test.want); got != 1 {
				t.Errorf("rows of #43 with the parent #%d = %d", test.want, got)
			}
			if got := fake.SubIssueNumbers(shop, test.want); !slices.Contains(got, 43) {
				t.Errorf("sub-issues of #%d = %v", test.want, got)
			}
			for _, other := range []int64{12, 41, 42} {
				if other != test.want && slices.Contains(fake.SubIssueNumbers(shop, other), 43) {
					t.Errorf("#43 is also a sub-issue of #%d", other)
				}
			}
			waitForPolls(t, fake)
			if got := countRows(t, server, "SELECT count(*) FROM issues i WHERE i.repository = ? AND i.number = 43 AND coalesce((SELECT p.number FROM issues p WHERE p.id = i.parent), 12) = ?", shop, test.want); got != 1 {
				t.Errorf("rows of #43 with the parent #%d after the polls = %d", test.want, got)
			}
		})
	}
}

func TestSetProjectChangesNothingWhenTheTaskHasThatParent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, setProject(44, 41)+setProject(43, 0))
	addProjectTree(fake, 12)
	waitForProjectTree(t, server)
	waitForPolls(t, fake)
	before := countRows(t, server, "SELECT count(*) FROM outgoing_changes")

	session := run(t, server, leadSpec(t), "Move 44 to 41", "Move 43 to 0")

	if got, want := reply(t, server, session), "owner/shop#44 is already below the Project owner/shop#41. Mobius changed nothing.owner/shop#43 is already directly below the Workstream issue. Mobius changed nothing."; got != want {
		t.Errorf("reply = %q", got)
	}
	if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes"); got != before {
		t.Errorf("outgoing changes = %d, want %d", got, before)
	}
}

func TestSetProjectRefusesAWorkstreamIssueAProjectAnIssueOfAnotherWorkstreamAndAProjectThatIsNotOpenOfThisWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, setProject(12, 41)+setProject(42, 41)+setProject(52, 41)+setProject(98, 41)+setProject(43, 43)+setProject(43, 51)+setProject(43, 45)+setProject(43, 98))
	addProjectTree(fake, 12)
	fake.AddIssue(shop, 45, "Closed project")
	fake.AddLabel(shop, 45, "mobius:project", "owner")
	fake.AddSubIssue(shop, 12, 45)
	fake.CloseIssue(shop, 45)
	fake.AddIssue(shop, 13, "Other Workstream")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 51, "Other project")
	fake.AddLabel(shop, 51, "mobius:project", "owner")
	fake.AddSubIssue(shop, 13, 51)
	fake.AddIssue(shop, 52, "Other task")
	fake.AddSubIssue(shop, 13, 52)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number IN (41, 42, 43, 44, 45, 51, 52) AND workstream IS NOT NULL", shop) == 7 &&
			countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number IN (41, 42, 45, 51) AND kind = 'project'", shop) == 4
	})

	session := run(t, server, leadSpec(t), "Move 12 to 41", "Move 42 to 41", "Move 52 to 41", "Move 98 to 41", "Move 43 to 43", "Move 43 to 51", "Move 43 to 45", "Move 43 to 98")

	calls := mcpCalls(t, server, session)
	want := []string{
		"owner/shop#12 is a Workstream issue.",
		"owner/shop#42 is a Project.",
		"owner/shop#52 is not in this Workstream.",
		"owner/shop#98 is not in this Workstream.",
		"owner/shop#43 is not an open Project of this Workstream.",
		"owner/shop#51 is not an open Project of this Workstream.",
		"owner/shop#45 is not an open Project of this Workstream.",
		"owner/shop#98 is not an open Project of this Workstream.",
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v", calls)
	}
	for i, message := range want {
		if calls[i]["error"] != message {
			t.Errorf("call %d error = %v, want %q", i, calls[i]["error"], message)
		}
	}
	if got := countRows(t, server, "SELECT count(*) FROM outgoing_changes WHERE field = 'parent'"); got != 0 {
		t.Errorf("parent changes = %d", got)
	}
}

func TestSetProjectClearsTheBucketOfATaskThatWasBelowABucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, setProject(43, 41))
	fake.AddIssue(shop, 90, "Bucket 1 of #12")
	fake.AddLabel(shop, 90, "mobius:bucket", "owner")
	fake.AddSubIssue(shop, 12, 90)
	addProjectTree(fake, 90)
	waitForProjectTree(t, server)
	testkit.WaitFor(t, func() bool {
		return countRows(t, server, "SELECT count(*) FROM issues WHERE repository = ? AND number = 43 AND json_extract(metadata, '$.github.bucket') = 90", shop) == 1
	})

	run(t, server, leadSpec(t), "Move 43 to 41")

	waitForPolls(t, fake)
	if got := countRows(t, server, "SELECT count(*) FROM issues i WHERE i.repository = ? AND i.number = 43 AND json_extract(i.metadata, '$.github.bucket') IS NULL AND (SELECT p.number FROM issues p WHERE p.id = i.parent) = 41", shop); got != 1 {
		t.Errorf("rows of #43 with no bucket below #41 = %d", got)
	}
	if got := fake.SubIssueNumbers(shop, 90); len(got) != 0 {
		t.Errorf("sub-issues of the bucket = %v", got)
	}
}
