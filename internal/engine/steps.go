package engine

import (
	"context"
	"database/sql"
	"log"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// addStep adds the step row of the session of a. The group values are the values of the session.
func (a *Agent) addStep(ctx context.Context, kind, startedAt, endedAt string, attempt sql.NullInt64, result string) error {
	spec := a.spec
	binding, _ := roleBinding(a.engine.config, spec.Role)
	return a.engine.queries.AddStepTime(ctx, store.AddStepTimeParams{
		Kind:         kind,
		Session:      sql.NullInt64{Int64: a.id, Valid: true},
		Task:         sql.NullInt64{Int64: spec.Task, Valid: spec.Task != 0},
		Issue:        spec.Issue,
		Workstream:   spec.Workstream,
		Organization: spec.Organization,
		Repository:   spec.Repository,
		Role:         spec.Role,
		Harness:      string(a.harness),
		Model:        binding.Model,
		Effort:       sql.NullString{String: binding.Effort, Valid: binding.Effort != ""},
		StartedAt:    startedAt,
		EndedAt:      endedAt,
		Attempt:      attempt,
		Result:       sql.NullString{String: result, Valid: result != ""},
	})
}

// endStep adds the row of a wait or a step of a that began at startedAt and ends now. Its result is empty after
// success, stopped when the context of the session ended, and fail in each other case. The row is written after the
// end of the context.
func (a *Agent) endStep(ctx context.Context, kind, startedAt string, attempt sql.NullInt64, stepErr error) {
	result := ""
	switch {
	case stepErr == nil:
	case ctx.Err() != nil:
		result = "stopped"
	default:
		result = "fail"
	}
	if err := a.addStep(context.WithoutCancel(ctx), kind, startedAt, now(), attempt, result); err != nil {
		log.Printf("add the %s step of the session %d: %v", kind, a.id, err)
	}
}

// implementerSession gives the newest Implementer session of the task, or false when the task has none.
func (e *Engine) implementerSession(ctx context.Context, task store.Task) (store.Session, bool, error) {
	id, err := e.lastSession(ctx, task, func(session store.Session) bool { return session.Role == ImplementerRole })
	if err != nil || !id.Valid {
		return store.Session{}, false, err
	}
	session, err := e.queries.GetSession(ctx, id.Int64)
	return session, err == nil, err
}

// addCIStep adds the ci row of the wait of the task for the CI. It starts when the task entered checks and ends now.
// The group values are the values of session, the Implementer session of the work that the CI tested.
func (e *Engine) addCIStep(ctx context.Context, task store.Task, session store.Session, result string) {
	err := e.queries.AddStepTime(ctx, store.AddStepTimeParams{
		Kind:         "ci",
		Session:      sql.NullInt64{Int64: session.ID, Valid: true},
		Task:         sql.NullInt64{Int64: task.ID, Valid: true},
		Issue:        sql.NullInt64{Int64: task.Issue, Valid: true},
		Workstream:   task.Workstream,
		Organization: owner(task.Repository),
		Repository:   task.Repository,
		Role:         session.Role,
		Harness:      session.Harness,
		Model:        session.Model,
		Effort:       session.Effort,
		StartedAt:    task.StateAt,
		EndedAt:      now(),
		Result:       sql.NullString{String: result, Valid: true},
	})
	if err != nil {
		log.Printf("add the CI wait of the task %d: %v", task.ID, err)
	}
}
