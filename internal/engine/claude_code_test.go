package engine_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const replyHello = "[[prompts]]\nreply = [\"Hello\"]\n"

var chromeMeta = map[string]any{"claudeCode": map[string]any{"options": map[string]any{"extraArgs": map[string]any{"chrome": nil}}}}

// browserText gives the text of the file claude_code.md.
func browserText(t *testing.T) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("prompts", "claude_code.md"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(text))
}

// sessionMeta gives the _meta of the last session/new that the fake agent got with the Mobius MCP server. It is empty
// when the request had no _meta.
func sessionMeta(t *testing.T, dataDir string) string {
	t.Helper()
	meta, err := fs.ReadFile(os.DirFS(filepath.Join(dataDir, "harnesses")), "session_meta")
	if err != nil {
		t.Fatal(err)
	}
	return string(meta)
}

func assertChromeMeta(t *testing.T, dataDir string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(sessionMeta(t, dataDir)), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, chromeMeta) {
		t.Errorf("_meta = %v", got)
	}
}

func TestALeadSessionOnClaudeCodeGetsChromeAndTheBrowserText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, replyHello)

	sendChat(t, server, leadChat, "Plan the loyalty API")
	prompt := promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]

	assertChromeMeta(t, dataDir)
	if !strings.Contains(prompt, browserText(t)) {
		t.Errorf("prompt has no browser text: %s", prompt)
	}
}

func TestACoordinatorSessionOnClaudeCodeGetsChromeAndTheBrowserText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, replyHello)

	sendChat(t, server, coordinatorChat, "Which Workstreams are open?")
	prompt := testkit.WaitForValue(t, func() (string, bool) {
		prompts := coordinatorPrompts(t, server)
		return strings.Join(prompts, ""), len(prompts) > 0
	})

	assertChromeMeta(t, dataDir)
	if !strings.Contains(prompt, browserText(t)) {
		t.Errorf("prompt has no browser text: %s", prompt)
	}
}

func TestAnImplementerSessionOnClaudeCodeGetsNoChrome(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, replyHello, func(cfg *config.Config) {
		cfg.Roles.Implementer.Harness = config.ClaudeCode
	})

	session := run(t, server, implementerSpec(t, server, fake, 41), "Store plans in cents.")

	if meta := sessionMeta(t, dataDir); meta != "" {
		t.Errorf("_meta = %s", meta)
	}
	if prompt := promptTexts(t, server, session)[0]; strings.Contains(prompt, browserText(t)) {
		t.Errorf("prompt has the browser text: %s", prompt)
	}
}

func TestALeadSessionOnAnotherHarnessGetsNoChromeAndNoBrowserText(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectWith(t, fake, replyHello, func(cfg *config.Config) {
		cfg.Roles.Lead.Harness = config.Devin
	})

	sendChat(t, server, leadChat, "Plan the loyalty API")
	prompt := promptTexts(t, server, endedChatSession(t, server, 0).ID)[0]

	if meta := sessionMeta(t, dataDir); meta != "" {
		t.Errorf("_meta = %s", meta)
	}
	if strings.Contains(prompt, browserText(t)) {
		t.Errorf("prompt has the browser text: %s", prompt)
	}
}
