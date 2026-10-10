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
triager     = { harness = "claude-code", model = "sonnet",  effort = "medium" }
implementer = { harness = "devin",       model = "swe-1.5", effort = "high" }
researcher  = { harness = "antigravity", model = "gemini-3-pro" }
reviewer    = { harness = "claude-code", model = "opus",    effort = "high" }
judge       = { harness = "claude-code", model = "haiku",   effort = "low" }
curator     = { harness = "claude-code", model = "sonnet",  effort = "medium" }
`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestAnAgentWithFreeSlotsStarts(t *testing.T) {
	t.Parallel()
	running := map[string]int{ImplementerRole: 1}

	if got := limitReason(limits(t), running, nil, ImplementerRole); got != "" {
		t.Errorf("reason = %q", got)
	}
}

func TestAFullRoleGivesItsCount(t *testing.T) {
	t.Parallel()
	running := map[string]int{ImplementerRole: 2}

	if got := limitReason(limits(t), running, nil, ImplementerRole); got != "no free implementer slot (2/2)" {
		t.Errorf("reason = %q", got)
	}
}

func TestAFullLeadLimitBlocksALeadSession(t *testing.T) {
	t.Parallel()
	running := map[string]int{LeadRole: 8}

	if got := limitReason(limits(t), running, nil, LeadRole); got != "no free lead slot (8/8)" {
		t.Errorf("reason = %q", got)
	}
}

func TestAnEarlierAgentThatFitsTakesTheLastSlot(t *testing.T) {
	t.Parallel()
	running := map[string]int{ImplementerRole: 1}

	if got := limitReason(limits(t), running, []string{ImplementerRole}, ImplementerRole); got != "no free implementer slot (2/2)" {
		t.Errorf("reason = %q", got)
	}
}

func TestAnEarlierAgentOfAFullRoleDoesNotBlockALaterAgent(t *testing.T) {
	t.Parallel()
	running := map[string]int{ImplementerRole: 2}

	if got := limitReason(limits(t), running, []string{ImplementerRole}, ReviewerRole); got != "" {
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
		return taskRank(work, a, at(times[a])).compare(taskRank(work, b, at(times[b])))
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

func TestAWaitingSessionWithNoTaskComesAfterEachTaskWithWork(t *testing.T) {
	t.Parallel()
	tasksWithWork := map[int64]Work{2: work(7, 500)}
	session := waiterRank(waiter{at(5), 9})

	if taskRank(tasksWithWork, 2, at(20)).compare(session) >= 0 {
		t.Error("the task with work comes after the session")
	}
	if taskRank(tasksWithWork, 1, at(10)).compare(session) <= 0 {
		t.Error("the task with no work comes before the older session")
	}
}
