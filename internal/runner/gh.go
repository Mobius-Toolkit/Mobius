package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
)

// GH is the main function of the gh of the agent environment. It runs with args and the environment of getenv,
// and gives the exit code.
//
// With MOBIUS_GH_TOKEN_URL, GH gets the token of the session from that URL, and runs the first gh on PATH that is not
// this program with the token in GH_TOKEN. The Lead gets the installation token of the App, and each other role gets
// the read-only installation token. With no MOBIUS_GH_TOKEN_URL, GH refuses to run.
//
// With MOBIUS_GH_WRITES_URL, GH sends the label changes, the new issues and the comments of args to that URL before it
// runs the real gh. When the send fails, it does not run the real gh.
//
// With MOBIUS_GH_MARK (the JSON of a GHMark), GH adds the mark line to each body that args gives to a gh issue create, gh issue comment, gh pr
// comment or gh issue edit, and to the comment of a gh issue close. The mark line is Elsewhere when the -R flag names another
// repository than Repository. The records and the real gh get the marked text.
func GH(args []string, getenv func(string) string) int {
	url := getenv(ghURLEnv)
	if url == "" {
		fmt.Fprintln(os.Stderr, "Do not use gh. Use the Mobius tools.")
		return 1
	}
	gh := FindGH(getenv("PATH"))
	if gh == "" {
		fmt.Fprintln(os.Stderr, "gh is not on PATH.")
		return 1
	}
	token, err := ghToken(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	stdin := io.Reader(os.Stdin)
	if encoded := getenv(ghMarkEnv); encoded != "" {
		var mark GHMark
		if err = json.Unmarshal([]byte(encoded), &mark); err == nil {
			args, stdin, err = markBodies(args, stdin, mark)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if writesURL := getenv(ghWritesURLEnv); writesURL != "" {
		var writes []mcp.GHWrite
		if writes, stdin, err = ghWrites(args, stdin); err == nil && len(writes) > 0 {
			err = sendGHWrites(writesURL, writes)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return runGH(gh, args, token, stdin)
}

// ghToken gives the body of the response to url. The body of a response with an error status is the error.
func ghToken(url string) (string, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", errors.New(strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

// runGH runs the real gh with args, token and stdin, and gives its exit code.
func runGH(gh string, args []string, token string, stdin io.Reader) int {
	cmd := exec.Command(gh)
	cmd.Args = append(cmd.Args, args...)
	cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	cmd.Stdin = stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		fmt.Fprintf(os.Stderr, "%s: %v\n", gh, err)
		return 1
	}
	return cmd.ProcessState.ExitCode()
}
