package engine

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The Roles of the sessions table that take a slot.
const (
	LeadRole        = "lead_chat"
	TriagerRole     = "triager"
	ImplementerRole = "implementer"
	ResearcherRole  = "researcher"
	ReviewerRole    = "reviewer"
	JudgeRole       = "judge"
)

// group is a Role with its name in the queue reasons and its title on the agents page.
type group struct {
	role, name, title string
}

// groups are the Roles that take a slot, in the order of the agents page.
var groups = []group{
	{LeadRole, "lead", "Lead"},
	{TriagerRole, "triager", "Triager"},
	{ImplementerRole, "implementer", "Implementer"},
	{ResearcherRole, "researcher", "Researcher"},
	{ReviewerRole, "reviewer", "Reviewer"},
	{JudgeRole, "judge", "Judge"},
}

// workerRoles are the Worker Roles. A pause of its Harness and a drain hold a Worker in the queue.
var workerRoles = map[string]bool{ImplementerRole: true, ResearcherRole: true, ReviewerRole: true}

// ErrLeftQueue tells that the task of a session left the queue before the session got a slot, for example after a decline.
var ErrLeftQueue = errors.New("the task left the queue")

// roleBinding gives the Role binding of role in cfg.
func roleBinding(cfg *config.Config, role string) (config.RoleBinding, bool) {
	roles := &cfg.Roles
	switch role {
	case LeadRole:
		return roles.Lead, true
	case TriagerRole:
		return roles.Triager, true
	case ImplementerRole:
		return roles.Implementer, true
	case ResearcherRole:
		return roles.Researcher, true
	case ReviewerRole:
		return roles.Reviewer, true
	case JudgeRole:
		return roles.Judge, true
	}
	return config.RoleBinding{}, false
}

// Work is a pull request with a fix round for a failed check run or a conflict round.
type Work struct {
	Repository  string
	PullRequest int64
	CreatedAt   time.Time
}

func pullRequestWork(repository github.Repository, pullRequest *gh.PullRequest) Work {
	return Work{Repository: repository.FullName, PullRequest: int64(pullRequest.GetNumber()), CreatedAt: pullRequest.GetCreatedAt().Time}
}

type workers struct {
	mu sync.Mutex
	// running counts the sessions that hold a slot, by Role.
	running map[string]int
	// queued holds the Role of the session of each queued task, by the id of the task.
	queued map[int64]string
	// waiting holds the Role of each queued session with no task.
	waiting map[waiter]string
	// work holds the pull requests with work for an agent, by the id of their task.
	work map[int64]Work
	// changed wakes all queued sessions at each change of the counts, the queue or the work.
	changed signal
}

// waiter is a queued session with no task.
type waiter struct {
	since   time.Time
	session int64
}

func newWorkers() workers {
	return workers{running: map[string]int{}, queued: map[int64]string{}, waiting: map[waiter]string{}}
}

// ReplaceWork replaces the pull requests with work for an agent, by the id of their task.
func (e *Engine) ReplaceWork(work map[int64]Work) {
	w := &e.workers
	w.mu.Lock()
	same := maps.EqualFunc(w.work, work, func(a, b Work) bool {
		return a.Repository == b.Repository && a.PullRequest == b.PullRequest && a.CreatedAt.Equal(b.CreatedAt)
	})
	w.work = work
	w.mu.Unlock()
	if !same {
		w.changed.notify()
	}
}

// addWork adds the pull request with work for an agent of the task, before the next ReplaceWork.
func (e *Engine) addWork(task int64, work Work) {
	w := &e.workers
	w.mu.Lock()
	if w.work == nil {
		w.work = map[int64]Work{}
	}
	w.work[task] = work
	w.mu.Unlock()
	w.changed.notify()
}

// currentWork gives the pull requests with work for an agent, by the id of their task.
func (e *Engine) currentWork() map[int64]Work {
	w := &e.workers
	w.mu.Lock()
	defer w.mu.Unlock()
	return maps.Clone(w.work)
}

// WakeQueue makes each queued session read the queue again. Call it after a change of the state of a queued task,
// for example after a decline.
func (e *Engine) WakeQueue() {
	e.workers.changed.notify()
}

// rank is the place of a queued session: the lowest rank gets the next slot. A task of a pull request with work
// (a fix round for a failed check run or a conflict round) comes first, by the creation time of the pull request.
// The others follow by their time in the queue, and at the same time a task comes before a session with no task.
type rank struct {
	// class is 0 for a task with work and 1 for the others.
	class int
	at    time.Time
	// kind is 0 for a task and 1 for a session with no task.
	kind int
	// id is the id of the task, or the id of a session with no task.
	id int64
}

func (r rank) compare(other rank) int {
	return cmp.Or(cmp.Compare(r.class, other.class), r.at.Compare(other.at), cmp.Compare(r.kind, other.kind), cmp.Compare(r.id, other.id))
}

func taskRank(work map[int64]Work, task int64, queuedAt time.Time) rank {
	if found, ok := work[task]; ok {
		return rank{0, found.CreatedAt, 0, task}
	}
	return rank{1, queuedAt, 0, task}
}

func waiterRank(w waiter) rank {
	return rank{1, w.since, 1, w.session}
}

// queuedTask is a task in the state queued with its time in the queue.
type queuedTask struct {
	id       int64
	queuedAt time.Time
}

