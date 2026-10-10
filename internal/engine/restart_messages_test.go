package engine_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const messageSeed = `INSERT INTO chat_messages (id, organization, repository, workstream, author, time, text, browser_id, delivered_at, stopped_at)
	VALUES (%d, 'owner', '%s', %d, '%s', '2026-10-04T10:00:0%dZ', '%s', %s, %s, %s)`

func seedMessage(id int64, repository string, workstream int64, author, text, browserID, deliveredAt, stoppedAt string) string {
	quote := func(value string) string {
		if value == "" {
			return "NULL"
		}
		return "'" + value + "'"
	}
	return fmt.Sprintf(messageSeed, id, repository, workstream, author, id, text, quote(browserID), quote(deliveredAt), quote(stoppedAt))
}

func startWithLeadMessages(t *testing.T, statements ...string) *testserver.Server {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	seed(t, dataDir, statements...)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nreply = [\"Seen\"]\n")
	return startServer(t, fake, dataDir, "")
}

func joinedLeadPrompts(t *testing.T, server *testserver.Server) string {
	t.Helper()
	return strings.Join(leadPrompts(t, server), "\n---\n")
}

func TestARestartGivesAStoredOwnerMessageToTheLeadAndSetsItsDeliveryTime(t *testing.T) {
	t.Parallel()
	server := startWithLeadMessages(t, seedMessage(1, shop, 12, "Owner", "Plan the loyalty API.", "a1", "", ""))

	testkit.WaitFor(t, func() bool { return strings.Contains(joinedLeadPrompts(t, server), "Plan the loyalty API.") })

	testkit.WaitFor(t, func() bool {
		var deliveredAt *string
		if err := server.DB.QueryRow("SELECT delivered_at FROM chat_messages WHERE id = 1").Scan(&deliveredAt); err != nil {
			t.Fatal(err)
		}
		return deliveredAt != nil
	})
}

func TestARestartGivesTwoOwnerMessagesInTheOrderOfTheirIdsBeforeTheReadyEvents(t *testing.T) {
	t.Parallel()
	server := startWithLeadMessages(t,
		`INSERT INTO lead_events (repository, workstream, issue, kind, payload, time) VALUES ('owner/shop', 12, 41, 'comment', 'A comment before the restart.', '2026-10-04T10:00:00Z')`,
		seedMessage(2, shop, 12, "Owner", "Second message.", "a2", "", ""),
		seedMessage(1, shop, 12, "Owner", "First message.", "a1", "", ""))

	testkit.WaitFor(t, func() bool { return len(leadPrompts(t, server)) >= 3 })

	inOrder(t, joinedLeadPrompts(t, server), "First message.", "Second message.", "A comment before the restart.")
}

func TestARestartGivesAnOwnerMessageToTheTriagerChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	seed(t, dataDir, seedMessage(1, "", 0, "Owner", "Start a Workstream for loyalty points.", "a1", "", ""))
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nreply = [\"Seen\"]\n")

	server := startServer(t, fake, dataDir, "")

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(triagerPrompts(t, server), func(prompt string) bool {
			return strings.Contains(prompt, "Start a Workstream for loyalty points.")
		})
	})
	testkit.WaitFor(t, func() bool {
		var deliveredAt *string
		if err := server.DB.QueryRow("SELECT delivered_at FROM chat_messages WHERE id = 1").Scan(&deliveredAt); err != nil {
			t.Fatal(err)
		}
		return deliveredAt != nil
	})
}

func TestARestartDoesNotGiveADeliveredAStoppedOrAnUnnamedOwnerMessageAgain(t *testing.T) {
	t.Parallel()
	server := startWithLeadMessages(t,
		seedMessage(1, shop, 12, "Owner", "Waiting message.", "a1", "", ""),
		seedMessage(2, shop, 12, "Owner", "Delivered message.", "a2", "2026-10-04T10:01:00Z", ""),
		seedMessage(3, shop, 12, "Owner", "Stopped message.", "a3", "", "2026-10-04T10:01:00Z"),
		seedMessage(4, shop, 12, "Owner", "Old message.", "", "", ""),
		seedMessage(5, shop, 12, "Lead", "Lead message.", "a5", "", ""))

	testkit.WaitFor(t, func() bool { return strings.Contains(joinedLeadPrompts(t, server), "Waiting message.") })
	testkit.WaitFor(t, func() bool { return !chatView(t, server, leadChat).Writing })

	prompts := joinedLeadPrompts(t, server)
	for _, text := range []string{"Delivered message.", "Stopped message.", "Old message.", "Lead message."} {
		if strings.Contains(prompts, text) {
			t.Errorf("the agent got %q again: %s", text, prompts)
		}
	}
}

func TestAMessageOfThisRunWaitsBehindTheStoredMessageOfTheEarlierRun(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	seed(t, dataDir, seedMessage(1, shop, 12, "Owner", "Earlier message.", "a1", "", ""))
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nwhen = \"Running message.\"\nhang = true\n[[prompts]]\nreply = [\"Seen\"]\n")
	release := fake.HoldNext("GET /repos/{owner}/{repo}/labels")
	server := startServer(t, fake, dataDir, "")
	testkit.WaitFor(t, func() bool {
		return server.Engine.SendChat(t.Context(), leadChat, "a2", "Running message.", nil) == nil
	})
	testkit.WaitFor(t, func() bool { return strings.Contains(joinedLeadPrompts(t, server), "Running message.") })
	sendChatID(t, server, "a3", "Waiting message.")

	release()
	server.WaitForFirstPoll(t, shop)
	if err := server.Engine.StopChat(t.Context(), leadChat); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return strings.Contains(joinedLeadPrompts(t, server), "Waiting message.") })
	prompts := leadPrompts(t, server)
	if len(prompts) != 3 || !strings.HasSuffix(prompts[0], "Running message.") || prompts[1] != "Earlier message." || prompts[2] != "Waiting message." {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAStoredOwnerMessageThatTheDrainHeldGoesToTheAgentAfterTheDrain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	seed(t, dataDir,
		seedMessage(1, shop, 12, "Owner", "Lead message.", "a1", "", ""),
		seedMessage(2, "", 0, "Owner", "Triager message.", "a2", "", ""))
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, options+"[[prompts]]\nreply = [\"Seen\"]\n")
	release := fake.HoldNext("GET /repos/{owner}/{repo}/labels")
	server := startServer(t, fake, dataDir, "")
	startDrain(t, server)
	testkit.WaitFor(t, func() bool { return server.Engine.Draining().On })

	release()
	server.WaitForFirstPoll(t, shop)
	if prompts := joinedLeadPrompts(t, server); prompts != "" {
		t.Fatalf("the agent got a message during the drain: %s", prompts)
	}
	cancelDrain(t, server)

	testkit.WaitFor(t, func() bool { return strings.Contains(joinedLeadPrompts(t, server), "Lead message.") })
	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(triagerPrompts(t, server), func(prompt string) bool {
			return strings.Contains(prompt, "Triager message.")
		})
	})
	testkit.WaitFor(t, func() bool {
		var undelivered int
		if err := server.DB.QueryRow("SELECT COUNT(*) FROM chat_messages WHERE id IN (1, 2) AND delivered_at IS NULL").Scan(&undelivered); err != nil {
			t.Fatal(err)
		}
		return undelivered == 0
	})
}
