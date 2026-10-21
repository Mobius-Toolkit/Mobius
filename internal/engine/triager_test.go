package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const triager = `
[[prompts]]
when = "The Owner rejected"
reply = ["Understood."]

[[prompts]]
when = "and it failed"
reply = ["Seen the failure."]

[[prompts]]
when = "Keep triaging."
reply = ["The triage goes on."]

[[prompts]]
when = "Report of the Researcher"
reply = ["Thanks for the report."]

[[prompts]]
when = "Create it as proposed."
call = { tool = "create_workstream", arguments = { title = "Refunds", brief = "Refund each order." } }

[[prompts]]
when = "Send it to the Lead."
call = { tool = "message_lead", arguments = { workstream = 12, text = "Add refunds to the loyalty plans." } }

[[prompts]]
when = "Where do refunds end?"
busy = "1s"
reply = ["Refunds end in the ledger."]

[[prompts]]
when = "Where do refunds start?"
busy = "1s"
reply = ["Refunds start at the order."]

[[prompts]]
when = "# Question\n\nWhere do refunds stop?"
reply = ["LONG_REPORT"]

[[prompts]]
when = "You are a Researcher"
reply = ["Refunds live in the plan model."]

[[prompts]]
when = "# Issue\n\nowner/shop#60 "
list_tools = true

[[prompts]]
when = "# Issue\n\nowner/shop#61 "
reply = ["Proposal: the Workstream Refunds."]

[[prompts]]
when = "# Issue\n\nowner/shop#66 "
reply = ["Proposal: the Workstream Refunds."]

[[prompts]]
when = "# Issue\n\nowner/shop#68 "
reply = ["Proposal: the Workstream Refunds."]

[[prompts]]
when = "# Issue\n\nowner/shop#62 "
reply = ["Proposal: a message to the Lead of #12."]

[[prompts]]
when = "# Issue\n\nowner/shop#71 "
reply = ["Proposal: a message to the Lead of #12."]

[[prompts]]
when = "# Issue\n\nowner/shop#72 "
reply = ["Proposal: a message to the Lead of #12."]

[[prompts]]
when = "# Issue\n\nowner/shop#63 "
call = { tool = "start_researcher", arguments = { question = "Where do refunds live?" } }

[[prompts]]
when = "# Issue\n\nowner/shop#64 "
call = { tool = "start_researcher", arguments = { question = "Where do refunds end?" } }

[[prompts]]
when = "# Issue\n\nowner/shop#65 "
call = { tool = "start_researcher", arguments = { question = "Where do refunds start?" } }

[[prompts]]
when = "# Issue\n\nowner/shop#69 "
call = { tool = "start_researcher", arguments = { question = "Where do refunds start?" } }

[[prompts]]
when = "# Issue\n\nowner/shop#70 "
call = { tool = "start_researcher", arguments = { question = "Where do refunds stop?" } }

[[prompts]]
when = "Please use points."
reply = ["Move it to the loyalty Workstream."]

[[prompts]]
when = "Please cover refunds."
reply = ["Refunds are in the Workstream."]

[[prompts]]
when = "# Issue\n\nowner/shop#53 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\nowner/shop#54 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\nowner/shop#56 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\nowner/shop#57 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\nowner/shop#58 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\nowner/shop#59 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\nowner/shop#55 "
hang = true

[[prompts]]
when = "# Issue\n\nowner/shop#50 "
shell = "pwd"
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\nowner/shop#51 "
hang = true

[[prompts]]
when = "# Issue\n\nowner/shop#52 "
reply = ["Create the Workstream \"Loyalty points\" for this issue."]

[[prompts]]
when = "# Owner message\n\nStart a Workstream for loyalty points."
reply = ["Title: Loyalty points\n\nBrief: Give points for each order."]

[[prompts]]
when = "Yes, create it."
call = { tool = "create_workstream", arguments = { title = "Loyalty points", brief = "Give points for each order." } }
`

// coordinatorChat is the Coordinator chat of the organization owner.
var coordinatorChat = engine.ChatKey{Organization: "owner"}

// issueTriagers is the key of the sessions of the Triagers of the issues of owner/shop, and of the Researchers of the
// Coordinator chat.
var issueTriagers = engine.ChatKey{Organization: "owner", Repository: shop}

// connectTriager starts a server with the Workstream #12 and its Brief, and the Triager of triager.
func connectTriager(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string) {
	t.Helper()
	server, dataDir := connect(t, fake, triager)
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	waitForPoll(t, server, fake)
	return server, dataDir
}

func TestANewCoordinatorChatSessionStartsWhileGitHubRefusesEachRequest(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.SetRateLimited(true)

	sendChat(t, server, coordinatorChat, "Which Workstreams are open?")

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		for _, session := range chatSessions(t, server, coordinatorChat, engine.CoordinatorRole) {
			if prompts := promptTexts(t, server, session.ID); len(prompts) > 0 {
				return prompts[0], true
			}
		}
		return "", false
	})
	for _, part := range []string{
		"# Open Workstreams\n\n#12 Integrate loyalty plans (owner/shop)\n\nShip loyalty plans to all shops.\n",
		"# Owner message\n\nWhich Workstreams are open?",
	} {
		if !strings.Contains(prompt, part) {
			t.Errorf("%q not in %s", part, prompt)
		}
	}
}

