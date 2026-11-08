package engine_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	// onlyKeyInRoundTwo is a Reviewer that reports only PLAN_API_KEY for the issue body of the second round.
	onlyKeyInRoundTwo = "[[prompts]]\nwhen = \"Plans have a price. Round two.\"\ncall = { tool = \"report_deployment_needs\", arguments = { needs = [" +
		"{ kind = \"env_var\", name = \"PLAN_API_KEY\", path = \"plan.txt\", line = 1, reason = \"The code reads it at start.\", mandatory = true, after_merge = false }] } }\n\n"
	// reportInRoundTwo is an Implementer that gives no report in its first turn, and commits and gives a report in the round
	// with the findings of the Lead.
	reportInRoundTwo = "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo fixed > fix.txt && git add fix.txt && git commit -q -m Fix\"\n" +
		"call = { tool = \"submit_report\", arguments = { report = \"Cause: plans had no unit.\\nTests: pass.\" } }\n\n" + commits
)

func pullBody(fake *testkit.FakeGitHub) string {
	return fake.PullRequests(shop)[0].Body
}

// todosSectionOf gives the todos section of the description with lines, as Mobius writes it.
func todosSectionOf(lines ...string) string {
	return "<!-- todos -->\n### Deployment todos\n\n" + strings.Join(lines, "\n") + "\n<!-- /todos -->"
}

func waitForBody(t *testing.T, fake *testkit.FakeGitHub, want string) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return strings.Contains(pullBody(fake), want) })
}

// approvableWithReport is approvableWith with an Implementer that gives a report. It gives a server with a task that the
// Lead can approve.
func approvableWithReport(t *testing.T, fake *testkit.FakeGitHub, level, reviewer string) *testserver.Server {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, reviewer+leadApproves+leadStarts, commitsWithReport, keepSessionOpen)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, level)
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == level })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	return server
}

func TestAnApprovalWithNeedsWritesTheTodosSectionBetweenTheReportAndTheScreenshotsSections(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWithReport(t, fake, "off", twoNeeds)
	opening := pullBody(fake)
	fake.SetBody(shop, 42, opening+"\n\n"+screenshotsSection)

	sendChat(t, server, leadChat, "Approve #41")

	waitForBody(t, fake, "<!-- todos -->")
	want := opening + "\n\n" + todosSectionOf("- [ ] PLAN_API_KEY (mandatory, open)", "- [ ] plan.currency (optional, open)") + "\n\n" + screenshotsSection
	if body := pullBody(fake); body != want {
		t.Errorf("body = %q", body)
	}
	if !strings.Contains(opening, reportSectionOf(firstReport)) {
		t.Errorf("opening = %q", opening)
	}
}

func TestCompleteAndDismissChangeTheLineOfTheTodoToCheckedWithItsState(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, twoNeeds)
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "todo")) == 2 })
	todos := itemsOf(t, server, "todo")
	waitForBody(t, fake, "<!-- todos -->")

	if status, body := doneTodo(t, server, todos[0].ID, `{"note":""}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if want := todosSectionOf("- [x] PLAN_API_KEY (mandatory, done)", "- [ ] plan.currency (optional, open)"); !strings.Contains(pullBody(fake), want) {
		t.Errorf("body = %q", pullBody(fake))
	}

	if status, body := dismissItem(t, server, todos[1].ID); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if want := todosSectionOf("- [x] PLAN_API_KEY (mandatory, done)", "- [x] plan.currency (optional, dismissed)"); !strings.Contains(pullBody(fake), want) {
		t.Errorf("body = %q", pullBody(fake))
	}
}

func TestAnAfterMergeTodoShowsTheMarkAndCompleteAfterTheMergeChangesTheBodyOfTheMergedPullRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "merge", true, afterMergeNeed)
	sendChat(t, server, leadChat, "Approve #41")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "" })
	todo := waitForItem(t, server, "todo")
	waitForBody(t, fake, "<!-- todos -->")
	if want := todosSectionOf("- [ ] roles.lead.context (mandatory, after merge, open)"); !strings.Contains(pullBody(fake), want) {
		t.Fatalf("body = %q", pullBody(fake))
	}

	if status, body := doneTodo(t, server, todo.ID, `{"note":"Added."}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if want := todosSectionOf("- [x] roles.lead.context (mandatory, after merge, done)"); !strings.Contains(pullBody(fake), want) {
		t.Errorf("body = %q", pullBody(fake))
	}
}

