package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const failedTurn = `
[[prompts]]
error = { code = -32000, message = "Failed to authenticate" }
`

const failedTurnAfterToolCall = `
[[prompts]]
updates = ['{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Read"}']
error = { code = -32000, message = "Failed" }
`

// failedThen is the shell of a Harness that fails its turn while fail exists, and runs then after that.
func failedThen(fail, then string) string {
	if then == "" {
		then = "true"
	}
	return fmt.Sprintf("if [ -e '%s' ]; then kill -9 $PPID; sleep 5; else %s; fi", fail, then)
}

func chatMessageByText(t *testing.T, server *testserver.Server, key engine.ChatKey, text string) (store.ChatMessage, bool) {
	t.Helper()
	for _, message := range chatView(t, server, key).Messages {
		if message.Text == text {
			return message, true
		}
	}
	return store.ChatMessage{}, false
}

func waitForFailedMessage(t *testing.T, server *testserver.Server, key engine.ChatKey, text string) store.ChatMessage {
	t.Helper()
	return testkit.WaitForValue(t, func() (store.ChatMessage, bool) {
		message, ok := chatMessageByText(t, server, key, text)
		return message, ok && message.FailedAt.Valid && !message.DeliveredAt.Valid && !message.StoppedAt.Valid
	})
}

func waitForDeliveredMessage(t *testing.T, server *testserver.Server, key engine.ChatKey, text string) {
	t.Helper()
	testkit.WaitFor(t, func() bool {
		for _, message := range chatView(t, server, key).Messages {
			if message.Text == text && message.DeliveredAt.Valid && !message.FailedAt.Valid {
				return true
			}
		}
		return false
	})
}

func waitForFailedItem(t *testing.T, server *testserver.Server, text string) inboxItem {
	t.Helper()
	return testkit.WaitForValue(t, func() (inboxItem, bool) {
		for _, item := range itemsOf(t, server, "Lead failed") {
			if item.Text == text {
				return item, true
			}
		}
		return inboxItem{}, false
	})
}

func waitForNoFailedItems(t *testing.T, server *testserver.Server) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return len(itemsOf(t, server, "Lead failed")) == 0 })
}

func waitForChatEnd(t *testing.T, server *testserver.Server, key engine.ChatKey) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return !chatView(t, server, key).Writing })
}

// Serial: it shortens ToolsTimeout.
func TestAFailedStartKeepsTheOwnerMessageAndAddsAFailureItem(t *testing.T) {
	defer func(limit time.Duration) { *engine.ToolsTimeout = limit }(*engine.ToolsTimeout)
	*engine.ToolsTimeout = 20 * time.Millisecond
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, "skip_tools_list = 100\n"+options, func(cfg *config.Config) { cfg.MaxWorkerRestarts = 0 })
	const text = "Start a Workstream for loyalty points."

	sendChat(t, server, coordinatorChat, text)

	failed := waitForFailedMessage(t, server, coordinatorChat, text)
	if failed.StoppedAt.Valid || failed.CancelledAt.Valid {
		t.Errorf("message = %+v", failed)
	}
	item := waitForFailedItem(t, server, text)
	if item.Title != `Coordinator did not get: "Start a Workstream for loyalty points."` ||
		item.Reason != "The Coordinator did not get your message" || item.Text != text {
		t.Errorf("item = %+v", item)
	}
	sessions := chatSessions(t, server, coordinatorChat, engine.CoordinatorRole)
	if len(sessions) != 4 {
		t.Errorf("sessions = %d", len(sessions))
	}
	for _, session := range sessions {
		if session.EndReason.String != "failed" {
			t.Errorf("session = %+v", session)
		}
	}
	if got := len(itemsOf(t, server, "Lead failed")); got != 1 {
		t.Errorf("items = %d", got)
	}
}

