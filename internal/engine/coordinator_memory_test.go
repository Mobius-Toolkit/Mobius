package engine_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestTheSecondCoordinatorSessionHasTheFileAndTheMemoryOfTheFirstSession(t *testing.T) {
	t.Parallel()
	server, dataDir := connect(t, testkit.NewFakeGitHub(t), `
[[prompts]]
when = "Use what you learned."
reply = ["Second done."]

[[prompts]]
when = "Learn the unit."
reply = ["First done."]
shell = "printf -- '- [Unit](unit.md)\n' > MEMORY.md && printf 'Cents.\n' > unit.md"
`)

	sendChat(t, server, coordinatorChat, "Learn the unit.")

	first := waitForChatSession(t, server, coordinatorChat, engine.CoordinatorRole, func(session store.Session) bool { return session.EndedAt.Valid })
	folder := filepath.Join(dataDir, "coordinators", "owner")
	if note, err := fs.ReadFile(os.DirFS(folder), "unit.md"); err != nil || string(note) != "Cents.\n" {
		t.Errorf("unit.md = %q, %v", note, err)
	}
	if prompt := promptTexts(t, server, first.ID)[0]; strings.Contains(prompt, "- [Unit](unit.md)") {
		t.Errorf("the first prompt of the first session has the note: %s", prompt)
	}

	sendChat(t, server, coordinatorChat, "Use what you learned.")

	waitForChat(t, server, coordinatorChat, "Coordinator", "Second done.")
	second := chatSessions(t, server, coordinatorChat, engine.CoordinatorRole)[1]
	if want := "# MEMORY.md\n\n- [Unit](unit.md)\n\n"; !strings.Contains(promptTexts(t, server, second.ID)[0], want) {
		t.Errorf("prompt = %s", promptTexts(t, server, second.ID)[0])
	}
	if note, err := fs.ReadFile(os.DirFS(folder), "unit.md"); err != nil || string(note) != "Cents.\n" {
		t.Errorf("unit.md after the second session = %q, %v", note, err)
	}
}

func TestTheCoordinatorsOfTwoOrganizationsHaveDifferentDirectories(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository("other/thing")
	server, dataDir := connect(t, fake, `
[[prompts]]
when = "Note for other."
reply = ["Other done."]
shell = "printf 'other\n' > organization.txt"

[[prompts]]
when = "Note for owner."
reply = ["Owner done."]
shell = "printf 'owner\n' > organization.txt"
`)

	sendChat(t, server, coordinatorChat, "Note for owner.")
	sendChat(t, server, engine.ChatKey{Organization: "other"}, "Note for other.")

	waitForChat(t, server, coordinatorChat, "Coordinator", "Owner done.exit 0")
	waitForChat(t, server, engine.ChatKey{Organization: "other"}, "Coordinator", "Other done.exit 0")
	for _, organization := range []string{"owner", "other"} {
		note, err := fs.ReadFile(os.DirFS(filepath.Join(dataDir, "coordinators", organization)), "organization.txt")
		if err != nil || string(note) != organization+"\n" {
			t.Errorf("organization.txt of %s = %q, %v", organization, note, err)
		}
	}
}