func TestAReviewThatRemovesTheNeedOfATodoRemovesItsLineAtTheNextApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := todoBeforeAFixRound(t, fake, "off", onlyKeyInRoundTwo+twoNeeds)
	waitForBody(t, fake, "plan.currency (optional, open)")

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return !strings.Contains(pullBody(fake), "plan.currency") })
	if want := todosSectionOf("- [ ] PLAN_API_KEY (mandatory, open)"); !strings.Contains(pullBody(fake), want) {
		t.Errorf("body = %q", pullBody(fake))
	}
}

func TestAReviewThatRemovesTheLastNeedRemovesTheTodosSectionAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := todoBeforeAFixRound(t, fake, "off", noNeedInRoundTwo+mandatoryNeed)
	waitForBody(t, fake, "<!-- todos -->")
	withSection := pullBody(fake)

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return !strings.Contains(pullBody(fake), "<!-- todos -->") })
	section := todosSectionOf("- [ ] PLAN_API_KEY (mandatory, open)")
	if want := strings.Replace(withSection, "\n\n"+section, "", 1); pullBody(fake) != want || want == withSection {
		t.Errorf("body = %q, want %q", pullBody(fake), want)
	}
}

func TestAFailedWriteOfTheTodosSectionKeepsTheTodoItemOpenAndASecondCompleteWritesTheSection(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := approvableWith(t, fake, "off", true, mandatoryNeed)
	sendChat(t, server, leadChat, "Approve #41")
	todo := waitForItem(t, server, "todo")
	waitForBody(t, fake, "<!-- todos -->")
	fake.FailBodyWrites(true)

	if status, body := doneTodo(t, server, todo.ID, `{"note":"Set in prod."}`); status == http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if len(itemsOf(t, server, "todo")) != 1 || !strings.Contains(pullBody(fake), "- [ ] PLAN_API_KEY (mandatory, open)") {
		t.Errorf("todo items = %d, body = %q", len(itemsOf(t, server, "todo")), pullBody(fake))
	}
	fake.FailBodyWrites(false)

	if status, body := doneTodo(t, server, todo.ID, `{"note":"Set in prod."}`); status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	if len(itemsOf(t, server, "todo")) != 0 || !strings.Contains(pullBody(fake), "- [x] PLAN_API_KEY (mandatory, done)") {
		t.Errorf("todo items = %d, body = %q", len(itemsOf(t, server, "todo")), pullBody(fake))
	}
}

func TestAnApprovalThatChangesNoTodoLineMakesNoWriteOfTheDescription(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := todoBeforeAFixRound(t, fake, "off", twoNeeds)
	waitForBody(t, fake, "<!-- todos -->")
	writes := fake.BodyWrites(shop, 42)
	if writes != 1 {
		t.Fatalf("writes = %d", writes)
	}

	sendChat(t, server, leadChat, "Approve #41")

	testkit.WaitFor(t, func() bool { return taskState(t, server) == "ready_for_review" })
	waitForPolls(t, fake)
	if got := fake.BodyWrites(shop, 42); got != writes {
		t.Errorf("writes = %d, want %d", got, writes)
	}
}

func TestANewReportSectionOfAFixRoundComesBeforeTheTodosSection(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, _ := connectTask(t, fake, mandatoryNeed+leadApproves+leadFindings+leadStarts, reportInRoundTwo, keepSessionOpen)
	authorize(t, server)
	setWorkstreamAutopilot(t, server, "off")
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "off" })
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForReadyEvents(t, fake, server, 1)
	sendChat(t, server, leadChat, "Approve #41")
	waitForBody(t, fake, "<!-- todos -->")
	withTodos := pullBody(fake)

	fake.SetBody(shop, 41, "Plans have a price. Round two.")
	sendChat(t, server, leadChat, "Send the findings to #41")

	waitForBody(t, fake, "<!-- report -->")
	text, section, _ := strings.Cut(withTodos, "<!-- todos -->")
	want := strings.TrimRight(text, "\n") + "\n\n" + reportSectionOf(firstReport) + "\n\n<!-- todos -->" + section
	if body := pullBody(fake); body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}
