package engine_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// leadWriteScript gives the prompts of a fake Lead whose prompt "Lead write N." runs the gh command number N, from 1.
func leadWriteScript(commands ...string) string {
	var script strings.Builder
	for i, command := range commands {
		fmt.Fprintf(&script, "\n[[prompts]]\nwhen = \"Lead write %d.\"\nshell = %q\n", i+1, command)
	}
	return script.String()
}

// stubGH makes a gh that changes nothing the real gh of the agents of dataDir. The test makes the change on the fake
// GitHub as the App, which is what the real gh does with the installation token of the Lead.
func stubGH(t *testing.T, dataDir string) {
	t.Helper()
	program, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(program, filepath.Join(dataDir, "harnesses", "gh")); err != nil {
		t.Fatal(err)
	}
}

// leadWrites runs the gh command number n of the fake Lead.
func leadWrites(t *testing.T, server *testserver.Server, n int) {
	t.Helper()
	session := run(t, server, leadSpec(t), fmt.Sprintf("Lead write %d.", n))
	if text := reply(t, server, session); !strings.HasSuffix(text, "exit 0") {
		t.Fatalf("reply = %q", text)
	}
}

// connectLeadWrites is connectTriager with the prompts of commands for the Lead.
func connectLeadWrites(t *testing.T, fake *testkit.FakeGitHub, commands ...string) *testserver.Server {
	t.Helper()
	server, dataDir := connect(t, fake, triager+leadWriteScript(commands...))
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	stubGH(t, dataDir)
	waitForPoll(t, server, fake)
	return server
}

func TestTheGHOfTheLeadRecordsTheWritesOfEachSubcommandWithTheSessionOfTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectLeadWrites(t, fake,
		"gh issue edit 41 42 -R owner/shop --add-label mobius:ready --remove-label mobius:triage",
		"gh issue create -R owner/shop --title 'Add points' --label mobius:triage,bug",
		"gh pr comment 42 -R owner/shop --body 'Please rebase.'",
		"gh issue list -R owner/shop",
	)

	for n := 1; n <= 4; n++ {
		leadWrites(t, server, n)
	}

	var got []string
	rows, err := server.DB.Query("SELECT session, repository, issue, kind, labels, text FROM lead_writes ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var session, issue int64
		var repository, kind, labels, text string
		if err := rows.Scan(&session, &repository, &issue, &kind, &labels, &text); err != nil {
			t.Fatal(err)
		}
		if session == 0 {
			t.Error("a write has no session")
		}
		got = append(got, fmt.Sprintf("%s#%d %s %s %q", repository, issue, kind, labels, text))
	}
	want := []string{
		`owner/shop#41 labeled ["mobius:ready"] ""`,
		`owner/shop#41 unlabeled ["mobius:triage"] ""`,
		`owner/shop#42 labeled ["mobius:ready"] ""`,
		`owner/shop#42 unlabeled ["mobius:triage"] ""`,
		`owner/shop#0 created ["mobius:triage","bug"] "Add points"`,
		`owner/shop#42 commented [] "Please rebase."`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("writes = %q", got)
	}
}

func TestTheRemovalOfMobiusTriageThatTheLeadMadeStopsTheTriager(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := connectLeadWrites(t, fake, "gh issue edit 51 -R owner/shop --remove-label mobius:triage")
			fake.AddIssue(shop, 51, "Rename plans")
			fake.AddLabel(shop, 51, "mobius:triage", "owner")
			waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return !session.EndedAt.Valid })
			if recorded {
				leadWrites(t, server, 1)
			}

			fake.RemoveLabel(shop, 51, "mobius:triage", app)

			if recorded {
				waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndReason.String == "stopped" })
				return
			}
			waitForPolls(t, fake)
			if sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(sessions) != 1 || sessions[0].EndedAt.Valid {
				t.Errorf("sessions = %+v", sessions)
			}
		})
	}
}