func TestMobiusTriageOnAnOldIssueStartsTheTriager(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTriager(t, fake)
	fake.AddIssue(shop, 50, "Add loyalty points")
	fake.SetBody(shop, 50, "Points for each order.")

	fake.AddLabel(shop, 50, "mobius:triage", "owner")

	session := waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	if session.EndReason.String != "done" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	inOrder(t, prompts[0],
		"You are the Triager",
		"# Open Workstreams\n\n#12 Integrate loyalty plans (owner/shop)\n\nShip loyalty plans to all shops.\n",
		"# Issue\n\nowner/shop#50 Add loyalty points\n\nPoints for each order.",
	)
	id := strconv.FormatInt(session.ID, 10)
	if got := reply(t, server, session.ID); !strings.Contains(got, "/scratch/"+id+"\nexit 0") {
		t.Errorf("reply = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "scratch", id)); !os.IsNotExist(err) {
		t.Errorf("the scratch directory stays: %v", err)
	}
	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 1 {
		t.Errorf("sessions = %+v", got)
	}
}

func TestANewIssueOfATrustedUserGetsMobiusTriageAndOneTriagerThatReplies(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)

	fake.CreateIssue(shop, 52, "Add loyalty points", "Points for each order.", "owner")

	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: `Create the Workstream "Loyalty points" for this issue.`}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 52), proposal) })
	if got := fake.Labels(shop, 52); !slices.Equal(got, []string{"mobius:triage"}) {
		t.Errorf("labels = %v", got)
	}
	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 1 {
		t.Errorf("sessions = %+v", got)
	}
	if got := fake.Comments(shop, 52); len(got) != 1 {
		t.Errorf("comments = %v", got)
	}
}

// triagerTurns gives the issues of owner/shop with a Triager turn that did not end.
func triagerTurns(t *testing.T, server *testserver.Server) []int64 {
	t.Helper()
	numbers, err := store.New(server.DB).ListTriagerTurns(t.Context(), shop)
	if err != nil {
		t.Fatal(err)
	}
	return numbers
}

func TestARestartStartsAgainATriagerTurnThatDidNotEndAndTheTurnEndsAfterTheReply(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	seed(t, dataDir, "INSERT INTO triager_turns (repository, issue) VALUES ('owner/shop', 52)")
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 52, "Add loyalty points")
	fake.AddLabel(shop, 52, "mobius:triage", "owner")
	fake.AddIssue(shop, 60, "Later change")
	testkit.InstallFakeAgent(t, dataDir, options+triager)

	server := startServer(t, fake, dataDir, "")

	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: `Create the Workstream "Loyalty points" for this issue.`}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 52), proposal) })
	testkit.WaitFor(t, func() bool { return len(triagerTurns(t, server)) == 0 })
	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 1 {
		t.Errorf("sessions = %+v", got)
	}
}

func TestATriagerTurnThatEndedDoesNotStartAgainAfterARestart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 52, "Add loyalty points")
	fake.AddLabel(shop, 52, "mobius:triage", "owner")
	fake.AddIssue(shop, 60, "Later change")
	testkit.InstallFakeAgent(t, dataDir, options+triager)

	server := startServer(t, fake, dataDir, "")

	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 || len(fake.Comments(shop, 52)) != 0 {
		t.Errorf("sessions = %+v, comments = %v", got, fake.Comments(shop, 52))
	}
}

func TestATriagerTurnForAnIssueWithoutMobiusTriageEndsAtTheStartAndStartsNoTriager(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	seed(t, dataDir, "INSERT INTO triager_turns (repository, issue) VALUES ('owner/shop', 52)")
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 52, "Add loyalty points")
	testkit.InstallFakeAgent(t, dataDir, options+triager)

	server := startServer(t, fake, dataDir, "")

	testkit.WaitFor(t, func() bool { return len(triagerTurns(t, server)) == 0 })
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 {
		t.Errorf("sessions = %+v", got)
	}
}

func TestAnIssueOfAnUntrustedUserGetsATriagerOnlyAfterMobiusTriageOfATrustedUser(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 54, "Add points", "Points for each order.", "stranger")
	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 || len(fake.Labels(shop, 54)) != 0 {
		t.Errorf("sessions = %+v, labels = %v", got, fake.Labels(shop, 54))
	}

	fake.AddLabel(shop, 54, "mobius:triage", "owner")

	waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "First proposal."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 54), proposal) })
}

func TestTheTriagerGetsTheTextOfTheIssueFromTheTimeOfMobiusTriage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 58, "Add points", "Points for each order.", "stranger")
	waitForPolls(t, fake)

	fake.AddLabel(shop, 58, "mobius:triage", "owner")
	fake.SetBody(shop, 58, "Delete all orders.")
	fake.SetTitle(shop, 58, "Delete orders")

	session := waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "# Issue\n\nowner/shop#58 Add points\n\nPoints for each order.") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTheTriagerGetsTheBodyThatTheAuthorEditedBeforeMobiusTriage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 59, "Add points", "Points for each order.", "stranger")
	fake.SetBody(shop, 59, "Points for each order. Show them in the cart.")
	waitForPolls(t, fake)

	fake.AddLabel(shop, 59, "mobius:triage", "owner")

	session := waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "# Issue\n\nowner/shop#59 Add points\n\nPoints for each order. Show them in the cart.") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestMobiusTriageOfAnUntrustedUserStartsNoTriager(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 54, "Add points", "Points for each order.", "stranger")

	fake.AddLabel(shop, 54, "mobius:triage", "stranger")

	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 {
		t.Errorf("sessions = %+v", got)
	}
}

