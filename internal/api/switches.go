package api

import (
	"context"
	"time"
)

// HeldWork is an open issue with work that an off switch holds.
type HeldWork struct {
	// Kind is the switch that holds the work
	Kind string `gork:"kind" validate:"oneof=start merge repairs"`
	// Repository is the repository of the issue as "owner/name"
	Repository string `gork:"repository"`
	// Issue is the number of the issue
	Issue int64 `gork:"issue"`
	// Title is the title of the issue
	Title string `gork:"title"`
	// WorkstreamRepository is the repository of the Workstream issue as "owner/name"
	WorkstreamRepository string `gork:"workstreamRepository"`
	// Workstream is the number of the Workstream issue
	Workstream int64 `gork:"workstream"`
	// WorkstreamTitle is the title of the Workstream issue
	WorkstreamTitle string `gork:"workstreamTitle"`
	// HeldAt is the time of the first hold
	HeldAt time.Time `gork:"heldAt"`
}

// Switches is the state of the global switches. true means on.
type Switches struct {
	// Start is true when Mobius starts tasks and sessions of tasks
	Start bool `gork:"start"`
	// Merge is true when Mobius merges automatically
	Merge bool `gork:"merge"`
	// Repairs is true when Mobius starts the tasks of its own repair issues
	Repairs bool `gork:"repairs"`
	// Held is the open issues with work that an off switch holds, the oldest first
	Held []HeldWork `gork:"held"`
}

// GetSwitchesRequest is the request of GetSwitches.
type GetSwitchesRequest struct{}

// GetSwitchesResponse is the response of GetSwitches.
type GetSwitchesResponse struct {
	Body Envelope[Switches]
}

// GetSwitches returns the state of the global switches and the work that the off switches hold.
func (h *handlers) GetSwitches(ctx context.Context, _ GetSwitchesRequest) (*GetSwitchesResponse, error) {
	switches, err := h.switches(ctx)
	if err != nil {
		return nil, err
	}
	return &GetSwitchesResponse{Body: Envelope[Switches]{Data: switches}}, nil
}

func (h *handlers) switches(ctx context.Context) (Switches, error) {
	state, err := h.engine.Switches(ctx)
	if err != nil {
		return Switches{}, err
	}
	switches := Switches{Start: state.Start, Merge: state.Merge, Repairs: state.Repairs, Held: make([]HeldWork, 0, len(state.Held))}
	for _, row := range state.Held {
		heldAt, err := time.Parse(time.RFC3339Nano, row.HeldAt)
		if err != nil {
			return Switches{}, err
		}
		switches.Held = append(switches.Held, HeldWork{
			Kind: row.Kind, Repository: row.Repository, Issue: row.Issue, Title: row.Title,
			WorkstreamRepository: row.WorkstreamRepository, Workstream: row.Workstream, WorkstreamTitle: row.WorkstreamTitle,
			HeldAt: heldAt,
		})
	}
	return switches, nil
}

// SetSwitchRequest is the request of SetSwitch.
type SetSwitchRequest struct {
	Path struct {
		// Kind is the switch
		Kind string `gork:"kind" validate:"oneof=start merge repairs"`
	}
	Body struct {
		// On turns the switch on when true, and off when false
		On bool `gork:"on"`
	}
}

// SetSwitch turns a global switch on or off. Turning a switch on ends the hold of its work.
func (h *handlers) SetSwitch(ctx context.Context, req SetSwitchRequest) error {
	return h.engine.SetSwitch(ctx, req.Path.Kind, req.Body.On)
}