func TestTheAddOfMobiusAutopilotThatTheLeadMadeTurnsAutopilotOnAndGivesTheLeadOneEvent(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := connectLeadWrites(t, fake, "gh issue edit 12 -R owner/shop --add-label mobius:autopilot")
			if recorded {
				leadWrites(t, server, 1)
			}

			fake.AddLabel(shop, 12, "mobius:autopilot", app)

			if recorded {
				testkit.WaitFor(t, func() bool {
					return workstreams(t, server)[0].Autopilot == "start" && len(autopilotEvents(t, server)) == 1
				})
				return
			}
			waitForPolls(t, fake)
			if level := workstreams(t, server)[0].Autopilot; level != "off" || len(autopilotEvents(t, server)) != 0 {
				t.Errorf("level = %s, events = %q", level, autopilotEvents(t, server))
			}
		})
	}
}

func TestAnEventOfTheAppMoreThanTenMinutesAfterAWriteOfTheLeadDoesNotMatchIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectLeadWrites(t, fake, "gh issue edit 12 -R owner/shop --add-label mobius:autopilot")
	leadWrites(t, server, 1)
	if _, err := server.DB.Exec("UPDATE lead_writes SET created_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-11 minutes')"); err != nil {
		t.Fatal(err)
	}

	fake.AddLabel(shop, 12, "mobius:autopilot", app)

	waitForPolls(t, fake)
	if level := workstreams(t, server)[0].Autopilot; level != "off" || len(autopilotEvents(t, server)) != 0 {
		t.Errorf("level = %s, events = %q", level, autopilotEvents(t, server))
	}
}

func TestASecondEventOfTheAppWithTheSameLabelDoesNotMatchAWriteOfTheLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectLeadWrites(t, fake, "gh issue edit 12 -R owner/shop --add-label mobius:autopilot")
	leadWrites(t, server, 1)
	fake.AddLabel(shop, 12, "mobius:autopilot", app)
	testkit.WaitFor(t, func() bool { return workstreams(t, server)[0].Autopilot == "start" })
	fake.RemoveLabel(shop, 12, "mobius:autopilot", "owner")
	testkit.WaitFor(t, func() bool {
		return workstreams(t, server)[0].Autopilot == "off" && len(autopilotEvents(t, server)) == 2
	})

	fake.AddLabel(shop, 12, "mobius:autopilot", app)

	waitForPolls(t, fake)
	if level := workstreams(t, server)[0].Autopilot; level != "off" || len(autopilotEvents(t, server)) != 2 {
		t.Errorf("level = %s, events = %q", level, autopilotEvents(t, server))
	}
}

func TestANewIssueThatTheLeadCreatedGetsATriagerAndTheLabelThatTheLeadAdded(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := connectLeadWrites(t, fake, "gh issue create -R owner/shop --title 'Add loyalty points' --label mobius:triage")
			if recorded {
				leadWrites(t, server, 1)
			}

			fake.CreateIssue(shop, 52, "Add loyalty points", "Points for each order.", app)
			fake.AddLabel(shop, 52, "mobius:triage", app)

			if recorded {
				proposal := testkit.Comment{Author: app, Body: `Create the Workstream "Loyalty points" for this issue.`}
				testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 52), proposal) })
				return
			}
			waitForPolls(t, fake)
			if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 {
				t.Errorf("sessions = %+v", got)
			}
		})
	}
}

func TestANewIssueThatTheLeadCreatedWithNoLabelGetsMobiusTriageAndATriager(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := connectLeadWrites(t, fake, "gh issue create -R owner/shop --title 'Add loyalty points'")
			if recorded {
				leadWrites(t, server, 1)
			}

			fake.CreateIssue(shop, 52, "Add loyalty points", "Points for each order.", app)

			if recorded {
				proposal := testkit.Comment{Author: app, Body: `Create the Workstream "Loyalty points" for this issue.`}
				testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 52), proposal) })
				if got := fake.Labels(shop, 52); !slices.Equal(got, []string{"mobius:triage"}) {
					t.Errorf("labels = %v", got)
				}
				return
			}
			waitForPolls(t, fake)
			if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 || len(fake.Labels(shop, 52)) != 0 {
				t.Errorf("sessions = %+v, labels = %v", got, fake.Labels(shop, 52))
			}
		})
	}
}

