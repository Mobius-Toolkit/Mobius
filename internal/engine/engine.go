// Package engine holds the work of Mobius on the managed repositories: the poll, the Mobius labels, the checkup,
// the trust rules, the Workstreams with their local copy, the Autopilot switch and the close, the agent sessions with
// their Mobius tools and Transcripts, the Worker slots, the usage-limit pauses, the Housekeeper, the recovery after a
// restart, the drain and the upgrade.
package engine

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/mcp"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// Engine polls the repositories of the Mobius Apps and runs the agent sessions.
type Engine struct {
	db      *sql.DB
	queries *store.Queries
	github  *github.GitHub
	config  *config.Config
	agents  Agents
	// labelsFixed holds the full names of the repositories whose labels the poll fixed. Only the poll uses it.
	labelsFixed map[string]bool
	// recovered holds the full names of the repositories whose lost tasks the poll handed to a human. Only the poll uses it.
	recovered map[string]bool
	// copied holds the full names of the repositories whose copy is complete. Only the poll uses it.
	copied map[string]bool

	workers workers
	drain   drain
	// pausing makes one pause of two sessions that reach the same usage limit.
	pausing sync.Mutex
	// pausesChanged wakes the sessions that wait for the end of a pause.
	pausesChanged signal
	upgrading     atomic.Bool

	mu        sync.Mutex
	listeners map[chan Change]bool
	// upgradeFailure is the error of the last upgrade, or "".
	upgradeFailure string
}

// Agents is the setup of the agent sessions.
type Agents struct {
	// MCP serves the Mobius MCP server and the gh token to the sessions on the listener at Addr.
	MCP  *mcp.Server
	Addr string
	// Path is the PATH of the Harness commands.
	Path string
}

// New gives the Engine of the database db and of the GitHub Apps of gh with the settings of cfg.
func New(db *sql.DB, gh *github.GitHub, cfg *config.Config, agents Agents) *Engine {
	return &Engine{
		db:          db,
		queries:     store.New(db),
		github:      gh,
		config:      cfg,
		agents:      agents,
		labelsFixed: map[string]bool{},
		recovered:   map[string]bool{},
		copied:      map[string]bool{},
		workers:     newWorkers(),
		listeners:   map[chan Change]bool{},
	}
}

// Recover ends the sessions of the earlier run of the server, and starts the timer of each pause.
// The sessions of the earlier run have no Harness process. Call Recover before Run and before the first session.
func (e *Engine) Recover(ctx context.Context) error {
	ids, err := e.queries.ListOpenSessionIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := e.queries.EndSession(ctx, endParams(id, "restart")); err != nil {
			return err
		}
	}
	pauses, err := e.queries.ListHarnessPauses(ctx)
	if err != nil {
		return err
	}
	for _, pause := range pauses {
		if err := e.timer(pause); err != nil {
			return err
		}
	}
	return nil
}

// Run polls each poll_interval and runs the Housekeeper each housekeeper_interval, until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() { every(ctx, e.config.HousekeeperInterval, e.keepHouse) })
	every(ctx, e.config.PollInterval, e.poll)
	wg.Wait()
}

// inTx runs work in one transaction.
func (e *Engine) inTx(ctx context.Context, work func(*store.Queries) error) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := work(e.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// every runs work now and then after each interval, until ctx ends.
func every(ctx context.Context, interval time.Duration, work func(context.Context)) {
	for {
		work(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// signal wakes all its waiters at each notify.
type signal struct {
	mu sync.Mutex
	ch chan struct{}
}

// wait gives a channel that closes at the next notify. A waiter takes the channel before it reads the state that
// it waits for, so it loses no notify between the read and the wait.
func (s *signal) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch == nil {
		s.ch = make(chan struct{})
	}
	return s.ch
}

func (s *signal) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch != nil {
		close(s.ch)
		s.ch = nil
	}
}
