package engine_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func sendChatID(t *testing.T, server *testserver.Server, id, text string, images ...engine.Image) {
	t.Helper()
	if err := server.Engine.SendChat(t.Context(), leadChat, id, text, images); err != nil {
		t.Fatal(err)
	}
}

func imageDirs(t *testing.T, dataDir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "images"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestASecondMessageWithTheSameBrowserIDMakesNoMessageAndNoTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectScript(t, fake, imageReading+options+"[[prompts]]\nreply = [\"Ok\"]\n[[prompts]]\nreply = [\"Ok\"]\n", keepSessionOpen)

	sendChatID(t, server, "one", "First", pngImage)
	waitForChat(t, server, leadChat, "Lead", "Ok"+imageReply(pngImage))
	sendChatID(t, server, "one", "First", pngImage)
	sendChatID(t, server, "two", "Second")
	testkit.WaitFor(t, func() bool { return len(chatLines(t, server, leadChat)) == 4 })

	want := []chatLine{{"Owner", "First"}, {"Lead", "Ok" + imageReply(pngImage)}, {"Owner", "Second"}, {"Lead", "Ok"}}
	if got := chatLines(t, server, leadChat); !reflect.DeepEqual(got, want) {
		t.Errorf("chat = %+v", got)
	}
	session := chatSessions(t, server, leadChat, engine.LeadRole)[0]
	if got := len(promptTexts(t, server, session.ID)); got != 2 {
		t.Errorf("prompts = %d", got)
	}
	if got := imageDirs(t, dataDir); got != 1 {
		t.Errorf("image directories = %d", got)
	}
}

func TestTheDeliveryTimeOfAnOwnerMessageIsEmptyBeforeTheTurnAndSetAfterItsStart(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, options+"[[prompts]]\nreply = [\"Ok\"]\n", keepSessionOpen)
	changes, stop := server.Engine.Listen()
	defer stop()

	sendChatID(t, server, "one", "First")

	isOwner := func(change engine.Change) bool { return change.Message != nil && change.Message.Author == "Owner" }
	added := waitForChange(t, changes, isOwner).Message
	if added.BrowserID.String != "one" || added.DeliveredAt.Valid {
		t.Errorf("added message = %+v", added)
	}
	delivered := waitForChange(t, changes, isOwner).Message
	if delivered.ID != added.ID || !delivered.DeliveredAt.Valid {
		t.Errorf("delivered message = %+v", delivered)
	}
	view := chatView(t, server, leadChat)
	if got := view.Messages[0].DeliveredAt; got != delivered.DeliveredAt {
		t.Errorf("stored delivery time = %v, event = %v", got, delivered.DeliveredAt)
	}
}

func TestASecondTurnForAnOwnerMessageKeepsItsDeliveryTime(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectScript(t, fake, options+"[[prompts]]\nreply = [\"Ok\"]\n", keepSessionOpen)
	sendChatID(t, server, "one", "First")
	waitForChat(t, server, leadChat, "Lead", "Ok")
	first := chatView(t, server, leadChat).Messages[0]
	changes, stop := server.Engine.Listen()
	defer stop()

	if err := server.Engine.DeliverMessage(t.Context(), first.ID); err != nil {
		t.Fatal(err)
	}

	if got := chatView(t, server, leadChat).Messages[0]; got.DeliveredAt != first.DeliveredAt {
		t.Errorf("delivery time = %v, want %v", got.DeliveredAt, first.DeliveredAt)
	}
	select {
	case change := <-changes:
		if change.Message != nil {
			t.Errorf("change = %+v", change.Message)
		}
	default:
	}
}

func TestTwoMessagesWithTheSameBrowserIDAtTheSameTimeMakeOneMessage(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectScript(t, fake, imageReading+options+"[[prompts]]\nreply = [\"Ok\"]\n", keepSessionOpen)
	var sent sync.WaitGroup

	for range 5 {
		sent.Go(func() { sendChatID(t, server, "one", "First", pngImage) })
	}
	sent.Wait()

	waitForChat(t, server, leadChat, "Lead", "Ok"+imageReply(pngImage))
	if got := len(chatLines(t, server, leadChat)); got != 2 {
		t.Errorf("messages = %d", got)
	}
	if got := imageDirs(t, dataDir); got != 1 {
		t.Errorf("image directories = %d", got)
	}
}