func TestTheAddOfMobiusTriageThatTheLeadMadeStartsATriager(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server := connectLeadWrites(t, fake, "gh issue edit 53 -R owner/shop --add-label mobius:triage")
			fake.AddIssue(shop, 53, "Add points")
			if recorded {
				leadWrites(t, server, 1)
			}

			fake.AddLabel(shop, 53, "mobius:triage", app)

			if recorded {
				waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndReason.String == "done" })
				return
			}
			waitForPolls(t, fake)
			if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 {
				t.Errorf("sessions = %+v", got)
			}
		})
	}
}

func TestTheRemovalOfNeedsHumanThatTheLeadMadeSetsTheRoundCountersBackToZero(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := seedWaitingAdjusted(t, fake, "needs_human", leadWriteScript("gh issue edit 41 -R owner/shop --remove-label mobius:needs-human"), func(cfg *config.Config) { stubGH(t, cfg.DataDir) })
			testkit.WaitFor(t, func() bool { return hasLabel(fake, "mobius:needs-human") && pullRequestHasNeedsHuman(fake) })
			waitForPolls(t, fake)
			setFixRounds(t, server, 5)
			if recorded {
				leadWrites(t, server, 1)
			}

			fake.RemoveLabel(shop, 41, "mobius:needs-human", app)

			testkit.WaitFor(t, func() bool { return implementerRounds(t, server) == 1 })
			want := 6
			if recorded {
				want = 1
			}
			testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).FixRounds == int64(want) })
		})
	}
}

func TestTheAddOfMobiusReadyThatTheLeadMadeSetsTheRoundCountersBackToZero(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, dataDir := connectWith(t, fake, leadWriteScript("gh issue edit 41 -R owner/shop --add-label mobius:ready"), func(*config.Config) {})
			stubGH(t, dataDir)
			addStoppedTask(t, fake, server)
			if recorded {
				leadWrites(t, server, 1)
			}

			fake.AddLabel(shop, 41, "mobius:ready", app)

			testkit.WaitFor(t, func() bool { return implementerRounds(t, server) == 1 })
			want := 4
			if recorded {
				want = 1
			}
			testkit.WaitFor(t, func() bool { return liveTask(t, server, 41).FixRounds == int64(want) })
		})
	}
}

func TestTheRemovalOfAStateLabelThatTheLeadMadeStopsTheTask(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := seedWaitingAdjusted(t, fake, "approval", leadWriteScript("gh issue edit 41 -R owner/shop --remove-label mobius:working"), func(cfg *config.Config) { stubGH(t, cfg.DataDir) })
			waitForPolls(t, fake)
			if recorded {
				leadWrites(t, server, 1)
			}

			fake.RemoveLabel(shop, 41, "mobius:working", app)

			if recorded {
				testkit.WaitFor(t, func() bool { return taskState(t, server) == "stopped" })
				return
			}
			waitForPolls(t, fake)
			if state := taskState(t, server); state != "approval" {
				t.Errorf("state = %s", state)
			}
		})
	}
}

