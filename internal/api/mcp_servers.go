package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// McpPairName is the name of a header or of an environment variable of an MCP server. The API never gives its value.
type McpPairName struct {
	// Name is the name of the header or of the environment variable
	Name string `gork:"name"`
}

// McpServer is an MCP server of an organization.
type McpServer struct {
	// ID is the id of the server
	ID int64 `gork:"id"`
	// Name is the name of the server, unique in the organization
	Name string `gork:"name"`
	// Kind is http or stdio
	Kind string `gork:"kind" validate:"oneof=http stdio"`
	// URL is the address of an http server, and empty for a stdio server
	URL string `gork:"url"`
	// Headers are the headers of an http server
	Headers []McpPairName `gork:"headers"`
	// Command is the command of a stdio server, and empty for an http server
	Command string `gork:"command"`
	// Args are the arguments of the command of a stdio server
	Args []string `gork:"args"`
	// Env are the environment variables of a stdio server
	Env []McpPairName `gork:"env"`
}

// McpPair is a header or an environment variable that the Owner sends. An empty value keeps the stored value of the
// same name when the Owner changes a server.
type McpPair struct {
	// Name is the name of the header or of the environment variable
	Name string `gork:"name"`
	// Value is the secret value
	Value string `gork:"value"`
}

// McpServerBody is the body of a request that adds or changes an MCP server.
type McpServerBody struct {
	// Name has only letters, digits, - and _, and is not mobius
	Name string `gork:"name"`
	// Kind is http or stdio
	Kind string `gork:"kind"`
	// URL is the absolute http or https address of an http server. A stdio server ignores it.
	URL string `gork:"url"`
	// Headers are the headers of an http server. A stdio server ignores them.
	Headers []McpPair `gork:"headers"`
	// Command is the command of a stdio server. An http server ignores it.
	Command string `gork:"command"`
	// Args are the arguments of the command. An http server ignores them.
	Args []string `gork:"args"`
	// Env are the environment variables of a stdio server. An http server ignores them.
	Env []McpPair `gork:"env"`
}

func mcpServerOf(server engine.McpServer) McpServer {
	return McpServer{
		ID: server.ID, Name: server.Name, Kind: server.Kind, URL: server.URL, Headers: mcpPairNames(server.Headers),
		Command: server.Command, Args: server.Args, Env: mcpPairNames(server.Env),
	}
}

func mcpPairNames(pairs []engine.McpPair) []McpPairName {
	names := make([]McpPairName, 0, len(pairs))
	for _, pair := range pairs {
		names = append(names, McpPairName{Name: pair.Name})
	}
	return names
}

func mcpPairsOf(pairs []McpPair) []engine.McpPair {
	converted := make([]engine.McpPair, 0, len(pairs))
	for _, pair := range pairs {
		converted = append(converted, engine.McpPair{Name: pair.Name, Value: pair.Value})
	}
	return converted
}

func mcpServerInput(body McpServerBody) engine.McpServer {
	return engine.McpServer{
		Name: body.Name, Kind: body.Kind, URL: body.URL, Headers: mcpPairsOf(body.Headers), Command: body.Command,
		Args: body.Args, Env: mcpPairsOf(body.Env),
	}
}

