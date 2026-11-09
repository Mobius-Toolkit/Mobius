package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	mcpOAuthNone      = "none"
	mcpOAuthConnected = "connected"
	mcpOAuthReconnect = "reconnect"

	mcpOAuthFlowLife = 10 * time.Minute

	mcpOAuthCallbackPath = "/api/mcp-oauth/callback"
	mcpOAuthClientPath   = "/api/mcp-oauth/client"
)

// mcpOAuthClient reads the metadata of the MCP servers and of their authorization servers, and calls their token and
// registration endpoints. A client with no Transport gets the checks of oauthex for the metadata requests.
var mcpOAuthClient = &http.Client{Timeout: 30 * time.Second}

// McpClientDocument is the Client ID Metadata Document of Mobius.
type McpClientDocument struct {
	ClientID     string
	ClientName   string
	RedirectURIs []string
}

// McpOAuthClientDocument gives the Client ID Metadata Document of Mobius. It gives false when the config has no ui_url.
func (e *Engine) McpOAuthClientDocument() (McpClientDocument, bool) {
	if e.config.UIURL == "" {
		return McpClientDocument{}, false
	}
	return McpClientDocument{
		ClientID: e.config.UIURL + mcpOAuthClientPath, ClientName: "Mobius",
		RedirectURIs: []string{e.config.UIURL + mcpOAuthCallbackPath},
	}, true
}

// StartMcpOAuth finds the authorization server of the http MCP server, gets a client ID, stores the flow and gives the
// authorization URL for the browser of the Owner. A client ID in clientID is used as it is, with the secret in
// clientSecret. A new start for the same server replaces its flow and keeps its tokens until the new flow stores new tokens. It gives ErrNoMcpServer for an id
// that is not a server of the organization, and a refusal with the reason when the flow cannot start.
func (e *Engine) StartMcpOAuth(ctx context.Context, organization string, id int64, clientID, clientSecret string) (string, error) {
	row, err := e.queries.GetMcpServer(ctx, store.GetMcpServerParams{ID: id, Organization: organization})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoMcpServer
	}
	if err != nil {
		return "", err
	}
	server, err := mcpServerOf(row)
	if err != nil {
		return "", err
	}
	switch {
	case server.Kind != mcpHTTP:
		return "", refuse("Only an http MCP server can use OAuth.")
	case e.config.UIURL == "":
		return "", refuse("OAuth needs ui_url in the config of Mobius.")
	case !strings.HasPrefix(e.config.UIURL, "https://"):
		return "", refuse("OAuth needs a ui_url that starts with https://.")
	case slices.ContainsFunc(server.Headers, func(pair McpPair) bool { return strings.EqualFold(pair.Name, "Authorization") }):
		return "", refuse("The server has a header Authorization. Remove it to use OAuth.")
	}
	document, _ := e.McpOAuthClientDocument()
	redirectURI := document.RedirectURIs[0]

	resource := canonicalMcpResource(server.URL)
	metadataURL, scopes, err := mcpChallenge(ctx, resource)
	if err != nil {
		return "", err
	}
	metadata, err := protectedResourceMetadata(ctx, metadataURL, resource)
	if err != nil {
		return "", err
	}
	if len(metadata.AuthorizationServers) == 0 {
		return "", refuse("The MCP server names no authorization server.")
	}
	issuer, err := authServerMetadata(ctx, metadata.AuthorizationServers[0])
	if err != nil {
		return "", err
	}
	if !slices.Contains(issuer.CodeChallengeMethodsSupported, "S256") {
		return "", refuse("The authorization server does not support PKCE with S256.")
	}
	if len(scopes) == 0 {
		scopes = metadata.ScopesSupported
	}
	if slices.Contains(issuer.ScopesSupported, "offline_access") && !slices.Contains(scopes, "offline_access") {
		scopes = append(slices.Clone(scopes), "offline_access")
	}

	switch {
	case clientID != "":
	case issuer.ClientIDMetadataDocumentSupported:
		clientID = document.ClientID
	case issuer.RegistrationEndpoint != "":
		registered, err := oauthex.RegisterClient(ctx, issuer.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{
			RedirectURIs: document.RedirectURIs, ClientName: document.ClientName, GrantTypes: []string{"authorization_code", "refresh_token"},
			ResponseTypes: []string{"code"}, TokenEndpointAuthMethod: "none",
		}, mcpOAuthClient)
		if err != nil {
			return "", refuse("The registration of Mobius at the authorization server failed: %v", err)
		}
		clientID, clientSecret = registered.ClientID, registered.ClientSecret
	default:
		return "", refuse("The authorization server has no way to give Mobius a client ID. Enter a client ID.")
	}

	state := rand.Text()
	verifier := oauth2.GenerateVerifier()
	config := oauth2.Config{
		ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURI, Scopes: scopes,
		Endpoint: oauth2.Endpoint{AuthURL: issuer.AuthorizationEndpoint, TokenURL: issuer.TokenEndpoint},
	}
	authorizationURL := config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", metadata.Resource))
	now := e.timeNow().UTC().Format(time.RFC3339Nano)
	err = e.queries.StartMcpServerOAuth(ctx, store.StartMcpServerOAuthParams{
		Server: id, ClientID: clientID, ClientSecret: clientSecret, Issuer: issuer.Issuer, TokenEndpoint: issuer.TokenEndpoint,
		Resource: metadata.Resource, Scope: strings.Join(scopes, " "), State: state, CodeVerifier: verifier,
		FlowStartedAt: sql.NullString{String: now, Valid: true}, UpdatedAt: now,
	})
	if err != nil {
		return "", err
	}
	e.publish(Change{Workstreams: true})
	return authorizationURL, nil
}