func TestACommentOfTheLeadAfterAnAnswerOfTheOwnerDoesNotHideTheAnswer(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			prepareAutopilot(fake, true)
			addTaskIssue(fake, 41, "Add plan model")
			addTaskIssue(fake, 42, "Add plan price")
			dataDir := t.TempDir()
			script := "[[prompts]]\nwhen = \"answer of #41\"\nreply = [\"Seen\"]\n\n[[prompts]]\nwhen = \"dispatch of #41\"\ncall = { tool = \"ask\", arguments = { repository = \"owner/shop\", n = 41, text = \"Cents or dollars?\" } }\n" +
				leadWriteScript("gh issue comment 41 -R owner/shop --body 'Looking into it.'") + "\n[[prompts]]\nreply = [\"Seen\"]\n"
			testkit.InstallFakeAgent(t, dataDir, options+script)
			stubGH(t, dataDir)
			cfg := testserver.Config(t, dataDir)
			cfg.Roles.Implementer.Max = 1
			keepSessionOpen(cfg)
			server := startServerWith(t, fake, cfg, "")
			liveTaskOf(t, server, 42)
			testkit.WaitFor(t, func() bool { return taskState(t, server) == "asked" })
			fake.AddComment(shop, 41, "owner", "Cents.")
			testkit.WaitFor(t, func() bool { return taskState(t, server) == "answered" })
			if recorded {
				leadWrites(t, server, 1)
			}
			fake.AddComment(shop, 41, app, "Looking into it.")
			waitForPolls(t, fake)

			fake.CloseIssue(shop, 42)

			prompt := waitForLeadPrompt(t, server, ` answer of #41 "Add plan model"`)
			if got := strings.Contains(prompt, ` reply on #41 "Add plan model" by @owner:`+"\n\n> Cents."); got != recorded {
				t.Errorf("the prompt has the answer = %t: %s", got, prompt)
			}
			testkit.WaitFor(t, func() bool { return taskState(t, server) == "dispatched" })
		})
	}
}

func TestACommentOfTheLeadThatTheSamePollReadsWithAnAnswerOfTheOwnerDoesNotHideTheAnswer(t *testing.T) {
	t.Parallel()
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%t", recorded), func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			prepareAutopilot(fake, true)
			addTaskIssue(fake, 41, "Add plan model")
			dataDir := t.TempDir()
			script := "[[prompts]]\nwhen = \"dispatch of #41\"\ncall = { tool = \"ask\", arguments = { repository = \"owner/shop\", n = 41, text = \"Cents or dollars?\" } }\n" +
				leadWriteScript("gh issue comment 41 -R owner/shop --body 'Looking into it.'") + "\n[[prompts]]\nreply = [\"Seen\"]\n"
			testkit.InstallFakeAgent(t, dataDir, options+script)
			stubGH(t, dataDir)
			cfg := testserver.Config(t, dataDir)
			cfg.Roles.Implementer.Max = 1
			keepSessionOpen(cfg)
			server := startServerWith(t, fake, cfg, "")
			testkit.WaitFor(t, func() bool { return taskState(t, server) == "asked" })
			if recorded {
				leadWrites(t, server, 1)
			}
			commentReads := func() int {
				reads := 0
				for _, request := range fake.Requests() {
					if strings.HasSuffix(request.Path, "/issues/comments") {
						reads++
					}
				}
				return reads
			}
			before := commentReads()
			release := fake.HoldNext("GET /repos/{owner}/{repo}/issues/comments")

			fake.AddComment(shop, 41, "owner", "Cents.")
			testkit.WaitFor(t, func() bool { return commentReads() > before })
			fake.AddComment(shop, 41, app, "Looking into it.")
			release()

			if recorded {
				testkit.WaitFor(t, func() bool { return taskState(t, server) != "asked" })
				return
			}
			waitForPolls(t, fake)
			if state := taskState(t, server); state != "asked" {
				t.Errorf("state = %s", state)
			}
		})
	}
}

func TestTheSecondLabelOfOneAddOfTheLeadHasTheEffectOfTheAddOfAPerson(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := connectLeadWrites(t, fake, "gh issue edit 12 -R owner/shop --add-label bug,mobius:autopilot")
	leadWrites(t, server, 1)

	fake.AddLabel(shop, 12, "bug", app)
	fake.AddLabel(shop, 12, "mobius:autopilot", app)

	testkit.WaitFor(t, func() bool {
		return workstreams(t, server)[0].Autopilot == "start" && len(autopilotEvents(t, server)) == 1
	})
}
