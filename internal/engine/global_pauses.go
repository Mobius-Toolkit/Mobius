package engine

import (
	"context"
	"slices"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// startOffReason is the queue reason of a session of a task that the Start switch holds.
const startOffReason = "Autopilot paused: no task starts"

// startOffRefusal is the text of a Lead tool that the Start switch refuses.
const startOffRefusal = "Mobius started no task: the Owner turned Start off on the Settings page. Try again after the Owner turns Start on."

// repairsOffRefusal is the text of a Lead tool that the Repairs switch refuses.
const repairsOffRefusal = "Mobius started no task: the Owner turned Repairs off on the Settings page. Try again after the Owner turns Repairs on."

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

// startOff tells if the Start switch is off.
func (e *Engine) startOff(ctx context.Context) (bool, error) {
	pauses, err := e.queries.ListGlobalPauses(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(pauses, func(pause store.GlobalPause) bool { return pause.Kind == "start" }), nil
}

// holdStart tells if the Start switch is off, and then records the issue as held work. It is the check of each
// start of a task.
func (e *Engine) holdStart(ctx context.Context, repository string, issue int64) (bool, error) {
	off, err := e.startOff(ctx)
	if err != nil || !off {
		return false, err
	}
	return true, e.holdWork(ctx, "start", repository, issue)
}

// startOffRefused gives the refusal of a Lead tool while the Start switch is off, or nil.
func (e *Engine) startOffRefused(ctx context.Context) error {
	off, err := e.startOff(ctx)
	if err != nil || !off {
		return err
	}
	return refuse(startOffRefusal)
}

// repairsOff tells if the Repairs switch is off.
func (e *Engine) repairsOff(ctx context.Context) (bool, error) {
	pauses, err := e.queries.ListGlobalPauses(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(pauses, func(pause store.GlobalPause) bool { return pause.Kind == "repairs" }), nil
}

// repairIssue tells if the issue is the issue of a CI break, a check timeout or a CI failure of repository.
func (e *Engine) repairIssue(ctx context.Context, repository string, issue int64) (bool, error) {
	return e.queries.IsRepairIssue(ctx, store.IsRepairIssueParams{Repository: repository, Issue: issue})
}

// holdRepairs tells if the Repairs switch is off and the issue is a repair issue, and then records the issue as held
// work. It comes after holdStart at each start of a task.
func (e *Engine) holdRepairs(ctx context.Context, repository string, issue int64) (bool, error) {
	off, err := e.repairsOff(ctx)
	if err != nil || !off {
		return false, err
	}
	repair, err := e.repairIssue(ctx, repository, issue)
	if err != nil || !repair {
		return false, err
	}
	return true, e.holdWork(ctx, "repairs", repository, issue)
}

// repairsOffRefused gives the refusal of a Lead tool for a repair issue while the Repairs switch is off, or nil.
func (e *Engine) repairsOffRefused(ctx context.Context, repository string, issue int64) error {
	off, err := e.repairsOff(ctx)
	if err != nil || !off {
		return err
	}
	repair, err := e.repairIssue(ctx, repository, issue)
	if err != nil || !repair {
		return err
	}
	return refuse(repairsOffRefusal)
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
	e.workers.changed.notify()
	if on {
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
