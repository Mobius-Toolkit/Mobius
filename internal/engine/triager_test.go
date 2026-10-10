package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const triager = `
[[prompts]]
when = "Please use points."
reply = ["Move it to the loyalty Workstream."]

[[prompts]]
when = "Please cover refunds."
reply = ["Refunds are in the Workstream."]

[[prompts]]
when = "# Issue\n\n#53 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\n#54 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\n#56 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\n#57 "
reply = ["First proposal."]

[[prompts]]
when = "# Issue\n\n#55 "
hang = true

[[prompts]]
when = "# Issue\n\n#50 "
shell = "pwd"
call = { tool = "move_issue", arguments = { n = 50, workstream = 12 } }

[[prompts]]
when = "# Issue\n\n#51 "
hang = true

[[prompts]]
when = "# Issue\n\n#52 "
reply = ["Create the Workstream \"Loyalty points\" for this issue."]

[[prompts]]
when = "# Owner message\n\nStart a Workstream for loyalty points."
reply = ["Title: Loyalty points\n\nBrief: Give points for each order."]

[[prompts]]
when = "Yes, create it."
call = { tool = "create_workstream", arguments = { title = "Loyalty points", brief = "Give points for each order." } }
`

// triagerChat is the Triager chat of the organization owner.
var triagerChat = engine.ChatKey{Organization: "owner"}

// issueTriagers is the key of the sessions of the Triagers of the issues of owner/shop, and of the Researchers of the
// Triager chat.
var issueTriagers = engine.ChatKey{Organization: "owner", Repository: shop}

// connectTriager starts a server with the Workstream #12 and its Brief, and the Triager of triager.
func connectTriager(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string) {
	t.Helper()
	server, dataDir := connect(t, fake, triager)
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	return server, dataDir
}

func TestAnIssueWithNoWorkstreamGoesToTheTriagerThatMovesIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTriager(t, fake)
	fake.AddIssue(shop, 50, "Add loyalty points")
	fake.SetBody(shop, 50, "Points for each order.")

	fake.AddLabel(shop, 50, "mobius:ready", "owner")

	if task := liveTaskOf(t, server, 50); task.Workstream != 12 {
		t.Errorf("task = %+v", task)
	}
	if got := fake.SubIssueNumbers(shop, 12); !slices.Equal(got, []int64{50}) {
		t.Errorf("sub-issues = %v", got)
	}
	// Mobius adds the activity after the task.
	dispatched := testkit.WaitForValue(t, func() (activity, bool) {
		for _, a := range activities(t, server) {
			if a.Text == `Dispatched "Add loyalty points"` {
				return a, true
			}
		}
		return activity{}, false
	})
	if dispatched.Actor != "owner" {
		t.Errorf("actor = %s", dispatched.Actor)
	}
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
		"# Issue\n\n#50 Add loyalty points\n\nPoints for each order.",
	)
	id := strconv.FormatInt(session.ID, 10)
	if got := reply(t, server, session.ID); !strings.Contains(got, "/scratch/"+id+"\nexit 0") {
		t.Errorf("reply = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "scratch", id)); !os.IsNotExist(err) {
		t.Errorf("the scratch directory stays: %v", err)
	}
	if slices.Contains(fake.Labels(shop, 50), "mobius:no-workstream") {
		t.Errorf("labels = %v", fake.Labels(shop, 50))
	}
}

func TestARemovalOfTheLabelStopsTheTriagerAndAProposalGoesToTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 51, "Rename plans")
	fake.AddIssue(shop, 52, "Add loyalty points")
	fake.AddLabel(shop, 51, "mobius:ready", "owner")
	fake.AddLabel(shop, 52, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		return len(chatSessions(t, server, issueTriagers, engine.TriagerRole)) == 2 && slices.Equal(fake.Labels(shop, 51), []string{"mobius:no-workstream"})
	})

	fake.RemoveLabel(shop, 51, "mobius:no-workstream", "owner")

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
}

