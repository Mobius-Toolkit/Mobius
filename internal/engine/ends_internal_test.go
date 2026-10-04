package engine

import (
	"testing"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/mobius-go/internal/github"
)

func TestAnIssueOfTheSameRepositoryInEachLetterCaseIsNotInAnotherRepository(t *testing.T) {
	if inOtherRepository(&gh.Issue{RepositoryURL: new("https://api.github.com/repos/Owner/Shop")}, "owner/shop") {
		t.Error("the issue is in another repository")
	}
}

func TestAnIssueOfAnotherRepositoryIsInAnotherRepository(t *testing.T) {
	for _, url := range []string{"https://api.github.com/repos/owner/billing", "https://api.github.com/repos/owner/workshop"} {
		if !inOtherRepository(&gh.Issue{RepositoryURL: &url}, "owner/shop") {
			t.Errorf("%s is the same repository", url)
		}
	}
}

func openOf(resolved bool, authors ...string) bool {
	trusted := func(login string) bool { return login != "mallory" }
	return openThread(github.ReviewThread{ID: "RT_1", Comment: 1, Resolved: resolved, Authors: authors}, trusted, "mobius-app[bot]")
}

func TestAFindingOfTheReviewerWithNoReplyIsOpen(t *testing.T) {
	if !openOf(false, "mobius-app[bot]") {
		t.Error("the finding is not open")
	}
}

func TestAThreadWithALastReplyOfTheMobiusAppIsNotOpen(t *testing.T) {
	if openOf(false, "mobius-app[bot]", "mobius-app[bot]") || openOf(false, "owner", "Mobius-App[bot]") {
		t.Error("the thread is open")
	}
}

func TestACommentOfATrustedUserAfterAReplyOfTheMobiusAppIsOpen(t *testing.T) {
	if !openOf(false, "owner", "mobius-app[bot]", "owner") {
		t.Error("the thread is not open")
	}
}

func TestAResolvedThreadIsNotOpen(t *testing.T) {
	if openOf(true, "owner") {
		t.Error("the thread is open")
	}
}

func TestAThreadThatAnUntrustedAuthorStartedIsNotOpen(t *testing.T) {
	if openOf(false, "mallory", "owner") {
		t.Error("the thread is open")
	}
}

func TestAReplyOfAnUntrustedAuthorDoesNotCount(t *testing.T) {
	if openOf(false, "owner", "mobius-app[bot]", "mallory") {
		t.Error("the thread is open")
	}
}

func TestTheReportIsTheTextAfterTheLastToolCall(t *testing.T) {
	a := &Agent{}
	for _, update := range [][2]string{
		{"agent_message_chunk", "I read the code."},
		{"tool_call", ""},
		{"tool_call_update", ""},
		{"agent_message_chunk", "Plans store "},
		{"agent_message_chunk", "cents."},
		{"agent_thought_chunk", "Done."},
	} {
		if err := a.addReply(t.Context(), update[0], update[1]); err != nil {
			t.Fatal(err)
		}
	}
	if report := a.replyText(); report != "Plans store cents." {
		t.Errorf("report = %q", report)
	}
}