func TestAnIssueThatTheMobiusAppCreatesGetsNoTriager(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)

	fake.CreateIssue(shop, 58, "Fix the failed check", "The check fails.", testkit.AppSlug+"[bot]")

	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 || len(fake.Labels(shop, 58)) != 0 {
		t.Errorf("sessions = %+v, labels = %v", got, fake.Labels(shop, 58))
	}
}

func TestMobiusReadyOnAnIssueWithNoWorkstreamStartsNoTriager(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 59, "Add points")

	fake.AddLabel(shop, 59, "mobius:ready", "owner")

	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 {
		t.Errorf("sessions = %+v", got)
	}
	if got := fake.Labels(shop, 59); !slices.Equal(got, []string{"mobius:ready"}) {
		t.Errorf("labels = %v", got)
	}
}

func TestARemovalOfTheLabelStopsTheTriagerAndAProposalGoesToTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 51, "Rename plans")
	fake.AddIssue(shop, 52, "Add loyalty points")
	fake.AddLabel(shop, 51, "mobius:triage", "owner")
	fake.AddLabel(shop, 52, "mobius:triage", "owner")
	testkit.WaitFor(t, func() bool {
		return len(chatSessions(t, server, issueTriagers, engine.TriagerRole)) == 2 && slices.Equal(fake.Labels(shop, 51), []string{"mobius:triage"})
	})

	fake.RemoveLabel(shop, 51, "mobius:triage", "owner")

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions, !slices.ContainsFunc(sessions, func(session store.Session) bool { return !session.EndedAt.Valid })
	})
	var reasons []string
	for _, session := range sessions {
		reasons = append(reasons, session.EndReason.String)
	}
	slices.Sort(reasons)
	if !slices.Equal(reasons, []string{"done", "stopped"}) {
		t.Errorf("end reasons = %v", reasons)
	}
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: `Create the Workstream "Loyalty points" for this issue.`}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 52), proposal) })
	if got := fake.Comments(shop, 51); len(got) != 0 {
		t.Errorf("comments of #51 = %v", got)
	}
	testkit.WaitFor(t, func() bool { return len(triagerTurns(t, server)) == 0 })
}

func TestAProposalThatGitHubRefusesGoesToTheIssueAtTheNextPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 51, "Rename plans")
	fake.AddIssue(shop, 52, "Add loyalty points")
	fake.FailAddComment(shop, 52, true)
	fake.AddLabel(shop, 51, "mobius:triage", "owner")
	fake.AddLabel(shop, 52, "mobius:triage", "owner")
	testkit.WaitFor(t, func() bool {
		return len(chatSessions(t, server, issueTriagers, engine.TriagerRole)) == 2 && slices.Equal(fake.Labels(shop, 51), []string{"mobius:triage"})
	})

	fake.RemoveLabel(shop, 51, "mobius:triage", "owner")

	testkit.WaitFor(t, func() bool { return commentWrites(t, server) == 1 })
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: `Create the Workstream "Loyalty points" for this issue.`}
	if slices.Contains(fake.Comments(shop, 52), proposal) {
		t.Error("the proposal is on the issue")
	}

	fake.FailAddComment(shop, 52, false)

	testkit.WaitFor(t, func() bool {
		return slices.Contains(fake.Comments(shop, 52), proposal) && commentWrites(t, server) == 0
	})
}

func TestACommentOfATrustedUserStartsTheTriagerAgainWithTheCommentsOfTrustedAuthors(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 53, "Add points")
	fake.AddComment(shop, 53, "stranger", "I want this too.")
	fake.AddLabel(shop, 53, "mobius:triage", "owner")
	waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	testkit.WaitFor(t, func() bool { return len(fake.Comments(shop, 53)) == 2 })

	fake.AddComment(shop, 53, "owner", "Please use points.")

	second := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions[len(sessions)-1], len(sessions) == 2 && sessions[1].EndedAt.Valid
	})
	prompts := promptTexts(t, server, second.ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	inOrder(t, prompts[0],
		"# Issue\n\nowner/shop#53 Add points",
		"# Comments\n\n@mobius-test[bot], ",
		":\nFirst proposal.\n\n@owner, ",
		":\nPlease use points.\n",
	)
	if strings.Contains(prompts[0], "I want this too.") {
		t.Errorf("prompt = %q", prompts[0])
	}
	if got := fake.Labels(shop, 53); !slices.Equal(got, []string{"mobius:triage"}) {
		t.Errorf("labels = %v", got)
	}
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "Move it to the loyalty Workstream."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 53), proposal) })
}

