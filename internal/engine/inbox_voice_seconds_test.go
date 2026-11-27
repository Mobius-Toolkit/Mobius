package engine_test

import (
	"net/http"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestTheAnswerOfACoordinatorQuestionKeepsTheVoiceSecondsInTheStoredChatMessage(t *testing.T) {
	t.Parallel()
	server, _ := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Cents, please."
reply = ["Got the answer"]

[[prompts]]
when = "Ask the Owner"
call = { tool = "ask", arguments = { text = "Two choices.", questions = [{ text = "Which unit?", options = [{ title = "Cents" }, { title = "Dollars" }] }] } }

[[prompts]]
reply = ["Asked"]
`)
	sendChat(t, server, coordinatorChat, "Ask the Owner")
	item := testkit.WaitForValue(t, func() (coordinatorItem, bool) {
		items := coordinatorItems(t, server)
		if len(items) != 1 {
			return coordinatorItem{}, false
		}
		return items[0], true
	})

	status, body := postForm(t, server, "/api/inbox/"+itoa(item.ID)+"/answer",
		textPart("text", "Cents, please."), textPart("voice", "true"), textPart("voiceSeconds", "12"))

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	waitForChat(t, server, coordinatorChat, "Coordinator", "Got the answer")
	if got := count(t, server, "SELECT voice_seconds FROM chat_messages WHERE author = 'Owner' AND text = 'Cents, please.' AND voice = 1"); got != 12 {
		t.Errorf("voice seconds = %d, want 12", got)
	}
}

func TestTheDoneNoteOfATodoWithAnImageKeepsTheVoiceSecondsInTheStoredChatMessage(t *testing.T) {
	t.Parallel()
	server := approvableWith(t, testkit.NewFakeGitHub(t), "off", true, mandatoryNeed)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY. The Owner got it for review. The pull request stays a draft until the Owner closes the open todos.")
	todo := waitForItem(t, server, "todo")

	status, body := postForm(t, server, "/api/inbox/"+itoa(todo.ID)+"/done",
		textPart("note", "Set in prod."), pngPart, textPart("voice", "true"), textPart("voiceSeconds", "7"))

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if got := count(t, server, "SELECT voice_seconds FROM chat_messages WHERE author = 'Owner' AND text = 'Set in prod.' AND voice = 1 AND display_only = 0"); got != 7 {
		t.Errorf("voice seconds = %d, want 7", got)
	}
}

func TestTheDoneNoteOfATodoWithOnlyTextAndVoiceKeepsTheVoiceSecondsInTheStoredDisplayOnlyMessage(t *testing.T) {
	t.Parallel()
	server := approvableWith(t, testkit.NewFakeGitHub(t), "off", true, mandatoryNeed)
	sendChat(t, server, leadChat, "Approve #41")
	waitForChat(t, server, leadChat, "Lead", "Approved pull request #42 of #41. The Owner got the deployment todos: PLAN_API_KEY. The Owner got it for review. The pull request stays a draft until the Owner closes the open todos.")
	todo := waitForItem(t, server, "todo")

	status, body := postForm(t, server, "/api/inbox/"+itoa(todo.ID)+"/done",
		textPart("note", "Set in prod."), textPart("voice", "true"), textPart("voiceSeconds", "5"))

	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if got := count(t, server, "SELECT voice_seconds FROM chat_messages WHERE author = 'Owner' AND text = 'Set in prod.' AND voice = 1 AND display_only = 1"); got != 5 {
		t.Errorf("voice seconds = %d, want 5", got)
	}
}

func TestTheDecisionOnAnActionKeepsTheVoiceSecondsInTheStoredApprovalMessage(t *testing.T) {
	t.Parallel()
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			t.Parallel()
			server, _ := connect(t, testkit.NewFakeGitHub(t), approvalSeen+callMessageLead("Send.", 12, "Create the task issues.")+seenScript)
			run(t, server, coordinatorSpec(t), "Send.")
			action := waitForActions(t, server, 1)[0]

			status, body := postForm(t, server, "/api/actions/"+itoa(action.ID)+"/"+decision,
				textPart("message", "Look."), textPart("voice", "true"), textPart("voiceSeconds", "9"))

			if status != http.StatusOK {
				t.Fatalf("status = %d: %s", status, body)
			}
			if got := count(t, server, "SELECT voice_seconds FROM chat_messages WHERE author = 'Approval' AND voice = 1"); got != 9 {
				t.Errorf("voice seconds = %d, want 9", got)
			}
		})
	}
}
