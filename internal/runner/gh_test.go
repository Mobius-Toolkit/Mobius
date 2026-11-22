package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// startGH runs the gh of the agent environment of dataDir with args and the environment variable
// MOBIUS_GH_SESSION_URL=url when url is not empty. The real gh is the gh in realDir.
func startGH(t *testing.T, dataDir, realDir, url string, args ...string) (string, string, int) {
	t.Helper()
	return startGHWithMark(t, dataDir, realDir, url, "", "", args...)
}

// startGHWithMark is startGH with the environment variable MOBIUS_GH_MARK=mark when mark is not empty, and with stdin.
func startGHWithMark(t *testing.T, dataDir, realDir, url, mark, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := command(context.Background(), filepath.Join(dataDir, "agent-env", "bin", "gh"), t.TempDir(), dataDir, realDir, url, mark, "")
	cmd.Args = append(cmd.Args, args...)
	cmd.Stdin = strings.NewReader(stdin)
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
	return scriptGH(t, "exec "+printenv+` "$@"`)
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

func TestTheGHOfADSHSessionGetsTheSessionURLThroughTheEnvironmentFilter(t *testing.T) {
	dataDir := prepare(t)
	realDir := realGH(t)
	tokenURL := tokenServer(t, http.StatusOK, "ghu_1")
	// dsh starts each shell command from scrubbedParentEnv of @deepseek-ai/dsh-subprocess. It drops each name that
	// matches SENSITIVE_ENV_PATTERN = /KEY|PASSWORD|SECRET|TOKEN/i, and each name with the prefix DSH_. This script
	// gives the command the environment that dsh keeps.
	filter := filepath.Join(t.TempDir(), "dsh-environment")
	filterScript := `#!/bin/sh
set -f
for name in $(env | cut -d= -f1); do
  upper=$(printf '%s' "$name" | tr '[:lower:]' '[:upper:]')
  case "$upper" in
    *KEY*|*PASSWORD*|*SECRET*|*TOKEN*|DSH_*) unset "$name" ;;
  esac
done
exec gh GH_TOKEN
`
	if err := os.WriteFile(filter, []byte(filterScript), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.InstallFakeHarness(t, dataDir, Program(config.DSH), fmt.Sprintf("[[prompts]]\nshell = %q\n", "sh '"+filter+"'"))
	path := filepath.Join(dataDir, "harnesses") + string(filepath.ListSeparator) + realDir + string(filepath.ListSeparator) + "/usr/bin:/bin"

	var reply strings.Builder
	session, err := Start(t.Context(), config.DSH, 0, false, false, dataDir, dataDir, path, "", nil, tokenURL, "", "", func(params json.RawMessage, _ bool) {
		var notification struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if err := json.Unmarshal(params, &notification); err != nil {
			t.Error(err)
		}
		if notification.Update.SessionUpdate == "agent_message_chunk" {
			reply.WriteString(notification.Update.Content.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)

	if _, err := session.Prompt(t.Context(), "Run gh.", nil); err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(reply.String()); got != "ghu_1\nexit 0" {
		t.Errorf("reply = %q, want the token of the session and exit 0", got)
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

// scriptGH makes a script the real gh.
func scriptGH(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "gh")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"+script+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chmod := exec.Command("chmod", "+x", "gh")
	chmod.Dir = dir
	if err := chmod.Run(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTheGHAddsTheMarkToTheBodyOfEachSubcommand(t *testing.T) {
	dataDir := prepare(t)
	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("from a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const mark = "— Lead of #14 The title"
	const markElsewhere = "— Lead of owner/shop#14 The title"
	encoded, err := GHMark{Repository: "owner/shop", Here: mark, Elsewhere: markElsewhere}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		args  []string
		stdin string
		// want is what the real gh prints: its args, then its stdin.
		want string
	}{
		"issue create": {
			args: []string{"issue", "create", "-R", "owner/shop", "-t", "A title", "--body", "text"},
			want: "issue create -R owner/shop -t A title --body text\n\n" + mark + "\n",
		},
		"issue comment": {
			args: []string{"issue", "comment", "5", "-R", "owner/shop", "-b", "text"},
			want: "issue comment 5 -R owner/shop -b text\n\n" + mark + "\n",
		},
		"pr comment with an attached value": {
			args: []string{"pr", "comment", "5", "-R", "owner/shop", "--body=text"},
			want: "pr comment 5 -R owner/shop --body=text\n\n" + mark + "\n",
		},
		"issue edit": {
			args: []string{"issue", "edit", "5", "-R", "owner/shop", "-btext"},
			want: "issue edit 5 -R owner/shop -btext\n\n" + mark + "\n",
		},
		"a second issue edit": {
			args: []string{"issue", "edit", "5", "-R", "owner/shop", "-b", "text\n\n" + mark},
			want: "issue edit 5 -R owner/shop -b text\n\n" + mark + "\n",
		},
		"issue close": {
			args: []string{"issue", "close", "5", "-R", "owner/shop", "-c", "done"},
			want: "issue close 5 -R owner/shop -c done\n\n" + mark + "\n",
		},
		"body file": {
			args: []string{"issue", "comment", "5", "-R", "owner/shop", "--body-file", file},
			want: "issue comment 5 -R owner/shop --body-file -\nfrom a file\n\n" + mark,
		},
		"body file with an attached value": {
			args: []string{"issue", "comment", "5", "-R", "owner/shop", "-F" + file},
			want: "issue comment 5 -R owner/shop -F-\nfrom a file\n\n" + mark,
		},
		"stdin": {
			args:  []string{"issue", "comment", "5", "-R", "owner/shop", "-F", "-"},
			stdin: "from stdin",
			want:  "issue comment 5 -R owner/shop -F -\nfrom stdin\n\n" + mark,
		},
		"a body with the mark of another role": {
			args: []string{"issue", "comment", "5", "-R", "owner/shop", "-b", "text\n\n— Curator"},
			want: "issue comment 5 -R owner/shop -b text\n\n" + mark + "\n",
		},
		"another repository": {
			args: []string{"issue", "comment", "5", "-R", "owner/garden", "-b", "text"},
			want: "issue comment 5 -R owner/garden -b text\n\n" + markElsewhere + "\n",
		},
		"the same repository with another case": {
			args: []string{"issue", "comment", "5", "-R", "Owner/Shop", "-b", "text"},
			want: "issue comment 5 -R Owner/Shop -b text\n\n" + mark + "\n",
		},
		"edit with labels only": {
			args: []string{"issue", "edit", "5", "-R", "owner/shop", "--add-label", "a"},
			want: "issue edit 5 -R owner/shop --add-label a\n",
		},
		"issue list": {
			args: []string{"issue", "list", "-R", "owner/shop", "--search", "-b x"},
			want: "issue list -R owner/shop --search -b x\n",
		},
		"gh api": {
			args: []string{"api", "-X", "POST", "repos/owner/shop/issues/5/comments", "-f", "body=text"},
			want: "api -X POST repos/owner/shop/issues/5/comments -f body=text\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code := startGHWithMark(t, dataDir, scriptGH(t, `echo "$@"; /bin/cat`), tokenServer(t, http.StatusOK, "ghs_1"), encoded, test.stdin, test.args...)

			if stderr != "" || code != 0 {
				t.Fatalf("gh = %q, %q, exit %d", stdout, stderr, code)
			}
			if stdout != test.want {
				t.Errorf("the real gh printed %q, want %q", stdout, test.want)
			}
		})
	}
}