func TestAnErrorChunkOfTheHarnessDoesNotDeliverTheMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
reply = ["Failed to authenticate"]
error = { code = -32000, message = "Failed to authenticate" }
`)
	const text = "Plan the loyalty API"

	sendChat(t, server, leadChat, text)

	failed := waitForFailedMessage(t, server, leadChat, text)
	if failed.DeliveredAt.Valid || failed.StoppedAt.Valid {
		t.Errorf("message = %+v", failed)
	}
	item := waitForFailedItem(t, server, text)
	if item.Title != `Lead of #12 Integrate loyalty plans did not get: "Plan the loyalty API"` || item.Reason != "The Lead did not get your message" {
		t.Errorf("item = %+v", item)
	}
}

func TestAFailedTurnAfterAToolCallClearsTheDeliveryAndTheNextSessionGetsTheMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, failedTurnAfterToolCall)
	const text = "Plan the loyalty API"

	sendChat(t, server, leadChat, text)

	failed := waitForFailedMessage(t, server, leadChat, text)
	if failed.DeliveredAt.Valid || failed.StoppedAt.Valid {
		t.Errorf("message = %+v", failed)
	}
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 4 {
		t.Fatalf("sessions = %d", len(sessions))
	}
	if prompts := promptTexts(t, server, sessions[1].ID); len(prompts) != 1 || !strings.Contains(prompts[0], text) {
		t.Errorf("prompts of the second session = %q", prompts)
	}
}

func TestATurnThatFailsOnceAndThenSucceedsDeliversTheMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fail := filepath.Join(dataDir, "fail")
	waiting := filepath.Join(dataDir, "waiting")
	release := filepath.Join(dataDir, "release")
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	then := fmt.Sprintf("if [ -e '%s' ]; then rm '%s'; kill -9 $PPID; sleep 5; fi; touch '%s'; while [ ! -e '%s' ]; do sleep 0.01; done", fail, fail, waiting, release)
	script := options + fmt.Sprintf("[[prompts]]\nshell = \"%s\"\nreply = [\"Seen\"]\n", then)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, script)
	cfg := testserver.Config(t, dataDir)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, server.Engine.AccessChecked)
	const text = "Plan the loyalty API"

	sendChat(t, server, leadChat, text)

	testkit.WaitFor(t, func() bool {
		_, err := os.Stat(waiting)
		return err == nil
	})
	message, ok := chatMessageByText(t, server, leadChat, text)
	if !ok || message.DeliveredAt.Valid || message.FailedAt.Valid {
		t.Errorf("message = %+v", message)
	}
	if got := len(itemsOf(t, server, "Lead failed")); got != 0 {
		t.Errorf("items = %d", got)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	waitForDeliveredMessage(t, server, leadChat, text)
	if got := len(itemsOf(t, server, "Lead failed")); got != 0 {
		t.Errorf("items = %d", got)
	}
}

