package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
)

// GH is the main function of the gh of the agent environment. It runs with args and the environment of getenv,
// and gives the exit code.
//
// With MOBIUS_GH_SESSION_URL, GH gets the token of the session from that URL, and runs the first gh on PATH that is not
// this program with the token in GH_TOKEN. The Lead gets the installation token of the App, and each other role gets
// the read-only installation token. With no MOBIUS_GH_SESSION_URL, GH refuses to run.
//
// With MOBIUS_GH_MARK (the JSON of a GHMark), GH adds the mark line to each body that args gives to a gh issue create, gh issue comment, gh pr
// comment or gh issue edit, and to the comment of a gh issue close. The mark line is Elsewhere when the -R flag names another
// repository than Repository. The real gh gets the marked text.
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
	token, err := responseBody(http.MethodGet, url)
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
	return runGH(gh, args, token, stdin)
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
