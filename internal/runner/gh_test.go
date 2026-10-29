package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// startGH runs the gh of the agent environment of dataDir with args and the environment variable
// MOBIUS_GH_TOKEN_URL=url when url is not empty. The real gh is the gh in realDir.
func startGH(t *testing.T, dataDir, realDir, url string, args ...string) (string, string, int) {
	t.Helper()
	return startGHWithWrites(t, dataDir, realDir, url, "", "", args...)
}

// startGHWithWrites is startGH with the environment variable MOBIUS_GH_WRITES_URL=writesURL when writesURL is not empty,
// and with stdin.
func startGHWithWrites(t *testing.T, dataDir, realDir, url, writesURL, stdin string, args ...string) (string, string, int) {
	t.Helper()
	return startGHWithMark(t, dataDir, realDir, url, writesURL, "", stdin, args...)
}

// startGHWithMark is startGHWithWrites with the environment variable MOBIUS_GH_MARK=mark when mark is not empty.
func startGHWithMark(t *testing.T, dataDir, realDir, url, writesURL, mark, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := command(context.Background(), filepath.Join(dataDir, "agent-env", "bin", "gh"), t.TempDir(), dataDir, realDir, url, writesURL, mark)
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
	dir := t.TempDir()
	script := "#!/bin/sh\nexec " + printenv + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
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

// writesServer answers each request with status, and gives the bodies of the requests.
func writesServer(t *testing.T, status int) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.WriteHeader(status)
		if status != http.StatusNoContent {
			_, _ = w.Write([]byte("no record\n"))
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(bodies)
	}
}

func TestTheGHSendsTheWritesOfEachSubcommandBeforeItRunsTheRealGH(t *testing.T) {
	dataDir := prepare(t)
	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("from a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(issue int, kind, labels, text string) string {
		return fmt.Sprintf(`{"repository":"owner/shop","issue":%d,"kind":%q,"labels":%s,"text":%q}`, issue, kind, labels, text)
	}
	for name, test := range map[string]struct {
		args  []string
		stdin string
		want  []string
	}{
		"edit labels of two issues": {
			args: []string{"issue", "edit", "12", "#13", "-R", "owner/shop", "--add-label", "a,b", "--remove-label=c", "--add-label", "d"},
			want: []string{
				write(12, "labeled", `["a"]`, ""), write(12, "labeled", `["b"]`, ""), write(12, "labeled", `["d"]`, ""), write(12, "unlabeled", `["c"]`, ""),
				write(13, "labeled", `["a"]`, ""), write(13, "labeled", `["b"]`, ""), write(13, "labeled", `["d"]`, ""), write(13, "unlabeled", `["c"]`, ""),
			},
		},
		"edit labels of a pull request URL": {
			args: []string{"pr", "edit", "https://github.com/owner/shop/pull/7", "--add-label", "a"},
			want: []string{write(7, "labeled", `["a"]`, "")},
		},
		"create": {
			args: []string{"issue", "create", "--repo", "github.com/owner/shop", "-t", "A title", "-l", "a", "--label", "b", "--body", "text"},
			want: []string{write(0, "created", `["a","b"]`, "A title")},
		},
		"comment": {
			args: []string{"issue", "comment", "5", "-R", "owner/shop", "--body", "a comment"},
			want: []string{write(5, "commented", `null`, "a comment")},
		},
		"comment of a pull request with an attached value": {
			args: []string{"pr", "comment", "5", "-R", "owner/shop", "-bshort"},
			want: []string{write(5, "commented", `null`, "short")},
		},
		"comment with a body file": {
			args: []string{"issue", "comment", "https://github.com/owner/shop/issues/5", "--body-file", file},
			want: []string{write(5, "commented", `null`, "from a file")},
		},
		"comment with stdin": {
			args:  []string{"issue", "comment", "5", "-R", "owner/shop", "-F", "-"},
			stdin: "from stdin",
			want:  []string{write(5, "commented", `null`, "from stdin")},
		},
		"close with a comment": {
			args: []string{"issue", "close", "5", "-R", "owner/shop", "--comment", "done", "--reason", "completed"},
			want: []string{write(5, "commented", `null`, "done")},
		},
		"no repository": {
			args: []string{"issue", "comment", "5", "--body", "a comment"},
		},
		"no write": {
			args: []string{"issue", "list", "-R", "owner/shop", "--label", "a"},
		},
		"gh api": {
			args: []string{"api", "-X", "POST", "repos/owner/shop/issues/5/comments", "-f", "body=text"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			writesURL, bodies := writesServer(t, http.StatusNoContent)

			stdout, stderr, code := startGHWithWrites(t, dataDir, scriptGH(t, `echo "$@"; /bin/cat`), tokenServer(t, http.StatusOK, "ghs_1"), writesURL, test.stdin, test.args...)

			if stderr != "" || code != 0 {
				t.Fatalf("gh = %q, %q, exit %d", stdout, stderr, code)
			}
			if want := strings.Join(test.args, " ") + "\n" + test.stdin; stdout != want {
				t.Errorf("the real gh printed %q, want %q", stdout, want)
			}
			got := bodies()
			if len(test.want) == 0 {
				if len(got) != 0 {
					t.Errorf("requests = %q", got)
				}
				return
			}
			if want := `{"writes":[` + strings.Join(test.want, ",") + `]}`; len(got) != 1 || got[0] != want {
				t.Errorf("requests = %q, want %q", got, want)
			}
		})
	}
}

