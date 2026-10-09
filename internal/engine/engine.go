// Package engine holds the work of Mobius on the managed repositories: the poll, the Mobius labels, the checkup,
// the trust rules, the Workstreams with their local copy, the Autopilot switch and the close, the dispatch, the Lead
// chat with the Lead events, the Triager, the Inbox, the Tasks tab, the Implementer with the local check and the pull
// request, the Reviewer with the review rounds, the Judge, the fix rounds and the conflict rounds, the end of a task,
// the Researcher, the agent sessions with their Mobius tools and Transcripts, the Worker slots, the usage-limit pauses,
// the Housekeeper, the recovery after a restart, the drain and the upgrade.
package engine

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
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
	// quiet holds the newest item of the Judge of each task in its quiet period, by the id of the task. Only the poll
	// uses it.
	quiet map[int64]quietItem
	// pulls holds what the poll read of each pull request, for the Judge. Only the poll uses it.
	pulls map[pullKey]*pullState
	// ciWait holds the head of each task in checks that has no CI, with the time of its first poll, by the id of the task.
	// Only the poll uses it.
	ciWait map[int64]ciWait

	workers workers
	drain   drain
	// pausing makes one pause of two sessions that reach the same usage limit, and one Inbox item of two checks on a
	// full disk.
	pausing sync.Mutex
	// limitItems holds the last usage-limit Inbox item of each Harness. Recover fills it before the first session. Then only pause uses it, under pausing.
	limitItems map[config.Harness]int64
	// pausesChanged wakes the sessions that wait for the end of a pause.
	pausesChanged signal
	upgrading     atomic.Bool

	// chatOrder makes a chat message and its item take the same place in the chat and in the queue of the agent.
	chatOrder sync.Mutex
	// chatsMu guards chats and the fields of each chat.
	chatsMu sync.Mutex
	chats   map[ChatKey]*chat
	// triages holds the Triager of each issue.
	triagesMu sync.Mutex
	triages   map[triageKey]triage
	// memoryMu makes the read and the write of the memory files, and the checks before a write, run one after the other.
	memoryMu sync.Mutex
	// gitMu makes the git commands of the bare clones run one after the other. Two git commands that write the refs of
	// a clone at the same time can fail on a ref lock.
	gitMu sync.Mutex
	// checks holds one value for each local check that runs, at most max_checks.
	checks chan struct{}
	// diskFreed wakes the checks that wait for free disk space.
	diskFreed signal

	// stopsMu guards stops, live and closed.
	stopsMu sync.Mutex
	// stops holds the context of the Workers of each task, by the id of the task, of each Researcher, by its
	// researcherKey, and of each Curator, by its curatorKey. A stop of the task, the Researcher or the Curator ends
	// the context.
	stops map[any]stopper
	// live counts the goroutines of the Workers of each key that run now, and the holds of the key. A goroutine counts
	// from its start to its return, also after a stop of its key.
	live map[any]int
	// closed tells that Run ended, so no new Worker starts.
	closed bool
	// running counts the Workers that run.
	running sync.WaitGroup

	// detailsMu guards implementers, researchers, and the pending details of their sessions.
	detailsMu sync.Mutex
	// implementers holds the open Implementer session of each task, by the id of the task.
	implementers map[int64]*Agent
	// researchers holds the session of each Researcher that runs, by the id of its session.
	researchers map[int64]*Agent

	// curatorsMu guards curators.
	curatorsMu sync.Mutex
	// curators holds each repository with a Curator that runs. The value tells that one more Curator waits.
	curators map[string]bool

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
		db:           db,
		queries:      store.New(db),
		github:       gh,
		config:       cfg,
		agents:       agents,
		labelsFixed:  map[string]bool{},
		recovered:    map[string]bool{},
		copied:       map[string]bool{},
		quiet:        map[int64]quietItem{},
		pulls:        map[pullKey]*pullState{},
		ciWait:       map[int64]ciWait{},
		limitItems:   map[config.Harness]int64{},
		workers:      newWorkers(),
		chats:        map[ChatKey]*chat{},
		triages:      map[triageKey]triage{},
		checks:       make(chan struct{}, cfg.MaxChecks),
		stops:        map[any]stopper{},
		live:         map[any]int{},
		implementers: map[int64]*Agent{},
		researchers:  map[int64]*Agent{},
		curators:     map[string]bool{},
		listeners:    map[chan Change]bool{},
	}
}

