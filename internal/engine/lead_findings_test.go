package engine_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
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
	if !strings.HasSuffix(prompt, "# Open items\n\nFindings of the Lead:\n"+keptFindings) {
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
	if !strings.HasSuffix(prompt, "# Open items\n\nFindings of the Lead:\n"+keptFindings) {
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
	if !strings.HasSuffix(prompt, "# Open items\n\nFindings of the Lead:\n"+keptFindings) {
		t.Errorf("prompt = %s", prompt)
	}
	testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).LeadFindings == "" })
	if task := liveTask(t, server, 41); task.FixRounds != 1 {
		t.Errorf("task = %+v", task)
	}
}
