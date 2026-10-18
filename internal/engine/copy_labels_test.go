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
call = { tool = "close_question", arguments = { n = 41, answer = "Dollars." } }

[[prompts]]
when = "dispatch of #41"
call = { tool = "ask", arguments = { n = 41, text = "Cents or dollars?" } }

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
