package engine_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const triager = `
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

// issueTriagers is the key of the sessions of the Triagers of the issues of owner/shop.
var issueTriagers = engine.ChatKey{Organization: "owner", Repository: shop}

// connectTriager starts a server with the Workstream #12 and its Brief, and the Triager of triager.
func connectTriager(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string) {
	t.Helper()
	server, dataDir := connect(t, fake, triager)
	fake.SetBody(shop, 12, "Ship loyalty plans to all shops.")
	return server, dataDir
}

func TestAnIssueWithNoWorkstreamGoesToTheTriagerThatMovesIt(t *testing.T) {
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

func TestTheTriagerChatCreatesAWorkstreamAfterTheApproval(t *testing.T) {
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
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTriager(t, fake)
	unknown := engine.ChatKey{}

	err := server.Engine.SendChat(t.Context(), unknown, "Start a Workstream for loyalty points.")

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