// FinishMcpOAuth ends the flow with the state that the authorization server sent back to the browser of the Owner: it
// checks the flow, gets the tokens for the code and stores them. It gives the id of the server of the flow, or 0 when
// no flow has the state. A refusal has the reason for the Owner. A flow that fails after its state matches stores the
// reason, so the server shows reconnect.
func (e *Engine) FinishMcpOAuth(ctx context.Context, code, state, iss, authorizationError string) (int64, error) {
	row, err := e.queries.GetMcpServerOAuthByState(ctx, state)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, refuse("No connection waits for this answer of the authorization server.")
	}
	if err != nil {
		return 0, err
	}
	fail := func(reason string) (int64, error) {
		err := e.queries.FailMcpServerOAuth(ctx, store.FailMcpServerOAuthParams{
			Failure: reason, UpdatedAt: e.timeNow().UTC().Format(time.RFC3339Nano), Server: row.Server, State: state,
		})
		if err != nil {
			return row.Server, err
		}
		e.publish(Change{Workstreams: true})
		return row.Server, refuse("%s", reason)
	}
	started, err := time.Parse(time.RFC3339Nano, row.FlowStartedAt.String)
	if err != nil {
		return row.Server, err
	}
	if e.timeNow().Sub(started) > mcpOAuthFlowLife {
		return fail("The connection started more than 10 minutes ago. Connect again.")
	}
	if authorizationError != "" {
		return fail("The authorization server did not allow the connection.")
	}
	issuer, err := authServerMetadata(ctx, row.Issuer)
	if err != nil {
		return fail("The authorization server gives no metadata. Connect again.")
	}
	if iss != row.Issuer && (iss != "" || issuer.AuthorizationResponseIssParameterSupported) {
		return fail("The answer of the authorization server has a wrong issuer.")
	}
	document, _ := e.McpOAuthClientDocument()
	config := oauth2.Config{
		ClientID: row.ClientID, ClientSecret: row.ClientSecret, RedirectURL: document.RedirectURIs[0],
		Endpoint: oauth2.Endpoint{TokenURL: row.TokenEndpoint},
	}
	if row.ClientSecret == "" {
		config.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	token, err := config.Exchange(context.WithValue(ctx, oauth2.HTTPClient, mcpOAuthClient), code,
		oauth2.VerifierOption(row.CodeVerifier), oauth2.SetAuthURLParam("resource", row.Resource))
	if err != nil || token.AccessToken == "" {
		return fail("The authorization server did not give a token for the code.")
	}
	expires := sql.NullString{}
	if !token.Expiry.IsZero() {
		expires = sql.NullString{String: token.Expiry.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	err = e.queries.StoreMcpServerOAuthTokens(ctx, store.StoreMcpServerOAuthTokensParams{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: expires,
		UpdatedAt: e.timeNow().UTC().Format(time.RFC3339Nano), Server: row.Server, State: state,
	})
	if err != nil {
		return row.Server, err
	}
	e.publish(Change{Workstreams: true})
	return row.Server, nil
}

// DisconnectMcpOAuth removes the OAuth flow and the tokens of the server. It gives ErrNoMcpServer for an id that is not
// a server of the organization.
func (e *Engine) DisconnectMcpOAuth(ctx context.Context, organization string, id int64) error {
	_, err := e.queries.GetMcpServer(ctx, store.GetMcpServerParams{ID: id, Organization: organization})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoMcpServer
	}
	if err != nil {
		return err
	}
	if err := e.queries.DeleteMcpServerOAuth(ctx, id); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// mcpOAuthState gives the OAuth state of a server and the reason of a reconnect.
func mcpOAuthState(row store.McpServerOauth) (string, string) {
	switch {
	case row.Failure != "":
		return mcpOAuthReconnect, row.Failure
	case row.AccessToken != "":
		return mcpOAuthConnected, ""
	}
	return mcpOAuthNone, ""
}

// canonicalMcpResource gives the canonical URI of an MCP server: the URL with a lower case host and with no fragment.
func canonicalMcpResource(address string) string {
	resource, err := url.Parse(address)
	if err != nil {
		return address
	}
	resource.Host = strings.ToLower(resource.Host)
	resource.Fragment = ""
	return resource.String()
}

// mcpChallenge sends an MCP request with no token to the resource, and gives the resource_metadata URL and the scopes
// of its Bearer challenge.
func mcpChallenge(ctx context.Context, resource string) (string, []string, error) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"Mobius","version":"1"}}}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, resource, strings.NewReader(body))
	if err != nil {
		return "", nil, refuse("The URL of the MCP server is not valid: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := mcpOAuthClient.Do(request)
	if err != nil {
		return "", nil, refuse("The MCP server did not answer: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return "", nil, refuse("The MCP server did not ask for authorization. It answered with status %d.", response.StatusCode)
	}
	challenges, err := oauthex.ParseWWWAuthenticate(response.Header.Values("WWW-Authenticate"))
	if err != nil {
		return "", nil, refuse("The WWW-Authenticate header of the MCP server is not valid: %v", err)
	}
	var metadataURL string
	var scopes []string
	for _, challenge := range challenges {
		if metadataURL == "" {
			metadataURL = challenge.Params["resource_metadata"]
		}
		if challenge.Scheme == "bearer" && scopes == nil {
			scopes = strings.Fields(challenge.Params["scope"])
		}
	}
	return metadataURL, scopes, nil
}

// protectedResourceMetadata reads the Protected Resource Metadata of the resource from the URL of the challenge, then
// from the well-known URL with the path of the resource, then from the well-known URL at the root.
func protectedResourceMetadata(ctx context.Context, challengeURL, resource string) (*oauthex.ProtectedResourceMetadata, error) {
	address, err := url.Parse(resource)
	if err != nil {
		return nil, refuse("The URL of the MCP server is not valid: %v", err)
	}
	withPath := *address
	withPath.Path = "/.well-known/oauth-protected-resource/" + strings.TrimLeft(address.Path, "/")
	root := *address
	root.Path = "/.well-known/oauth-protected-resource"
	rootResource := *address
	rootResource.Path = ""
	candidates := []struct{ url, resource string }{{withPath.String(), resource}, {root.String(), rootResource.String()}}
	if challengeURL != "" {
		candidates = slices.Insert(candidates, 0, struct{ url, resource string }{challengeURL, resource})
	}
	for _, candidate := range candidates {
		metadata, err := oauthex.GetProtectedResourceMetadata(ctx, candidate.url, candidate.resource, mcpOAuthClient)
		if err == nil {
			return metadata, nil
		}
	}
	return nil, refuse("The MCP server gives no Protected Resource Metadata.")
}

// authServerMetadata reads the metadata of the authorization server of issuer.
func authServerMetadata(ctx context.Context, issuer string) (*oauthex.AuthServerMeta, error) {
	metadata, err := auth.GetAuthServerMetadata(ctx, issuer, mcpOAuthClient)
	if err != nil {
		return nil, refuse("The metadata of the authorization server is not valid: %v", err)
	}
	if metadata == nil {
		return nil, refuse("The authorization server %s gives no metadata.", issuer)
	}
	return metadata, nil
}
