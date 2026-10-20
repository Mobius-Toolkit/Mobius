package engine_test

import (
	"reflect"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const chatLink = "[Open the Lead chat in Mobius](https://mobius.example.com/workstreams/owner/shop/12)"

func withUIURL(cfg *config.Config) { cfg.UIURL = "https://mobius.example.com" }

func TestTheBodyOfAPullRequestEndsWithTheLinkToTheLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, withUIURL)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)
	want := []testkit.PullRequest{{Number: 42, Title: "Add plan model", Body: "Workstream:\n- #12\n\nIssue:\n- #41\n\nCloses #41\n\n" + chatLink, Head: "mobius/41", Base: "main", Draft: true}}
	if !reflect.DeepEqual(fake.PullRequests(shop), want) {
		t.Errorf("pull requests = %+v", fake.PullRequests(shop))
	}
}

func TestCreateIssueEndsTheBodyWithTheLinkToTheLeadChatOfTheWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, call("create_issue", `{ title = "Add plan model", body = "Plans have a price.", parent = 41, blocked_by = [] }`), withUIURL)
	fake.AddIssue(shop, 41, "Add plan table")
	fake.AddSubIssue(shop, 12, 41)

	session := run(t, server, leadSpec(t), "Plan the work.")

	if got := reply(t, server, session); got != "Created #42." {
		t.Errorf("reply = %q", got)
	}
	if _, body := fake.Issue(shop, 42); body != "Plans have a price.\n\n"+chatLink {
		t.Errorf("body = %q", body)
	}
}

func TestCreateIssueAddsNoLinkWithNoUIURL(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, call("create_issue", `{ title = "Add plan model", body = "Plans have a price.", parent = 12, blocked_by = [] }`))

	run(t, server, leadSpec(t), "Plan the work.")

	if _, body := fake.Issue(shop, 13); body != "Plans have a price." {
		t.Errorf("body = %q", body)
	}
}
