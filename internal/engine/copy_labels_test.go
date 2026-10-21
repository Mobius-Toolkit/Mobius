package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestATreeReadBeforeALocalLabelChangeDoesNotWriteTheOldLabelsBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return slices.Contains(copiedLabels(t, server, 41), "mobius:question") })
	fake.AddIssue(shop, 50, "Plan the tiers")
	waitForPolls(t, fake)
	parentReads := func() int {
		reads := 0
		for _, request := range fake.Requests() {
			if strings.HasSuffix(request.Path, "/issues/50/parent") {
				reads++
			}
		}
		return reads
	}
	before := parentReads()
	release := fake.HoldNext("GET /repos/{owner}/{repo}/issues/{number}/parent")
	fake.AddBlockedBy(shop, 41, 50)
	testkit.WaitFor(t, func() bool { return parentReads() > before })

	sendChat(t, server, leadChat, "Dollars were chosen")
	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["result"] == "Closed the question on #41."
	})
	release()

	testkit.WaitFor(t, func() bool { return len(copiedBlockers(t, server)) == 1 })
	if labels := copiedLabels(t, server, 41); slices.Contains(labels, "mobius:question") {
		t.Errorf("labels = %v", labels)
	}
}

func TestAPollIssueReadBeforeALocalLabelChangeDoesNotWriteTheOldLabelsBack(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return slices.Contains(copiedLabels(t, server, 41), "mobius:question") })
	waitForPolls(t, fake)
	reached, release := fake.HoldIssueEvents(shop, 12)
	fake.SetTitle(shop, 12, "Plan the shop")
	fake.SetTitle(shop, 41, "Add the plan model")
	<-reached

	sendChat(t, server, leadChat, "Dollars were chosen")
	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["result"] == "Closed the question on #41."
	})
	release()

	testkit.WaitFor(t, func() bool { return copiedTitle(t, server, 41) == "Add the plan model" })
	if labels := copiedLabels(t, server, 41); slices.Contains(labels, "mobius:question") {
		t.Errorf("labels = %v", labels)
	}
}

func TestALocalLabelChangeDuringAPollDoesNotKeepTheOldLabelsOfAnotherIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	fake.AddSubIssueOf(shop, 12, 50, "Plan the tiers")
	testkit.WaitFor(t, func() bool { return slices.Contains(copiedLabels(t, server, 41), "mobius:question") })
	waitForPolls(t, fake)
	reached, release := fake.HoldIssueEvents(shop, 12)
	fake.SetTitle(shop, 12, "Plan the shop")
	fake.AddLabel(shop, 50, "bug", "owner")
	fake.SetTitle(shop, 41, "Add the plan model")
	<-reached

	sendChat(t, server, leadChat, "Dollars were chosen")
	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["result"] == "Closed the question on #41."
	})
	release()

	testkit.WaitFor(t, func() bool { return slices.Contains(copiedLabels(t, server, 50), "bug") })
	if labels := copiedLabels(t, server, 41); slices.Contains(labels, "mobius:question") {
		t.Errorf("labels = %v", labels)
	}
}

func TestATreeCopyKeepsALocalLabelChangeAndStillWritesTheTree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	fake.AddSubIssueOf(shop, 41, 50, "Store the price in cents")
	testkit.WaitFor(t, func() bool { return slices.Contains(copiedLabels(t, server, 41), "mobius:question") })
	waitForPolls(t, fake)
	subIssueReads := func(number string) int {
		reads := 0
		for _, request := range fake.Requests() {
			if strings.HasSuffix(request.Path, "/issues/"+number+"/sub_issues") {
				reads++
			}
		}
		return reads
	}

	// The check of the tree reads the sub-issues of #12 and holds. Then the tree copy reads them and holds, and then it
	// reads the sub-issues of #41 and holds. The labels of #41 are from the read of the sub-issues of #12.
	// A poll that runs already can read the sub-issues of #41 and #50 now, so the first hold matches only #12.
	before := subIssueReads("12")
	releaseCheck := fake.HoldNextOf("GET /repos/{owner}/{repo}/issues/{number}/sub_issues", "/repos/"+shop+"/issues/12/sub_issues")
	testkit.WaitFor(t, func() bool { return subIssueReads("12") > before })
	releaseRoot := fake.HoldNext("GET /repos/{owner}/{repo}/issues/{number}/sub_issues")
	fake.AddSubIssueOf(shop, 12, 60, "Plan the tiers")
	releaseCheck()
	testkit.WaitFor(t, func() bool { return subIssueReads("12") > before+1 })
	before41 := subIssueReads("41")
	releaseTask := fake.HoldNext("GET /repos/{owner}/{repo}/issues/{number}/sub_issues")
	releaseRoot()
	testkit.WaitFor(t, func() bool { return subIssueReads("41") > before41 })

	sendChat(t, server, leadChat, "Dollars were chosen")
	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["result"] == "Closed the question on #41."
	})
	releaseTask()

	testkit.WaitFor(t, func() bool { return slices.Equal(issueNumbers(t, server), []int64{41, 50, 60}) })
	if labels := copiedLabels(t, server, 41); slices.Contains(labels, "mobius:question") {
		t.Errorf("labels = %v", labels)
	}
}

func TestANewWorkstreamCopyKeepsALocalLabelChangeAndStillWritesTheTree(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "Dollars were chosen"
call = { tool = "close_question", arguments = { repository = "owner/shop", n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { repository = "owner/shop", n = 41, text = "Cents or dollars?" } }

[[prompts]]
reply = ["Seen"]
`)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return slices.Contains(copiedLabels(t, server, 41), "mobius:question") })
	waitForPolls(t, fake)
	subIssueReads := func(number string) int {
		reads := 0
		for _, request := range fake.Requests() {
			if strings.HasSuffix(request.Path, "/issues/"+number+"/sub_issues") {
				reads++
			}
		}
		return reads
	}
	before := subIssueReads("70")
	release := fake.HoldNextOf("GET /repos/{owner}/{repo}/issues/{number}/sub_issues", "/repos/"+shop+"/issues/70/sub_issues")
	fake.AddIssue(shop, 70, "Add the shop pages")
	fake.AddSubIssueOf(shop, 70, 71, "Write the home page")
	fake.AddLabel(shop, 70, "mobius:workstream", "owner")
	testkit.WaitFor(t, func() bool { return subIssueReads("70") > before })

	sendChat(t, server, leadChat, "Dollars were chosen")
	testkit.WaitFor(t, func() bool {
		calls := leadCalls(t, server)
		return len(calls) == 2 && calls[1]["result"] == "Closed the question on #41."
	})
	release()

	testkit.WaitFor(t, func() bool { return slices.Contains(issueNumbers(t, server), 71) })
	if labels := copiedLabels(t, server, 41); slices.Contains(labels, "mobius:question") {
		t.Errorf("labels = %v", labels)
	}
}
