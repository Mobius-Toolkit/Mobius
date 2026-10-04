// Package engine holds the work of Mobius on the managed repositories: the poll, the Mobius labels,
// the checkup, the trust rules, and the agent sessions with their Mobius tools and Transcripts.
package engine

import (
	"context"
	"sync"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/mcp"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// Engine polls the repositories of the Mobius Apps and runs the agent sessions.
type Engine struct {
	queries      *store.Queries
	github       *github.GitHub
	trustedUsers []string
	trustedBots  []string
	agents       Agents
	// labelsFixed holds the full names of the repositories whose labels the poll fixed. Only the poll uses it.
	labelsFixed map[string]bool

	mu        sync.Mutex
	listeners map[chan Change]bool
}

// Agents is the setup of the agent sessions.
type Agents struct {
	// MCP serves the Mobius MCP server and the gh token to the sessions on the listener at Addr.
	MCP  *mcp.Server
	Addr string
	// DataDir holds the agent environment.
	DataDir string
	// Path is the PATH of the Harness commands.
	Path string
}

// New gives the Engine of the GitHub Apps of gh.
func New(queries *store.Queries, gh *github.GitHub, trustedUsers, trustedBots []string, agents Agents) *Engine {
	return &Engine{
		queries:      queries,
		github:       gh,
		trustedUsers: trustedUsers,
		trustedBots:  trustedBots,
		agents:       agents,
		labelsFixed:  map[string]bool{},
		listeners:    map[chan Change]bool{},
	}
}

// Run polls now and then after each interval, until ctx ends.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	for {
		e.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
