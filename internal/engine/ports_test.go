package engine_test

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// portStatus asks the port route at url for one port, and gives the status.
func portStatus(t *testing.T, url string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url+"?count=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

func requireTwoPortsOfTheRange(t *testing.T, file string) {
	t.Helper()
	lines := strings.Fields(readText(t, file))
	if len(lines) != 2 || lines[0] == lines[1] {
		t.Fatalf("ports = %q", lines)
	}
	for _, line := range lines {
		if port, err := strconv.Atoi(line); err != nil || port < 20000 || port > 32767 {
			t.Errorf("port = %q", line)
		}
	}
}

func TestAnAgentSessionGetsPortsFromMobiusPortUntilItEnds(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	ports := filepath.Join(t.TempDir(), "ports")
	server, dataDir := connectScript(t, fake, options+fmt.Sprintf("[[prompts]]\nshell = \"mobius-port 2 > '%s'\"\n", ports), noChange)

	sendChat(t, server, leadChat, "Plan the loyalty API")

	testkit.WaitFor(t, func() bool { return strings.Count(readTextIfThere(ports), "\n") == 2 })
	requireTwoPortsOfTheRange(t, ports)
	env, err := fs.ReadFile(os.DirFS(filepath.Join(dataDir, "harnesses")), "env")
	if err != nil {
		t.Fatal(err)
	}
	var url string
	for line := range strings.Lines(string(env)) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "MOBIUS_PORT_URL="); ok {
			url = value
		}
	}
	if url == "" {
		t.Fatalf("env = %s", env)
	}
	testkit.WaitFor(t, func() bool { return portStatus(t, url) == http.StatusNotFound })
}

func readTextIfThere(file string) string {
	text, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return ""
	}
	return string(text)
}

func TestTheInitScriptGetsPortsFromMobiusPortAndTheKeyEndsWithTheScript(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, commits, noChange)
	ports, url := filepath.Join(dataDir, "ports"), filepath.Join(dataDir, "url")
	fake.SetInit(shop, fmt.Sprintf("mobius-port 2 > '%s' && printenv MOBIUS_PORT_URL > '%s'", ports, url))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForSteps(t, server, "init", 1)
	requireTwoPortsOfTheRange(t, ports)
	if status := portStatus(t, strings.TrimSpace(readText(t, url))); status != http.StatusNotFound {
		t.Errorf("status of the port key of the init script = %d", status)
	}
}

func TestTheCheckScriptGetsPortsFromMobiusPortAndTheKeyEndsWithTheScript(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	server, _ := connectTaskIn(t, fake, dataDir, leadStarts, commits, noChange)
	ports, url := filepath.Join(dataDir, "ports"), filepath.Join(dataDir, "url")
	fake.SetCheck(shop, fmt.Sprintf("mobius-port 2 > '%s' && printenv MOBIUS_PORT_URL > '%s'", ports, url))

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	waitForSteps(t, server, "check", 1)
	requireTwoPortsOfTheRange(t, ports)
	if status := portStatus(t, strings.TrimSpace(readText(t, url))); status != http.StatusNotFound {
		t.Errorf("status of the port key of the check script = %d", status)
	}
}
