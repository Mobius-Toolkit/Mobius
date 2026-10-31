package engine_test

import (
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	firstReport  = "Cause: plans had no unit.\nTests: pass."
	secondReport = "Cause: plans had no unit.\nTests: pass.\nUpgrade: none."
	// commitsWithReport is an Implementer that commits plan.txt and gives firstReport.
	commitsWithReport  = "[[prompts]]\n" + commitCents + "call = { tool = \"submit_report\", arguments = { report = \"Cause: plans had no unit.\\nTests: pass.\" } }\n\n"
	screenshotsSection = "<!-- screenshots -->\n### UI screenshots\n\nThe screenshots are pending.\n<!-- /screenshots -->"
)

// reportSectionOf gives the section of the description with report, as Mobius writes it.
func reportSectionOf(report string) string {
	return "<!-- report -->\n### Report\n\n" + report + "\n\n— Implementer of #41\n<!-- /report -->"
}

// roundWithReport is an Implementer that gives firstReport in its first turn and runs fix in the round with the findings of
// the Lead.
func roundWithReport(fix string) string {
	return "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"echo cents per month > plan.txt && git commit -q -am 'Fix the plan'\"\n" + fix + "\n" + commitsWithReport
}

func TestTheDescriptionOfTheNewPullRequestHasTheReportAndTheReviewerGetsIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commitsWithReport, withUIURL)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)
	want := "Workstream:\n- #12\n\nIssue:\n- #41\n\nCloses #41\n\n" + chatLink + "\n\n" + reportSectionOf(firstReport)
	if body := fake.PullRequests(shop)[0].Body; body != want {
		t.Errorf("body = %q", body)
	}
	if task := liveTask(t, server, 41); task.Report != firstReport {
		t.Errorf("report = %q", task.Report)
	}
	reviewer := endedReviewers(t, server, 1)[0]
	if prompt := promptTexts(t, server, reviewer.ID)[0]; !strings.Contains(prompt, "# Report\n\n"+firstReport+"\n\n# Commits") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestTheReviewerPromptSaysThatTheImplementerGaveNoReport(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)
	reviewer := endedReviewers(t, server, 1)[0]
	if prompt := promptTexts(t, server, reviewer.ID)[0]; !strings.Contains(prompt, "# Report\n\nThe Implementer gave no report.\n\n# Commits") {
		t.Errorf("prompt = %s", prompt)
	}
	if body := fake.PullRequests(shop)[0].Body; strings.Contains(body, "<!-- report -->") {
		t.Errorf("body = %q", body)
	}
}

func TestAFixRoundGetsTheReportAndReplacesTheSectionBeforeTheScreenshots(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fix := "call = { tool = \"submit_report\", arguments = { report = \"Cause: plans had no unit.\\nTests: pass.\\nUpgrade: none.\" } }\n"
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, roundWithReport(fix), noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	opening := fake.PullRequests(shop)[0].Body
	fake.SetBody(shop, 42, opening+"\n\n"+screenshotsSection)

	sendChat(t, server, leadChat, "Send the findings to #41")

	testkit.WaitFor(t, func() bool { return strings.Contains(fake.PullRequests(shop)[0].Body, "Upgrade: none.") })
	text, _, _ := strings.Cut(opening, "<!-- report -->")
	want := text + reportSectionOf(secondReport) + "\n\n" + screenshotsSection
	if body := fake.PullRequests(shop)[0].Body; body != want {
		t.Errorf("body = %q", body)
	}
	round := roundPrompt(t, server)
	if want := "# Open items\n\nFindings of the Lead:\nRemove the lines out of scope.\n\nIf your change makes your report wrong, call `submit_report` with the full new report."; !strings.Contains(round, want) {
		t.Errorf("prompt = %s", round)
	}
	if task := liveTask(t, server, 41); task.Report != secondReport {
		t.Errorf("report = %q", task.Report)
	}
}

func TestSubmitReportRefusesAReportOfMoreThan20000CharactersAndKeepsTheReport(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	long := strings.Repeat("a", 20_001)
	fix := "call = { tool = \"submit_report\", arguments = { report = \"" + long + "\" } }\n"
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, roundWithReport(fix), noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })

	sendChat(t, server, leadChat, "Send the findings to #41")

	replies := testkit.WaitForValue(t, func() ([]string, bool) {
		var replies []string
		for _, session := range roleSessions(t, server, engine.ImplementerRole) {
			replies = append(replies, reply(t, server, session.ID))
		}
		return replies, len(replies) == 2 && strings.Contains(replies[1], "error:")
	})
	if want := "error: The report has 20001 characters. The limit is 20000. Shorten the report and call `submit_report` again."; !strings.Contains(replies[1], want) {
		t.Errorf("reply = %q", replies[1])
	}
	if task := liveTask(t, server, 41); task.Report != firstReport {
		t.Errorf("report = %q", task.Report)
	}
}

func TestSubmitReportRefusesAReportThatContainsAMarkerAndKeepsTheReport(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fix := "call = { tool = \"submit_report\", arguments = { report = \"The section `<!-- /report -->` is the end.\" } }\n"
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, roundWithReport(fix), noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })

	sendChat(t, server, leadChat, "Send the findings to #41")

	replies := testkit.WaitForValue(t, func() ([]string, bool) {
		var replies []string
		for _, session := range roleSessions(t, server, engine.ImplementerRole) {
			replies = append(replies, reply(t, server, session.ID))
		}
		return replies, len(replies) == 2 && strings.Contains(replies[1], "error:")
	})
	if want := "error: The report contains the marker `<!-- /report -->`."; !strings.Contains(replies[1], want) {
		t.Errorf("reply = %q", replies[1])
	}
	if task := liveTask(t, server, 41); task.Report != firstReport {
		t.Errorf("report = %q", task.Report)
	}
}