func TestACommentOfTheAppOrOfAnUntrustedUserDoesNotStartTheTriagerAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 54, "Add points")
	fake.AddLabel(shop, 54, "mobius:triage", "owner")
	waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	testkit.WaitFor(t, func() bool { return len(fake.Comments(shop, 54)) == 1 })

	fake.AddComment(shop, 54, "stranger", "Please use points.")
	fake.AddAppComment(shop, 54, "owner", "Please use points.")

	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 1 {
		t.Errorf("sessions = %+v", got)
	}
}

func TestACommentThatTheDrainHeldStartsTheTriagerAgainAfterACancel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 56, "Add points")
	fake.AddLabel(shop, 56, "mobius:triage", "owner")
	waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	testkit.WaitFor(t, func() bool { return len(fake.Comments(shop, 56)) == 1 })
	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}

	fake.AddComment(shop, 56, "owner", "Please use points.")

	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 1 {
		t.Errorf("sessions = %+v", got)
	}

	cancelDrain(t, server)

	second := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions[len(sessions)-1], len(sessions) == 2 && sessions[1].EndedAt.Valid
	})
	if prompts := promptTexts(t, server, second.ID); len(prompts) != 1 || !strings.Contains(prompts[0], ":\nPlease use points.\n") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestACommentWhileTheTriagerRunsStopsItAndStartsANewRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 55, "Add refunds")
	fake.AddLabel(shop, 55, "mobius:triage", "owner")
	waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return len(promptTexts(t, server, session.ID)) == 1 })

	fake.AddComment(shop, 55, "owner", "Please cover refunds.")

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions, len(sessions) == 2 && sessions[0].EndedAt.Valid && sessions[1].EndedAt.Valid
	})
	if sessions[0].EndReason.String != "stopped" || sessions[1].EndReason.String != "done" {
		t.Errorf("end reasons = %s, %s", sessions[0].EndReason.String, sessions[1].EndReason.String)
	}
	if prompts := promptTexts(t, server, sessions[1].ID); len(prompts) != 1 || !strings.Contains(prompts[0], ":\nPlease cover refunds.\n") {
		t.Errorf("prompts = %q", prompts)
	}
	if prompts := promptTexts(t, server, sessions[0].ID); len(prompts) != 1 || strings.Contains(prompts[0], "Please cover refunds.") {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTheTriagerHasNoMoveIssueTool(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)

	fake.CreateIssue(shop, 60, "Add refunds", "Refund each order.", "owner")

	session := waitForChatSession(t, server, issueTriagers, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	if got := toolNames(t, reply(t, server, session.ID)); !slices.Equal(got, []string{"create_workstream", "message_lead", "start_researcher"}) {
		t.Errorf("tools = %q", got)
	}
}

func TestTheTriagerCreatesAWorkstreamOnlyAfterTheOwnerApprovesInTheInbox(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 61, "Add refunds", "Refund each order.", "owner")
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "Proposal: the Workstream Refunds."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 61), proposal) })

	fake.AddComment(shop, 61, "owner", "Create it as proposed.")

	action := waitForActions(t, server, 1)[0]
	second := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions[len(sessions)-1], len(sessions) == 2 && sessions[1].EndedAt.Valid
	})
	waitForPolls(t, fake)
	if fake.HasIssue(shop, 62) {
		t.Error("the Workstream exists")
	}
	if state, _ := fake.State(shop, 61); state != "open" {
		t.Errorf("state = %s", state)
	}
	items := itemsOf(t, server, "approval")
	if len(items) != 1 || items[0].Title != `Create the Workstream "Refunds"` || items[0].Link != "https://github.com/owner/shop/issues/61" ||
		items[0].Action == nil || items[0].Action.ID != action.ID {
		t.Errorf("items = %+v", items)
	}
	if got := reply(t, server, second.ID); !strings.HasPrefix(got, actionWaits) {
		t.Errorf("reply = %q", got)
	}

	decided(t, server, action.ID, "approve", "")

	if title, body := fake.Issue(shop, 62); title != "Refunds" || body != "Refund each order." {
		t.Errorf("issue = %s, %s", title, body)
	}
	if got := fake.Labels(shop, 62); !slices.Equal(got, []string{"mobius:workstream"}) {
		t.Errorf("labels = %v", got)
	}
	link := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "The triage ends. The Workstream is https://github.com/owner/shop/issues/62"}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 61), link) })
	if state, reason := fake.State(shop, 61); state != "closed" || reason != "completed" {
		t.Errorf("state = %s, %s", state, reason)
	}
	waitForPolls(t, fake)
	if got := fake.Comments(shop, 61); len(got) != 4 {
		t.Errorf("comments = %v", got)
	}
	if sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(sessions) != 2 {
		t.Errorf("sessions = %d", len(sessions))
	}
}

