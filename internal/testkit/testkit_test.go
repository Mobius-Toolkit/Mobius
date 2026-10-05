package testkit

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

func TestMain(m *testing.M) {
	Main(m)
}

func TestTheRunnerStartsTheFakeAgentOfAFakeHarness(t *testing.T) {
	dataDir := t.TempDir()
	work := t.TempDir()
	InstallFakeAgent(t, dataDir, `
[options]
model = ["sonnet", "opus"]
thought_level = ["low", "high"]
mode = ["default", "bypassPermissions"]

[[prompts]]
reply = ["Hello."]
shell = "pwd"
`)
	harnesses := filepath.Join(dataDir, "harnesses")
	var mu sync.Mutex
	var reply strings.Builder
	session, err := runner.Start(context.Background(), config.ClaudeCode, work, dataDir, harnesses+":"+os.Getenv("PATH"), "http://127.0.0.1:1/mcp/key", "", func(params json.RawMessage) {
		var notification acp.SessionNotification
		if err := json.Unmarshal(params, &notification); err != nil {
			t.Error(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if chunk := notification.Update.AgentMessageChunk; chunk != nil {
			reply.WriteString(chunk.Content.Text.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)

	if err := session.Configure(context.Background(), "opus", "low"); err != nil {
		t.Fatal(err)
	}
	stopReason, err := session.Prompt(context.Background(), "Hi.")
	if err != nil {
		t.Fatal(err)
	}

	if stopReason != acp.StopReasonEndTurn {
		t.Errorf("stop reason = %s", stopReason)
	}
	cwd, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if reply.String() != "Hello."+cwd+"\nexit 0" {
		t.Errorf("reply = %q", reply.String())
	}
	mu.Unlock()
	for file, want := range map[string]string{
		"mcp_url": "http://127.0.0.1:1/mcp/key",
		"pwd":     work + "\n",
		"env":     "GIT_TERMINAL_PROMPT=0\n",
	} {
		text, err := fs.ReadFile(os.DirFS(harnesses), file)
		if err != nil || !strings.Contains(string(text), want) {
			t.Errorf("%s = %q, %v", file, text, err)
		}
	}
}

func TestWaitForValueGivesTheValueOfTheFirstTrueCheck(t *testing.T) {
	calls := 0

	got := WaitForValue(t, func() (int, bool) {
		calls++
		return calls, calls == 3
	})

	if got != 3 {
		t.Errorf("value = %d", got)
	}
}
