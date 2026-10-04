package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

// GH is the main function of the gh of the agent environment. It runs with args and the environment of getenv,
// and gives the exit code.
//
// With MOBIUS_GH_TOKEN_URL, GH gets the token of the Owner from that URL, and runs the first gh on PATH that is not
// this program with the token in GH_TOKEN. With no MOBIUS_GH_TOKEN_URL, GH refuses to run.
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
	return runGH(gh, args, token)
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

// runGH runs the real gh with args and token, and gives its exit code.
func runGH(gh string, args []string, token string) int {
	cmd := exec.Command(gh)
	cmd.Args = append(cmd.Args, args...)
	cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	cmd.Stdin = os.Stdin
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
