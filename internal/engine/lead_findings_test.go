package engine_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const keptFindings = "Remove the lines out of scope.\n"

func TestStartFixRoundKeepsTheFindingsOfTheLeadOnAWorkingTaskForTheNextFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	goFile := filepath.Join(dataDir, "go")
	first := fmt.Sprintf("[[prompts]]\nwhen = \"Store plans in cents.\"\nshell = '''while [ ! -e '%s' ]; do sleep 0.05; done; echo cents > plan.txt && git add plan.txt && git commit -q -m 'Add plan model''''\n\n", goFile)
	round := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"true\"\n\n"
	server, _ := connectTaskIn(t, fake, dataDir, noFinding+leadFindings+leadStarts, round+first, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "working" })

	sendChat(t, server, leadChat, "Send the findings to #41")

	waitForChat(t, server, leadChat, "Lead", "Kept the findings on #41. The next fix round gets them.")
	if task := liveTask(t, server, 41); task.LeadFindings != keptFindings || task.FixRounds != 0 {
		t.Errorf("task = %+v", task)
	}

	touch(t, goFile)

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		for _, prompt := range implementerPrompts(t, server) {
			if strings.Contains(prompt, "# Open items\n") {
				return prompt, true
			}
		}
		return "", false
	})
	if !strings.HasPrefix(prompt, "# Open items\n\nFindings of the Lead:\n"+keptFindings) {
		t.Errorf("prompt = %s", prompt)
	}
	testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).LeadFindings == "" })
	if task := liveTask(t, server, 41); task.FixRounds != 1 {
		t.Errorf("task = %+v", task)
	}
}

func TestStartFixRoundKeepsTheFindingsOfTheLeadOnAReviewedTaskAndStartsAFixRoundAtChecks(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	round := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"true\"\n\n"
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, round+commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })
	thread := fake.AddReviewComment(shop, 42, 0, app, "Use cents.")
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'reviewed' WHERE issue = 41 AND state = 'approval'"); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)

	sendChat(t, server, leadChat, "Send the findings to #41")

	waitForChat(t, server, leadChat, "Lead", "Kept the findings on #41. The next fix round gets them.")
	if state := taskState(t, server); state != "reviewed" {
		t.Errorf("state = %s", state)
	}
	if prompts := implementerPrompts(t, server); len(prompts) != 1 {
		t.Errorf("prompts = %q", prompts)
	}

	fake.ResolveReviewThread(thread)

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		for _, prompt := range implementerPrompts(t, server) {
			if strings.Contains(prompt, "# Open items\n") {
				return prompt, true
			}
		}
		return "", false
	})
	if !strings.HasPrefix(prompt, "# Open items\n\nFindings of the Lead:\n"+keptFindings) {
		t.Errorf("prompt = %s", prompt)
	}
	testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).LeadFindings == "" })
	if task := liveTask(t, server, 41); task.FixRounds != 1 {
		t.Errorf("task = %+v", task)
	}
}

func TestThePollStartsAFixRoundForKeptFindingsOnATaskInApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	round := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\nshell = \"true\"\n\n"
	server, _ := connectTask(t, fake, noFinding+leadStarts, round+commits, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return taskState(t, server) == "approval" })

	if _, err := server.DB.Exec("UPDATE tasks SET lead_findings = ? WHERE issue = 41", keptFindings); err != nil {
		t.Fatal(err)
	}

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		for _, prompt := range implementerPrompts(t, server) {
			if strings.Contains(prompt, "# Open items\n") {
				return prompt, true
			}
		}
		return "", false
	})
	if !strings.HasPrefix(prompt, "# Open items\n\nFindings of the Lead:\n"+keptFindings) {
		t.Errorf("prompt = %s", prompt)
	}
	testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).LeadFindings == "" })
	if task := liveTask(t, server, 41); task.FixRounds != 1 {
		t.Errorf("task = %+v", task)
	}
}

func TestStartFixRoundRefusesATaskThatLeftChecksDuringTheCall(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	id := fake.AddCheckRun(shop, checkRun("build", sha, "in_progress", ""))
	waitForPolls(t, fake)
	pollReached, releasePoll := fake.HoldPullRequest(shop, 42)
	<-pollReached
	toolReached, releaseTool := fake.HoldPullRequest(shop, 42)

	sendChat(t, server, leadChat, "Send the findings to #41")
	<-toolReached
	fake.SetCheckRunStatus(id, "completed", "success")
	releasePoll()
	waitForTask(t, server, 41, "approval")
	releaseTool()

	waitForChat(t, server, leadChat, "Lead", "error: The task of #41 is not checks any more.")
	if task := liveTask(t, server, 41); task.State != "approval" || task.LeadFindings != "" || task.FixRounds != 0 {
		t.Errorf("task = %+v", task)
	}
	if sessions := roleSessions(t, server, engine.ImplementerRole); len(sessions) != 1 {
		t.Errorf("sessions = %+v", sessions)
	}
}

