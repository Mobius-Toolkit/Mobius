package runner

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
)

// portServer serves the ports of the keys of the server. It gives the server and the address of the listener.
func portServer(t *testing.T) (*mcp.Server, string) {
	t.Helper()
	server := mcp.New()
	mux := http.NewServeMux()
	server.Register(mux)
	listener := httptest.NewServer(mux)
	t.Cleanup(listener.Close)
	return server, strings.TrimPrefix(listener.URL, "http://")
}

func runPort(url string, args ...string) (string, string, int) {
	var stdout, stderr strings.Builder
	getenv := func(name string) string {
		if name == portURLEnv {
			return url
		}
		return ""
	}
	code := Port(args, getenv, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func requireFreePorts(t *testing.T, output string, count int) {
	t.Helper()
	lines := strings.Fields(output)
	if len(lines) != count || strings.Count(output, "\n") != count {
		t.Fatalf("output = %q, want %d lines", output, count)
	}
	for i, line := range lines {
		port, err := strconv.Atoi(line)
		if err != nil || port < 20000 || port > 32767 || slices.Contains(lines[:i], line) {
			t.Errorf("output = %q", output)
			continue
		}
		listener, err := net.Listen("tcp", "127.0.0.1:"+line)
		if err != nil {
			t.Errorf("port %s: %v", line, err)
			continue
		}
		_ = listener.Close()
	}
}

func TestPrepareMakesTheMobiusPortOfTheAgentEnvironmentALinkToThisProgram(t *testing.T) {
	dataDir := prepare(t)
	if err := Prepare(dataDir); err != nil {
		t.Fatal(err)
	}

	target, err := os.Readlink(filepath.Join(dataDir, "agent-env", "bin", "mobius-port"))
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil || target != self {
		t.Errorf("link = %s, want %s: %v", target, self, err)
	}
}

func TestPortGivesOnePortWithNoArgumentAndTheNumberOfPortsOfTheCount(t *testing.T) {
	server, addr := portServer(t)
	url := mcp.PortURL(addr, server.OpenPorts())

	for count, args := range map[int][]string{1: nil, 4: {"4"}} {
		stdout, stderr, code := runPort(url, args...)

		if code != 0 || stderr != "" {
			t.Errorf("mobius-port %v = exit %d, %q", args, code, stderr)
		}
		requireFreePorts(t, stdout, count)
	}
}

func TestPortRefusesABadCountWithExitCode2AndReservesNoPort(t *testing.T) {
	var requests atomic.Int32
	counter := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(counter.Close)

	for _, args := range [][]string{{"0"}, {"-1"}, {"two"}, {"1.5"}, {""}, {"1", "2"}} {
		stdout, stderr, code := runPort(counter.URL, args...)

		if stdout != "" || stderr == "" || code != 2 {
			t.Errorf("mobius-port %q = %q, %q, exit %d", args, stdout, stderr, code)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests = %d", got)
	}
}

func TestPortWithNoURLRefusesToRun(t *testing.T) {
	stdout, stderr, code := runPort("")

	if stdout != "" || stderr != "mobius-port runs only in a script or an agent session of Mobius.\n" || code != 1 {
		t.Errorf("mobius-port = %q, %q, exit %d", stdout, stderr, code)
	}
}

func TestPortWritesTheErrorOfTheServerWithExitCode1(t *testing.T) {
	server, addr := portServer(t)
	closed := server.OpenPorts()
	server.Close(closed)
	exhausted := server.OpenPorts()

	for _, c := range []struct{ url, count, want string }{
		{mcp.PortURL(addr, closed), "1", "404 page not found\n"},
		{mcp.PortURL(addr, exhausted), "12769", "the range 20000-32767 has no 12769 free ports\n"},
	} {
		stdout, stderr, code := runPort(c.url, c.count)

		if stdout != "" || stderr != c.want || code != 1 {
			t.Errorf("mobius-port %s = %q, %q, exit %d", c.count, stdout, stderr, code)
		}
	}
}

func TestACommandWithAPortURLHasTheURLAndNoInheritedURL(t *testing.T) {
	t.Setenv("MOBIUS_PORT_URL", "http://127.0.0.1:1/ports/parent")

	without := command(context.Background(), "/bin/agent", "/work", t.TempDir(), "/usr/bin:/bin", "", "", "", "")
	with := command(context.Background(), "/bin/agent", "/work", t.TempDir(), "/usr/bin:/bin", "", "", "", "http://127.0.0.1:6363/ports/key")

	for _, entry := range without.Env {
		if strings.HasPrefix(entry, "MOBIUS_PORT_URL=") {
			t.Errorf("env has %s", entry)
		}
	}
	if !slices.Contains(with.Env, "MOBIUS_PORT_URL=http://127.0.0.1:6363/ports/key") || slices.Contains(with.Env, "MOBIUS_PORT_URL=http://127.0.0.1:1/ports/parent") {
		t.Errorf("env = %v", with.Env)
	}
}

func TestACheckScriptGetsFreePortsFromMobiusPort(t *testing.T) {
	dataDir := prepare(t)
	server, addr := portServer(t)
	key := server.OpenPorts()
	worktree := t.TempDir()
	if err := os.Mkdir(filepath.Join(worktree, ".mobius"), 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(worktree, ".mobius", "check")
	if err := os.WriteFile(file, []byte("#!/bin/sh\nset -- $(mobius-port 3)\necho \"$1\"\necho \"$2\"\necho \"$3\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, scriptMode); err != nil {
		t.Fatal(err)
	}

	got, passed, err := Check(context.Background(), dataDir, worktree, os.Getenv("PATH"), mcp.PortURL(addr, key), time.Minute)

	if err != nil || !passed {
		t.Fatalf("Check = %q, %v, %v", got, passed, err)
	}
	requireFreePorts(t, got, 3)
}