func (e *Engine) queuedTasks(ctx context.Context) ([]queuedTask, error) {
	rows, err := e.queries.ListQueuedTasks(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]queuedTask, 0, len(rows))
	for _, row := range rows {
		queuedAt, err := time.Parse(time.RFC3339Nano, row.QueuedAt.String)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, queuedTask{row.ID, queuedAt})
	}
	return tasks, nil
}

// takeSlot holds until the session of a gets a slot of its Role, and shows the reason of the wait on the session.
// A session with a task waits at the place of its task, and the task goes from queued to working. A session with
// no task waits behind each session that queued before it.
func (e *Engine) takeSlot(ctx context.Context, a *Agent) error {
	w := &e.workers
	role, task := a.spec.Role, a.spec.Task
	worker := workerRoles[role]
	place := waiter{time.Now(), a.id}
	w.mu.Lock()
	if task != 0 {
		w.queued[task] = role
	} else {
		w.waiting[place] = role
	}
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.queued, task)
		delete(w.waiting, place)
		w.mu.Unlock()
		w.changed.notify()
	}()
	shown := ""
	for {
		changed := w.changed.wait()
		queue, err := e.queuedTasks(ctx)
		if err != nil {
			return err
		}
		paused := ""
		if worker {
			pause, err := e.queries.GetHarnessPause(ctx, string(a.harness))
			switch {
			case err == nil:
				if paused, err = pausedReason(pause); err != nil {
					return err
				}
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
		}
		reason, ok := e.slotReason(a, place, queue, worker, paused)
		if !ok {
			return ErrLeftQueue
		}
		if reason == "" {
			a.slot = role
			a.tracked = a.tracked || worker
			break
		}
		if reason != shown {
			session, err := e.queries.SetQueueReason(ctx, store.SetQueueReasonParams{QueueReason: sql.NullString{String: reason, Valid: true}, ID: a.id})
			if err != nil {
				return err
			}
			e.publish(Change{Node: new(node(session))})
			shown = reason
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
	if task == 0 {
		return nil
	}
	moved, err := e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: "working", ID: task, FromState: "queued"})
	if err != nil {
		return err
	}
	if moved == 0 {
		return ErrLeftQueue
	}
	return nil
}

// slotReason gives the reason why the session of a must wait, or "" when the session took a slot. A Worker that
// takes a slot also counts in the drain. It gives false when the task of the session is not in queue. paused is
// the reason of a pause of the Harness of a Worker, or "".
func (e *Engine) slotReason(a *Agent, place waiter, queue []queuedTask, worker bool, paused string) (string, bool) {
	w := &e.workers
	role, task := a.spec.Role, a.spec.Task
	w.mu.Lock()
	defer w.mu.Unlock()
	own := waiterRank(place)
	if task != 0 {
		index := slices.IndexFunc(queue, func(queued queuedTask) bool { return queued.id == task })
		if index < 0 {
			return "", false
		}
		own = taskRank(w.work, task, queue[index].queuedAt)
	}
	type ranked struct {
		rank rank
		role string
	}
	var earlier []ranked
	for _, queued := range queue {
		other, ok := w.queued[queued.id]
		if r := taskRank(w.work, queued.id, queued.queuedAt); ok && r.compare(own) < 0 {
			earlier = append(earlier, ranked{r, other})
		}
	}
	for other, otherRole := range w.waiting {
		if r := waiterRank(other); r.compare(own) < 0 {
			earlier = append(earlier, ranked{r, otherRole})
		}
	}
	slices.SortFunc(earlier, func(a, b ranked) int { return a.rank.compare(b.rank) })
	roles := make([]string, 0, len(earlier))
	for _, other := range earlier {
		roles = append(roles, other.role)
	}
	var reason string
	switch {
	case worker && e.draining():
		reason = drainReason
	case paused != "":
		reason = paused
	default:
		reason = limitReason(e.config, w.running, roles, role)
	}
	// The drain can start after the check above.
	if reason == "" && worker && !e.trackWorker() {
		reason = drainReason
	}
	if reason != "" {
		return reason, true
	}
	w.running[role]++
	delete(w.queued, task)
	delete(w.waiting, place)
	return "", true
}

// releaseSlot frees a slot of role.
func (e *Engine) releaseSlot(role string) {
	w := &e.workers
	w.mu.Lock()
	w.running[role]--
	w.mu.Unlock()
	w.changed.notify()
}

// limitReason gives the reason why a session of role must wait, or "". Each session of earlier that fits takes
// a slot before it, so a session of a full Role does not hold a later session of another Role.
func limitReason(cfg *config.Config, running map[string]int, earlier []string, role string) string {
	counts := maps.Clone(running)
	for _, other := range earlier {
		if fullReason(cfg, counts, other) == "" {
			counts[other]++
		}
	}
	return fullReason(cfg, counts, role)
}

func fullReason(cfg *config.Config, running map[string]int, role string) string {
	binding, _ := roleBinding(cfg, role)
	if binding.CountsInMaxAgents {
		total := 0
		for other, count := range running {
			if otherBinding, _ := roleBinding(cfg, other); otherBinding.CountsInMaxAgents {
				total += count
			}
		}
		if total >= cfg.MaxAgents {
			return fmt.Sprintf("no free agent slot (%d/%d)", total, cfg.MaxAgents)
		}
	}
	if count := running[role]; count >= binding.Max {
		index := slices.IndexFunc(groups, func(g group) bool { return g.role == role })
		return fmt.Sprintf("no free %s slot (%d/%d)", groups[index].name, count, binding.Max)
	}
	return ""
}
