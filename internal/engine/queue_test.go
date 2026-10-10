package engine_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// roleSpec gives the spec of a session of role in the Workstream #12.
func roleSpec(t *testing.T, role string) engine.Spec {
	t.Helper()
	spec := leadSpec(t)
	spec.Role = role
	return spec
}

// implementerSpec adds a queued task of the issue number in the Workstream #12, and gives the spec of its Implementer.
// The issue is open on the fake GitHub, so the poll keeps the task.
func implementerSpec(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub, number int64) engine.Spec {
	t.Helper()
	if !fake.HasIssue(shop, number) {
		fake.AddIssue(shop, number, "Task")
	}
	queuedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := server.DB.Exec("INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, queued_at) VALUES (?, ?, 12, 'queued', ?, ?)", shop, number, queuedAt, queuedAt)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	spec := roleSpec(t, engine.ImplementerRole)
	spec.Issue = sql.NullInt64{Int64: number, Valid: true}
	spec.Task = id
	return spec
}

// started is the result of Start.
type started struct {
	agent *engine.Agent
	err   error
}

// startLater starts the session of spec in the background, and gives the result of Start when it comes.
func startLater(ctx context.Context, server *testserver.Server, spec engine.Spec) <-chan started {
	result := make(chan started, 1)
	go func() {
		agent, err := server.Engine.Start(ctx, spec)
		result <- started{agent, err}
	}()
	return result
}

// await gives the agent of the result, and fails the test after an error.
func await(t *testing.T, result <-chan started) *engine.Agent {
	t.Helper()
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	return r.agent
}

func end(t *testing.T, agent *engine.Agent, reason string) {
	t.Helper()
	if err := agent.End(t.Context(), reason); err != nil {
		t.Fatal(err)
	}
}

// session gives the session id of the Workstream #12.
func session(t *testing.T, server *testserver.Server, id int64) store.Session {
	t.Helper()
	for _, node := range tree(t, server) {
		if node.Session.ID == id {
			return node.Session
		}
	}
	t.Fatalf("no session %d", id)
	return store.Session{}
}

// queued waits for a session of role in the Workstream #12 with a queue reason, and gives it.
func queued(t *testing.T, server *testserver.Server, role string) store.Session {
	t.Helper()
	return testkit.WaitForValue(t, func() (store.Session, bool) {
		for _, node := range tree(t, server) {
			if node.Session.Role == role && node.Session.QueueReason.Valid {
				return node.Session, true
			}
		}
		return store.Session{}, false
	})
}

func parseTime(t *testing.T, text string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// startsAfter fails the test when the session later did not start at or after the end of the session earlier.
func startsAfter(t *testing.T, later, earlier store.Session) {
	t.Helper()
	if later.QueueReason.Valid || parseTime(t, later.StartedAt).Before(parseTime(t, earlier.EndedAt.String)) {
		t.Errorf("session %+v starts before the end of %+v", later, earlier)
	}
}

func TestASecondImplementerWaitsForTheImplementerLimitWhileAResearcherStarts(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 })
	first := start(t, server, implementerSpec(t, server, fake, 41))
	second := startLater(t.Context(), server, implementerSpec(t, server, fake, 43))

	waiting := queued(t, server, engine.ImplementerRole)

	if waiting.QueueReason.String != "no free implementer slot (1/1)" {
		t.Errorf("queue reason = %q", waiting.QueueReason.String)
	}
	// The full Implementer limit does not stop a Researcher.
	researcher := start(t, server, roleSpec(t, engine.ResearcherRole))
	end(t, researcher, "done")
	end(t, first, "done")
	agent := await(t, second)
	defer end(t, agent, "done")
	if agent.ID() != waiting.ID {
		t.Errorf("agent = %d, want %d", agent.ID(), waiting.ID)
	}
	startsAfter(t, session(t, server, agent.ID()), session(t, server, first.ID()))
	var state string
	if err := server.DB.QueryRow("SELECT state FROM tasks WHERE issue = 43").Scan(&state); err != nil || state != "working" {
		t.Errorf("state = %q: %v", state, err)
	}
}