const (
	// leadRounds is a Lead that starts a fix round of #41 for a message that it gets now, and keeps other findings for a
	// second message.
	leadRounds = "[[prompts]]\nwhen = \"\\n\\nKeep the findings of #41\"\ncall = { tool = \"start_fix_round\", arguments = { repository = \"owner/shop\", n = 41, findings = \"Name the unit.\" } }\n\n" +
		"[[prompts]]\nwhen = \"\\n\\nStart the round of #41\"\ncall = { tool = \"start_fix_round\", arguments = { repository = \"owner/shop\", n = 41, findings = \"Use cents.\" } }\n\n"
	eachRound  = "shell = \"echo $$ >> plan.txt && git commit -q -am 'Store the unit'\"\n\n"
	keptUnit   = "Name the unit.\n"
	keptResult = "Kept the findings on #41. The next fix round gets them."
)

// promptsWith gives the Implementer prompts of the Workstream #12 that have text.
func promptsWith(t *testing.T, server *testserver.Server, text string) []string {
	t.Helper()
	var found []string
	for _, prompt := range implementerPrompts(t, server) {
		if strings.Contains(prompt, text) {
			found = append(found, prompt)
		}
	}
	return found
}

func TestStartFixRoundKeepsTheFindingsOfTheLeadOnAQueuedFixRoundForTheNextFixRound(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	goFile := filepath.Join(dataDir, "go")
	implementer := fmt.Sprintf("[[prompts]]\nwhen = \"Round prices down.\"\nshell = '''while [ ! -e '%s' ]; do sleep 0.05; done; echo dollars > price.txt && git add price.txt && git commit -q -m 'Add price''''\n\n", goFile) +
		"[[prompts]]\nwhen = \"Use cents.\"\n" + eachRound +
		"[[prompts]]\nwhen = \"Name the unit.\"\n" + eachRound + commits
	server, _ := connectTaskIn(t, fake, dataDir, noFinding+leadRounds+leadStartsTwo, implementer, func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 })
	fake.AddSubIssueOf(shop, 12, 43, "Task 43")
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitForState(t, server, 41, "approval")
	fake.AddLabel(shop, 43, "mobius:ready", "owner")
	waitForState(t, server, 43, "working")
	sendChat(t, server, leadChat, "Start the round of #41")
	waitForState(t, server, 41, "queued")
	waitQueued(t, server, engine.ImplementerRole, 41)

	sendChat(t, server, leadChat, "Keep the findings of #41")

	waitForChat(t, server, leadChat, "Lead", keptResult)
	if task := liveTask(t, server, 41); task.State != "queued" || task.LeadFindings != keptUnit || task.FixRounds != 1 {
		t.Errorf("task = %+v", task)
	}

	touch(t, goFile)

	testkit.WaitFor(t, func() bool {
		return liveTask(t, server, 41).FixRounds == 2 && liveTask(t, server, 41).LeadFindings == ""
	})
	testkit.WaitFor(t, func() bool { return len(promptsWith(t, server, "Name the unit.")) == 1 })
	round := promptsWith(t, server, "Use cents.")
	if len(round) != 1 || strings.Contains(round[0], "Name the unit.") {
		t.Errorf("round prompts = %q", round)
	}
	if next := promptsWith(t, server, "Name the unit.")[0]; !strings.Contains(next, "Findings of the Lead:\n"+keptUnit) || strings.Contains(next, "Use cents.") {
		t.Errorf("next prompt = %s", next)
	}
}

func TestStartFixRoundKeepsTheFindingsOfTheLeadWhileTheReviewerOfTheTaskIsQueued(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	implementer := "[[prompts]]\nwhen = \"Remove the lines out of scope.\"\n" + eachRound + commits
	server, _ := connectTask(t, fake, noFinding+leadFindings+leadStarts, implementer, func(cfg *config.Config) { cfg.Roles.Reviewer.Max = 1 })
	spec := implementerSpec(t, server, fake, 43)
	spec.Role = engine.ReviewerRole
	holder := start(t, server, spec)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	waitQueued(t, server, engine.ReviewerRole, 41)
	waitForState(t, server, 41, "queued")

	sendChat(t, server, leadChat, "Send the findings to #41")

	waitForChat(t, server, leadChat, "Lead", keptResult)
	if task := liveTask(t, server, 41); task.State != "queued" || task.LeadFindings != keptFindings || task.FixRounds != 0 {
		t.Errorf("task = %+v", task)
	}

	end(t, holder, "done")

	testkit.WaitFor(t, func() bool {
		return liveTask(t, server, 41).FixRounds == 1 && liveTask(t, server, 41).LeadFindings == ""
	})
	testkit.WaitFor(t, func() bool { return len(promptsWith(t, server, "Remove the lines out of scope.")) > 0 })
	prompts := promptsWith(t, server, "Remove the lines out of scope.")
	if len(prompts) != 1 || !strings.HasPrefix(prompts[0], "# Open items\n\nFindings of the Lead:\n"+keptFindings) {
		t.Errorf("prompts = %q", prompts)
	}
}
