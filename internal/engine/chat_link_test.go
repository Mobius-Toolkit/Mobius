package engine_test

import (
	"reflect"
	"slices"
	"strings"
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

func TestAnApprovedCreateWorkstreamWritesTheBriefWithTheLinkToItsOwnLeadChat(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, approvalSeen+callCreateWorkstream("Create.", "Upkeep", "Keep the code healthy.", "mobius:maintenance")+seenScript, withUIURL)
	run(t, server, coordinatorSpec(t), "Create.")
	action := waitForActions(t, server, 1)[0]

	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Result != "Created the Workstream #13." {
		t.Errorf("action = %+v", done)
	}
	if _, body := fake.Issue(shop, 13); body != "Keep the code healthy.\n\n"+strings.Replace(chatLink, "/12)", "/13)", 1) {
		t.Errorf("body = %q", body)
	}
}

func TestASecondApprovalOfCreateWorkstreamAfterAFailedAddOfTheLabelsKeepsOneLinkAndOneIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, approvalSeen+callCreateWorkstream("Create.", "Upkeep", "Keep the code healthy.", "mobius:maintenance")+seenScript, withUIURL)
	fake.FailAddLabels(shop, 13, true)
	run(t, server, coordinatorSpec(t), "Create.")
	action := waitForActions(t, server, 1)[0]

	failed := decided(t, server, action.ID, "approve", "")

	if failed.State != "pending" || !strings.HasPrefix(failed.Error, "GitHub created #13, but the add of the Workstream labels failed.") {
		t.Errorf("action = %+v", failed)
	}
	fake.FailAddLabels(shop, 13, false)
	done := decided(t, server, action.ID, "approve", "")

	if done.State != "done" || done.Error != "" {
		t.Errorf("action = %+v", done)
	}
	want := "Keep the code healthy.\n\n" + strings.Replace(chatLink, "/12)", "/13)", 1)
	if _, body := fake.Issue(shop, 13); body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	if labels := fake.Labels(shop, 13); !slices.Equal(labels, []string{"mobius:workstream", "mobius:maintenance"}) {
		t.Errorf("labels of #13 = %v", labels)
	}
	if fake.HasIssue(shop, 14) {
		t.Error("the Workstream exists two times")
	}
}

func TestEditBriefWritesTheLinkToTheLeadChatOneTime(t *testing.T) {
	t.Parallel()
	for name, brief := range map[string]string{
		"no link":       "Ship loyalty plans.",
		"the same link": "Ship loyalty plans.\n\n" + chatLink + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := connectWith(t, fake, approvalSeen+callEditBrief("Change it.", brief, "Gift cards are in scope.")+seenScript, withUIURL)
			run(t, server, leadSpec(t), "Change it.")
			action := waitForActions(t, server, 1)[0]

			done := decided(t, server, action.ID, "approve", "")

			if done.State != "done" {
				t.Errorf("action = %+v", done)
			}
			if _, body := fake.Issue(shop, 12); body != "Ship loyalty plans.\n\n"+chatLink {
				t.Errorf("body = %q", body)
			}
		})
	}
}
