package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func TestTheTasksTabLeavesOutAClosedBucketAndShowsItsIssuesAtTheDepthOfTheBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 40, "First task")
	fake.AddSubIssue(shop, 12, 40)
	fake.AddIssue(shop, 41, "Bucket 1 of #12")
	fake.AddLabel(shop, 41, "mobius:bucket", "owner")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Closed task")
	fake.AddSubIssue(shop, 41, 50)
	fake.AddIssue(shop, 51, "Task of the closed task")
	fake.AddSubIssue(shop, 50, 51)
	fake.CloseIssue(shop, 50)
	fake.CloseIssue(shop, 41)

	server := startCopied(t, fake)

	waitForTasks(t, server, []taskLine{line(40, "First task", "open", 0), line(50, "Closed task", "closed", 0), line(51, "Task of the closed task", "open", 1)})
}

func TestAutopilotDispatchesTheIssueBelowAnOpenBucketAndListTasksLeavesTheBucketOut(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	prepareAutopilot(fake, true)
	addTaskIssue(fake, 41, "Bucket 1 of #12")
	fake.AddLabel(shop, 41, "mobius:bucket", "owner")
	fake.AddIssue(shop, 42, "Add plan model")
	fake.AddSubIssue(shop, 41, 42)
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nwhen = \"Read the work\"\ncall = { tool = \"list_tasks\" }\n\n"+seen)
	server := startServerWith(t, fake, testserver.Config(t, dataDir), "")

	liveTaskOf(t, server, 42)
	waitForPolls(t, fake)
	_, text := leadReply(t, server)

	if hasLiveTask(t, server, 41) || activeTasks(t, server) != 1 {
		t.Error("the bucket got a task")
	}
	if !strings.Contains(text, "owner/shop#42 Add plan model") || strings.Contains(text, "Bucket") {
		t.Errorf("list_tasks = %q", text)
	}
}

func TestTheDetailsOfAWorkstreamCountTheIssuesBelowABucketAndNotTheBucketAndTheClosureKeepsTheBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Bucket 1 of #12")
	fake.AddLabel(shop, 41, "mobius:bucket", "owner")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddIssue(shop, 50, "Closed task")
	fake.AddSubIssue(shop, 41, 50)
	fake.CloseIssue(shop, 50)
	fake.AddIssue(shop, 51, "Open task")
	fake.AddSubIssue(shop, 41, 51)
	fake.AddIssue(shop, 52, "Other open task")
	fake.AddSubIssue(shop, 12, 52)

	server := startCopied(t, fake)

	if status, got := details(t, server, 12); status != 200 || got.OpenTasks != 2 || got.CompletedTasks != 1 {
		t.Errorf("details = %d, %+v", status, got)
	}
	items := closure(t, server)
	if len(items) != 4 || items[0].Number != 41 || items[1].Number != 51 || items[2].Number != 52 || items[3].Number != 12 {
		t.Errorf("closure = %+v", items)
	}
}

func TestAnOpenBucketThatTheLeadCreatedWithNoWorkstreamStartsNoTriager(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectLeadWrites(t, fake, "gh issue create -R owner/shop --title 'Bucket 1 of #12' --label mobius:bucket")
	leadWrites(t, server, 1)

	fake.CreateLabeledIssue(shop, 52, "Bucket 1 of #12", "A bucket.", app, "mobius:bucket")

	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 {
		t.Errorf("sessions = %+v", got)
	}
	if got := fake.Labels(shop, 52); len(got) != 1 || got[0] != "mobius:bucket" {
		t.Errorf("labels = %v", got)
	}
}
