package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	mcpHTTP  = "http"
	mcpStdio = "stdio"
)

// ErrNoMcpServer is the error of an MCP server id that is not a server of the organization.
var ErrNoMcpServer = errors.New("the organization has no such MCP server")

// ErrMcpServerNameTaken is the error of an MCP server name that another server of the organization has.
var ErrMcpServerNameTaken = errors.New("another MCP server of the organization has this name")

var mcpServerName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// McpPair is a header or an environment variable of an MCP server. Its value is a secret.
type McpPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// McpServer is an MCP server of an organization. An http server has URL and Headers. A stdio server has Command, Args
// and Env.
type McpServer struct {
	ID      int64
	Name    string
	Kind    string
	URL     string
	Headers []McpPair
	Command string
	Args    []string
	Env     []McpPair
}

// McpServers gives the MCP servers of the organization in name order.
func (e *Engine) McpServers(ctx context.Context, organization string) ([]McpServer, error) {
	rows, err := e.queries.ListMcpServers(ctx, organization)
	if err != nil {
		return nil, err
	}
	return mcpServersOf(rows)
}

// AddMcpServer adds the server to the organization and gives it with its id. It refuses a server that fails a check,
// and gives ErrMcpServerNameTaken for a name that another server of the organization has.
func (e *Engine) AddMcpServer(ctx context.Context, organization string, server McpServer) (McpServer, error) {
	if err := checkMcpServer(server, nil); err != nil {
		return McpServer{}, err
	}
	var added McpServer
	err := e.inTx(ctx, func(q *store.Queries) error {
		if err := checkMcpServerName(ctx, q, organization, 0, server.Name); err != nil {
			return err
		}
		params, err := e.addMcpServerParams(organization, server)
		if err != nil {
			return err
		}
		row, err := q.AddMcpServer(ctx, params)
		if err != nil {
			return err
		}
		added, err = mcpServerOf(row)
		return err
	})
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return added, err
}

// UpdateMcpServer replaces the server with the id. A header or an environment variable with an empty value keeps the
// stored value of the same name, and is refused when the server has no such name. It gives ErrNoMcpServer for an id that
// is not a server of the organization.
func (e *Engine) UpdateMcpServer(ctx context.Context, organization string, id int64, server McpServer) (McpServer, error) {
	var updated McpServer
	err := e.inTx(ctx, func(q *store.Queries) error {
		stored, err := q.GetMcpServer(ctx, store.GetMcpServerParams{ID: id, Organization: organization})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoMcpServer
		}
		if err != nil {
			return err
		}
		old, err := mcpServerOf(stored)
		if err != nil {
			return err
		}
		if err := checkMcpServer(server, &old); err != nil {
			return err
		}
		if err := checkMcpServerName(ctx, q, organization, id, server.Name); err != nil {
			return err
		}
		server.Headers = keepSecrets(server.Headers, old.Headers)
		server.Env = keepSecrets(server.Env, old.Env)
		params, err := e.addMcpServerParams(organization, server)
		if err != nil {
			return err
		}
		row, err := q.UpdateMcpServer(ctx, store.UpdateMcpServerParams{
			Name: params.Name, Kind: params.Kind, Url: params.Url, HeaderList: params.HeaderList, Command: params.Command,
			Args: params.Args, Env: params.Env, UpdatedAt: params.UpdatedAt, ID: id, Organization: organization,
		})
		if err != nil {
			return err
		}
		updated, err = mcpServerOf(row)
		return err
	})
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return updated, err
}

// DeleteMcpServer deletes the server and removes it from the selection of each Workstream. It gives ErrNoMcpServer for
// an id that is not a server of the organization.
func (e *Engine) DeleteMcpServer(ctx context.Context, organization string, id int64) error {
	deleted, err := e.queries.DeleteMcpServer(ctx, store.DeleteMcpServerParams{ID: id, Organization: organization})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrNoMcpServer
	}
	e.publish(Change{Workstreams: true})
	return nil
}

