package engine_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestClosingATaskWithAnOpenPullRequestClosesTheIssueAndThePullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"ready for Lead approval of #41\"\ncall = { tool = \"close_task\", arguments = { n = 41, reason = \"#43 has this work.\" } }\n\n" + leadStarts
	server, _ := connectTask(t, fake, lead, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool {
		state, _ := fake.State(shop, 41)
		return state == "closed" && !hasLiveTask(t, server, 41)
	})
	if state, reason := fake.State(shop, 41); state != "closed" || reason != "not_planned" {
		t.Errorf("state = %s, reason = %s", state, reason)
	}
	if state, _ := fake.State(shop, 42); state != "closed" {
		t.Errorf("pull request state = %s", state)
	}
	if slices.Contains(fake.Labels(shop, 41), "mobius:ready") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
	runs := fake.CheckRuns(shop)
	if last := runs[len(runs)-1]; last.HeadSHA != head(t, fake, "mobius/41") || last.Conclusion != "failure" || last.Output.Title != "Closed" || last.Output.Summary != "#43 has this work." {
		t.Errorf("check runs = %+v", runs)
	}
	comment := testkit.Comment{Author: "mobius-test[bot]", Body: "#43 has this work."}
	if !slices.Contains(fake.Comments(shop, 42), comment) || !slices.Contains(fake.Comments(shop, 41), comment) {
		t.Errorf("comments = %+v, %+v", fake.Comments(shop, 42), fake.Comments(shop, 41))
	}
	head(t, fake, "mobius/41")
}

func TestClosingATaskIssueWithNoLiveTaskClosesItAtOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nwhen = \"Close it\"\ncall = { tool = \"close_task\", arguments = { n = 41, reason = \"Main has this work.\" } }\n\n[[prompts]]\nwhen = \"List it\"\ncall = { tool = \"list_tasks\" }\n")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	waitForTasks(t, server, []taskLine{line(41, "Add plan model", "open", 0)})

	session := run(t, server, leadSpec(t), "Close it", "List it")

	calls := mcpCalls(t, server, session)
	if len(calls) != 2 || calls[0]["result"] != "Closed #41." || calls[1]["result"] != "" {
		t.Errorf("calls = %+v", calls)
	}
	if state, reason := fake.State(shop, 41); state != "closed" || reason != "not_planned" {
		t.Errorf("state = %s, reason = %s", state, reason)
	}
	if got, want := fake.Comments(shop, 41), []testkit.Comment{{Author: "mobius-test[bot]", Body: "Main has this work."}}; !slices.Equal(got, want) {
		t.Errorf("comments = %+v", got)
	}
}

func TestClosingTheWorkstreamIssueOrAnIssueOfAnotherWorkstreamIsRefused(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nwhen = \"Close 12\"\ncall = { tool = \"close_task\", arguments = { n = 12, reason = \"Done.\" } }\n\n[[prompts]]\nwhen = \"Close 51\"\ncall = { tool = \"close_task\", arguments = { n = 51, reason = \"Done.\" } }\n")
	fake.AddIssue(shop, 13, "Other Workstream")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 51, "Other task")
	fake.AddSubIssue(shop, 13, 51)
	testkit.WaitFor(t, func() bool {
		status, text := send(t, server, http.MethodGet, "/api/workstreams/owner/shop/13/tasks", "")
		return status == http.StatusOK && strings.Contains(text, `"number":51`)
	})

	session := run(t, server, leadSpec(t), "Close 12", "Close 51")

	calls := mcpCalls(t, server, session)
	if len(calls) != 2 || calls[0]["error"] != "#12 is not a task issue of this Workstream." || calls[1]["error"] != "#51 is not a task issue of this Workstream." {
		t.Errorf("calls = %+v", calls)
	}
	for _, number := range []int64{12, 51} {
		if state, _ := fake.State(shop, number); state != "open" || len(fake.Comments(shop, number)) != 0 {
			t.Errorf("#%d: state = %s, comments = %+v", number, state, fake.Comments(shop, number))
		}
	}
}