func TestANewMessageAfterTheLastTryGivesTheFailedMessageFirst(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fail := filepath.Join(dataDir, "fail")
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := options + fmt.Sprintf("[[prompts]]\nshell = \"%s\"\nreply = [\"Seen\"]\n", failedThen(fail, ""))
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, script)
	cfg := testserver.Config(t, dataDir)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, server.Engine.AccessChecked)
	const first = "Plan the loyalty API"
	const second = "Add a plan price"

	sendChat(t, server, leadChat, first)
	waitForFailedMessage(t, server, leadChat, first)
	waitForFailedItem(t, server, first)
	waitForChatEnd(t, server, leadChat)
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}

	sendChat(t, server, leadChat, second)

	waitForDeliveredMessage(t, server, leadChat, first)
	waitForDeliveredMessage(t, server, leadChat, second)
	waitForNoFailedItems(t, server)
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	last := sessions[len(sessions)-1]
	prompts := promptTexts(t, server, last.ID)
	if len(prompts) != 2 || !strings.HasSuffix(prompts[0], first) || !strings.HasSuffix(prompts[1], second) {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestTheIntervalRetryStartsTheChatOnceAndDeliversTheMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fail := filepath.Join(dataDir, "fail")
	waiting := filepath.Join(dataDir, "waiting")
	release := filepath.Join(dataDir, "release")
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	then := fmt.Sprintf("touch '%s'; while [ ! -e '%s' ]; do sleep 0.01; done", waiting, release)
	script := options + fmt.Sprintf("[[prompts]]\nshell = \"%s\"\nreply = [\"Seen\"]\n", failedThen(fail, then))
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, script)
	cfg := testserver.Config(t, dataDir)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, server.Engine.AccessChecked)
	const text = "Plan the loyalty API"

	sendChat(t, server, leadChat, text)
	waitForFailedMessage(t, server, leadChat, text)
	waitForFailedItem(t, server, text)
	waitForChatEnd(t, server, leadChat)
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	server.Engine.SetClock(func() time.Time { return time.Now().Add(engine.FailedRetry + time.Minute) })

	testkit.WaitFor(t, func() bool {
		_, err := os.Stat(waiting)
		return err == nil
	})
	if got := len(chatSessions(t, server, leadChat, engine.LeadRole)); got != 5 {
		t.Errorf("sessions = %d", got)
	}
	waitForPolls(t, fake)
	if got := len(chatSessions(t, server, leadChat, engine.LeadRole)); got != 5 {
		t.Errorf("sessions after more polls = %d", got)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	waitForDeliveredMessage(t, server, leadChat, text)
	waitForNoFailedItems(t, server)
	if got := len(chatSessions(t, server, leadChat, engine.LeadRole)); got != 5 {
		t.Errorf("sessions = %d", got)
	}
}

func TestARestartGivesAMessageThatWaitsAfterAFailureToTheAgent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fail := filepath.Join(dataDir, "fail")
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := options + fmt.Sprintf("[[prompts]]\nshell = \"%s\"\nreply = [\"Seen\"]\n", failedThen(fail, ""))
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, script)
	cfg := testserver.Config(t, dataDir)
	first := startServerWith(t, fake, cfg, "")
	first.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, first.Engine.AccessChecked)
	const text = "Plan the loyalty API"

	sendChatID(t, first, "a1", text)
	waitForFailedMessage(t, first, leadChat, text)
	waitForFailedItem(t, first, text)
	waitForChatEnd(t, first, leadChat)
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	first.Stop()

	server := testserver.Start(t, dataDir, fake.URL)

	waitForDeliveredMessage(t, server, leadChat, text)
	waitForNoFailedItems(t, server)
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if prompts := promptTexts(t, server, sessions[len(sessions)-1].ID); len(prompts) == 0 || !strings.HasSuffix(prompts[0], text) {
		t.Errorf("prompts = %q", prompts)
	}
}

func TestAFailedMessageOfTheCoordinatorNamesTheChatAndTheStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, failedTurn)
	const text = "Please explain why the queue keeps growing after every plan\nSecond line."

	sendChat(t, server, coordinatorChat, text)

	waitForFailedMessage(t, server, coordinatorChat, text)
	item := waitForFailedItem(t, server, text)
	if item.Title != `Coordinator did not get: "Please explain why the queue keeps growi…"` ||
		item.Reason != "The Coordinator did not get your message" || item.Text != text {
		t.Errorf("item = %+v", item)
	}
}

func TestAFailedMessageOfTheLeadNamesTheChatAndTheStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, failedTurn)
	const text = "Plan the loyalty points API for the shop checkout\nSecond line."

	sendChat(t, server, leadChat, text)

	waitForFailedMessage(t, server, leadChat, text)
	item := waitForFailedItem(t, server, text)
	if item.Title != `Lead of #12 Integrate loyalty plans did not get: "Plan the loyalty points API for the shop…"` ||
		item.Reason != "The Lead did not get your message" || item.Text != text {
		t.Errorf("item = %+v", item)
	}
}

