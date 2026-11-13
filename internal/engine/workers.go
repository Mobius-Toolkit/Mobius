package engine

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
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
	CoordinatorRole = "coordinator"
	TriagerRole     = "triager"
	ImplementerRole = "implementer"
	ResearcherRole  = "researcher"
	ReviewerRole    = "reviewer"
	JudgeRole       = "judge"
	CuratorRole     = "curator"
	DiagnoserRole   = "diagnoser"
	OperatorRole    = "operator"
)

// group is a Role with its name in the queue reasons and its title on the agents page.
type group struct {
	role, name, title string
}

// groups are the Roles that take a slot, in the order of the agents page.
var groups = []group{
	{LeadRole, "lead", "Lead"},
	{CoordinatorRole, "coordinator", "Coordinator"},
	{TriagerRole, "triager", "Triager"},
	{ImplementerRole, "implementer", "Implementer"},
	{ResearcherRole, "researcher", "Researcher"},
	{ReviewerRole, "reviewer", "Reviewer"},
	{JudgeRole, "judge", "Judge"},
	{CuratorRole, "curator", "Curator"},
	{DiagnoserRole, "diagnoser", "Diagnoser"},
	{OperatorRole, "operator", "Operator"},
}

// roleTitle gives the title of role in groups.
func roleTitle(role string) string {
	return groups[slices.IndexFunc(groups, func(g group) bool { return g.role == role })].title
}

// workerRoles are the Worker Roles. A pause of its Harness and a drain hold a Worker in the queue.
var workerRoles = map[string]bool{ImplementerRole: true, ResearcherRole: true, ReviewerRole: true, CuratorRole: true, DiagnoserRole: true, OperatorRole: true}

// ErrLeftQueue tells that the task of a session left the queue before the session got a slot, for example after a decline.
var ErrLeftQueue = errors.New("the task left the queue")