func mcpServerError(err error) error {
	switch {
	case engine.Refused(err):
		return api.NewHTTPError(http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, engine.ErrMcpServerNameTaken):
		return api.NewHTTPError(http.StatusConflict, err.Error())
	case errors.Is(err, engine.ErrNoMcpServer):
		return api.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return err
}

// ListMcpServersRequest is the request of ListMcpServers.
type ListMcpServersRequest struct {
	Path struct {
		// Organization is the owner of the repositories
		Organization string `gork:"organization"`
	}
}

// ListMcpServersResponse is the response of ListMcpServers.
type ListMcpServersResponse struct {
	Body Envelope[[]McpServer]
}

// ListMcpServers returns the MCP servers of the organization in name order, with the names of the headers and of the
// environment variables and with no secret value.
func (h *handlers) ListMcpServers(ctx context.Context, req ListMcpServersRequest) (*ListMcpServersResponse, error) {
	servers, err := h.engine.McpServers(ctx, req.Path.Organization)
	if err != nil {
		return nil, err
	}
	data := make([]McpServer, 0, len(servers))
	for _, server := range servers {
		data = append(data, mcpServerOf(server))
	}
	return &ListMcpServersResponse{Body: Envelope[[]McpServer]{Data: data}}, nil
}

// AddMcpServerRequest is the request of AddMcpServer.
type AddMcpServerRequest struct {
	Path struct {
		// Organization is the owner of the repositories
		Organization string `gork:"organization"`
	}
	Body McpServerBody
}

// AddMcpServerResponse is the response of AddMcpServer.
type AddMcpServerResponse struct {
	Body Envelope[McpServer]
}

// AddMcpServer adds an MCP server to the organization. It returns 422 with the reason when a field fails a check, and
// 409 when another server of the organization has the name.
func (h *handlers) AddMcpServer(ctx context.Context, req AddMcpServerRequest) (*AddMcpServerResponse, error) {
	server, err := h.engine.AddMcpServer(ctx, req.Path.Organization, mcpServerInput(req.Body))
	if err != nil {
		return nil, mcpServerError(err)
	}
	return &AddMcpServerResponse{Body: Envelope[McpServer]{Data: mcpServerOf(server)}}, nil
}

// ChangeMcpServerRequest is the request of ChangeMcpServer.
type ChangeMcpServerRequest struct {
	Path struct {
		// Organization is the owner of the repositories
		Organization string `gork:"organization"`
		// ID is the id of the server
		ID int64 `gork:"id"`
	}
	Body McpServerBody
}

// ChangeMcpServerResponse is the response of ChangeMcpServer.
type ChangeMcpServerResponse struct {
	Body Envelope[McpServer]
}

// ChangeMcpServer replaces an MCP server of the organization. A header or an environment variable with an empty value
// keeps the stored value of the same name. It returns 404 for an id that is not a server of the organization, 422 with
// the reason when a field fails a check, and 409 when another server of the organization has the name.
func (h *handlers) ChangeMcpServer(ctx context.Context, req ChangeMcpServerRequest) (*ChangeMcpServerResponse, error) {
	server, err := h.engine.UpdateMcpServer(ctx, req.Path.Organization, req.Path.ID, mcpServerInput(req.Body))
	if err != nil {
		return nil, mcpServerError(err)
	}
	return &ChangeMcpServerResponse{Body: Envelope[McpServer]{Data: mcpServerOf(server)}}, nil
}

// DeleteMcpServerRequest is the request of DeleteMcpServer.
type DeleteMcpServerRequest struct {
	Path struct {
		// Organization is the owner of the repositories
		Organization string `gork:"organization"`
		// ID is the id of the server
		ID int64 `gork:"id"`
	}
}

// DeleteMcpServer deletes an MCP server of the organization and removes it from the selection of each Workstream. It
// returns 404 for an id that is not a server of the organization.
func (h *handlers) DeleteMcpServer(ctx context.Context, req DeleteMcpServerRequest) error {
	return mcpServerError(h.engine.DeleteMcpServer(ctx, req.Path.Organization, req.Path.ID))
}

// SetWorkstreamMcpServersRequest is the request of SetWorkstreamMcpServers.
type SetWorkstreamMcpServersRequest struct {
	Path IssuePath
	Body struct {
		// IDs are the ids of the selected servers
		IDs []int64 `gork:"ids"`
	}
}

// SetWorkstreamMcpServers replaces the MCP servers that the Owner selected for the Workstream. It returns 404 when the
// issue is not an open Workstream of the local copy, and 422 for an id of a server of another organization.
func (h *handlers) SetWorkstreamMcpServers(ctx context.Context, req SetWorkstreamMcpServersRequest) error {
	repository := req.Path.Owner + "/" + req.Path.Name
	err := h.engine.CheckOpenWorkstream(ctx, repository, req.Path.Number)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusNotFound, err.Error())
	}
	if err != nil {
		return err
	}
	return mcpServerError(h.engine.SetWorkstreamMcpServers(ctx, repository, req.Path.Number, req.Body.IDs))
}