func TestEachQueuedOwnerMessageGetsItsOwnFailureItem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
shell = "sleep 0.5"
error = { code = -32000, message = "Failed" }
`)
	const first = "Plan the loyalty points API for the shop checkout"
	const second = "Another message that waits in the queue of the chat"

	sendChat(t, server, leadChat, first)
	sendChat(t, server, leadChat, second)

	waitForFailedMessage(t, server, leadChat, first)
	waitForFailedMessage(t, server, leadChat, second)
	waitForFailedItem(t, server, first)
	waitForFailedItem(t, server, second)
	if got := len(itemsOf(t, server, "Lead failed")); got != 2 {
		t.Errorf("items = %d", got)
	}
}

func TestSendAgainCancelsTheWaitingMessageAndPostsOneCopy(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fail := filepath.Join(dataDir, "fail")
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := options + fmt.Sprintf("[[prompts]]\nshell = \"%s\"\nreply = [\"Seen\"]\n", failedThen(fail, ""))
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, script)
	cfg := testserver.Config(t, dataDir)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, server.Engine.AccessChecked)
	const text = "Plan the loyalty API"

	sendChatID(t, server, "a1", text)
	waitForFailedMessage(t, server, leadChat, text)
	item := waitForFailedItem(t, server, text)
	waitForChatEnd(t, server, leadChat)
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}

	if err := server.Engine.SendAgain(t.Context(), item.ID); err != nil {
		t.Fatal(err)
	}

	waitForNoFailedItems(t, server)
	old, ok := chatMessageByText(t, server, leadChat, text)
	if !ok || !old.CancelledAt.Valid || old.StoppedAt.Valid {
		t.Fatalf("old message = %+v", old)
	}
	var copied store.ChatMessage
	for _, message := range chatView(t, server, leadChat).Messages {
		if message.Author == "Owner" {
			copied = message
		}
	}
	if copied.ID == old.ID || copied.Text != text || !copied.BrowserID.Valid {
		t.Errorf("copy = %+v", copied)
	}
	waitForDeliveredMessage(t, server, leadChat, text)
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	prompts := promptTexts(t, server, sessions[len(sessions)-1].ID)
	if len(prompts) == 0 {
		t.Fatal("the copy has no prompt")
	}
	if got := strings.Count(prompts[0], text); got != 1 {
		t.Errorf("the first prompt of the copy gives the text %d times", got)
	}
	before := len(sessions)

	server.Engine.SetClock(func() time.Time { return time.Now().Add(engine.FailedRetry + time.Minute) })
	waitForPolls(t, fake)
	if got := len(chatSessions(t, server, leadChat, engine.LeadRole)); got != before {
		t.Errorf("sessions after the interval = %d", got)
	}

	server.Stop()
	restarted := testserver.Start(t, dataDir, fake.URL)
	waitForPolls(t, fake)
	if got := len(chatSessions(t, restarted, leadChat, engine.LeadRole)); got != before {
		t.Errorf("sessions after the restart = %d", got)
	}
}

func TestSendAgainKeepsTheWaitingMessageWhenTheCopyFails(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fail := filepath.Join(dataDir, "fail")
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := options + fmt.Sprintf("[[prompts]]\nshell = \"%s\"\nreply = [\"Seen\"]\n", failedThen(fail, ""))
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, script)
	cfg := testserver.Config(t, dataDir)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, server.Engine.AccessChecked)
	const text = "Plan the loyalty API"

	sendChatImages(t, server, leadChat, text, []engine.Image{pngImage})
	waitForFailedMessage(t, server, leadChat, text)
	item := waitForFailedItem(t, server, text)
	waitForChatEnd(t, server, leadChat)
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	images := filepath.Join(dataDir, "images")
	last, err := store.New(server.DB).MaxChatMessageID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The copy gets the next id, and its images cannot move onto a directory that is not empty.
	taken := filepath.Join(images, fmt.Sprint(last+1))
	if err := os.MkdirAll(taken, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taken, "taken"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.Engine.SendAgain(t.Context(), item.ID); err == nil {
		t.Fatal("Send again succeeded with a directory on the images of the copy")
	}

	message, ok := chatMessageByText(t, server, leadChat, text)
	if !ok || message.CancelledAt.Valid || message.DeliveredAt.Valid || !message.FailedAt.Valid {
		t.Errorf("message = %+v", message)
	}
	var owners int
	for _, other := range chatView(t, server, leadChat).Messages {
		if other.Author == "Owner" && other.Text == text {
			owners++
		}
	}
	if owners != 1 {
		t.Errorf("Owner messages = %d", owners)
	}
	if items := itemsOf(t, server, "Lead failed"); len(items) != 1 || items[0].ID != item.ID {
		t.Errorf("items = %+v", items)
	}

	server.Engine.SetClock(func() time.Time { return time.Now().Add(engine.FailedRetry + time.Minute) })

	waitForDeliveredMessage(t, server, leadChat, text)
	waitForNoFailedItems(t, server)
}

func TestDismissKeepsTheWaitingMessageAndTheNextTryDeliversIt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fail := filepath.Join(dataDir, "fail")
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := options + fmt.Sprintf("[[prompts]]\nshell = \"%s\"\nreply = [\"Seen\"]\n", failedThen(fail, ""))
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, script)
	cfg := testserver.Config(t, dataDir)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, server.Engine.AccessChecked)
	const text = "Plan the loyalty points API for the shop checkout\nSecond line."

	sendChat(t, server, leadChat, text)
	waitForFailedMessage(t, server, leadChat, text)
	item := waitForFailedItem(t, server, text)
	waitForChatEnd(t, server, leadChat)
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}

	if err := server.Engine.DismissByOwner(t.Context(), item.ID); err != nil {
		t.Fatal(err)
	}

	waitForFailedMessage(t, server, leadChat, text)
	message, ok := chatMessageByText(t, server, leadChat, text)
	if !ok || !message.FailedAt.Valid || message.CancelledAt.Valid || message.DeliveredAt.Valid {
		t.Errorf("message = %+v", message)
	}
	testkit.WaitFor(t, func() bool {
		for _, record := range chatLines(t, server, leadChat) {
			if record.Author == "Record" && record.Text == `You dismissed the failed message "Plan the loyalty points API for the shop…".` {
				return true
			}
		}
		return false
	})

	server.Engine.SetClock(func() time.Time { return time.Now().Add(engine.FailedRetry + time.Minute) })

	waitForDeliveredMessage(t, server, leadChat, text)
}

// Serial: it shortens HangTimeout.
func TestAHangOnTheSecondMessageOfASessionFailsThatMessage(t *testing.T) {
	shortHang(t)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, `
