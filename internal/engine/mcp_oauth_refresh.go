package engine

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	// mcpTokenReuse is the time in which a start uses the token that an earlier start refreshed.
	mcpTokenReuse = time.Minute

	mcpRefusedReason = "The authorization server refused the refresh token."
	mcpExpiredReason = "The access token expired, and the authorization server gave no refresh token."
)

// errMcpRefused is the error of a token endpoint that refuses the refresh token.
var errMcpRefused = errors.New("the token endpoint refused the refresh token")

// mcpRefresh makes the starts of one server get its token one after the other. at is the time of its last refresh.
type mcpRefresh struct {
	mu sync.Mutex
	at time.Time
}

func (e *Engine) mcpRefreshOf(server int64) *mcpRefresh {
	e.mcpRefreshesMu.Lock()
	defer e.mcpRefreshesMu.Unlock()
	if e.mcpRefreshes[server] == nil {
		e.mcpRefreshes[server] = &mcpRefresh{}
	}
	return e.mcpRefreshes[server]
}

// withMcpTokens gives each http server that has an OAuth token the header Authorization with a valid access token, after
// its other headers. The error of a server whose token fails names the server, and has no token.
func (e *Engine) withMcpTokens(ctx context.Context, servers []McpServer) error {
	for i, server := range servers {
		if server.Kind != mcpHTTP {
			continue
		}
		token, err := e.mcpAccessToken(ctx, server.ID, server.Name)
		if err != nil {
			return err
		}
		if token != "" {
			servers[i].Headers = append(servers[i].Headers, McpPair{Name: "Authorization", Value: "Bearer " + token})
		}
	}
	return nil
}

// mcpAccessToken gives a valid access token of the server, or "" for a server with no OAuth token. It refreshes the
// token, unless an earlier start refreshed it in mcpTokenReuse. A refresh token that the token endpoint refuses, and a
// token that expired with no refresh token, set the failure of the server. A failure of the endpoint, or of the
// network, sets no failure.
func (e *Engine) mcpAccessToken(ctx context.Context, id int64, name string) (string, error) {
	refresh := e.mcpRefreshOf(id)
	refresh.mu.Lock()
	defer refresh.mu.Unlock()
	row, err := e.queries.GetMcpServerOAuth(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if row.Failure != "" {
		return "", mcpNeedsConnect(name, row.Failure)
	}
	if row.AccessToken == "" {
		return "", nil
	}
	valid := true
	if row.ExpiresAt.Valid {
		expires, err := time.Parse(time.RFC3339Nano, row.ExpiresAt.String)
		if err != nil {
			return "", err
		}
		valid = expires.After(e.timeNow())
	}
	switch {
	case valid && (row.RefreshToken == "" || e.timeNow().Sub(refresh.at) < mcpTokenReuse):
		return row.AccessToken, nil
	case row.RefreshToken == "":
		return "", e.failMcpToken(ctx, name, row, mcpExpiredReason)
	}
	answer, err := refreshMcpToken(ctx, row)
	if errors.Is(err, errMcpRefused) {
		return "", e.failMcpToken(ctx, name, row, mcpRefusedReason)
	}
	if err != nil {
		return "", fmt.Errorf("the MCP server %s gave no access token: %w", name, err)
	}
	now := e.timeNow()
	expires := sql.NullString{}
	if seconds, err := answer.ExpiresIn.Int64(); err == nil && seconds > 0 {
		expires = sql.NullString{String: now.Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339Nano), Valid: true}
	}
	err = e.queries.StoreMcpServerOAuthRefresh(ctx, store.StoreMcpServerOAuthRefreshParams{
		AccessToken: answer.AccessToken, RefreshToken: cmp.Or(answer.RefreshToken, row.RefreshToken), ExpiresAt: expires,
		UpdatedAt: now.UTC().Format(time.RFC3339Nano), Server: id,
		CurrentAccessToken: row.AccessToken, CurrentRefreshToken: row.RefreshToken,
	})
	if err != nil {
		return "", err
	}
	refresh.at = now
	return answer.AccessToken, nil
}

// failMcpToken sets the failure of the server for the token of row, and gives the error of the start.
func (e *Engine) failMcpToken(ctx context.Context, name string, row store.McpServerOauth, reason string) error {
	err := e.queries.FailMcpServerOAuthToken(ctx, store.FailMcpServerOAuthTokenParams{
		Failure: reason, UpdatedAt: e.timeNow().UTC().Format(time.RFC3339Nano), Server: row.Server, AccessToken: row.AccessToken,
	})
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return mcpNeedsConnect(name, reason)
}

func mcpNeedsConnect(name, reason string) error {
	return fmt.Errorf("the MCP server %s needs a new Connect: %s", name, reason)
}

type mcpTokenAnswer struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	ExpiresIn    json.Number `json:"expires_in"`
}

// refreshMcpToken asks the token endpoint of row for a new access token. A client with a secret sends it in the header
// Authorization first, then in the form, as FinishMcpOAuth does through oauth2. It gives errMcpRefused for a 400 or a
// 401 answer. No error has the body of an answer, because the body can have a token.
func refreshMcpToken(ctx context.Context, row store.McpServerOauth) (mcpTokenAnswer, error) {
	answer, err := requestMcpRefresh(ctx, row, row.ClientSecret != "")
	if row.ClientSecret != "" && errors.Is(err, errMcpRefused) {
		return requestMcpRefresh(ctx, row, false)
	}
	return answer, err
}

func requestMcpRefresh(ctx context.Context, row store.McpServerOauth, basic bool) (mcpTokenAnswer, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {row.RefreshToken}, "resource": {row.Resource}}
	if !basic {
		form.Set("client_id", row.ClientID)
		if row.ClientSecret != "" {
			form.Set("client_secret", row.ClientSecret)
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, row.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return mcpTokenAnswer{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if basic {
		request.SetBasicAuth(url.QueryEscape(row.ClientID), url.QueryEscape(row.ClientSecret))
	}
	response, err := mcpOAuthClient.Do(request)
	if err != nil {
		return mcpTokenAnswer{}, err
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized:
		return mcpTokenAnswer{}, errMcpRefused
	case response.StatusCode != http.StatusOK:
		return mcpTokenAnswer{}, fmt.Errorf("the authorization server answered with status %d", response.StatusCode)
	}
	var answer mcpTokenAnswer
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&answer); err != nil || answer.AccessToken == "" {
		return mcpTokenAnswer{}, errors.New("the authorization server gave no access token")
	}
	return answer, nil
}