// SetWorkstreamMcpServers replaces the selection of the Workstream with the servers of ids. It refuses an id of a
// server that is not in the organization of the repository.
func (e *Engine) SetWorkstreamMcpServers(ctx context.Context, repositoryName string, workstream int64, ids []int64) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	err = e.inTx(ctx, func(q *store.Queries) error {
		for _, id := range ids {
			_, err := q.GetMcpServer(ctx, store.GetMcpServerParams{ID: id, Organization: repository.Owner()})
			if errors.Is(err, sql.ErrNoRows) {
				return refuse("The organization %s has no MCP server %d.", repository.Owner(), id)
			}
			if err != nil {
				return err
			}
		}
		if err := q.DeleteWorkstreamMcpServers(ctx, store.DeleteWorkstreamMcpServersParams{Repository: repository.FullName, Workstream: workstream}); err != nil {
			return err
		}
		for _, id := range ids {
			if err := q.AddWorkstreamMcpServer(ctx, store.AddWorkstreamMcpServerParams{Repository: repository.FullName, Workstream: workstream, Server: id}); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		e.publish(Change{Workstreams: true})
	}
	return err
}

// WorkstreamMcpServers gives the servers that the Owner selected for the Workstream, with their secrets: first the
// http servers, then the stdio servers, each kind in name order.
func (e *Engine) WorkstreamMcpServers(ctx context.Context, repository string, workstream int64) ([]McpServer, error) {
	rows, err := e.queries.ListWorkstreamMcpServers(ctx, store.ListWorkstreamMcpServersParams{Repository: repository, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	servers, err := mcpServersOf(rows)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(servers, func(a, b McpServer) int {
		return strings.Compare(a.Kind, b.Kind)
	})
	return servers, nil
}

// workstreamMcpServerIDs gives the ids of the servers that the Owner selected for the Workstream, in name order of the
// servers.
func (e *Engine) workstreamMcpServerIDs(ctx context.Context, repository string, workstream int64) ([]int64, error) {
	rows, err := e.queries.ListWorkstreamMcpServers(ctx, store.ListWorkstreamMcpServersParams{Repository: repository, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

func checkMcpServerName(ctx context.Context, q *store.Queries, organization string, id int64, name string) error {
	rows, err := q.ListMcpServers(ctx, organization)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(rows, func(row store.McpServer) bool { return row.Name == name && row.ID != id }) {
		return ErrMcpServerNameTaken
	}
	return nil
}

// checkMcpServer refuses a server that fails a rule. An empty value is allowed only for a name that old has.
func checkMcpServer(server McpServer, old *McpServer) error {
	if !mcpServerName.MatchString(server.Name) {
		return refuse("The name of an MCP server has only letters, digits, - and _.")
	}
	if strings.EqualFold(server.Name, "mobius") {
		return refuse("The name of an MCP server must not be mobius.")
	}
	switch server.Kind {
	case mcpHTTP:
		address, err := url.Parse(server.URL)
		if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" {
			return refuse("The url of an http MCP server must be an absolute http or https URL.")
		}
		var stored []McpPair
		if old != nil {
			stored = old.Headers
		}
		return checkMcpPairs("header", server.Headers, stored)
	case mcpStdio:
		if empty(server.Command) {
			return refuse("The command of a stdio MCP server must not be empty.")
		}
		var stored []McpPair
		if old != nil {
			stored = old.Env
		}
		return checkMcpPairs("env", server.Env, stored)
	}
	return refuse("The kind of an MCP server is http or stdio.")
}

func checkMcpPairs(label string, pairs, stored []McpPair) error {
	seen := map[string]bool{}
	for _, pair := range pairs {
		if empty(pair.Name) {
			return refuse("The name of a %s must not be empty.", label)
		}
		if seen[pair.Name] {
			return refuse("The %s %s is twice in the server.", label, pair.Name)
		}
		seen[pair.Name] = true
		if pair.Value == "" && !slices.ContainsFunc(stored, func(old McpPair) bool { return old.Name == pair.Name }) {
			return refuse("The %s %s needs a value.", label, pair.Name)
		}
	}
	return nil
}

// keepSecrets gives pairs with the stored value for each pair that has an empty value.
func keepSecrets(pairs, stored []McpPair) []McpPair {
	kept := make([]McpPair, 0, len(pairs))
	for _, pair := range pairs {
		if pair.Value == "" {
			i := slices.IndexFunc(stored, func(old McpPair) bool { return old.Name == pair.Name })
			pair.Value = stored[i].Value
		}
		kept = append(kept, pair)
	}
	return kept
}

// addMcpServerParams gives the row of the server. A server keeps only the fields of its kind.
func (e *Engine) addMcpServerParams(organization string, server McpServer) (store.AddMcpServerParams, error) {
	params := store.AddMcpServerParams{
		Organization: organization, Name: server.Name, Kind: server.Kind, UpdatedAt: e.timeNow().UTC().Format(time.RFC3339Nano),
	}
	headers, args, env := []McpPair{}, []string{}, []McpPair{}
	if server.Kind == mcpHTTP {
		params.Url = server.URL
		headers = server.Headers
	} else {
		params.Command = server.Command
		args = server.Args
		env = server.Env
	}
	var err error
	if params.HeaderList, err = encodeJSON(headers); err != nil {
		return store.AddMcpServerParams{}, err
	}
	if params.Args, err = encodeJSON(args); err != nil {
		return store.AddMcpServerParams{}, err
	}
	if params.Env, err = encodeJSON(env); err != nil {
		return store.AddMcpServerParams{}, err
	}
	return params, nil
}

func encodeJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func mcpServersOf(rows []store.McpServer) ([]McpServer, error) {
	servers := make([]McpServer, 0, len(rows))
	for _, row := range rows {
		server, err := mcpServerOf(row)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, nil
}

func mcpServerOf(row store.McpServer) (McpServer, error) {
	server := McpServer{ID: row.ID, Name: row.Name, Kind: row.Kind, URL: row.Url, Command: row.Command}
	if err := json.Unmarshal([]byte(row.HeaderList), &server.Headers); err != nil {
		return McpServer{}, err
	}
	if err := json.Unmarshal([]byte(row.Args), &server.Args); err != nil {
		return McpServer{}, err
	}
	if err := json.Unmarshal([]byte(row.Env), &server.Env); err != nil {
		return McpServer{}, err
	}
	return server, nil
}
