package engine

import (
	"context"
	"slices"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// Switches is the state of the global switches. true means on.
type Switches struct {
	Start   bool
	Merge   bool
	Repairs bool
	Held    []HeldWork
}

// HeldWork is an open issue with work that an off switch holds.
type HeldWork struct {
	Kind                 string
	Repository           string
	Issue                int64
	Title                string
	WorkstreamRepository string
	Workstream           int64
	WorkstreamTitle      string
	HeldAt               string
}

// Switches gives the state of the global switches and the work that the off switches hold.
func (e *Engine) Switches(ctx context.Context) (Switches, error) {
	pauses, err := e.queries.ListGlobalPauses(ctx)
	if err != nil {
		return Switches{}, err
	}
	rows, err := e.queries.ListHeldWork(ctx)
	if err != nil {
		return Switches{}, err
	}
	switches := Switches{Start: true, Merge: true, Repairs: true, Held: []HeldWork{}}
	for _, pause := range pauses {
		switch pause.Kind {
		case "start":
			switches.Start = false
		case "merge":
			switches.Merge = false
		case "repairs":
			switches.Repairs = false
		}
	}
	for _, row := range rows {
		held := HeldWork{
			Kind: row.Kind, Repository: row.Repository, Issue: row.Issue, Title: row.Title,
			WorkstreamRepository: row.WorkstreamRepository, Workstream: row.Workstream, WorkstreamTitle: row.WorkstreamTitle,
			HeldAt: row.HeldAt,
		}
		same := func(other HeldWork) bool {
			return other.Kind == held.Kind && other.Repository == held.Repository && other.Issue == held.Issue
		}
		if !slices.ContainsFunc(switches.Held, same) {
			switches.Held = append(switches.Held, held)
		}
	}
	return switches, nil
}

// SetSwitch turns the switch kind on or off. Turning it on also forgets the work that it held. Turning it off again
// keeps the first time.
func (e *Engine) SetSwitch(ctx context.Context, kind string, on bool) error {
	err := e.inTx(ctx, func(q *store.Queries) error {
		if !on {
			return q.AddGlobalPause(ctx, store.AddGlobalPauseParams{Kind: kind, PausedAt: now()})
		}
		if err := q.DeleteHeldWork(ctx, kind); err != nil {
			return err
		}
		return q.DeleteGlobalPause(ctx, kind)
	})
	if err != nil {
		return err
	}
	e.publish(Change{Switches: true})
	if kind == "merge" && on {
		select {
		case e.pollWake <- struct{}{}:
		default:
		}
	}
	return nil
}

// holdWork records that the switch kind holds the work of the issue of repository.
func (e *Engine) holdWork(ctx context.Context, kind, repository string, issue int64) error {
	added, err := e.queries.AddHeldWork(ctx, store.AddHeldWorkParams{Kind: kind, Repository: repository, Issue: issue, HeldAt: now()})
	if err != nil || added == 0 {
		return err
	}
	e.publish(Change{Switches: true})
	return nil
}

// mergeOff tells if the Merge switch is off.
func (e *Engine) mergeOff(ctx context.Context) (bool, error) {
	pauses, err := e.queries.ListGlobalPauses(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(pauses, func(pause store.GlobalPause) bool { return pause.Kind == "merge" }), nil
}
