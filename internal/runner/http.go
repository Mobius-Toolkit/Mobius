package runner

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// responseBody gives the body of the response to a request with method to url. The body of a response with an error
// status is the error.
func responseBody(method, url string) (string, error) {
	request, err := http.NewRequestWithContext(context.Background(), method, url, nil)
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
