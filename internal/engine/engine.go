// Package engine holds the work of Mobius on the managed repositories: the poll, the Mobius labels,
// the checkup and the trust rules.
package engine

import (
	"context"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// Engine polls the repositories of the Mobius Apps.
type Engine struct {
	queries      *store.Queries
	github       *github.GitHub
	trustedUsers []string
	trustedBots  []string
	// labelsFixed holds the full names of the repositories whose labels the poll fixed. Only the poll uses it.
	labelsFixed map[string]bool
}

// New gives the Engine of the GitHub Apps of gh.
func New(queries *store.Queries, gh *github.GitHub, trustedUsers, trustedBots []string) *Engine {
	return &Engine{
		queries:      queries,
		github:       gh,
		trustedUsers: trustedUsers,
		trustedBots:  trustedBots,
		labelsFixed:  map[string]bool{},
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