func TestAnApprovalThatFailsAtTheCloseOfTheIssueCommentsAndRunsAgainWithoutANewWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 66, "Add refunds", "Refund each order.", "owner")
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "Proposal: the Workstream Refunds."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 66), proposal) })
	fake.AddComment(shop, 66, "owner", "Create it as proposed.")
	action := waitForActions(t, server, 1)[0]
	fake.FailClose(shop, 66)

	failed := decided(t, server, action.ID, "approve", "")

	if failed.State != "pending" || !strings.Contains(failed.Error, "GitHub created #67") {
		t.Errorf("action = %+v", failed)
	}
	failure := testkit.WaitForValue(t, func() (testkit.Comment, bool) {
		for _, comment := range fake.Comments(shop, 66) {
			if strings.HasPrefix(comment.Body, "The Owner approved `create_workstream`, and it failed") {
				return comment, true
			}
		}
		return testkit.Comment{}, false
	})
	if failure.Author != testkit.AppSlug+"[bot]" {
		t.Errorf("comment = %v", failure)
	}
	testkit.WaitFor(t, func() bool {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return len(sessions) == 3 && sessions[2].EndedAt.Valid
	})
	fake.AllowClose(shop, 66)

	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" {
		t.Errorf("action = %+v", done)
	}
	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 66); return state == "closed" })
	if fake.HasIssue(shop, 68) {
		t.Error("a second Workstream exists")
	}
}

func TestAnIssueThatTheOwnerReopensAfterTheEndOfTheTriageGetsANewTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 68, "Add refunds", "Refund each order.", "owner")
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "Proposal: the Workstream Refunds."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 68), proposal) })
	fake.AddComment(shop, 68, "owner", "Create it as proposed.")
	decided(t, server, waitForActions(t, server, 1)[0].ID, "approve", "")
	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 68); return state == "closed" })
	testkit.WaitFor(t, func() bool {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return len(sessions) == 2 && sessions[1].EndedAt.Valid
	})

	fake.ReopenIssue(shop, 68)
	fake.AddComment(shop, 68, "owner", "Keep triaging.")

	answer := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "The triage goes on."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 68), answer) })
	if state, _ := fake.State(shop, 68); state != "open" {
		t.Errorf("state = %s", state)
	}
}

func TestTheOwnerRejectsAMessageOfTheTriagerAndTheIssueGetsTheMessageAndANewTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 71, "Add refunds", "Refund each order.", "owner")
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "Proposal: a message to the Lead of #12."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 71), proposal) })
	fake.AddComment(shop, 71, "owner", "Send it to the Lead.")
	action := waitForActions(t, server, 1)[0]

	rejected := decided(t, server, action.ID, "reject", "Ask me again tomorrow.")

	if rejected.State != "rejected" || rejected.Message != "Ask me again tomorrow." {
		t.Errorf("action = %+v", rejected)
	}
	notice := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "The Owner rejected `message_lead`.\n\nThe message of the Owner: Ask me again tomorrow."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 71), notice) })
	third := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions[len(sessions)-1], len(sessions) == 3 && sessions[2].EndedAt.Valid
	})
	prompts := promptTexts(t, server, third.ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], notice.Body) {
		t.Errorf("prompts = %q", prompts)
	}
	waitForPolls(t, fake)
	if events := leadEvents(t, server); len(events) != 0 {
		t.Errorf("events = %v", events)
	}
	if state, _ := fake.State(shop, 71); state != "open" {
		t.Errorf("state = %s", state)
	}
}

func TestTheOwnerApprovesAMessageOfTheTriagerAfterItsSessionEndedAndTheLeadGetsTheLinkToTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 62, "Add refunds", "Refund each order.", "owner")
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "Proposal: a message to the Lead of #12."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 62), proposal) })
	fake.AddComment(shop, 62, "owner", "Send it to the Lead.")
	action := waitForActions(t, server, 1)[0]
	testkit.WaitFor(t, func() bool {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return len(sessions) == 2 && sessions[1].EndedAt.Valid
	})
	waitForPolls(t, fake)
	if len(leadEvents(t, server)) != 0 {
		t.Errorf("events = %v", leadEvents(t, server))
	}

	decided(t, server, action.ID, "approve", "")

	link := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "The triage ends. The Workstream is https://github.com/owner/shop/issues/12"}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 62), link) })
	if state, reason := fake.State(shop, 62); state != "closed" || reason != "completed" {
		t.Errorf("state = %s, %s", state, reason)
	}
	text := "Message of the Triager, approved by the Owner:\n\nAdd refunds to the loyalty plans.\n\nhttps://github.com/owner/shop/issues/62"
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind == "triager" && event.Delivered })
	})
	if events := leadEvents(t, server); !slices.ContainsFunc(events, func(event leadEvent) bool {
		return event.Workstream == 12 && event.Kind == "triager" && event.Payload == text
	}) {
		t.Errorf("events = %v", events)
	}
	waitForPolls(t, fake)
	if got := fake.Comments(shop, 62); len(got) != 4 {
		t.Errorf("comments = %v", got)
	}
	if sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(sessions) != 2 {
		t.Errorf("sessions = %d", len(sessions))
	}
}

