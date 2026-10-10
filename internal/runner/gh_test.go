package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// startGH runs the gh of the agent environment of dataDir with args and the environment variable
// MOBIUS_GH_TOKEN_URL=url when url is not empty. The real gh is the gh in realDir.
func startGH(t *testing.T, dataDir, realDir, url string, args ...string) (string, string, int) {
	t.Helper()
	cmd := command(context.Background(), filepath.Join(dataDir, "agent-env", "bin", "gh"), t.TempDir(), dataDir, realDir, url)
	cmd.Args = append(cmd.Args, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// realGH makes printenv the real gh, so "gh GH_TOKEN" prints the token of the real gh.
func realGH(t *testing.T) string {
	t.Helper()
	printenv, err := exec.LookPath("printenv")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(printenv, filepath.Join(dir, "gh")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func prepare(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := Prepare(dataDir); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func tokenServer(t *testing.T, status int, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestPrepareMakesTheGHOfTheAgentEnvironmentALinkToThisProgram(t *testing.T) {
	dataDir := prepare(t)
	if err := Prepare(dataDir); err != nil {
		t.Fatal(err)
	}

	target, err := os.Readlink(filepath.Join(dataDir, "agent-env", "bin", "gh"))
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil || target != self {
		t.Errorf("link = %s, want %s: %v", target, self, err)
	}
}

func TestTheGHOfASessionWithNoTokenURLRefusesToRun(t *testing.T) {
	dataDir := prepare(t)

	stdout, stderr, code := startGH(t, dataDir, realGH(t), "", "GH_TOKEN")

	if stdout != "" || stderr != "Do not use gh. Use the Mobius tools.\n" || code != 1 {
		t.Errorf("gh = %q, %q, exit %d", stdout, stderr, code)
	}
}

func TestTheGHRunsTheRealGHWithTheTokenOfTheURL(t *testing.T) {
	dataDir := prepare(t)

	stdout, stderr, code := startGH(t, dataDir, realGH(t), tokenServer(t, http.StatusOK, "ghu_1"), "GH_TOKEN")

	if stdout != "ghu_1\n" || stderr != "" || code != 0 {
		t.Errorf("gh = %q, %q, exit %d", stdout, stderr, code)
	}
}

func TestTheGHPrintsTheErrorOfTheTokenURL(t *testing.T) {
	dataDir := prepare(t)

	stdout, stderr, code := startGH(t, dataDir, realGH(t), tokenServer(t, http.StatusInternalServerError, "Tell the Owner to authorize the App.\n"), "GH_TOKEN")

	if stdout != "" || stderr != "Tell the Owner to authorize the App.\n" || code != 1 {
		t.Errorf("gh = %q, %q, exit %d", stdout, stderr, code)
	}
}

// The real gh of a PATH with only links to this program would be the gh itself.
func TestTheGHNeverRunsItself(t *testing.T) {
	dataDir := prepare(t)
	other := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(other, "gh")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "agent-env", "bin") + string(filepath.ListSeparator) + other

	stdout, stderr, code := startGH(t, dataDir, path, tokenServer(t, http.StatusOK, "ghu_1"), "GH_TOKEN")

	if stdout != "" || stderr != "gh is not on PATH.\n" || code != 1 {
		t.Errorf("gh = %q, %q, exit %d", stdout, stderr, code)
	}
}

func TestFindGHSkipsEachGHThatIsThisProgram(t *testing.T) {
	dataDir := prepare(t)
	realDir := realGH(t)
	path := filepath.Join(dataDir, "agent-env", "bin") + string(filepath.ListSeparator) + realDir

	if got := FindGH(path); got != filepath.Join(realDir, "gh") {
		t.Errorf("gh = %q", got)
	}
	if got := FindGH(filepath.Join(dataDir, "agent-env", "bin")); got != "" {
		t.Errorf("gh = %q", got)
	}
}

func TestTheHandlerGivesTheRawParamsOfEachUpdate(t *testing.T) {
	var got []string
	handle := handler(func(params json.RawMessage, _ bool) { got = append(got, string(params)) }, &wire{})
	params := `{"sessionId":"s","update":{"sessionUpdate":"a_future_kind","x":[1, 2]}}`

	result, err := handle(context.Background(), "session/update", json.RawMessage(params))

	if result != nil || err != nil || len(got) != 1 || got[0] != params {
		t.Errorf("result = %v, %v, updates = %q", result, err, got)
	}
	if _, err := handle(context.Background(), "fs/read_text_file", json.RawMessage(`{}`)); err == nil || err.Code != -32601 {
		t.Errorf("error = %v", err)
	}
}