[[prompts]]
when = "Plan the loyalty API"
updates = ['{"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "Read."}}']
reply = ["Seen"]

[[prompts]]
when = "Add a plan price"
hang = true

[[prompts]]
when = "You had no activity"
hang = true
`, keepSessionOpen)
	const first = "Plan the loyalty API"
	const second = "Add a plan price"

	sendChat(t, server, leadChat, first)
	sendChat(t, server, leadChat, second)

	waitForDeliveredMessage(t, server, leadChat, first)
	failed := waitForFailedMessage(t, server, leadChat, second)
	if failed.DeliveredAt.Valid || failed.StoppedAt.Valid {
		t.Errorf("message = %+v", failed)
	}
	item := waitForFailedItem(t, server, second)
	if item.Title != `Lead of #12 Integrate loyalty plans did not get: "Add a plan price"` {
		t.Errorf("item = %+v", item)
	}
	message, ok := chatMessageByText(t, server, leadChat, first)
	if !ok || !message.DeliveredAt.Valid || message.FailedAt.Valid {
		t.Errorf("first message = %+v", message)
	}
	if got := len(itemsOf(t, server, "Lead failed")); got != 1 {
		t.Errorf("items = %d", got)
	}
}

// Serial: it changes runner.StartTimeout.
func TestSendAgainWhileARetryChatWaitsForItsStartGivesTheTextOneTime(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	fail := filepath.Join(dataDir, "fail")
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := options + fmt.Sprintf("[[prompts]]\nshell = \"%s\"\nreply = [\"Seen\"]\n", failedThen(fail, ""))
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	testkit.InstallFakeAgent(t, dataDir, script)
	cfg := testserver.Config(t, dataDir)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	testkit.WaitFor(t, server.Engine.AccessChecked)
	const text = "Plan the loyalty API"
	const next = "Add a plan price"

	sendChat(t, server, leadChat, text)
	waitForFailedMessage(t, server, leadChat, text)
	item := waitForFailedItem(t, server, text)
	waitForChatEnd(t, server, leadChat)
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	before := len(chatSessions(t, server, leadChat, engine.LeadRole))

	defer func(limit time.Duration) { runner.StartTimeout = limit }(runner.StartTimeout)
	runner.StartTimeout = 2 * time.Second
	testkit.InstallFakeAgent(t, dataDir, "hang_start = 1\n"+options+"[[prompts]]\nreply = [\"Seen\"]\n")

	sendChat(t, server, leadChat, next)
	testkit.WaitFor(t, func() bool {
		return len(chatSessions(t, server, leadChat, engine.LeadRole)) > before
	})
	if err := server.Engine.SendAgain(t.Context(), item.ID); err != nil {
		t.Fatal(err)
	}

	waitForNoFailedItems(t, server)
	old, ok := chatMessageByText(t, server, leadChat, text)
	if !ok || !old.CancelledAt.Valid || old.StoppedAt.Valid || old.DeliveredAt.Valid {
		t.Errorf("old message = %+v", old)
	}
	var copied store.ChatMessage
	for _, message := range chatView(t, server, leadChat).Messages {
		if message.Author == "Owner" && message.Text == text {
			copied = message
		}
	}
	if copied.ID == old.ID || copied.ID == 0 {
		t.Fatalf("copy = %+v", copied)
	}
	if !copied.BrowserID.Valid {
		t.Error("the copy has no browser id")
	}
	testkit.WaitFor(t, func() bool {
		for _, message := range chatView(t, server, leadChat).Messages {
			if message.ID == copied.ID {
				return message.DeliveredAt.Valid
			}
		}
		return false
	})
	for _, message := range chatView(t, server, leadChat).Messages {
		if message.ID == old.ID && message.DeliveredAt.Valid {
			t.Errorf("the old message was delivered")
		}
	}
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	var got int
	for _, prompt := range promptTexts(t, server, sessions[len(sessions)-1].ID) {
		if prompt == text {
			got++
		}
	}
	if got != 1 {
		t.Errorf("prompts that give the text = %d", got)
	}
}