func TestAnApprovedMessageOfTheTriagerThatFailsAtTheCloseOfTheIssueSendsTheLeadOneEventWhenItRunsAgain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 72, "Add refunds", "Refund each order.", "owner")
	proposal := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "Proposal: a message to the Lead of #12."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 72), proposal) })
	fake.AddComment(shop, 72, "owner", "Send it to the Lead.")
	action := waitForActions(t, server, 1)[0]
	fake.FailClose(shop, 72)

	failed := decided(t, server, action.ID, "approve", "")

	if failed.State != "pending" || !strings.Contains(failed.Error, "the Lead of #12 got the message") {
		t.Errorf("action = %+v", failed)
	}
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(fake.Comments(shop, 72), func(comment testkit.Comment) bool {
			return strings.HasPrefix(comment.Body, "The Owner approved `message_lead`, and it failed")
		})
	})
	testkit.WaitFor(t, func() bool {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return len(sessions) == 3 && sessions[2].EndedAt.Valid
	})
	fake.AllowClose(shop, 72)

	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" {
		t.Errorf("action = %+v", done)
	}
	testkit.WaitFor(t, func() bool { state, _ := fake.State(shop, 72); return state == "closed" })
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind == "triager" && event.Delivered })
	})
	waitForPolls(t, fake)
	if events := leadEvents(t, server); len(events) != 1 {
		t.Errorf("events = %v", events)
	}
}

func TestTheReportOfAResearcherOfTheTriagerIsACommentThatStartsTheNextTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)

	fake.CreateIssue(shop, 63, "Add refunds", "Refund each order.", "owner")

	researcher := waitForChatSession(t, server, issueTriagers, engine.ResearcherRole, func(session store.Session) bool { return session.EndedAt.Valid })
	if !researcher.Issue.Valid || researcher.Issue.Int64 != 63 {
		t.Errorf("issue = %+v", researcher.Issue)
	}
	report := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: fmt.Sprintf("Report of the Researcher %d on \"Where do refunds live?\":\n\nRefunds live in the plan model.", researcher.ID)}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 63), report) })
	second := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions[len(sessions)-1], len(sessions) == 2 && sessions[1].EndedAt.Valid
	})
	prompts := promptTexts(t, server, second.ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], report.Body) {
		t.Errorf("prompts = %q", prompts)
	}
	thanks := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: "Thanks for the report."}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 63), thanks) })
}

// Serial: the issue must close before the busy Researcher reports.
func TestTheReportOfAResearcherAfterTheEndOfTheTriageStartsNoTurn(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 64, "Add refunds", "Refund each order.", "owner")
	researcher := waitForChatSession(t, server, issueTriagers, engine.ResearcherRole, func(store.Session) bool { return true })

	fake.CloseIssue(shop, 64)

	waitForChatSession(t, server, issueTriagers, engine.ResearcherRole, func(session store.Session) bool { return session.ID == researcher.ID && session.EndedAt.Valid })
	waitForPolls(t, fake)
	for _, comment := range fake.Comments(shop, 64) {
		if strings.HasPrefix(comment.Body, "Report of the Researcher") {
			t.Errorf("comment = %v", comment)
		}
	}
	if sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(sessions) != 1 {
		t.Errorf("sessions = %d", len(sessions))
	}
}

// Serial: the read of the issue must fail while the busy Researcher still works.
func TestTheReportOfAResearcherArrivesWhenTheReadOfTheIssueFails(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.CreateIssue(shop, 65, "Add refunds", "Refund each order.", "owner")
	researcher := waitForChatSession(t, server, issueTriagers, engine.ResearcherRole, func(store.Session) bool { return true })
	testkit.WaitFor(t, func() bool {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return len(sessions) == 1 && sessions[0].EndedAt.Valid
	})

	fake.FailIssueReads(shop, 65, 1)

	report := testkit.Comment{Author: testkit.AppSlug + "[bot]", Body: fmt.Sprintf("Report of the Researcher %d on \"Where do refunds start?\":\n\nRefunds start at the order.", researcher.ID)}
	testkit.WaitFor(t, func() bool { return slices.Contains(fake.Comments(shop, 65), report) })
	testkit.WaitFor(t, func() bool { return len(chatSessions(t, server, issueTriagers, engine.TriagerRole)) == 2 })
}

