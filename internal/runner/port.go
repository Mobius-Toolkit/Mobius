package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// portURLEnv is the environment variable of a script or an agent process that gives the URL of its free ports.
const portURLEnv = "MOBIUS_PORT_URL"

// Port is the main function of mobius-port of the agent environment. It runs with args and the environment of getenv,
// writes the reserved ports to stdout and the errors to stderr, and gives the exit code.
//
// The only argument is the number of ports, 1 by default. A number that is not a positive integer gives the exit code 2.
// Port gets the ports from the URL in MOBIUS_PORT_URL. Mobius holds each port for the script or the session until it
// ends.
func Port(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	count := 1
	if len(args) > 1 {
		return fail(stderr, 2, "Usage: mobius-port [COUNT]")
	}
	if len(args) == 1 {
		var err error
		if count, err = strconv.Atoi(args[0]); err != nil || count < 1 {
			return fail(stderr, 2, fmt.Sprintf("mobius-port: the count must be a positive integer, not %q.", args[0]))
		}
	}
	endpoint := getenv(portURLEnv)
	if endpoint == "" {
		return fail(stderr, 1, "mobius-port runs only in a script or an agent session of Mobius.")
	}
	ports, err := reservePorts(endpoint + "?" + url.Values{"count": {strconv.Itoa(count)}}.Encode())
	if err != nil {
		return fail(stderr, 1, err.Error())
	}
	if _, err := io.WriteString(stdout, ports); err != nil {
		return fail(stderr, 1, err.Error())
	}
	return 0
}

func fail(stderr io.Writer, code int, text string) int {
	_, _ = io.WriteString(stderr, text+"\n")
	return code
}

// reservePorts gives the body of the response to a POST to endpoint. The body of a response with an error status is the
// error.
func reservePorts(endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, nil)
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