func TestAFailedSaveTurnKeepsAQueuedEventForANewSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
when = "dispatch of #42"
reply = ["Seen two"]

[[prompts]]
when = "dispatch of #41"
reply = ["Seen one"]

[[prompts]]
when = "Save in the Workstream memory"
busy = "1s"
error = { code = -32000, message = "Failed to save the memory" }
`)

	dispatchTask(fake, 41, "Add plan model")
	first := waitForChatSession(t, server, leadChat, engine.LeadRole, func(session store.Session) bool {
		prompts := promptTexts(t, server, session.ID)
		return len(prompts) >= 2 && prompts[1] == savePrompt
	})

	dispatchTask(fake, 42, "Add plan route")
	testkit.WaitFor(t, func() bool { return server.Engine.ChatQueued(leadChat) == 1 })

	outcome := testkit.WaitForValue(t, func() (string, bool) {
		for _, event := range activities(t, server) {
			if strings.Contains(event.Text, "The Lead did not take this event") {
				return "activity", true
			}
		}
		for _, session := range chatSessions(t, server, leadChat, engine.LeadRole) {
			if session.ID == first.ID {
				continue
			}
			for _, prompt := range promptTexts(t, server, session.ID) {
				if strings.Contains(prompt, "dispatch of #42") {
					return "session", true
				}
			}
		}
		return "", false
	})
	if outcome != "session" {
		t.Fatal("a failed turn with no item sent the queued event to the activity feed")
	}

	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
}