// roleBinding gives the Role binding of role in roles.
func roleBinding(roles *config.Roles, role string) (config.RoleBinding, bool) {
	switch role {
	case LeadRole:
		return roles.Lead, true
	case CoordinatorRole:
		return roles.Coordinator, true
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
	case CuratorRole:
		return roles.Curator, true
	case DiagnoserRole:
		return roles.Diagnoser, true
	case OperatorRole:
		return roles.Operator, true
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
	// bindings counts the Implementer sessions that hold a slot, by the id of their binding. The first binding has the id 0.
	// A session keeps the counter that it incremented, so the count stays with its binding when a removal changes the id
	// of a binding.
	bindings map[int64]*int
	// queued holds the Role and the slot claim of the session of each queued task, by the id of the task.
	queued map[int64]slotWait
	// waiting holds the Role and the slot claim of each queued session with no task.
	waiting map[waiter]slotWait
	// work holds the pull requests with work for an agent, by the id of their task.
	work map[int64]Work
	// changed wakes all queued sessions at each change of the counts, the queue or the work.
	changed signal
}

// slotPin tells which binding of the Implementer a queued session can take: the first binding only (a session with a
// setting of its task or Workstream), the kept binding only (a round with a kept session of the level Role), or each
// binding.
type slotPin struct {
	first   bool
	hasKept bool
	kept    config.RoleBinding
}

func (p slotPin) keptBinding() *config.RoleBinding {
	if !p.hasKept {
		return nil
	}
	return &p.kept
}

// slotWait is the Role and the slot claim of a queued session.
type slotWait struct {
	role string
	pin  slotPin
}

// waiter is a queued session with no task.
type waiter struct {
	since   time.Time
	session int64
}

func newWorkers() workers {
	return workers{running: map[string]int{}, bindings: map[int64]*int{}, queued: map[int64]slotWait{}, waiting: map[waiter]slotWait{}}
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

// rank is the place of a queued session: the lowest rank gets the next slot. A CI task (see queuedTask) comes first,
// by its time in the queue. A task of a pull request with work (a fix round for a failed check run or a conflict
// round) follows, by the creation time of the pull request. The others follow by their time in the queue, and at the
// same time a task comes before a session with no task.
type rank struct {
	// class is 0 for a CI task, 1 for a task with work and 2 for the others.
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

func taskRank(work map[int64]Work, task int64, queuedAt time.Time, ci bool) rank {
	if ci {
		return rank{0, queuedAt, 0, task}
	}
	if found, ok := work[task]; ok {
		return rank{1, found.CreatedAt, 0, task}
	}
	return rank{2, queuedAt, 0, task}
}

func waiterRank(w waiter) rank {
	return rank{2, w.since, 1, w.session}
}

// queuedTask is a task in the state queued with its time in the queue. ci tells that its issue is the task issue of an
// open hard break of the CI of the default branch.
type queuedTask struct {
	id       int64
	queuedAt time.Time
	ci       bool
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
		tasks = append(tasks, queuedTask{row.ID, queuedAt, row.Ci})
	}
	return tasks, nil
}

// takeSlot holds until the session of a gets a slot of its Role, and shows the reason of the wait on the session.
// A session with a task waits at the place of its task, and the task goes from queued to working. A session with
// no task waits behind each session that queued before it. The drain does not count a session that is not a Worker
// while the access check of its binding holds it.
func (e *Engine) takeSlot(ctx context.Context, a *Agent) error {
	w := &e.workers
	role, task := a.spec.Role, a.spec.Task
	worker := workerRoles[role]
	place := waiter{time.Now(), a.id}
	w.mu.Lock()
	if task != 0 {
		w.queued[task] = slotWait{role: role}
	} else {
		w.waiting[place] = slotWait{role: role}
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
	var slots *implementerSlots
	for {
		changed := w.changed.wait()
		queue, err := e.queuedTasks(ctx)
		if err != nil {
			return err
		}
		if err := e.followBinding(ctx, a); err != nil {
			return err
		}
		paused := ""
		if worker {
			if paused, err = e.pausedHarness(ctx, a.binding.Harness); err != nil {
				return err
			}
		}
		if role == ImplementerRole {
			if slots, err = e.implementerSlots(ctx, a.spec); err != nil {
				return err
			}
		}
		startOff := false
		if task != 0 {
			if startOff, err = e.startOff(ctx); err != nil {
				return err
			}
		}
		noAccess := e.accessHold(a.binding)
		if !worker {
			if noAccess != "" {
				a.setUncounted(true)
			} else if !a.setUncounted(false) {
				noAccess = drainReason
			}
		}
		reason, ok := e.slotReason(a, place, queue, worker, paused, startOff, noAccess, slots)
		if !ok {
			return ErrLeftQueue
		}
		if reason == "" {
			a.slot = role
			a.tracked = a.tracked || worker
			break
		}
		if reason == startOffReason {
			if err := e.holdWork(ctx, "start", a.spec.Repository, a.spec.Issue.Int64); err != nil {
				return err
			}
		}
		if reason != shown {
			session, err := e.queries.SetQueueReason(ctx, store.SetQueueReasonParams{QueueReason: sql.NullString{String: reason, Valid: true}, ID: a.id})
			if err != nil {
				return err
			}
			e.publish(Change{Node: new(e.node(session))})
			if strings.HasPrefix(reason, pausedPrefix) || strings.HasPrefix(shown, pausedPrefix) {
				e.publish(Change{Pauses: true})
			}
			shown = reason
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
	if slots != nil && a.level == LevelRole {
		if err := e.useBinding(ctx, a, slots.binding(a.slotBinding)); err != nil {
			return err
		}
	}
	if task == 0 {
		return nil
	}
	moved, err := e.setTaskState(ctx, store.SetTaskStateParams{State: "working", ID: task, FromState: "queued"})
	if err != nil {
		return err
	}
	if moved == 0 {
		return ErrLeftQueue
	}
	return nil
}

// followBinding gives the session of a that waits for a slot the current binding of its task, Workstream or Role,
// because the Owner can change a setting while the session waits. A session that runs keeps its binding. For the
// Implementer, the binding of the Role is the first binding. A session of the level LevelRole gets the binding of its
// slot when it takes the slot.
func (e *Engine) followBinding(ctx context.Context, a *Agent) error {
	roles := e.mergedRoles()
	binding, level, err := e.sessionBinding(ctx, &roles, a.spec)
	if err != nil {
		return err
	}
	a.level = level
	return e.useBinding(ctx, a, binding)
}

// useBinding gives the session of a the binding, and writes it to the row of the session.
func (e *Engine) useBinding(ctx context.Context, a *Agent, binding config.RoleBinding) error {
	if binding == a.binding {
		return nil
	}
	session, err := e.queries.SetSessionBinding(ctx, store.SetSessionBindingParams{
		Harness: string(binding.Harness),
		Model:   binding.Model,
		Effort:  sql.NullString{String: binding.Effort, Valid: binding.Effort != ""},
		ID:      a.id,
	})
	if err != nil {
		return err
	}
	a.binding = binding
	e.publish(Change{Node: new(e.node(session))})
	e.publish(Change{Pauses: true})
	return nil
}

// slotChoice is a binding of the Implementer with the reason of the pause or of the access check that holds a session on it, or "".
type slotChoice struct {
	ImplementerBinding
	hold string
}

// implementerSlots are the bindings of the Implementer that a session can take a slot of.
type implementerSlots struct {
	choices []slotChoice
	// kept is the binding of the kept session of the task of the session when it follows the Role, or nil.
	kept *config.RoleBinding
}

// implementerSlots gives the bindings of the Implementer with their holds, for the session of spec. A session that
// cannot continue the kept session of its task has no kept binding.
func (e *Engine) implementerSlots(ctx context.Context, spec Spec) (*implementerSlots, error) {
	list := e.implementerList()
	slots := &implementerSlots{choices: make([]slotChoice, 0, len(list))}
	if spec.Continues {
		slots.kept = e.keptBinding(spec.Task)
	}
	for _, b := range list {
		hold, err := e.pausedHarness(ctx, b.Binding.Harness)
		if err != nil {
			return nil, err
		}
		if hold == "" {
			hold = e.accessHold(b.Binding)
		}
		slots.choices = append(slots.choices, slotChoice{b, hold})
	}
	return slots, nil
}

// keptChoice gives the binding of the list that is equal to kept, or nil.
func (s *implementerSlots) keptChoice(kept *config.RoleBinding) *slotChoice {
	if kept == nil {
		return nil
	}
	index := slices.IndexFunc(s.choices, func(c slotChoice) bool { return c.Binding == *kept })
	if index < 0 {
		return nil
	}
	return &s.choices[index]
}

// waitsOn gives the binding whose reason a session shows when no binding can take it: the binding of the kept session
// when it is in the list, and else the first binding.
func (s *implementerSlots) waitsOn(kept *config.RoleBinding) slotChoice {
	if c := s.keptChoice(kept); c != nil {
		return *c
	}
	return s.choices[0]
}

// pick gives the binding that the next session takes. A session with a kept session in the list takes only the binding
// of the kept session, because the round continues it. Else it is a binding with no hold and a free slot, with the
// fewest running sessions in counts, and then the first in the list.
func (s *implementerSlots) pick(counts map[int64]int, kept *config.RoleBinding) (slotChoice, bool) {
	if c := s.keptChoice(kept); c != nil {
		return *c, c.hold == "" && counts[c.ID] < c.Binding.Max
	}
	var best *slotChoice
	for i := range s.choices {
		c := &s.choices[i]
		if c.hold != "" || counts[c.ID] >= c.Binding.Max {
			continue
		}
		if best == nil || counts[c.ID] < counts[best.ID] {
			best = c
		}
	}
	if best == nil {
		return slotChoice{}, false
	}
	return *best, true
}

// take gives the binding that a queued session with pin takes, or false when it cannot take a slot.
func (s *implementerSlots) take(counts map[int64]int, pin slotPin) (slotChoice, bool) {
	if pin.first {
		c := s.choices[0]
		return c, counts[c.ID] < c.Binding.Max
	}
	return s.pick(counts, pin.keptBinding())
}

// binding gives the binding with the id id.
func (s *implementerSlots) binding(id int64) config.RoleBinding {
	return s.choices[slices.IndexFunc(s.choices, func(c slotChoice) bool { return c.ID == id })].Binding
}

// pausedHarness gives the queue reason of the pause of harness, or "" when it has no pause.
func (e *Engine) pausedHarness(ctx context.Context, harness config.Harness) (string, error) {
	pause, err := e.queries.GetHarnessPause(ctx, string(harness))
	switch {
	case err == nil:
		return pausedReason(pause)
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	}
	return "", err
}

// slotReason gives the reason why the session of a must wait, or "" when the session took a slot. A Worker that
// takes a slot also counts in the drain. It gives false when the task of the session is not in queue. paused is
// the reason of a pause of the Harness of a Worker, or "". startOff tells that the Start switch is off and the
// session has a task. noAccess is the reason why the access check of the Role binding of the session holds it, or "".
// slots is nil for each Role but the Implementer. A session of the Implementer that follows the Role takes the binding
// of slots with the fewest sessions, and a session with a setting takes a slot of the first binding. When no binding
// can take a session that follows the Role, it shows the reason of the first binding, which is the binding of a.
func (e *Engine) slotReason(a *Agent, place waiter, queue []queuedTask, worker bool, paused string, startOff bool, noAccess string, slots *implementerSlots) (string, bool) {
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
		own = taskRank(w.work, task, queue[index].queuedAt, queue[index].ci)
	}
	type ranked struct {
		rank rank
		slotWait
	}
	var earlier []ranked
	for _, queued := range queue {
		other, ok := w.queued[queued.id]
		if r := taskRank(w.work, queued.id, queued.queuedAt, queued.ci); ok && r.compare(own) < 0 {
			earlier = append(earlier, ranked{r, other})
		}
	}
	for other, otherWait := range w.waiting {
		if r := waiterRank(other); r.compare(own) < 0 {
			earlier = append(earlier, ranked{r, otherWait})
		}
	}
	slices.SortFunc(earlier, func(a, b ranked) int { return a.rank.compare(b.rank) })
	roles := make([]string, 0, len(earlier))
	for _, other := range earlier {
		roles = append(roles, other.role)
	}
	var counts map[int64]int
	var chosen *slotChoice
	if slots != nil {
		counts = make(map[int64]int, len(w.bindings))
		for id, count := range w.bindings {
			counts[id] = *count
		}
		pin := slotPin{first: a.level != LevelRole}
		if !pin.first && slots.kept != nil {
			pin.hasKept, pin.kept = true, *slots.kept
		}
		if w.setPin(task, place, pin) {
			w.changed.notify()
		}
		for _, other := range earlier {
			if other.role != ImplementerRole {
				continue
			}
			if c, ok := slots.take(counts, other.pin); ok {
				counts[c.ID]++
			}
		}
		if a.level == LevelRole {
			if c, ok := slots.pick(counts, slots.kept); ok {
				chosen = &c
				paused, noAccess = "", ""
			} else {
				paused, noAccess = slots.waitsOn(slots.kept).hold, ""
			}
		}
	}
	var reason string
	switch {
	case worker && e.draining():
		reason = drainReason
	case startOff:
		reason = startOffReason
	case paused != "":
		reason = paused
	case noAccess != "":
		reason = noAccess
	case chosen != nil:
	case slots != nil:
		target := slots.choices[0]
		if a.level == LevelRole {
			target = slots.waitsOn(slots.kept)
		}
		reason = fullReason(&config.Roles{Implementer: target.Binding}, map[string]int{role: counts[target.ID]}, role)
	default:
		merged := e.mergedRoles()
		reason = limitReason(&merged, w.running, roles, role)
	}
	// The drain can start after the check above.
	if reason == "" && worker && !e.trackWorker() {
		reason = drainReason
	}
	if reason != "" {
		return reason, true
	}
	w.running[role]++
	if slots != nil {
		a.slotBinding = 0
		if chosen != nil {
			a.slotBinding = chosen.ID
		}
		if w.bindings[a.slotBinding] == nil {
			w.bindings[a.slotBinding] = new(int)
		}
		a.slotCount = w.bindings[a.slotBinding]
		*a.slotCount++
	}
	delete(w.queued, task)
	delete(w.waiting, place)
	return "", true
}

// setPin sets the slot claim of the queued session of task, or of place with no task. It tells if the claim changed. The
// caller holds mu.
func (w *workers) setPin(task int64, place waiter, pin slotPin) bool {
	if task != 0 {
		wait := w.queued[task]
		w.queued[task] = slotWait{wait.role, pin}
		return wait.pin != pin
	}
	wait := w.waiting[place]
	w.waiting[place] = slotWait{wait.role, pin}
	return wait.pin != pin
}

// releaseSlot frees a slot of role. count is the counter of the Implementer binding of the slot.
func (e *Engine) releaseSlot(role string, count *int) {
	w := &e.workers
	w.mu.Lock()
	w.running[role]--
	if count != nil {
		*count--
	}
	w.mu.Unlock()
	w.changed.notify()
}

// limitReason gives the reason why a session of role must wait, or "". Each session of earlier that fits takes
// a slot before it, so a session of a full Role does not hold a later session of another Role.
func limitReason(roles *config.Roles, running map[string]int, earlier []string, role string) string {
	counts := maps.Clone(running)
	for _, other := range earlier {
		if fullReason(roles, counts, other) == "" {
			counts[other]++
		}
	}
	return fullReason(roles, counts, role)
}

func fullReason(roles *config.Roles, running map[string]int, role string) string {
	binding, _ := roleBinding(roles, role)
	if count := running[role]; count >= binding.Max {
		index := slices.IndexFunc(groups, func(g group) bool { return g.role == role })
		return fmt.Sprintf("no free %s slot (%d/%d)", groups[index].name, count, binding.Max)
	}
	return ""
}