func TestACommentOfATrustedUserStartsTheTriagerAgainWithTheCommentsOfTrustedAuthors(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	fake.AddIssue(shop, 53, "Add points")
	fake.AddComment(shop, 53, "stranger", "I want this too.")
	fake.AddLabel(shop, 53, "mobius:ready", "owner")
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
		"# Issue\n\n#53 Add points",
		"# Comments\n\n@mobius-test[bot], ",
		":\nFirst proposal.\n\n@owner, ",
		":\nPlease use points.\n",
	)
	if strings.Contains(prompts[0], "I want this too.") {
		t.Errorf("prompt = %q", prompts[0])
	}
	if got := fake.Labels(shop, 53); !slices.Equal(got, []string{"mobius:no-workstream"}) {
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
	fake.AddLabel(shop, 54, "mobius:ready", "owner")
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
	fake.AddLabel(shop, 56, "mobius:ready", "owner")
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
	fake.AddLabel(shop, 55, "mobius:ready", "owner")
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

func TestTheTriagerChatCreatesAWorkstreamAfterTheApproval(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	changes := listen(t, server)

	sendChat(t, server, triagerChat, "Start a Workstream for loyalty points.")
	waitForChat(t, server, triagerChat, "Triager", "Title: Loyalty points\n\nBrief: Give points for each order.")
	sendChat(t, server, triagerChat, "Yes, create it.")

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
		for _, session := range chatSessions(t, server, triagerChat, engine.TriagerRole) {
			prompts = append(prompts, promptTexts(t, server, session.ID)...)
		}
		return prompts, len(prompts) == 2
	})
	for _, part := range []string{
		"You are the Triager",
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

func TestTheTriagerChatRefusesAnUnknownOrganization(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	unknown := engine.ChatKey{}

	err := server.Engine.SendChat(t.Context(), unknown, "Start a Workstream for loyalty points.", nil)

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

func TestANewTriagerChatSessionGetsTheChatHistory(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	sendChat(t, server, triagerChat, "Start a Workstream for loyalty points.")
	waitForChatSession(t, server, triagerChat, engine.TriagerRole, func(session store.Session) bool { return session.EndReason.String == "idle" })

	sendChat(t, server, triagerChat, "Yes, create it.")

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		sessions := chatSessions(t, server, triagerChat, engine.TriagerRole)
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

// researchTriager is a Triager chat that starts a Researcher for a message of the Owner, and answers its report. The
// first prompt of a new chat session also has the Role prompt, so the report prompt comes first in the script.
const researchTriager = `
[[prompts]]
when = "# Researcher message"
reply = ["I have the report."]

[[prompts]]
when = "# Owner message"
call = { tool = "start_researcher", arguments = { question = "Where do plans store the price?" } }
`

// triagerPrompts gives the prompts of the Triager chat sessions, the oldest first.
func triagerPrompts(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	var texts []string
	for _, session := range chatSessions(t, server, triagerChat, engine.TriagerRole) {
		texts = append(texts, promptTexts(t, server, session.ID)...)
	}
	return texts
}

func TestAReportOfTheResearcherGoesToTheTriagerChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectResearch(t, fake, researchTriager)

	sendChat(t, server, triagerChat, "Where do plans store the price?")

	session := testkit.WaitForValue(t, func() (store.Session, bool) {
		sessions := chatSessions(t, server, issueTriagers, engine.ResearcherRole)
		if len(sessions) == 0 {
			return store.Session{}, false
		}
		return sessions[0], sessions[0].EndedAt.Valid
	})
	if session.EndReason.String != "done" || session.Parent.Int64 != chatSessions(t, server, triagerChat, engine.TriagerRole)[0].ID {
		t.Errorf("session = %+v", session)
	}
	prompts := promptTexts(t, server, session.ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	if !strings.Contains(prompts[0], "# Question\n\n"+question) || strings.Contains(prompts[0], "# Brief") {
		t.Errorf("prompt = %s", prompts[0])
	}
	waitForChat(t, server, triagerChat, "Triager", "I have the report.")
	report := fmt.Sprintf("# Researcher message\n\nReport of the Researcher %d on \"%s\":\n\n%s", session.ID, question, reply(t, server, session.ID))
	if prompts := triagerPrompts(t, server); !slices.ContainsFunc(prompts, func(prompt string) bool { return strings.Contains(prompt, report) }) {
		t.Errorf("prompts = %q", prompts)
	}
	messages := chatView(t, server, triagerChat).Messages
	if slices.ContainsFunc(messages, func(message store.ChatMessage) bool { return message.Author == "Researcher" }) {
		t.Errorf("messages = %+v", messages)
	}
}

func TestAReportOfTheResearcherStartsANewTriagerChatSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	gate := filepath.Join(t.TempDir(), "gate")
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+researchTriager)
	testkit.InstallFakeHarness(t, dataDir, "agy_acp_server", options+`
[[prompts]]
when = "You are a Researcher"
reply = ["Plans store the price in cents.\n"]
shell = "while [ ! -e `+gate+` ]; do sleep 0.05; done"
`)
	server := startServerWith(t, fake, testserver.Config(t, dataDir), "")
	server.WaitForFirstPoll(t, shop)
	sendChat(t, server, triagerChat, "Where do plans store the price?")
	first := waitForChatSession(t, server, triagerChat, engine.TriagerRole, func(session store.Session) bool { return session.EndedAt.Valid })
	if first.EndReason.String != "idle" {
		t.Fatalf("end reason = %s", first.EndReason.String)
	}

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	second := waitForChatSession(t, server, triagerChat, engine.TriagerRole, func(session store.Session) bool { return session.ID != first.ID })
	prompt := testkit.WaitForValue(t, func() (string, bool) {
		prompts := promptTexts(t, server, second.ID)
		return strings.Join(prompts, ""), len(prompts) > 0
	})
	researcher := chatSessions(t, server, issueTriagers, engine.ResearcherRole)[0]
	if !strings.Contains(prompt, fmt.Sprintf("# Researcher message\n\nReport of the Researcher %d on \"%s\"", researcher.ID, question)) || strings.Contains(prompt, "# Owner message\n\nReport") {
		t.Errorf("prompt = %s", prompt)
	}
}

func TestTheTriagerOfAnIssueGetsARefusalFromStartResearcher(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("start_researcher", `{ question = "Where do plans store the price?" }`))
	triagerOfIssue := engine.Spec{Role: engine.TriagerRole, Organization: "owner", Repository: shop, Dir: t.TempDir()}

	session := run(t, server, triagerOfIssue, "Research it.")

	if got, want := reply(t, server, session), "error: Only the Triager chat or the Lead chat starts a Researcher."; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
	if sessions := chatSessions(t, server, issueTriagers, engine.ResearcherRole); len(sessions) != 0 {
		t.Errorf("Researchers = %+v", sessions)
	}
}

