package engine

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
)

func limits(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(`
access_password = "correct horse"
trusted_users = ["owner"]

[roles]
lead        = { harness = "claude-code", model = "opus",    effort = "high" }
coordinator = { harness = "claude-code", model = "sonnet",  effort = "medium" }
triager     = { harness = "claude-code", model = "sonnet",  effort = "medium" }
implementer = { harness = "devin",       model = "swe-1.5", effort = "high" }
researcher  = { harness = "antigravity", model = "gemini-3-pro" }
reviewer    = { harness = "claude-code", model = "opus",    effort = "high" }
judge       = { harness = "claude-code", model = "haiku",   effort = "low" }
curator     = { harness = "claude-code", model = "sonnet",  effort = "medium" }
diagnoser   = { harness = "claude-code", model = "sonnet",  effort = "low" }
operator    = { harness = "claude-code", model = "sonnet",  effort = "medium" }
`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestAnAgentWithFreeSlotsStarts(t *testing.T) {
	t.Parallel()
	running := map[string]int{ImplementerRole: 1}

	if got := limitReason(&limits(t).Roles, running, nil, ImplementerRole); got != "" {
		t.Errorf("reason = %q", got)
	}
}

func TestAFullRoleGivesItsCount(t *testing.T) {
	t.Parallel()
	running := map[string]int{ImplementerRole: 2}

	if got := limitReason(&limits(t).Roles, running, nil, ImplementerRole); got != "no free implementer slot (2/2)" {
		t.Errorf("reason = %q", got)
	}
}

func TestAFullLeadLimitBlocksALeadSession(t *testing.T) {
	t.Parallel()
	running := map[string]int{LeadRole: 8}

	if got := limitReason(&limits(t).Roles, running, nil, LeadRole); got != "no free lead slot (8/8)" {
		t.Errorf("reason = %q", got)
	}
}

func TestAnEarlierAgentThatFitsTakesTheLastSlot(t *testing.T) {
	t.Parallel()
	running := map[string]int{ImplementerRole: 1}

	if got := limitReason(&limits(t).Roles, running, []string{ImplementerRole}, ImplementerRole); got != "no free implementer slot (2/2)" {
		t.Errorf("reason = %q", got)
	}
}

func TestAnEarlierAgentOfAFullRoleDoesNotBlockALaterAgent(t *testing.T) {
	t.Parallel()
	running := map[string]int{ImplementerRole: 2}

	if got := limitReason(&limits(t).Roles, running, []string{ImplementerRole}, ReviewerRole); got != "" {
		t.Errorf("reason = %q", got)
	}
}

func at(seconds int64) time.Time {
	return time.Unix(seconds, 0)
}

func work(pullRequest, createdAt int64) Work {
	return Work{Repository: "owner/shop", PullRequest: pullRequest, CreatedAt: at(createdAt)}
}

// order gives the tasks of queue in the order of their rank. Each item of queue is a task id and its time in the queue.
func order(work map[int64]Work, queue [][2]int64) []int64 {
	tasks := make([]int64, 0, len(queue))
	for _, queued := range queue {
		tasks = append(tasks, queued[0])
	}
	times := map[int64]int64{}
	for _, queued := range queue {
		times[queued[0]] = queued[1]
	}
	slices.SortFunc(tasks, func(a, b int64) int {
		return taskRank(work, a, at(times[a]), false).compare(taskRank(work, b, at(times[b]), false))
	})
	return tasks
}

func TestWithNoWorkTheQueueKeepsTheOrderOfTheTimeInTheQueue(t *testing.T) {
	t.Parallel()
	if got := order(nil, [][2]int64{{3, 10}, {1, 30}, {2, 20}}); !reflect.DeepEqual(got, []int64{3, 2, 1}) {
		t.Errorf("order = %v", got)
	}
}

func TestATaskWithWorkComesBeforeAnOlderTaskWithNoWork(t *testing.T) {
	t.Parallel()
	queue := [][2]int64{{1, 10}, {2, 20}, {3, 30}}

	if got := order(map[int64]Work{2: work(7, 500)}, queue); !reflect.DeepEqual(got, []int64{2, 1, 3}) {
		t.Errorf("order = %v", got)
	}
}

func TestTasksWithWorkFollowTheCreationTimeOfTheirPullRequest(t *testing.T) {
	t.Parallel()
	queue := [][2]int64{{1, 10}, {2, 20}, {3, 30}}
	tasksWithWork := map[int64]Work{1: work(7, 500), 2: work(8, 100), 3: work(9, 300)}

	if got := order(tasksWithWork, queue); !reflect.DeepEqual(got, []int64{2, 3, 1}) {
		t.Errorf("order = %v", got)
	}
}

func TestACITaskComesBeforeATaskWithWork(t *testing.T) {
	t.Parallel()
	tasksWithWork := map[int64]Work{1: work(7, 100)}

	if taskRank(tasksWithWork, 2, at(900), true).compare(taskRank(tasksWithWork, 1, at(10), false)) >= 0 {
		t.Error("the CI task comes after the task with work")
	}
	if taskRank(tasksWithWork, 2, at(900), true).compare(taskRank(nil, 3, at(10), false)) >= 0 {
		t.Error("the CI task comes after the older task with no work")
	}
	if taskRank(tasksWithWork, 2, at(900), true).compare(waiterRank(waiter{since: at(5), session: 9})) >= 0 {
		t.Error("the CI task comes after the session")
	}
}

func TestAWaitingSessionWithNoTaskComesAfterEachTaskWithWork(t *testing.T) {
	t.Parallel()
	tasksWithWork := map[int64]Work{2: work(7, 500)}
	session := waiterRank(waiter{since: at(5), session: 9})

	if taskRank(tasksWithWork, 2, at(20), false).compare(session) >= 0 {
		t.Error("the task with work comes after the session")
	}
	if taskRank(tasksWithWork, 1, at(10), false).compare(session) <= 0 {
		t.Error("the task with no work comes before the older session")
	}
}

func TestALaterSessionTakesNoSlotOfAFreeingBindingBeforeAnEarlierSessionThatFollowsTheRoleAndWaitsForItWhileTheFirstBindingIsPaused(t *testing.T) {
	t.Parallel()
	e := &Engine{workers: newWorkers()}
	slots := &implementerSlots{choices: []slotChoice{
		{ImplementerBinding{0, config.RoleBinding{Harness: config.Devin, Max: 1}}, "paused until 2100-01-01 00:00 UTC"},
		{ImplementerBinding{1, config.RoleBinding{Harness: config.ClaudeCode, Max: 1}}, ""},
	}}
	full := 1
	e.workers.bindings[1] = &full
	earlier, later := waiter{since: time.Unix(1, 0), session: 1}, waiter{since: time.Unix(2, 0), session: 2}
	e.workers.waiting[earlier] = slotWait{role: ImplementerRole}
	e.workers.waiting[later] = slotWait{role: ImplementerRole}
	first := &Agent{spec: Spec{Role: ImplementerRole}, level: LevelRole}
	second := &Agent{spec: Spec{Role: ImplementerRole}, level: LevelRole}
	if reason, _ := e.slotReason(first, earlier, nil, true, "", false, "", slots); reason == "" {
		t.Fatal("the earlier session took the full binding")
	}
	full = 0

	reason, ok := e.slotReason(second, later, nil, true, "", false, "", slots)

	if !ok || reason == "" {
		t.Errorf("reason = %q, %v, want the later session to wait for the earlier session", reason, ok)
	}
}
