package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const nextStep = "\n\nNext step: "

// wantStopNotice tests that issue #41 has one comment of Mobius with the reason text and the next action, and that the
// Inbox has one stopped item with the reason, the same text and the same next action. It gives the item.
func wantStopNotice(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, reason, text, next string) inboxItem {
	t.Helper()
	body := text + nextStep + next
	var comments []string
	for _, comment := range fake.Comments(shop, 41) {
		if comment.Author == "mobius-test[bot]" && strings.Contains(comment.Body, nextStep) {
			comments = append(comments, comment.Body)
		}
	}
	if len(comments) != 1 || comments[0] != body {
		t.Errorf("comments = %q, want %q", comments, body)
	}
	items := itemsOf(t, server, "stopped")
	if len(items) != 1 {
		t.Fatalf("stopped items = %+v", items)
	}
	if items[0].Reason != reason || !strings.HasPrefix(items[0].Text, body) || items[0].Issue != 41 || items[0].Link != "https://github.com/"+shop+"/issues/41" {
		t.Errorf("stopped item = %+v", items[0])
	}
	return items[0]
}

func TestALostTaskGetsOneCommentWithTheLostTextAndNoStoppedItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := lostServer(t, fake)
	lostText := "Mobius lost the state of this task. Add mobius:ready to start again."
	testkit.WaitFor(t, func() bool {
		return slices.Contains(fake.Comments(shop, 41), testkit.Comment{Author: "mobius-test[bot]", Body: lostText})
	})

	waitForPolls(t, fake)

	var lost []string
	for _, comment := range fake.Comments(shop, 41) {
		if comment.Body == lostText {
			lost = append(lost, comment.Body)
		}
	}
	if len(lost) != 1 {
		t.Errorf("comments = %+v", fake.Comments(shop, 41))
	}
	if items := itemsOf(t, server, "lost"); len(items) != 1 {
		t.Errorf("lost items = %+v", items)
	}
	if items := itemsOf(t, server, "stopped"); len(items) != 0 {
		t.Errorf("stopped items = %+v", items)
	}
}