func TestTheGHDoesNotRunTheRealGHWhenTheSendOfTheWritesFails(t *testing.T) {
	dataDir := prepare(t)
	writesURL, bodies := writesServer(t, http.StatusInternalServerError)

	stdout, stderr, code := startGHWithWrites(t, dataDir, scriptGH(t, `echo ran`), tokenServer(t, http.StatusOK, "ghs_1"), writesURL, "", "issue", "comment", "5", "-R", "owner/shop", "-b", "text")

	if stdout != "" || stderr != "no record\n" || code != 1 || len(bodies()) != 1 {
		t.Errorf("gh = %q, %q, exit %d, requests = %q", stdout, stderr, code, bodies())
	}
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
		// text is the text of the record of the comment, or "" for no such record.
		text string
		// repo is the repository of the record, or "" for owner/shop.
		repo string
	}{
		"issue create": {
			args: []string{"issue", "create", "-R", "owner/shop", "-t", "A title", "--body", "text"},
			want: "issue create -R owner/shop -t A title --body text\n\n" + mark + "\n",
		},
		"issue comment": {
			args: []string{"issue", "comment", "5", "-R", "owner/shop", "-b", "text"},
			want: "issue comment 5 -R owner/shop -b text\n\n" + mark + "\n",
			text: "text\n\n" + mark,
		},
		"pr comment with an attached value": {
			args: []string{"pr", "comment", "5", "-R", "owner/shop", "--body=text"},
			want: "pr comment 5 -R owner/shop --body=text\n\n" + mark + "\n",
			text: "text\n\n" + mark,
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
			text: "done\n\n" + mark,
		},
		"body file": {
			args: []string{"issue", "comment", "5", "-R", "owner/shop", "--body-file", file},
			want: "issue comment 5 -R owner/shop --body-file -\nfrom a file\n\n" + mark,
			text: "from a file\n\n" + mark,
		},
		"body file with an attached value": {
			args: []string{"issue", "comment", "5", "-R", "owner/shop", "-F" + file},
			want: "issue comment 5 -R owner/shop -F-\nfrom a file\n\n" + mark,
			text: "from a file\n\n" + mark,
		},
		"stdin": {
			args:  []string{"issue", "comment", "5", "-R", "owner/shop", "-F", "-"},
			stdin: "from stdin",
			want:  "issue comment 5 -R owner/shop -F -\nfrom stdin\n\n" + mark,
			text:  "from stdin\n\n" + mark,
		},
		"a body with the mark of another role": {
			args: []string{"issue", "comment", "5", "-R", "owner/shop", "-b", "text\n\n— Curator"},
			want: "issue comment 5 -R owner/shop -b text\n\n" + mark + "\n",
			text: "text\n\n" + mark,
		},
		"another repository": {
			args: []string{"issue", "comment", "5", "-R", "owner/garden", "-b", "text"},
			want: "issue comment 5 -R owner/garden -b text\n\n" + markElsewhere + "\n",
			repo: "owner/garden",
			text: "text\n\n" + markElsewhere,
		},
		"the same repository with another case": {
			args: []string{"issue", "comment", "5", "-R", "Owner/Shop", "-b", "text"},
			want: "issue comment 5 -R Owner/Shop -b text\n\n" + mark + "\n",
			repo: "Owner/Shop",
			text: "text\n\n" + mark,
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
			writesURL, bodies := writesServer(t, http.StatusNoContent)

			stdout, stderr, code := startGHWithMark(t, dataDir, scriptGH(t, `echo "$@"; /bin/cat`), tokenServer(t, http.StatusOK, "ghs_1"), writesURL, encoded, test.stdin, test.args...)

			if stderr != "" || code != 0 {
				t.Fatalf("gh = %q, %q, exit %d", stdout, stderr, code)
			}
			if stdout != test.want {
				t.Errorf("the real gh printed %q, want %q", stdout, test.want)
			}
			if test.text == "" {
				return
			}
			if test.repo == "" {
				test.repo = "owner/shop"
			}
			want := fmt.Sprintf(`{"writes":[{"repository":%q,"issue":5,"kind":"commented","labels":null,"text":%q}]}`, test.repo, test.text)
			if got := bodies(); len(got) != 1 || got[0] != want {
				t.Errorf("requests = %q, want %q", got, want)
			}
		})
	}
}
