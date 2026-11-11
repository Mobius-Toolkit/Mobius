package api

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

func TestAnAgentHasTheCloseTimeOfItsKeptSessionOnlyWhenItIsKept(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &handlers{engine: engine.New(db, nil, &config.Config{}, engine.Agents{})}
	row := store.Session{Role: "implementer", Harness: "devin", StartedAt: "2026-10-04T10:00:00Z", EndedAt: sql.NullString{String: "2026-10-04T10:05:00Z", Valid: true}}
	until := time.Date(2026, 10, 4, 11, 5, 0, 0, time.UTC)

	kept, err := h.agentOf(t.Context(), engine.Node{Session: row, Name: "implementer", KeptUntil: until})
	if err != nil {
		t.Fatal(err)
	}
	open, err := h.agentOf(t.Context(), engine.Node{Session: store.Session{Role: "implementer", Harness: "devin", StartedAt: row.StartedAt}, Name: "implementer"})
	if err != nil {
		t.Fatal(err)
	}

	if kept.KeptUntil == nil || !kept.KeptUntil.Equal(until) {
		t.Errorf("kept.KeptUntil = %v, want %v", kept.KeptUntil, until)
	}
	if open.KeptUntil != nil {
		t.Errorf("open.KeptUntil = %v, want nil", open.KeptUntil)
	}
}