// stopper is a context of Workers with the function that ends it. starts is the number of Workers that started with
// the context.
type stopper struct {
	ctx    context.Context
	stop   context.CancelFunc
	starts int
}

// startWorker runs work in the background with the context of key, a task id, a researcherKey or a curatorKey. The context
// ends at the next stop of key and at the end of Run. After the end of Run, startWorker does nothing and gives false.
func (e *Engine) startWorker(key any, work func(context.Context)) bool {
	return e.startNumberedWorker(key, func(ctx context.Context, _ int) { work(ctx) })
}

// startNumberedWorker runs work like startWorker. work gets the number of its start, which startedAfter compares.
func (e *Engine) startNumberedWorker(key any, work func(context.Context, int)) bool {
	return e.start(key, false, work)
}

// startIdleWorker runs work like startNumberedWorker, and gives false with no start when a Worker of key runs. The
// check and the start are one step, so a key never gets a second Worker from it.
func (e *Engine) startIdleWorker(key any, work func(context.Context, int)) bool {
	return e.start(key, true, work)
}

func (e *Engine) start(key any, idle bool, work func(context.Context, int)) bool {
	e.stopsMu.Lock()
	defer e.stopsMu.Unlock()
	if e.closed || idle && e.live[key] > 0 {
		return false
	}
	found, ok := e.stops[key]
	if !ok {
		found.ctx, found.stop = context.WithCancel(context.Background())
	}
	found.starts++
	e.stops[key] = found
	e.live[key]++
	e.running.Go(func() {
		defer e.uncount(key)
		work(found.ctx, found.starts)
	})
	return true
}

// uncount removes one goroutine of a Worker of key, or one hold of key, from the count.
func (e *Engine) uncount(key any) {
	e.stopsMu.Lock()
	defer e.stopsMu.Unlock()
	if e.live[key]--; e.live[key] == 0 {
		delete(e.live, key)
	}
}

// holdWorker makes key count as a key with a Worker until the returned function runs. A step that changes the state of
// a task to queued or working before it starts the goroutine of the Worker holds the key, so that the poll does not
// start a second Worker in between.
func (e *Engine) holdWorker(key any) func() {
	e.stopsMu.Lock()
	defer e.stopsMu.Unlock()
	e.live[key]++
	return func() { e.uncount(key) }
}

// hasWorker tells if a goroutine of a Worker of key runs, or a step holds key. A Worker that waits for a free slot runs.
func (e *Engine) hasWorker(key any) bool {
	e.stopsMu.Lock()
	defer e.stopsMu.Unlock()
	return e.live[key] > 0
}

// startedAfter tells if a Worker of key started after the Worker with the start number start.
func (e *Engine) startedAfter(key any, start int) bool {
	e.stopsMu.Lock()
	defer e.stopsMu.Unlock()
	return e.stops[key].starts > start
}

// ended tells that Run ended.
func (e *Engine) ended() bool {
	e.stopsMu.Lock()
	defer e.stopsMu.Unlock()
	return e.closed
}

// stop ends the context of the Workers of key.
func (e *Engine) stop(key any) {
	e.stopsMu.Lock()
	defer e.stopsMu.Unlock()
	if found, ok := e.stops[key]; ok {
		found.stop()
		delete(e.stops, key)
	}
}

// stopWorkers ends each Worker and waits for its end.
func (e *Engine) stopWorkers() {
	e.stopsMu.Lock()
	e.closed = true
	for key, found := range e.stops {
		found.stop()
		delete(e.stops, key)
	}
	e.stopsMu.Unlock()
	e.running.Wait()
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
		e.limitItems[config.Harness(pause.Harness)] = pause.InboxItem
		if err := e.timer(pause); err != nil {
			return err
		}
	}
	return nil
}

// Run polls each poll_interval and runs the Housekeeper each housekeeper_interval, until ctx ends. Then it ends each
// Worker, each chat and each Triager, and waits for their ends.
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() { every(ctx, e.config.HousekeeperInterval, e.keepHouse) })
	every(ctx, e.config.PollInterval, e.poll)
	wg.Wait()
	e.stopWorkers()
	e.stopAgents()
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
