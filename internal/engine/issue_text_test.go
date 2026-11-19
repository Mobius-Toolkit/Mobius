package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	editThenStart = "[[prompts]]\nwhen = \"dispatch of #41\"\ncall = { tool = \"edit_issue_body\", arguments = { repository = \"owner/shop\", n = 41, body = \"Prices are stored in cents.\" } }\n" +
		"then = { tool = \"start_implementer\", arguments = { repository = \"owner/shop\", n = 41, instructions = \"Store plans in cents.\" } }\n\n"
	rowBody = "Prices are stored in cents."
)

func TestTheImplementerPromptHasTheBodyOfTheRowBeforeItsSend(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, editThenStart, commits, noChange)
	fake.FailBodyWrites(true)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := implementerPrompts(t, server)
		return prompts, len(prompts) > 0
	})
	if !strings.Contains(prompts[0], "# Issue\n\nowner/shop#41 Add plan model (issue, open)\n\n"+rowBody+"\n") {
		t.Errorf("prompt = %s", prompts[0])
	}
	if strings.Contains(prompts[0], "Plans have a price.") {
		t.Errorf("the prompt has the body of GitHub: %s", prompts[0])
	}
	if _, body := fake.Issue(shop, 41); body != "Plans have a price." {
		t.Errorf("github body = %q", body)
	}
}

func TestTheReviewerPromptHasTheBodyOfTheRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	lead := "[[prompts]]\nwhen = \"You are the Reviewer\"\nshell = \"true\"\n\n" + editThenStart
	server, _ := connectTask(t, fake, lead, commits, noChange)
	fake.SetCheck(shop, "grep -q cents plan.txt")
	fake.FailBodyWrites(true)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	session := endedReviewers(t, server, 1)[0]
	prompts := promptTexts(t, server, session.ID)
	if !strings.Contains(prompts[0], "# Issue\n\n#41 Add plan model\n\n"+rowBody+"\n\n# Pull request") {
		t.Errorf("prompt = %s", prompts[0])
	}
	if strings.Contains(prompts[0], "Plans have a price.") {
		t.Errorf("the prompt has the body of GitHub: %s", prompts[0])
	}
}

func TestReadIssueGivesTheBodyOfTheRowAndTheCommentsOfGitHub(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake,
		editBodyCall("Edit #41.", `{ repository = "owner/shop", n = 41, body = "Prices are stored in cents." }`)+
			call("read_issue", `{ repository = "owner/shop", n = 41 }`),
		noChange)
	fake.AddIssue(shop, 41, "Plan API")
	fake.SetBody(shop, 41, "Plans have a price.")
	fake.AddSubIssue(shop, 12, 41)
	fake.AddComment(shop, 41, "owner", "Owner comment")
	waitForTasks(t, server, []taskLine{line(41, "Plan API", "open", 0)})
	fake.FailBodyWrites(true)

	session := run(t, server, leadSpec(t), "Edit #41.", "Read #41.")

	got := reply(t, server, session)
	for _, part := range []string{"owner/shop#41 Plan API (issue, open)\n\n" + rowBody + "\n\n# Comments\n", " UTC:\nOwner comment\n"} {
		if !strings.Contains(got, part) {
			t.Errorf("%q is not in %s", part, got)
		}
	}
	if strings.Contains(got, "Plans have a price.") {
		t.Errorf("the text has the body of GitHub: %s", got)
	}
}

func TestATaskWhoseIssueHasNoRowGetsTheBodyOfGitHub(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	gate := filepath.Join(t.TempDir(), "gate")
	lead := "[[prompts]]\nwhen = \"dispatch of #41\"\nshell = \"while [ ! -e '" + gate + "' ]; do sleep 0.05; done\"\n" + startImplementer
	server, _ := connectTask(t, fake, lead, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })
	fake.RemoveSubIssue(shop, 12, 41)
	testkit.WaitFor(t, func() bool {
		var count int
		_ = server.DB.QueryRow("SELECT count(*) FROM issues WHERE repository = ? AND number = 41", shop).Scan(&count)
		return count == 0
	})
	touch(t, gate)

	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		prompts := implementerPrompts(t, server)
		return prompts, len(prompts) > 0
	})
	if !strings.Contains(prompts[0], "# Issue\n\nowner/shop#41 Add plan model (issue, open)\n\nPlans have a price.\n") {
		t.Errorf("prompt = %s", prompts[0])
	}
}
