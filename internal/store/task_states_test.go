package store

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

type taskStateRow struct {
	State, Organization, Repository, StartedAt, EndedAt string
	Issue, Workstream                                   int64
	Session                                             sql.NullInt64
	Role, Harness, Model, Effort                        sql.NullString
}

func openQueries(t *testing.T) (*sql.DB, *Queries) {
	t.Helper()
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, New(db)
}

func taskStateRows(t *testing.T, db *sql.DB, task int64) []taskStateRow {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT state, issue, workstream, organization, repository, session, role, harness, model,
		effort, started_at, ended_at FROM task_states WHERE task = ? ORDER BY id`, task)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var found []taskStateRow
	for rows.Next() {
		var r taskStateRow
		if err := rows.Scan(&r.State, &r.Issue, &r.Workstream, &r.Organization, &r.Repository, &r.Session, &r.Role, &r.Harness,
			&r.Model, &r.Effort, &r.StartedAt, &r.EndedAt); err != nil {
			t.Fatal(err)
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

func taskStateAt(t *testing.T, db *sql.DB, task int64) string {
	t.Helper()
	var stateAt string
	if err := db.QueryRowContext(t.Context(), "SELECT state_at FROM tasks WHERE id = ?", task).Scan(&stateAt); err != nil {
		t.Fatal(err)
	}
	return stateAt
}

func TestATaskGetsOneRowForEachStateThatEnded(t *testing.T) {
	db, queries := openQueries(t)
	task, err := queries.AddTask(t.Context(), AddTaskParams{Repository: "o/r", Issue: 5, Workstream: 4, DispatchedAt: "2026-10-01T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	stateAts := []string{task.StateAt}
	moves := []struct{ from, to string }{{"dispatched", "queued"}, {"queued", "working"}, {"working", "reviewed"}, {"reviewed", "needs_human"}}
	for _, move := range moves {
		if _, err := queries.SetTaskState(t.Context(), SetTaskStateParams{State: move.to, ID: task.ID, FromState: move.from}); err != nil {
			t.Fatal(err)
		}
		stateAts = append(stateAts, taskStateAt(t, db, task.ID))
	}
	if err := queries.EndTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	stateAts = append(stateAts, taskStateAt(t, db, task.ID))

	rows := taskStateRows(t, db, task.ID)

	wantStates := []string{"dispatched", "queued", "working", "reviewed", "needs_human"}
	var states []string
	for i, row := range rows {
		states = append(states, row.State)
		if row.StartedAt != stateAts[i] || row.EndedAt != stateAts[i+1] {
			t.Errorf("row %d (%s) = %s to %s, want %s to %s", i, row.State, row.StartedAt, row.EndedAt, stateAts[i], stateAts[i+1])
		}
		if row.Issue != 5 || row.Workstream != 4 || row.Organization != "o" || row.Repository != "o/r" {
			t.Errorf("row %d group = %+v, want issue 5, Workstream 4, o/r", i, row)
		}
	}
	if !slices.Equal(states, wantStates) {
		t.Errorf("states = %v, want %v", states, wantStates)
	}
}

func TestAMoveFromQueuedToQueuedGivesARow(t *testing.T) {
	db, queries := openQueries(t)
	task, err := queries.AddTask(t.Context(), AddTaskParams{Repository: "o/r", Issue: 5, Workstream: 4, DispatchedAt: "2026-10-01T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.SetTaskState(t.Context(), SetTaskStateParams{State: "queued", ID: task.ID, FromState: "dispatched"}); err != nil {
		t.Fatal(err)
	}

	if _, err := queries.RequeueTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}

	rows := taskStateRows(t, db, task.ID)
	if len(rows) != 2 || rows[1].State != "queued" || rows[1].EndedAt != taskStateAt(t, db, task.ID) {
		t.Errorf("rows = %+v, want a dispatched row and a queued row that ends at the new state_at", rows)
	}
}

func TestTheGroupValuesComeFromTheNewestImplementerSession(t *testing.T) {
	db, queries := openQueries(t)
	addSession := func(role, model string, repository string, issue int64) Session {
		t.Helper()
		session, err := queries.AddSession(t.Context(), AddSessionParams{Role: role, Harness: "claude", Model: model,
			Effort: sql.NullString{String: "high", Valid: true}, Organization: "o", Repository: repository, Workstream: 4,
			Issue: sql.NullInt64{Int64: issue, Valid: true}, StartedAt: "2026-10-01T10:00:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	withoutSession, err := queries.AddTask(t.Context(), AddTaskParams{Repository: "o/r", Issue: 6, Workstream: 4, DispatchedAt: "2026-10-01T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := queries.AddTask(t.Context(), AddTaskParams{Repository: "o/r", Issue: 5, Workstream: 4, DispatchedAt: "2026-10-01T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	addSession("implementer", "old", "o/r", 5)
	newest := addSession("implementer", "new", "o/r", 5)
	addSession("reviewer", "reviewer", "o/r", 5)
	addSession("implementer", "other repository", "o/other", 5)
	addSession("implementer", "other issue", "o/r", 7)

	for _, id := range []int64{task.ID, withoutSession.ID} {
		if _, err := queries.SetTaskState(t.Context(), SetTaskStateParams{State: "queued", ID: id, FromState: "dispatched"}); err != nil {
			t.Fatal(err)
		}
	}

	rows := taskStateRows(t, db, task.ID)
	want := taskStateRow{
		State: "dispatched", Organization: "o", Repository: "o/r", Issue: 5, Workstream: 4,
		Session: sql.NullInt64{Int64: newest.ID, Valid: true},
		Role:    sql.NullString{String: "implementer", Valid: true}, Harness: sql.NullString{String: "claude", Valid: true},
		Model: sql.NullString{String: "new", Valid: true}, Effort: sql.NullString{String: "high", Valid: true},
		StartedAt: task.StateAt, EndedAt: taskStateAt(t, db, task.ID),
	}
	if len(rows) != 1 || rows[0] != want {
		t.Errorf("rows = %+v, want [%+v]", rows, want)
	}
	rows = taskStateRows(t, db, withoutSession.ID)
	if len(rows) != 1 || rows[0].Session.Valid || rows[0].Role.Valid || rows[0].Harness.Valid || rows[0].Model.Valid || rows[0].Effort.Valid {
		t.Errorf("rows = %+v, want one row with NULL session values", rows)
	}
}
