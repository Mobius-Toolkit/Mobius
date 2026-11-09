package api

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// StartMcpOAuthRequest is the request of StartMcpOAuth.
type StartMcpOAuthRequest struct {
	Path struct {
		// Organization is the owner of the repositories
		Organization string `gork:"organization"`
		// ID is the id of the server
		ID int64 `gork:"id"`
	}
	Body struct {
		// ClientID is the client ID that the Owner got from the authorization server, or empty
		ClientID string `gork:"clientId"`
		// ClientSecret is the secret of that client, or empty for a public client
		ClientSecret string `gork:"clientSecret"`
	}
}

// McpOAuthStart is the start of the OAuth flow of an MCP server.
type McpOAuthStart struct {
	// URL is the authorization page that the browser of the Owner opens
	URL string `gork:"url"`
}

// StartMcpOAuthResponse is the response of StartMcpOAuth.
type StartMcpOAuthResponse struct {
	Body Envelope[McpOAuthStart]
}

// StartMcpOAuth starts the OAuth flow of an http MCP server and returns the authorization URL. It returns 404 for an
// id that is not a server of the organization, and 422 with the reason when the flow cannot start.
func (h *handlers) StartMcpOAuth(ctx context.Context, req StartMcpOAuthRequest) (*StartMcpOAuthResponse, error) {
	authorizationURL, err := h.engine.StartMcpOAuth(ctx, req.Path.Organization, req.Path.ID, req.Body.ClientID, req.Body.ClientSecret)
	if err != nil {
		return nil, mcpServerError(err)
	}
	return &StartMcpOAuthResponse{Body: Envelope[McpOAuthStart]{Data: McpOAuthStart{URL: authorizationURL}}}, nil
}

// McpOAuthCallbackRequest is the request of McpOAuthCallback.
type McpOAuthCallbackRequest struct {
	Query struct {
		// Code is the authorization code
		Code string `gork:"code"`
		// State is the state of the flow
		State string `gork:"state"`
		// Iss is the issuer of the authorization server
		Iss string `gork:"iss"`
		// Error is the error code that the authorization server sends when the Owner does not allow the connection
		Error string `gork:"error"`
	}
}

// McpOAuthCallbackResponse is the response of McpOAuthCallback.
type McpOAuthCallbackResponse struct {
	Headers struct {
		// Location is the page that the browser opens next
		Location string `gork:"Location"`
	}
}

// McpOAuthCallback is the page where an authorization server sends the browser after the Owner allows or refuses the
// connection of an MCP server. It sends the browser to the page of the MCP servers with the result.
func (h *handlers) McpOAuthCallback(ctx context.Context, req McpOAuthCallbackRequest) (*McpOAuthCallbackResponse, error) {
	server, err := h.engine.FinishMcpOAuth(ctx, req.Query.Code, req.Query.State, req.Query.Iss, req.Query.Error)
	query := url.Values{"oauth": {"connected"}}
	if err != nil {
		query.Set("oauth", "failed")
		reason := "Mobius could not store the connection."
		if engine.Refused(err) {
			reason = err.Error()
		} else {
			log.Printf("finish the OAuth flow of an MCP server: %v", err)
		}
		query.Set("reason", reason)
	}
	if server != 0 {
		query.Set("server", strconv.FormatInt(server, 10))
	}
	resp := &McpOAuthCallbackResponse{}
	resp.Headers.Location = "/settings/mcp-servers?" + query.Encode()
	return resp, nil
}

// McpOAuthClientRequest is the request of McpOAuthClient.
type McpOAuthClientRequest struct{}

// McpOAuthClientDocument is the Client ID Metadata Document of Mobius.
type McpOAuthClientDocument struct {
	// ClientID is the URL of this document
	ClientID string `gork:"client_id"`
	// ClientName is the name that the authorization page shows
	ClientName string `gork:"client_name"`
	// RedirectURIs are the callback URLs of Mobius
	RedirectURIs []string `gork:"redirect_uris"`
	// GrantTypes are the grant types of Mobius
	GrantTypes []string `gork:"grant_types"`
	// ResponseTypes are the response types of Mobius
	ResponseTypes []string `gork:"response_types"`
	// TokenEndpointAuthMethod is none, because Mobius is a public client
	TokenEndpointAuthMethod string `gork:"token_endpoint_auth_method"`
}

// McpOAuthClientResponse is the response of McpOAuthClient. Its body has no envelope, because an authorization
// server reads it.
type McpOAuthClientResponse struct {
	Body McpOAuthClientDocument
}

// McpOAuthClient returns the Client ID Metadata Document of Mobius. It needs no device login, because an authorization
// server reads it. It returns 404 when the config has no ui_url.
func (h *handlers) McpOAuthClient(_ context.Context, _ McpOAuthClientRequest) (*McpOAuthClientResponse, error) {
	document, ok := h.engine.McpOAuthClientDocument()
	if !ok {
		return nil, api.NewHTTPError(http.StatusNotFound, "The config of Mobius has no ui_url.")
	}
	return &McpOAuthClientResponse{Body: McpOAuthClientDocument{
		ClientID: document.ClientID, ClientName: document.ClientName, RedirectURIs: document.RedirectURIs,
		GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}, TokenEndpointAuthMethod: "none",
	}}, nil
}

// DisconnectMcpOAuthRequest is the request of DisconnectMcpOAuth.
type DisconnectMcpOAuthRequest struct {
	Path struct {
		// Organization is the owner of the repositories
		Organization string `gork:"organization"`
		// ID is the id of the server
		ID int64 `gork:"id"`
	}
}

// DisconnectMcpOAuth removes the OAuth flow and the tokens of an MCP server. It returns 404 for an id that is not a
// server of the organization.
func (h *handlers) DisconnectMcpOAuth(ctx context.Context, req DisconnectMcpOAuthRequest) error {
	return mcpServerError(h.engine.DisconnectMcpOAuth(ctx, req.Path.Organization, req.Path.ID))
}
