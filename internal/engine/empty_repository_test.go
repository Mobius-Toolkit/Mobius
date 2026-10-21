package engine_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// startEmptyShop starts a server with the Workstream #12 and its task issue #41 in owner/shop, which has no commit.
func startEmptyShop(t *testing.T, fake *testkit.FakeGitHub, lead, implementer string) *testserver.Server {
	t.Helper()
	fake.AddEmptyRepository(shop)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	dataDir := t.TempDir()
	testkit.InstallFakeHarness(t, dataDir, "claude-agent-acp", options+lead)
	testkit.InstallFakeHarness(t, dataDir, "devin", options+implementer)
	server := startServerOnAddedRepository(t, fake, testserver.Config(t, dataDir), "")
	server.WaitForFirstPoll(t, shop)
	return server
}

func TestATaskInAnEmptyRepositoryStartsAndMobiusPushesTheFirstCommit(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startEmptyShop(t, fake, leadStarts, commits)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)
	if want := []testkit.PullRequest{{Number: 42, Title: "Add plan model", Body: "Workstream:\n- #12\n\nIssue:\n- #41\n\nCloses #41", Head: "mobius/41", Base: "main", Draft: true}}; !reflect.DeepEqual(fake.PullRequests(shop), want) {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
	remote := fake.Remote(shop)
	if log := testkit.Git(t, remote, "log", "--format=%s by %an <%ae>", "main"); log != "Start by mobius-test[bot] <41898282+mobius-test[bot]@users.noreply.github.com>" {
		t.Errorf("log of main = %s", log)
	}
	if tree := testkit.Git(t, remote, "rev-parse", "main^{tree}"); tree != emptyTree {
		t.Errorf("tree of main = %s", tree)
	}
	if parent := testkit.Git(t, remote, "rev-parse", "mobius/41^"); parent != head(t, fake, "main") {
		t.Errorf("parent of mobius/41 = %s", parent)
	}
}

func TestTheLeadChatOfAWorkstreamInAnEmptyRepositoryGetsItsFirstPrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startEmptyShop(t, fake, "[[prompts]]\nreply = [\"Seen\"]\n", commits)

	sendChat(t, server, leadChat, "Plan the next step")

	session := endedChatSession(t, server, 0)
	if got := promptTexts(t, server, session.ID)[0]; !strings.HasSuffix(got, "Plan the next step") {
		t.Errorf("prompt = %q", got)
	}
	if got := reply(t, server, session.ID); got != "Seen" {
		t.Errorf("reply = %q", got)
	}
	if count := testkit.Git(t, fake.Remote(shop), "rev-list", "--count", "main"); count != "1" {
		t.Errorf("commits on main = %s", count)
	}
}