func TestTheReportOfAResearcherArrivesBeforeTheNextTurnWhenTheFirstCommentFails(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.FailAddComment(shop, 69, true)
	fake.CreateIssue(shop, 69, "Add refunds", "Refund each order.", "owner")
	researcher := waitForChatSession(t, server, issueTriagers, engine.ResearcherRole, func(store.Session) bool { return true })

	waitForChatSession(t, server, issueTriagers, engine.ResearcherRole, func(session store.Session) bool { return session.ID == researcher.ID && session.EndedAt.Valid })
	testkit.WaitFor(t, func() bool { return commentWrites(t, server) > 0 })
	waitForPolls(t, fake)
	if sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(sessions) != 1 {
		t.Errorf("sessions = %d", len(sessions))
	}
	fake.FailAddComment(shop, 69, false)

	sessions := testkit.WaitForValue(t, func() ([]store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions, len(sessions) == 2 && sessions[1].EndedAt.Valid
	})
	report := fmt.Sprintf("Report of the Researcher %d on \"Where do refunds start?\":\n\nRefunds start at the order.", researcher.ID)
	if prompts := promptTexts(t, server, sessions[1].ID); len(prompts) != 1 || !strings.Contains(prompts[0], report) {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTheReportOfAResearcherThatIsTooLongForACommentArrivesCutAndStartsTheNextTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, strings.Replace(triager, "LONG_REPORT", "first"+strings.Repeat("a", 70_000)+"last", 1))
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	waitForPoll(t, server, fake)

	fake.CreateIssue(shop, 70, "Add refunds", "Refund each order.", "owner")

	researcher := waitForChatSession(t, server, issueTriagers, engine.ResearcherRole, func(session store.Session) bool { return session.EndedAt.Valid })
	header := fmt.Sprintf("Report of the Researcher %d on \"Where do refunds stop?\":\n\n", researcher.ID)
	report := testkit.WaitForValue(t, func() (string, bool) {
		for _, comment := range fake.Comments(shop, 70) {
			if strings.HasPrefix(comment.Body, header) {
				return comment.Body, true
			}
		}
		return "", false
	})
	if count := utf8.RuneCountInString(report); count != 65_535 || !strings.HasSuffix(report, "aaalast") || strings.Contains(report, "first") {
		t.Errorf("report = %d characters, ends with %q", count, report[len(report)-7:])
	}
	second := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.TriagerRole)
		return sessions[len(sessions)-1], len(sessions) == 2 && sessions[1].EndedAt.Valid
	})
	if prompts := promptTexts(t, server, second.ID); len(prompts) != 1 || !strings.Contains(prompts[0], report) {
		t.Errorf("the prompt has no report")
	}
}

func TestTheCoordinatorChatCreatesAWorkstreamAfterTheApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, approvalSeen+triager)
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	waitForPoll(t, server, fake)
	changes := listen(t, server)

	sendChat(t, server, coordinatorChat, "Start a Workstream for loyalty points.")
	waitForChat(t, server, coordinatorChat, "Coordinator", "Title: Loyalty points\n\nBrief: Give points for each order.")
	sendChat(t, server, coordinatorChat, "Yes, create it.")
	approveNext(t, server, 1)

	created := waitForChange(t, changes, func(change engine.Change) bool { return change.Created != nil }).Created
	if *created != (engine.Created{Repository: shop, Number: 13}) {
		t.Errorf("created = %+v", created)
	}
	if title, body := fake.Issue(shop, 13); title != "Loyalty points" || body != "Give points for each order." {
		t.Errorf("issue = %s, %s", title, body)
	}
	if got := fake.Labels(shop, 13); !slices.Equal(got, []string{"mobius:workstream"}) {
		t.Errorf("labels = %v", got)
	}
	prompts := testkit.WaitForValue(t, func() ([]string, bool) {
		var prompts []string
		for _, session := range chatSessions(t, server, coordinatorChat, engine.CoordinatorRole) {
			prompts = append(prompts, promptTexts(t, server, session.ID)...)
		}
		return prompts, len(prompts) == 3
	})
	for _, part := range []string{
		"You are the Coordinator",
		"When the Owner pastes a text with no issue number, treat it as a request of the Owner, not as an issue.",
		"# Open Workstreams\n\n#12 Integrate loyalty plans (owner/shop)\n\nShip loyalty plans to all shops.\n",
		"# Owner message\n\nStart a Workstream for loyalty points.",
	} {
		if !strings.Contains(prompts[0], part) {
			t.Errorf("%q not in %s", part, prompts[0])
		}
	}
	if !strings.Contains(prompts[1], "Yes, create it.") {
		t.Errorf("prompt = %s", prompts[1])
	}
}

func TestTheCoordinatorChatRefusesAnUnknownOrganization(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	unknown := engine.ChatKey{}

	err := server.Engine.SendChat(t.Context(), unknown, "", "Start a Workstream for loyalty points.", nil, nil)

	if !engine.Refused(err) || err.Error() != `Mobius has no repository in the organization "".` {
		t.Errorf("err = %v", err)
	}
	if got := chatView(t, server, unknown).Messages; len(got) != 0 {
		t.Errorf("messages = %+v", got)
	}
	if got := chatSessions(t, server, unknown, engine.TriagerRole); len(got) != 0 {
		t.Errorf("sessions = %+v", got)
	}
}

func TestANewCoordinatorChatSessionGetsTheChatHistory(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	sendChat(t, server, coordinatorChat, "Start a Workstream for loyalty points.")
	waitForChatSession(t, server, coordinatorChat, engine.CoordinatorRole, func(session store.Session) bool { return session.EndReason.String == "idle" })

	sendChat(t, server, coordinatorChat, "Yes, create it.")

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		sessions := chatSessions(t, server, coordinatorChat, engine.CoordinatorRole)
		if len(sessions) < 2 {
			return "", false
		}
		prompts := promptTexts(t, server, sessions[1].ID)
		return strings.Join(prompts, ""), len(prompts) > 0
	})
	_, history, _ := strings.Cut(prompt, "# Chat history\n\n")
	for _, part := range []string{"):\nStart a Workstream for loyalty points.\n\n", "):\nTitle: Loyalty points\n\nBrief: Give points for each order.\n\n"} {
		if !strings.Contains(history, part) {
			t.Errorf("%q not in %s", part, history)
		}
	}
	if !strings.HasSuffix(history, "# Owner message\n\nYes, create it.") {
		t.Errorf("history = %s", history)
	}
}