func TestATriagerChatSessionGetsTheImagesOfTheMessageAndATextMarkerForOlderImages(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, imageReading+options+"[[prompts]]\nwhen = \"Now plan it.\"\nreply = [\"Second answer\"]\n\n[[prompts]]\nreply = [\"First answer\"]\n", func(*config.Config) {})
	sendChatImages(t, server, triagerChat, "Look at this", []engine.Image{pngImage, jpegImage})
	first := waitForChatSession(t, server, triagerChat, engine.TriagerRole, func(session store.Session) bool { return session.EndReason.String == "idle" })
	if got, want := reply(t, server, first.ID), "First answer"+imageReply(pngImage)+imageReply(jpegImage); got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}

	sendChat(t, server, triagerChat, "Now plan it.")

	prompt := testkit.WaitForValue(t, func() (string, bool) {
		sessions := chatSessions(t, server, triagerChat, engine.TriagerRole)
		if len(sessions) < 2 {
			return "", false
		}
		prompts := promptTexts(t, server, sessions[1].ID)
		return strings.Join(prompts, ""), len(prompts) > 0
	})
	if !strings.Contains(prompt, "):\nLook at this\n[2 images]\n\n") {
		t.Errorf("prompt = %s", prompt)
	}
	second := chatSessions(t, server, triagerChat, engine.TriagerRole)[1]
	testkit.WaitFor(t, func() bool { return reply(t, server, second.ID) == "Second answer" })
}