func TestAJudgeWaitsForTheGlobalLimitBehindARunningImplementer(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) {
		cfg.MaxAgents = 1
		cfg.Roles.Judge.CountsInMaxAgents = true
	})
	implementer := start(t, server, implementerSpec(t, server, fake, 41))
	judge := startLater(t.Context(), server, roleSpec(t, engine.JudgeRole))

	waiting := queued(t, server, engine.JudgeRole)

	if waiting.QueueReason.String != "no free agent slot (1/1)" {
		t.Errorf("queue reason = %q", waiting.QueueReason.String)
	}
	end(t, implementer, "stopped")
	agent := await(t, judge)
	defer end(t, agent, "done")
	startsAfter(t, session(t, server, agent.ID()), session(t, server, implementer.ID()))
}

func TestAJudgeStartsWhileTheGlobalLimitIsFull(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.MaxAgents = 1 })
	implementer := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, implementer, "stopped")

	judge := start(t, server, roleSpec(t, engine.JudgeRole))

	end(t, judge, "done")
	if got := session(t, server, judge.ID()); got.QueueReason.Valid || got.EndReason.String != "done" {
		t.Errorf("judge = %+v", got)
	}
}

func TestALeadAndATriagerStartWhileTheGlobalLimitIsFull(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.MaxAgents = 1 })
	implementer := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, implementer, "stopped")

	lead := start(t, server, leadSpec(t))
	triager := start(t, server, engine.Spec{Role: engine.TriagerRole, Organization: "owner", Dir: t.TempDir()})

	end(t, lead, "idle")
	end(t, triager, "done")
	if got := session(t, server, lead.ID()); got.QueueReason.Valid {
		t.Errorf("lead = %+v", got)
	}
}

func TestASecondLeadWaitsForTheLeadLimit(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Lead.Max = 1 })
	first := start(t, server, leadSpec(t))
	other := leadSpec(t)
	other.Workstream = 50
	second := startLater(t.Context(), server, other)

	waiting := testkit.WaitForValue(t, func() (store.Session, bool) {
		var found store.Session
		err := server.DB.QueryRow("SELECT id, queue_reason FROM sessions WHERE workstream = 50").Scan(&found.ID, &found.QueueReason)
		return found, err == nil && found.QueueReason.Valid
	})

	if waiting.QueueReason.String != "no free lead slot (1/1)" {
		t.Errorf("queue reason = %q", waiting.QueueReason.String)
	}
	end(t, first, "idle")
	agent := await(t, second)
	defer end(t, agent, "idle")
	var startedAt string
	if err := server.DB.QueryRow("SELECT started_at FROM sessions WHERE id = ?", agent.ID()).Scan(&startedAt); err != nil {
		t.Fatal(err)
	}
	if parseTime(t, startedAt).Before(parseTime(t, session(t, server, first.ID()).EndedAt.String)) {
		t.Errorf("the second Lead started at %s, before the end of the first", startedAt)
	}
}

func TestAStopEndsAWaitingLead(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Lead.Max = 1 })
	first := start(t, server, leadSpec(t))
	defer end(t, first, "idle")
	ctx, stop := context.WithCancel(t.Context())
	second := startLater(ctx, server, leadSpec(t))
	waiting := queued(t, server, engine.LeadRole)

	stop()

	if r := <-second; !errors.Is(r.err, context.Canceled) {
		t.Errorf("error = %v", r.err)
	}
	if got := session(t, server, waiting.ID); got.EndReason.String != "stopped" || got.QueueReason.Valid {
		t.Errorf("session = %+v", got)
	}
	if rows := stepRows(t, server, "queue"); len(rows) != 2 || rows[1].Session != nullInt(waiting.ID) || rows[1].Result.String != "stopped" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestASessionWhoseTaskLeavesTheQueueEndsAsDeclined(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Implementer.Max = 1 })
	first := start(t, server, implementerSpec(t, server, fake, 41))
	defer end(t, first, "done")
	spec := implementerSpec(t, server, fake, 43)
	second := startLater(t.Context(), server, spec)
	waiting := queued(t, server, engine.ImplementerRole)

	if _, err := server.DB.Exec("UPDATE tasks SET state = 'ended' WHERE id = ?", spec.Task); err != nil {
		t.Fatal(err)
	}
	server.Engine.WakeQueue()

	if r := <-second; !errors.Is(r.err, engine.ErrLeftQueue) {
		t.Errorf("error = %v", r.err)
	}
	if got := session(t, server, waiting.ID); got.EndReason.String != "declined" {
		t.Errorf("session = %+v", got)
	}
	if rows := stepRows(t, server, "queue"); len(rows) != 2 || rows[1].Session != nullInt(waiting.ID) || rows[1].Result.String != "fail" {
		t.Errorf("rows = %+v", rows)
	}
}