// researchCoordinator is a Coordinator chat that starts a Researcher for a message of the Owner, and answers its report. The
// first prompt of a new chat session also has the Role prompt, so the report prompt comes first in the script.
const researchCoordinator = `
[[prompts]]
when = "# Researcher message"
reply = ["I have the report."]

[[prompts]]
when = "# Owner message"
call = { tool = "start_researcher", arguments = { repository = "owner/shop", question = "Where do plans store the price?" } }
`

// coordinatorPrompts gives the prompts of the Coordinator chat sessions, the oldest first.
func coordinatorPrompts(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var texts []string
	for _, session := range chatSessions(t, server, coordinatorChat, engine.CoordinatorRole) {
		texts = append(texts, promptTexts(t, server, session.ID)...)
	}
	return texts
}

func TestAReportOfTheResearcherGoesToTheCoordinatorChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearch(t, fake, researchCoordinator)

	sendChat(t, server, coordinatorChat, "Where do plans store the price?")

	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.ResearcherRole)
		if len(sessions) == 0 {
			return store.Session{}, false
		}
		return sessions[0], sessions[0].EndedAt.Valid
	})
	if session.EndReason.String != "done" || session.Parent.Int64 != chatSessions(t, server, coordinatorChat, engine.CoordinatorRole)[0].ID {
		t.Errorf("session = %+v", session)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	if !strings.Contains(prompts[0], "# Question\n\n"+question) || strings.Contains(prompts[0], "# Brief") {
		t.Errorf("prompt = %s", prompts[0])
	}
	waitForChat(t, server, coordinatorChat, "Coordinator", "I have the report.")
	report := fmt.Sprintf("# Researcher message\n\nReport of the Researcher %d on \"%s\":\n\n%s", session.ID, question, reply(t, server, session.ID))
	if prompts := coordinatorPrompts(t, server); !slices.ContainsFunc(prompts, func(prompt string) bool { return strings.Contains(prompt, report) }) {
		t.Errorf("prompts = %q", prompts)
	}
	messages := chatView(t, server, coordinatorChat).Messages
	if slices.ContainsFunc(messages, func(message store.ChatMessage) bool { return message.Author == "Researcher" }) {
		t.Errorf("messages = %+v", messages)
	}
}

func TestAReportOfTheResearcherStartsANewCoordinatorChatSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	gate := filepath.Join(t.TempDir(), "gate")
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+researchCoordinator)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+`
[[prompts]]
when = "You are a Researcher"
reply = ["Plans store the price in cents.\n"]
shell = "while [ ! -e `+gate+` ]; do sleep 0.05; done"
`)
	server := startServerWith(t, fake, testserver.Config(t, dataDir), "")
	server.WaitForFirstPoll(t, shop)
	sendChat(t, server, coordinatorChat, "Where do plans store the price?")
	first := waitForChatSession(t, server, coordinatorChat, engine.CoordinatorRole, func(session store.Session) bool { return session.EndedAt.Valid })
	if first.EndReason.String != "idle" {
		t.Fatalf("end reason = %s", first.EndReason.String)
	}

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	second := waitForChatSession(t, server, coordinatorChat, engine.CoordinatorRole, func(session store.Session) bool { return session.ID != first.ID })
	prompt := testkit.WaitForValue(t, func() (string, bool) {
		prompts := promptTexts(t, server, second.ID)
		return strings.Join(prompts, ""), len(prompts) > 0
	})
	researcher := chatSessions(t, server, issueTriagers, engine.ResearcherRole)[0]
	if !strings.Contains(prompt, fmt.Sprintf("# Researcher message\n\nReport of the Researcher %d on \"%s\"", researcher.ID, question)) || strings.Contains(prompt, "# Owner message\n\nReport") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestACoordinatorChatSessionGetsTheImagesOfTheMessageAndATextMarkerForOlderImages(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, imageReading+options+"[[prompts]]\nwhen = \"Now plan it.\"\nreply = [\"Second answer\"]\n\n[[prompts]]\nreply = [\"First answer\"]\n", func(*config.Config) {})
	sendChatImages(t, server, coordinatorChat, "Look at this", []engine.Image{pngImage, jpegImage})
	first := waitForChatSession(t, server, coordinatorChat, engine.CoordinatorRole, func(session store.Session) bool { return session.EndReason.String == "idle" })
	if got, want := reply(t, server, first.ID), "First answer"+imageReply(pngImage)+imageReply(jpegImage); got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}

	sendChat(t, server, coordinatorChat, "Now plan it.")

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		sessions := chatSessions(t, server, coordinatorChat, engine.CoordinatorRole)
		if len(sessions) < 2 {
			return "", false
		}
		prompts := promptTexts(t, server, sessions[1].ID)
		return strings.Join(prompts, ""), len(prompts) > 0
	})
	if !strings.Contains(prompt, "):\nLook at this\n[2 images]\n\n") {
		t.Errorf("prompt = %s", prompt)
	}
	second := chatSessions(t, server, coordinatorChat, engine.CoordinatorRole)[1]
	testkit.WaitFor(t, func() bool { return reply(t, server, second.ID) == "Second answer" })
}
